package installer

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testFile struct {
	name string
	data []byte
}

func makeFiles(n, size int, seed int64) []testFile {
	rng := rand.New(rand.NewSource(seed))
	files := make([]testFile, n)
	for i := range files {
		d := make([]byte, size+i*7)
		rng.Read(d)
		files[i] = testFile{name: fmt.Sprintf("dir%d/file%02d.bin", i%3, i), data: d}
	}
	return files
}

// buildStoredZip returns a streamable zip (stored entries) and the byte offset after each file.
func buildStoredZip(files []testFile) ([]byte, []int) {
	var buf bytes.Buffer
	var ends []int
	for _, f := range files {
		writeLocalFileHeader(&buf, f.name, 0, crc32.ChecksumIEEE(f.data), uint32(len(f.data)), uint32(len(f.data)))
		buf.Write(f.data)
		ends = append(ends, buf.Len())
	}
	buf.Write([]byte{0x50, 0x4b, 0x01, 0x02})
	return buf.Bytes(), ends
}

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func fastRetry(t *testing.T) {
	t.Helper()
	old := retryBase
	retryBase = time.Millisecond
	t.Cleanup(func() { retryBase = old })
}

func assertFiles(t *testing.T, dir string, files []testFile) {
	t.Helper()
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.name)))
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if !bytes.Equal(got, f.data) {
			t.Fatalf("%s: content mismatch (got %d bytes, want %d)", f.name, len(got), len(f.data))
		}
	}
}

// packServer serves the given parts at /part0, /part1, ... and lets a test hook each request.
func packServer(t *testing.T, parts [][]byte, hook func(w http.ResponseWriter, r *http.Request, data []byte) (handled bool)) (*httptest.Server, []PackInfo) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var idx int
		if _, err := fmt.Sscanf(r.URL.Path, "/part%d", &idx); err != nil || idx >= len(parts) {
			http.NotFound(w, r)
			return
		}
		if hook != nil && hook(w, r, parts[idx]) {
			return
		}
		http.ServeContent(w, r, "pack.zip", time.Time{}, bytes.NewReader(parts[idx]))
	}))
	t.Cleanup(ts.Close)
	packs := make([]PackInfo, len(parts))
	for i, p := range parts {
		packs[i] = PackInfo{URL: fmt.Sprintf("%s/part%d", ts.URL, i), Size: int64(len(p))}
	}
	return ts, packs
}

// cutBody answers with the full Content-Length but closes the connection after half of the body.
func cutBody(w http.ResponseWriter, data []byte) {
	conn, bw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	fmt.Fprintf(bw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nAccept-Ranges: bytes\r\n\r\n", len(data))
	bw.Write(data[:len(data)/2])
	bw.Flush()
	conn.Close()
}

func TestParallelPackStream(t *testing.T) {
	files := makeFiles(24, 300<<10, 1)
	zipBytes, _ := buildStoredZip(files)
	c1, c2 := len(zipBytes)/5+123, len(zipBytes)*3/5+77
	parts := [][]byte{zipBytes[:c1], zipBytes[c1:c2], zipBytes[c2:]}

	var inflight, peak atomic.Int32
	_, packs := packServer(t, parts, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if r.Method == http.MethodGet {
			n := inflight.Add(1)
			defer inflight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(3 * time.Millisecond)
		}
		return false
	})

	dest := t.TempDir()
	err := StreamInstallPacksWithOptions(context.Background(), nil, packs, dest, "1", StreamOptions{Connections: 4, BlockSize: 64 << 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)
	if peak.Load() < 2 {
		t.Fatalf("expected parallel Range requests, peak concurrency was %d", peak.Load())
	}
}

func TestPackStreamRetriesTransientErrors(t *testing.T) {
	fastRetry(t)
	files := makeFiles(10, 100<<10, 2)
	zipBytes, _ := buildStoredZip(files)
	parts := [][]byte{zipBytes[:len(zipBytes)/2], zipBytes[len(zipBytes)/2:]}

	var seen sync.Map
	_, packs := packServer(t, parts, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		if r.Method != http.MethodGet {
			return false
		}
		if _, loaded := seen.LoadOrStore(r.URL.Path+"|"+r.Header.Get("Range"), true); !loaded {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return true
		}
		return false
	})

	dest := t.TempDir()
	if err := StreamInstallPacksWithOptions(context.Background(), nil, packs, dest, "1", StreamOptions{Connections: 3, BlockSize: 32 << 10}, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)
}

func TestPackStreamUnknownSizeFallback(t *testing.T) {
	fastRetry(t)
	files := makeFiles(8, 90<<10, 3)
	zipBytes, _ := buildStoredZip(files)
	parts := [][]byte{zipBytes}

	var gets atomic.Int32
	_, packs := packServer(t, parts, func(w http.ResponseWriter, r *http.Request, data []byte) bool {
		if r.Method == http.MethodHead {
			http.Error(w, "no HEAD here", http.StatusMethodNotAllowed)
			return true
		}
		if gets.Add(1) == 1 {
			cutBody(w, data)
			return true
		}
		return false
	})
	packs[0].Size = 0

	dest := t.TempDir()
	if err := StreamInstallPacksWithOptions(context.Background(), nil, packs, dest, "1", StreamOptions{BlockSize: 32 << 10}, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)
	if gets.Load() < 2 {
		t.Fatalf("expected a reconnect, only %d GET(s) seen", gets.Load())
	}
}

func TestPackReaderCheckpointAtPackBoundary(t *testing.T) {
	files := makeFiles(6, 50<<10, 4)
	zipBytes, ends := buildStoredZip(files)
	cut := ends[2]
	parts := [][]byte{zipBytes[:cut], zipBytes[cut:]}

	for _, mode := range []string{"known-size", "sequential-fallback"} {
		t.Run(mode, func(t *testing.T) {
			_, packs := packServer(t, parts, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
				if mode == "sequential-fallback" && r.Method == http.MethodHead {
					http.Error(w, "no", http.StatusMethodNotAllowed)
					return true
				}
				return false
			})
			if mode == "sequential-fallback" {
				for i := range packs {
					packs[i].Size = 0
				}
			}

			r := NewResumableMultiPackReader(context.Background(), nil, packs, 0, 0, 0, nil)
			r.SetBlockSize(16 << 10)
			head := make([]byte, cut)
			if _, err := io.ReadFull(r, head); err != nil {
				t.Fatal(err)
			}
			pi, po, total := r.SaveCheckpoint()
			_ = r.Close()
			if pi != 1 || po != 0 || total != int64(cut) {
				t.Fatalf("checkpoint after pack 0 = (%d, %d, %d), want (1, 0, %d)", pi, po, total, cut)
			}

			r2 := NewResumableMultiPackReader(context.Background(), nil, packs, pi, po, total, nil)
			r2.SetBlockSize(16 << 10)
			defer r2.Close()
			rest, err := io.ReadAll(r2)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rest, zipBytes[cut:]) {
				t.Fatalf("resumed stream differs: got %d bytes, want %d", len(rest), len(zipBytes)-cut)
			}
		})
	}
}

func TestPackStreamPauseAndResume(t *testing.T) {
	files := makeFiles(20, 100<<10, 5)
	zipBytes, ends := buildStoredZip(files)
	cut := ends[9]
	parts := [][]byte{zipBytes[:cut], zipBytes[cut:]}
	_, packs := packServer(t, parts, nil)
	dest := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	opts := StreamOptions{Connections: 4, BlockSize: 32 << 10, SpeedLimitBytesPerSec: 2 << 20}
	err := StreamInstallPacksWithOptions(ctx, nil, packs, dest, "1", opts, func(p StreamProgress) {
		if p.CompletedFiles >= 5 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled on pause, got %v", err)
	}
	statePath := filepath.Join(dest, ".arclights.progress.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing after pause: %v", err)
	}

	if err := StreamInstallPacksWithOptions(context.Background(), nil, packs, dest, "1", StreamOptions{Connections: 4, BlockSize: 32 << 10}, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("state file should be removed after completion")
	}
}

func TestPackStreamRejectsPathTraversal(t *testing.T) {
	evil := []testFile{{name: "../escape.txt", data: []byte("nope")}}
	zipBytes, _ := buildStoredZip(evil)
	_, packs := packServer(t, [][]byte{zipBytes}, nil)

	root := t.TempDir()
	dest := filepath.Join(root, "game")
	err := StreamInstallPacks(context.Background(), nil, packs, dest, "1", nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "escape.txt")); statErr == nil {
		t.Fatal("file escaped the destination directory")
	}
}

func manifestFor(files []testFile) *Manifest {
	m := &Manifest{TotalCount: len(files)}
	for _, f := range files {
		m.Entries = append(m.Entries, GameFileEntry{Path: f.name, MD5: md5hex(f.data), Size: uint64(len(f.data))})
		m.TotalSize += uint64(len(f.data))
	}
	return m
}

func fileServer(t *testing.T, files []testFile, hook func(w http.ResponseWriter, r *http.Request, data []byte) bool) *httptest.Server {
	t.Helper()
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath["/"+f.name] = f.data
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := byPath[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if hook != nil && hook(w, r, data) {
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestInstallDoesNotHangWhenEveryDownloadFails(t *testing.T) {
	files := makeFiles(40, 1<<10, 6)
	ts := fileServer(t, nil, nil)

	done := make(chan error, 1)
	go func() {
		done <- InstallManifestFiles(context.Background(), nil, ts.URL, t.TempDir(), manifestFor(files), 2, 0, nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("InstallManifestFiles hung after all workers failed")
	}
}

func TestInstallResumesAndRetriesWithRange(t *testing.T) {
	fastRetry(t)
	files := makeFiles(1, 600<<10, 7)
	f := files[0]

	var mu sync.Mutex
	var ranges []string
	cuts := 0
	ts := fileServer(t, files, func(w http.ResponseWriter, r *http.Request, data []byte) bool {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		first := cuts == 0
		if first {
			cuts++
		}
		mu.Unlock()
		if first {
			cutBody(w, data)
			return true
		}
		return false
	})

	dest := t.TempDir()
	if err := InstallManifestFiles(context.Background(), nil, ts.URL, dest, manifestFor(files), 1, 0, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)

	mu.Lock()
	defer mu.Unlock()
	if len(ranges) < 2 || ranges[0] != "" || !strings.HasPrefix(ranges[1], "bytes=") || !strings.HasSuffix(ranges[1], "-"+fmt.Sprint(len(f.data)-1)) {
		t.Fatalf("expected a plain GET followed by a Range resume, got %q", ranges)
	}
}

func TestInstallHandlesLeftoverTmpFiles(t *testing.T) {
	fastRetry(t)
	files := makeFiles(2, 200<<10, 8)

	var mu sync.Mutex
	reqs := map[string][]string{}
	ts := fileServer(t, files, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		mu.Lock()
		reqs[r.URL.Path] = append(reqs[r.URL.Path], r.Header.Get("Range"))
		mu.Unlock()
		return false
	})

	dest := t.TempDir()
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dest, f.name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	half0, half1 := len(files[0].data)/2, len(files[1].data)/2
	os.WriteFile(filepath.Join(dest, files[0].name+".arclights.tmp"), files[0].data[:half0], 0o644)
	os.WriteFile(filepath.Join(dest, files[1].name+".arclights.tmp"), bytes.Repeat([]byte{0xAA}, half1), 0o644)

	if err := InstallManifestFiles(context.Background(), nil, ts.URL, dest, manifestFor(files), 1, 0, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)

	mu.Lock()
	defer mu.Unlock()
	if got := reqs["/"+files[0].name]; len(got) != 1 || got[0] != fmt.Sprintf("bytes=%d-%d", half0, len(files[0].data)-1) {
		t.Fatalf("file 0 should resume with one Range request, got %q", got)
	}
	got1 := reqs["/"+files[1].name]
	if len(got1) != 2 || got1[0] == "" || got1[1] != "" {
		t.Fatalf("file 1 should resume, fail the MD5 check and restart from scratch, got %q", got1)
	}
}

func TestInstallStartsLargestFilesFirst(t *testing.T) {
	var files []testFile
	for i, size := range []int{10, 5000, 300, 40000, 700} {
		files = append(files, testFile{name: fmt.Sprintf("f%d.bin", i), data: bytes.Repeat([]byte{byte(i + 1)}, size)})
	}
	var mu sync.Mutex
	var order []string
	ts := fileServer(t, files, func(w http.ResponseWriter, r *http.Request, _ []byte) bool {
		mu.Lock()
		order = append(order, r.URL.Path)
		mu.Unlock()
		return false
	})
	if err := InstallManifestFiles(context.Background(), nil, ts.URL, t.TempDir(), manifestFor(files), 1, 0, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"/f3.bin", "/f1.bin", "/f4.bin", "/f2.bin", "/f0.bin"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("download order = %v, want %v", order, want)
	}
}

func TestRepairReplacesCorruptFileOfCorrectSize(t *testing.T) {
	files := makeFiles(1, 64<<10, 9)
	f := files[0]
	ts := fileServer(t, files, nil)

	dest := t.TempDir()
	target := filepath.Join(dest, filepath.FromSlash(f.name))
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, bytes.Repeat([]byte{0x55}, len(f.data)), 0o644)

	diffs := []GameFileEntry{{Path: f.name, MD5: md5hex(f.data), Size: uint64(len(f.data))}}
	if err := RepairGameFiles(context.Background(), nil, ts.URL, dest, diffs, 2, nil); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, dest, files)
}

func TestRejectsUnsafeManifestPath(t *testing.T) {
	ts := fileServer(t, nil, nil)
	m := &Manifest{TotalCount: 1, TotalSize: 4, Entries: []GameFileEntry{{Path: "../evil.bin", MD5: md5hex([]byte("evil")), Size: 4}}}
	err := InstallManifestFiles(context.Background(), nil, ts.URL, t.TempDir(), m, 1, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
}

func TestRateLimiterThrottles(t *testing.T) {
	l := newRateLimiter(200 << 10)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.Wait(context.Background(), 50<<10); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 800*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("elapsed %v, expected ~0.9-1s", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := newRateLimiter(1)
	_ = slow.Wait(context.Background(), 1)
	if err := slow.Wait(ctx, 1<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait must stop on context cancel, got %v", err)
	}
}

func TestSpeedMeterSmoothsLinkStalls(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	m := &speedMeter{samples: []speedSample{{t: t0, bytes: 0}}}

	const perLiveTick = 4_000_000 * 20 / 17
	var total int64
	lo, hi := 1e18, 0.0
	for tick := 1; tick <= 300; tick++ {
		if tick%20 < 17 {
			total += perLiveTick
		}
		v := m.sample(t0.Add(time.Duration(tick)*100*time.Millisecond), total)
		if tick > 50 {
			lo, hi = min(lo, v), max(hi, v)
		}
	}
	if lo < 36e6 || hi > 44e6 {
		t.Fatalf("speed should stay near 40 MB/s despite the stalls, got %.1f..%.1f MB/s", lo/1e6, hi/1e6)
	}
}

func TestSpeedMeterRampUpAndIdle(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	m := &speedMeter{samples: []speedSample{{t: t0, bytes: 0}}}

	if v := m.sample(t0.Add(100*time.Millisecond), 4_000_000); v > 10e6 {
		t.Fatalf("first tick reported %.1f MB/s", v/1e6)
	}

	var total int64
	now := t0
	for i := 0; i < 50; i++ {
		now = now.Add(100 * time.Millisecond)
		total += 4_000_000
		m.sample(now, total)
	}
	var v float64
	for i := 0; i < 40; i++ {
		now = now.Add(100 * time.Millisecond)
		v = m.sample(now, total)
	}
	if v != 0 {
		t.Fatalf("speed after 4 idle seconds = %.2f MB/s, want 0", v/1e6)
	}
}
