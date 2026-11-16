package installer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Arclights/1.0"

// NewDownloadClient returns an http.Client tuned for bulk downloads from a CDN.
func NewDownloadClient(concurrency int) *http.Client {
	if concurrency <= 0 {
		concurrency = 16
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: dialer.DialContext,

		ForceAttemptHTTP2: false,
		// A non-nil empty map switches HTTP/2 off.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},

		MaxIdleConns:        concurrency * 2,
		MaxIdleConnsPerHost: concurrency * 2,
		IdleConnTimeout:     90 * time.Second,

		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,

		DisableCompression: true,
		ReadBufferSize:     256 << 10,
		WriteBufferSize:    32 << 10,
	}
	return &http.Client{Transport: tr}
}

const limiterBurst = 100 * time.Millisecond

type rateLimiter struct {
	mu   sync.Mutex
	bps  float64
	next time.Time
}

func newRateLimiter(bytesPerSec int64) *rateLimiter {
	if bytesPerSec <= 0 {
		return nil
	}
	return &rateLimiter{bps: float64(bytesPerSec)}
}

func (l *rateLimiter) Wait(ctx context.Context, n int) error {
	if l == nil || n <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	if floor := now.Add(-limiterBurst); l.next.Before(floor) {
		l.next = floor
	}
	l.next = l.next.Add(time.Duration(float64(n) / l.bps * float64(time.Second)))
	wait := l.next.Sub(now)
	l.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var errStalled = errors.New("download stalled: no data received")

type httpStatusError struct {
	Code int
	URL  string
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d from %s", e.Code, e.URL) }

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

const (
	maxAttempts  = 6
	stallTimeout = 30 * time.Second
)

var retryBase = 500 * time.Millisecond

const retryMax = 8 * time.Second

func isRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var pe *permanentError
	if errors.As(err, &pe) {
		return false
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusRequestTimeout || se.Code == http.StatusTooManyRequests
	}
	return true
}

func backoff(attempt int) time.Duration {
	d := retryBase << (attempt - 1)
	if d <= 0 || d > retryMax {
		d = retryMax
	}
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retry(ctx context.Context, fn func(attempt int) error) error {
	for attempt := 1; ; attempt++ {
		err := fn(attempt)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= maxAttempts || !isRetryable(err) {
			return err
		}
		if e := sleepCtx(ctx, backoff(attempt)); e != nil {
			return e
		}
	}
}

type watchdog struct {
	timer *time.Timer
	d     time.Duration
}

func newWatchdog(cancel context.CancelCauseFunc, d time.Duration) *watchdog {
	return &watchdog{timer: time.AfterFunc(d, func() { cancel(errStalled) }), d: d}
}

func (w *watchdog) Kick() { w.timer.Reset(w.d) }
func (w *watchdog) Stop() { w.timer.Stop() }

func causeOr(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		return errStalled
	}
	return err
}

func runPool[T any](parent context.Context, workers int, items []T, fn func(ctx context.Context, item T) error) error {
	if workers < 1 {
		workers = 1
	}
	if workers > len(items) {
		workers = len(items)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	jobs := make(chan T)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if err := fn(ctx, item); err != nil {
					once.Do(func() { firstErr = err; cancel() })
					return
				}
			}
		}()
	}

send:
	for _, it := range items {
		select {
		case <-ctx.Done():
			break send
		case jobs <- it:
		}
	}
	close(jobs)
	wg.Wait()

	if parent.Err() != nil {
		return parent.Err()
	}
	return firstErr
}

// progressCounter separates "bytes accounted toward completion" (done, includes data that was
// already on disk when resuming) from "bytes actually received" (wire, used for speed).
type progressCounter struct {
	done atomic.Int64
	wire atomic.Int64
}

// entryProgress is the per-file view of a progressCounter (single goroutine only).
type entryProgress struct {
	pc      *progressCounter
	counted int64
}

func (e *entryProgress) add(n int64) {
	e.counted += n
	e.pc.done.Add(n)
	e.pc.wire.Add(n)
}

func (e *entryProgress) align(n int64) {
	e.pc.done.Add(n - e.counted)
	e.counted = n
}

const speedWindow = 3 * time.Second

type speedSample struct {
	t     time.Time
	bytes int64
}

type speedMeter struct {
	samples []speedSample // oldest first
}

func newSpeedMeter(startBytes int64) *speedMeter {
	return &speedMeter{samples: []speedSample{{t: time.Now(), bytes: startBytes}}}
}

// sample records the running byte total at time now and returns the average speed in bytes/s.
func (m *speedMeter) sample(now time.Time, bytes int64) float64 {
	m.samples = append(m.samples, speedSample{t: now, bytes: bytes})

	cut := now.Add(-speedWindow)
	i := 0
	for i+1 < len(m.samples) && !m.samples[i+1].t.After(cut) {
		i++
	}
	if i > 0 {
		m.samples = append(m.samples[:0], m.samples[i:]...)
	}

	first := m.samples[0]
	dt := now.Sub(first.t).Seconds()
	if dt < 1 {
		dt = 1
	}
	v := float64(bytes-first.bytes) / dt
	if v < 0 {
		return 0
	}
	return v
}

// startTicker calls fn every interval until stop is called. stop waits for fn to finish,
// so nothing is emitted after it returns.
func startTicker(interval time.Duration, fn func(now time.Time)) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				fn(now)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-finished
		})
	}
}

// dirCache avoids calling MkdirAll for the same directory thousands of times.
type dirCache struct{ m sync.Map }

func (d *dirCache) ensure(dir string) error {
	if _, ok := d.m.Load(dir); ok {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	d.m.Store(dir, struct{}{})
	return nil
}
