package installer

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cdSignature  = 0x02014b50
	lfhSignature = 0x04034b50
)

func getRange(client *http.Client, url string, start, end int64) ([]byte, error) {
	return getRangeWithContext(context.Background(), client, url, start, end)
}

func getRangeWithContext(ctx context.Context, client *http.Client, url string, start, end int64) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create range request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode == http.StatusOK && start == 0 {
			limit := end + 1
			return io.ReadAll(io.LimitReader(resp.Body, limit))
		}
		return nil, fmt.Errorf("expected 206 Partial Content, got %s", resp.Status)
	}

	want := end - start + 1
	if want > 0 {
		return io.ReadAll(io.LimitReader(resp.Body, want))
	}
	return io.ReadAll(resp.Body)
}

// StreamProgress represents live download and extraction progress.
type StreamProgress struct {
	DownloadedBytes    int64   `json:"downloaded_bytes"`
	TotalDownloadBytes int64   `json:"total_download_bytes"`
	CompletedFiles     int     `json:"completed_files"`
	CurrentFile        string  `json:"current_file"`
	SpeedBps           float64 `json:"speed_bps"`
	Percent            float64 `json:"percent"`
	IsPaused           bool    `json:"is_paused"`
}

// StreamState holds the checkpoint for pausing and resuming downloads without data loss.
type StreamState struct {
	Version        string `json:"version"`
	PackIndex      int    `json:"pack_index"`
	PackOffset     int64  `json:"pack_offset"`
	TotalBytesRead int64  `json:"total_bytes_read"`
	CompletedFiles int    `json:"completed_files"`
}

// PackInfo represents an ordered part of a split multi-volume zip archive.
type PackInfo struct {
	URL  string
	Size int64
	MD5  string
}

// Defaults for the parallel pack reader.
const (
	defaultPackConnections = 8       // parallel HTTP connections per stream
	defaultPackBlockSize   = 4 << 20 // bytes fetched per Range request
)

// packBlock is one contiguous slice of a pack. Blocks are fetched in parallel but handed to
// the consumer strictly in order.
type packBlock struct {
	pack   int
	off    int64 // offset of data[0] inside the pack
	length int64
	data   []byte
	err    error
	last   bool // final block of its pack
	ready  chan struct{}
}

// ResumableMultiPackReader presents several split-zip parts as ONE continuous byte stream.
//
// SetSpeedLimit, SetConnections and SetBlockSize must be called before the first Read.
type ResumableMultiPackReader struct {
	ctx      context.Context
	fetchCtx context.Context
	cancel   context.CancelFunc
	client   *http.Client
	packs    []PackInfo

	onBytesRead func(n int)
	totalRead   atomic.Int64
	wire        atomic.Int64

	connections int
	blockSize   int64
	limiter     *rateLimiter

	startOnce sync.Once
	wg        sync.WaitGroup
	order     chan *packBlock
	free      chan []byte

	startPack   int
	startOffset int64

	checkpointMu sync.Mutex
	packIdx      int
	packOffset   int64
	cur          *packBlock
	curPos       int
}

// NewResumableMultiPackReader creates a chained multi-pack HTTP streaming reader that starts at
// byte startOffset of pack startPack.
func NewResumableMultiPackReader(ctx context.Context, client *http.Client, packs []PackInfo, startPack int, startOffset int64, startTotal int64, onBytesRead func(n int)) *ResumableMultiPackReader {
	if client == nil {
		client = NewDownloadClient(defaultPackConnections)
	}
	fetchCtx, cancel := context.WithCancel(ctx)
	r := &ResumableMultiPackReader{
		ctx:         ctx,
		fetchCtx:    fetchCtx,
		cancel:      cancel,
		client:      client,
		packs:       packs,
		onBytesRead: onBytesRead,
		connections: defaultPackConnections,
		blockSize:   defaultPackBlockSize,
		startPack:   startPack,
		startOffset: startOffset,
		packIdx:     startPack,
		packOffset:  startOffset,
	}
	r.totalRead.Store(startTotal)
	return r
}

// SetSpeedLimit configures the maximum download speed in bytes per second (0 = unlimited).
func (r *ResumableMultiPackReader) SetSpeedLimit(bytesPerSec int64) {
	r.limiter = newRateLimiter(bytesPerSec)
}

// SetConnections sets how many blocks are downloaded in parallel (default 8).
func (r *ResumableMultiPackReader) SetConnections(n int) {
	if n > 0 {
		r.connections = n
	}
}

// SetBlockSize sets the size of each Range request in bytes (default 4 MiB).
func (r *ResumableMultiPackReader) SetBlockSize(n int64) {
	if n > 0 {
		r.blockSize = n
	}
}

// GetTotalRead returns the current total bytes consumed from the stream.
func (r *ResumableMultiPackReader) GetTotalRead() int64 {
	return r.totalRead.Load()
}

// GetWireBytes returns the bytes received from the network so far. Unlike GetTotalRead it is
// not tied to the in-order delivery of blocks, so it gives a steadier speed measurement.
func (r *ResumableMultiPackReader) GetWireBytes() int64 {
	return r.wire.Load()
}

func (r *ResumableMultiPackReader) start() {
	window := r.connections * 2
	if window < 4 {
		window = 4
	}
	r.order = make(chan *packBlock, window)
	r.free = make(chan []byte, window+2)
	work := make(chan *packBlock, window+2)

	for i := 0; i < r.connections; i++ {
		r.wg.Add(1)
		go r.worker(work)
	}
	r.wg.Add(1)
	go r.dispatch(work)
}

func (r *ResumableMultiPackReader) getBuf(n int) []byte {
	select {
	case b := <-r.free:
		if cap(b) >= n {
			return b[:n]
		}
	default:
	}
	return make([]byte, n, max(n, int(r.blockSize)))
}

func (r *ResumableMultiPackReader) putBuf(b []byte) {
	if cap(b) == 0 {
		return
	}
	select {
	case r.free <- b[:0]:
	default:
	}
}

// dispatch walks the packs in order and queues blocks for the workers.
func (r *ResumableMultiPackReader) dispatch(work chan<- *packBlock) {
	defer r.wg.Done()
	defer close(r.order)
	defer close(work)

	pack, off := r.startPack, r.startOffset
	for ; pack < len(r.packs); pack, off = pack+1, 0 {
		if r.fetchCtx.Err() != nil {
			return
		}
		size, ranges := r.resolvePackSize(pack)
		if !ranges {
			if err := r.streamSequential(pack, off); err != nil {
				r.emitError(err)
				return
			}
			continue
		}
		for off < size {
			n := min(r.blockSize, size-off)
			blk := &packBlock{pack: pack, off: off, length: n, last: off+n == size, ready: make(chan struct{})}
			select {
			case r.order <- blk:
			case <-r.fetchCtx.Done():
				return
			}
			select {
			case work <- blk:
			case <-r.fetchCtx.Done():
				return
			}
			off += n
		}
	}
}

func (r *ResumableMultiPackReader) emitError(err error) {
	blk := &packBlock{err: err, ready: make(chan struct{})}
	close(blk.ready)
	select {
	case r.order <- blk:
	case <-r.fetchCtx.Done():
	}
}

// resolvePackSize asks the server for the real pack size. ranges=false means the pack has to be
// streamed sequentially (unknown size or no Range support).
func (r *ResumableMultiPackReader) resolvePackSize(pack int) (size int64, ranges bool) {
	size = -1
	ranges = true
	_ = retry(r.fetchCtx, func(int) error {
		req, err := http.NewRequestWithContext(r.fetchCtx, http.MethodHead, r.packs[pack].URL, nil)
		if err != nil {
			return &permanentError{err}
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := r.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return &httpStatusError{Code: resp.StatusCode, URL: r.packs[pack].URL}
		}
		if resp.ContentLength >= 0 {
			size = resp.ContentLength
		}
		if strings.EqualFold(resp.Header.Get("Accept-Ranges"), "none") {
			ranges = false
		}
		return nil
	})
	if size < 0 && r.packs[pack].Size > 0 {
		size = r.packs[pack].Size
	}
	if size < 0 {
		ranges = false
	}
	return size, ranges
}

func (r *ResumableMultiPackReader) worker(work <-chan *packBlock) {
	defer r.wg.Done()
	for blk := range work {
		if err := r.fetchCtx.Err(); err != nil {
			blk.err = err
		} else {
			blk.data, blk.err = r.fetchBlock(blk)
		}
		close(blk.ready)
	}
}

// fetchBlock downloads one block, retrying (and resuming inside the block) on transient errors.
func (r *ResumableMultiPackReader) fetchBlock(blk *packBlock) ([]byte, error) {
	buf := r.getBuf(int(blk.length))
	got := 0
	err := retry(r.fetchCtx, func(int) error {
		n, err := r.fetchRange(blk.pack, blk.off+int64(got), blk.off+blk.length-1, buf[got:])
		got += n
		if err == nil && got < len(buf) {
			err = io.ErrUnexpectedEOF
		}
		return err
	})
	if err != nil {
		r.putBuf(buf)
		return nil, err
	}
	return buf, nil
}

// fetchRange reads bytes [start, endInclusive] of a pack into dst.
func (r *ResumableMultiPackReader) fetchRange(pack int, start, endInclusive int64, dst []byte) (int, error) {
	reqCtx, cancel := context.WithCancelCause(r.fetchCtx)
	defer cancel(nil)

	u := r.packs[pack].URL
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return 0, &permanentError{err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, endInclusive))

	resp, err := r.client.Do(req)
	if err != nil {
		return 0, causeOr(reqCtx, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && start == 0:
	case resp.StatusCode == http.StatusOK:
		return 0, &permanentError{fmt.Errorf("server ignored the Range header for %s", u)}
	default:
		return 0, &httpStatusError{Code: resp.StatusCode, URL: u}
	}

	wd := newWatchdog(cancel, stallTimeout)
	defer wd.Stop()

	n := 0
	for n < len(dst) {
		m, rerr := resp.Body.Read(dst[n:])
		if m > 0 {
			n += m
			r.wire.Add(int64(m))
			wd.Stop() // waiting on the rate limiter is not a stall
			if err := r.limiter.Wait(r.fetchCtx, m); err != nil {
				return n, err
			}
			wd.Kick()
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return n, causeOr(reqCtx, rerr)
		}
	}
	if n < len(dst) {
		return n, io.ErrUnexpectedEOF
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return n, nil
}

// streamSequential is the fallback for a pack of unknown size: a single connection that is cut
// into blocks as it arrives. It reconnects (with a Range header) after transient errors.
func (r *ResumableMultiPackReader) streamSequential(pack int, off int64) error {
	attempt := 0
	for {
		pushed, done, err := r.streamSequentialOnce(pack, off)
		off += pushed
		if done {
			return nil
		}
		if cerr := r.fetchCtx.Err(); cerr != nil {
			return cerr
		}
		if pushed > 0 {
			attempt = 0
		}
		attempt++
		if attempt >= maxAttempts || !isRetryable(err) {
			return err
		}
		if e := sleepCtx(r.fetchCtx, backoff(attempt)); e != nil {
			return e
		}
	}
}

func (r *ResumableMultiPackReader) streamSequentialOnce(pack int, off int64) (pushed int64, done bool, err error) {
	reqCtx, cancel := context.WithCancelCause(r.fetchCtx)
	defer cancel(nil)

	u := r.packs[pack].URL
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return 0, false, &permanentError{err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, false, causeOr(reqCtx, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		if off > 0 { // Range ignored: skip what was already consumed
			if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
				return 0, false, causeOr(reqCtx, err)
			}
		}
	default:
		return 0, false, &httpStatusError{Code: resp.StatusCode, URL: u}
	}

	wd := newWatchdog(cancel, stallTimeout)
	defer wd.Stop()

	for {
		buf := r.getBuf(int(r.blockSize))
		filled := 0
		var rerr error
		for filled < len(buf) && rerr == nil {
			var m int
			m, rerr = resp.Body.Read(buf[filled:])
			if m > 0 {
				filled += m
				r.wire.Add(int64(m))
				wd.Stop()
				if werr := r.limiter.Wait(r.fetchCtx, m); werr != nil {
					r.putBuf(buf)
					return pushed, false, werr
				}
				wd.Kick()
			}
		}
		eof := errors.Is(rerr, io.EOF)

		if filled > 0 || eof {
			blk := &packBlock{pack: pack, off: off + pushed, length: int64(filled), data: buf[:filled], last: eof, ready: make(chan struct{})}
			close(blk.ready)
			select {
			case r.order <- blk:
			case <-r.fetchCtx.Done():
				return pushed, false, r.fetchCtx.Err()
			}
			pushed += int64(filled)
		} else {
			r.putBuf(buf)
		}
		if eof {
			return pushed, true, nil
		}
		if rerr != nil {
			return pushed, false, causeOr(reqCtx, rerr)
		}
	}
}

// Read implements io.Reader over the in-order block stream.
func (r *ResumableMultiPackReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.startOnce.Do(r.start)

	for {
		if err := r.fetchCtx.Err(); err != nil {
			return 0, err
		}

		if r.cur != nil {
			if r.curPos < len(r.cur.data) {
				n := copy(p, r.cur.data[r.curPos:])
				r.curPos += n
				if r.curPos == len(r.cur.data) && r.cur.last {
					r.setPosition(r.cur.pack+1, 0)
				} else {
					r.setPosition(r.cur.pack, r.cur.off+int64(r.curPos))
				}
				r.totalRead.Add(int64(n))
				if r.onBytesRead != nil {
					r.onBytesRead(n)
				}
				return n, nil
			}
			if r.cur.last {
				r.setPosition(r.cur.pack+1, 0)
			}
			r.putBuf(r.cur.data)
			r.cur = nil
		}

		select {
		case blk, ok := <-r.order:
			if !ok {
				if err := r.fetchCtx.Err(); err != nil {
					return 0, err
				}
				return 0, io.EOF
			}
			select {
			case <-blk.ready:
			case <-r.fetchCtx.Done():
				return 0, r.fetchCtx.Err()
			}
			if blk.err != nil {
				return 0, blk.err
			}
			r.cur, r.curPos = blk, 0
		case <-r.fetchCtx.Done():
			return 0, r.fetchCtx.Err()
		}
	}
}

func (r *ResumableMultiPackReader) setPosition(pack int, off int64) {
	r.checkpointMu.Lock()
	r.packIdx, r.packOffset = pack, off
	r.checkpointMu.Unlock()
}

// Close stops all background downloads and waits for them to exit.
func (r *ResumableMultiPackReader) Close() error {
	r.cancel()
	r.wg.Wait()
	return nil
}

// SaveCheckpoint records the current stream position before opening a new file.
// The position is that of the consumer, not of the prefetching workers, so resuming from it
// never skips data.
func (r *ResumableMultiPackReader) SaveCheckpoint() (packIdx int, packOff int64, totalRead int64) {
	r.checkpointMu.Lock()
	defer r.checkpointMu.Unlock()
	return r.packIdx, r.packOffset, r.totalRead.Load()
}

// ExtractFile downloads and extracts a single file from a remote zip archive via HTTP Range requests.
func ExtractFile(url string, remote_path string, path string) error {
	return ExtractFileWithContext(context.Background(), nil, url, remote_path, path)
}

// ExtractFileWithContext downloads and extracts a single file from a remote zip archive
// with a context for cancellation and an optional custom http.Client.
func ExtractFileWithContext(ctx context.Context, client *http.Client, url string, remote_path string, path string) error {
	if client == nil {
		client = http.DefaultClient
	}

	cleanRemote := strings.TrimPrefix(filepath.ToSlash(remote_path), "/")
	if cleanRemote == "" || !filepath.IsLocal(filepath.FromSlash(cleanRemote)) {
		return fmt.Errorf("refusing unsafe remote path: %q", remote_path)
	}

	headReq, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HEAD request for %s: %w", url, err)
	}
	headReq.Header.Set("User-Agent", userAgent)

	head, err := client.Do(headReq)
	if err != nil {
		return fmt.Errorf("failed HEAD request to %s: %w", url, err)
	}
	_ = head.Body.Close()

	if head.StatusCode < 200 || head.StatusCode > 299 {
		return fmt.Errorf("HEAD request to %s returned status %s", url, head.Status)
	}

	contentLengthStr := head.Header.Get("Content-Length")
	size, err := strconv.ParseInt(contentLengthStr, 10, 64)
	if err != nil || size <= 0 {
		return fmt.Errorf("invalid or missing Content-Length (%q) from %s: %w", contentLengthStr, url, err)
	}

	if size < 22 {
		return fmt.Errorf("remote archive %s is too small (%d bytes) to be a valid zip", url, size)
	}

	tailSize := int64(65557)
	if tailSize > size {
		tailSize = size
	}
	tail, err := getRangeWithContext(ctx, client, url, size-tailSize, size-1)
	if err != nil {
		return fmt.Errorf("failed to fetch archive tail from %s: %w", url, err)
	}

	eocdIdx := bytes.LastIndex(tail, []byte{0x50, 0x4b, 0x05, 0x06})
	if eocdIdx == -1 || len(tail)-eocdIdx < 22 {
		return fmt.Errorf("EOCD record not found in %s", url)
	}
	eocd := tail[eocdIdx:]
	cdSize := int64(binary.LittleEndian.Uint32(eocd[12:16]))
	cdOffset := int64(binary.LittleEndian.Uint32(eocd[16:20]))

	if cdOffset < 0 || cdSize < 0 || cdOffset+cdSize > size {
		return fmt.Errorf("invalid central directory range (offset=%d, size=%d) for archive size %d", cdOffset, cdSize, size)
	}

	cd, err := getRangeWithContext(ctx, client, url, cdOffset, cdOffset+cdSize-1)
	if err != nil {
		return fmt.Errorf("failed to fetch central directory from %s: %w", url, err)
	}

	var (
		localOffset int64 = -1
		compSize    uint32
		method      uint16
		crcVal      uint32
	)
	p := 0
	for p+46 <= len(cd) {
		if binary.LittleEndian.Uint32(cd[p:]) != cdSignature {
			break
		}
		m := binary.LittleEndian.Uint16(cd[p+10:])
		crc := binary.LittleEndian.Uint32(cd[p+16:])
		cs := binary.LittleEndian.Uint32(cd[p+20:])
		nameLen := int(binary.LittleEndian.Uint16(cd[p+28:]))
		extraLen := int(binary.LittleEndian.Uint16(cd[p+30:]))
		commentLen := int(binary.LittleEndian.Uint16(cd[p+32:]))
		relOffset := binary.LittleEndian.Uint32(cd[p+42:])

		entryLen := 46 + nameLen + extraLen + commentLen
		if p+46+nameLen > len(cd) {
			break
		}
		name := string(cd[p+46 : p+46+nameLen])
		nameNorm := strings.TrimPrefix(filepath.ToSlash(name), "/")

		if nameNorm == cleanRemote {
			localOffset = int64(relOffset)
			compSize = cs
			method = m
			crcVal = crc
			break
		}
		p += entryLen
	}
	if localOffset == -1 {
		return fmt.Errorf("file %q not found in archive %s", remote_path, url)
	}

	const headerMargin = 1024
	end := localOffset + headerMargin + int64(compSize)
	if end > size-1 {
		end = size - 1
	}
	chunk, err := getRangeWithContext(ctx, client, url, localOffset, end)
	if err != nil {
		return fmt.Errorf("failed to fetch local file chunk: %w", err)
	}
	if len(chunk) < 30 || binary.LittleEndian.Uint32(chunk[:4]) != lfhSignature {
		return fmt.Errorf("corrupted LFH signature at offset %d", localOffset)
	}

	lfhNameLen := int(binary.LittleEndian.Uint16(chunk[26:28]))
	lfhExtraLen := int(binary.LittleEndian.Uint16(chunk[28:30]))
	dataStart := 30 + lfhNameLen + lfhExtraLen

	var data []byte
	if len(chunk) >= dataStart+int(compSize) {
		data = chunk[dataStart : dataStart+int(compSize)]
	} else {
		exactStart := localOffset + int64(dataStart)
		exactEnd := exactStart + int64(compSize) - 1
		if exactEnd > size-1 {
			exactEnd = size - 1
		}
		data, err = getRangeWithContext(ctx, client, url, exactStart, exactEnd)
		if err != nil {
			return fmt.Errorf("failed to fetch compressed file data: %w", err)
		}
	}

	destDir := filepath.Dir(path)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	tmpPath := path + ".arclights.tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temporary file %s: %w", tmpPath, err)
	}

	success := false
	defer func() {
		if !success {
			_ = out.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := crc32.NewIEEE()
	writer := io.MultiWriter(out, hasher)

	switch method {
	case 0: // stored
		if _, err := io.Copy(writer, bytes.NewReader(data)); err != nil {
			return fmt.Errorf("failed to write stored file %s: %w", path, err)
		}
	case 8: // deflate
		fr := flate.NewReader(bytes.NewReader(data))
		_, err = io.Copy(writer, fr)
		_ = fr.Close()
		if err != nil {
			return fmt.Errorf("failed to decompress deflate file %s: %w", path, err)
		}
	default:
		return fmt.Errorf("unsupported compression method %d for file %s", method, path)
	}

	if crcVal != 0 && hasher.Sum32() != crcVal {
		return fmt.Errorf("CRC32 mismatch for %s: got %x, expected %x", remote_path, hasher.Sum32(), crcVal)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file %s: %w", tmpPath, err)
	}

	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("failed to finalize file %s: %w", path, err)
	}

	success = true
	return nil
}

// StreamOptions tunes StreamInstallPacksWithOptions. Zero values select defaults.
type StreamOptions struct {
	SpeedLimitBytesPerSec int64
	Connections           int
	BlockSize             int64
}

// StreamInstallPacks downloads and extracts split zip packs progressively with zero extra temporary storage.
func StreamInstallPacks(ctx context.Context, client *http.Client, packs []PackInfo, destDir string, version string, onProgress func(StreamProgress)) error {
	return StreamInstallPacksWithOptions(ctx, client, packs, destDir, version, StreamOptions{}, onProgress)
}

// StreamInstallPacksWithSpeedLimit downloads and extracts split zip packs progressively with rate limiting.
func StreamInstallPacksWithSpeedLimit(ctx context.Context, client *http.Client, packs []PackInfo, destDir string, version string, speedLimitBytesPerSec int64, onProgress func(StreamProgress)) error {
	return StreamInstallPacksWithOptions(ctx, client, packs, destDir, version, StreamOptions{SpeedLimitBytesPerSec: speedLimitBytesPerSec}, onProgress)
}

func StreamInstallPacksWithOptions(ctx context.Context, client *http.Client, packs []PackInfo, destDir string, version string, opts StreamOptions, onProgress func(StreamProgress)) error {
	if len(packs) == 0 {
		return errors.New("no packs provided")
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	statePath := filepath.Join(destDir, ".arclights.progress.json")
	var state StreamState
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state)
		if state.Version != version {
			state = StreamState{Version: version}
		}
	} else {
		state = StreamState{Version: version}
	}

	var totalDownloadBytes int64
	for _, p := range packs {
		totalDownloadBytes += p.Size
	}

	streamReader := NewResumableMultiPackReader(
		ctx,
		client,
		packs,
		state.PackIndex,
		state.PackOffset,
		state.TotalBytesRead,
		nil,
	)
	streamReader.SetSpeedLimit(opts.SpeedLimitBytesPerSec)
	streamReader.SetConnections(opts.Connections)
	streamReader.SetBlockSize(opts.BlockSize)
	defer streamReader.Close()

	var (
		fileMu         sync.RWMutex
		curFileName    string
		completedFiles = state.CompletedFiles
		speedBits      atomic.Uint64
	)

	meter := newSpeedMeter(0)
	calcSpeed := func(now time.Time, _ int64) float64 {
		v := meter.sample(now, streamReader.GetWireBytes())
		speedBits.Store(math.Float64bits(v))
		return v
	}
	getSpeed := func() float64 { return math.Float64frombits(speedBits.Load()) }

	var stopTicker func()
	if onProgress != nil {
		stopTicker = startTicker(progressInterval, func(now time.Time) {
			curBytes := streamReader.GetTotalRead()
			spd := calcSpeed(now, curBytes)
			percent := percentOf(curBytes, totalDownloadBytes)

			fileMu.RLock()
			fName := curFileName
			cFiles := completedFiles
			fileMu.RUnlock()

			onProgress(StreamProgress{
				DownloadedBytes:    curBytes,
				TotalDownloadBytes: totalDownloadBytes,
				CompletedFiles:     cFiles,
				CurrentFile:        fName,
				SpeedBps:           spd,
				Percent:            percent,
				IsPaused:           false,
			})
		})
	}
	stopTickerFunc := func() {
		if stopTicker != nil {
			stopTicker()
			stopTicker = nil
		}
	}
	defer stopTickerFunc()

	sigBuf := make([]byte, 4)
	headerBuf := make([]byte, 26)
	copyBuf := make([]byte, 512*1024)

	var flateReader io.ReadCloser
	var dirs dirCache

	for {
		select {
		case <-ctx.Done():
			stopTickerFunc()
			saveState(statePath, state)
			return ctx.Err()
		default:
		}

		checkPack, checkOff, checkTot := streamReader.SaveCheckpoint()
		state.PackIndex = checkPack
		state.PackOffset = checkOff
		state.TotalBytesRead = checkTot
		fileMu.Lock()
		state.CompletedFiles = completedFiles
		fileMu.Unlock()

		if _, err := io.ReadFull(streamReader, sigBuf); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			stopTickerFunc()
			saveState(statePath, state)
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			return fmt.Errorf("failed to read zip signature: %w", err)
		}

		sig := binary.LittleEndian.Uint32(sigBuf)
		if sig == 0x02014b50 { // Central Directory header PK\x01\x02 -> all files extracted
			break
		}
		if sig != 0x04034b50 { // Local File Header PK\x03\x04
			break
		}

		if _, err := io.ReadFull(streamReader, headerBuf); err != nil {
			stopTickerFunc()
			saveState(statePath, state)
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			return fmt.Errorf("failed to read local header: %w", err)
		}

		flag := binary.LittleEndian.Uint16(headerBuf[2:4])
		method := binary.LittleEndian.Uint16(headerBuf[4:6])
		crcVal := binary.LittleEndian.Uint32(headerBuf[10:14])
		compSize := binary.LittleEndian.Uint32(headerBuf[14:18])
		nameLen := binary.LittleEndian.Uint16(headerBuf[22:24])
		extraLen := binary.LittleEndian.Uint16(headerBuf[24:26])

		nameBytes := make([]byte, nameLen)
		if _, err := io.ReadFull(streamReader, nameBytes); err != nil {
			stopTickerFunc()
			saveState(statePath, state)
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			return fmt.Errorf("failed to read file name: %w", err)
		}
		fileName := string(nameBytes)

		fileMu.Lock()
		curFileName = fileName
		fileMu.Unlock()

		if extraLen > 0 {
			if _, err := io.CopyN(io.Discard, streamReader, int64(extraLen)); err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				return fmt.Errorf("failed to skip extra field: %w", err)
			}
		}

		isDir := strings.HasSuffix(fileName, "/")
		if !filepath.IsLocal(filepath.FromSlash(strings.TrimSuffix(fileName, "/"))) {
			stopTickerFunc()
			saveState(statePath, state)
			return fmt.Errorf("refusing unsafe path in archive: %q", fileName)
		}
		targetPath := filepath.Join(destDir, filepath.FromSlash(fileName))

		if isDir {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
		} else {
			if err := dirs.ensure(filepath.Dir(targetPath)); err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				return fmt.Errorf("failed to create parent dir for %s: %w", targetPath, err)
			}

			tmpPath := targetPath + ".arclights.tmp"
			outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				return fmt.Errorf("failed to create file %s: %w", tmpPath, err)
			}

			bufWriter := bufio.NewWriterSize(outFile, 512*1024)
			hasher := crc32.NewIEEE()
			fileWriter := io.MultiWriter(bufWriter, hasher)
			limitReader := &io.LimitedReader{R: streamReader, N: int64(compSize)}

			switch method {
			case 0: // Store
				if _, err := io.CopyBuffer(fileWriter, limitReader, copyBuf); err != nil {
					_ = outFile.Close()
					_ = os.Remove(tmpPath)
					stopTickerFunc()
					saveState(statePath, state)
					if errors.Is(err, context.Canceled) {
						return context.Canceled
					}
					return fmt.Errorf("failed to stream stored file %s: %w", fileName, err)
				}
			case 8: // Deflate
				if flateReader == nil {
					flateReader = flate.NewReader(limitReader)
				} else {
					if resetter, ok := flateReader.(flate.Resetter); ok {
						_ = resetter.Reset(limitReader, nil)
					} else {
						_ = flateReader.Close()
						flateReader = flate.NewReader(limitReader)
					}
				}

				if _, err := io.CopyBuffer(fileWriter, flateReader, copyBuf); err != nil {
					_ = flateReader.Close()
					flateReader = nil
					_ = outFile.Close()
					_ = os.Remove(tmpPath)
					stopTickerFunc()
					saveState(statePath, state)
					if errors.Is(err, context.Canceled) {
						return context.Canceled
					}
					return fmt.Errorf("failed to decompress file %s: %w", fileName, err)
				}

				if limitReader.N > 0 {
					_, _ = io.CopyN(io.Discard, streamReader, limitReader.N)
				}
			default:
				_ = outFile.Close()
				_ = os.Remove(tmpPath)
				stopTickerFunc()
				saveState(statePath, state)
				return fmt.Errorf("unsupported compression method %d for file %s", method, fileName)
			}

			if err := bufWriter.Flush(); err != nil {
				_ = outFile.Close()
				_ = os.Remove(tmpPath)
				stopTickerFunc()
				saveState(statePath, state)
				return fmt.Errorf("failed to flush buffer for %s: %w", fileName, err)
			}
			_ = outFile.Close()

			if (flag&0x08) == 0 && crcVal != 0 {
				computedCRC := hasher.Sum32()
				if computedCRC != crcVal {
					_ = os.Remove(tmpPath)
					stopTickerFunc()
					saveState(statePath, state)
					return fmt.Errorf("CRC32 mismatch for %s: got %x, expected %x", fileName, computedCRC, crcVal)
				}
			}

			if err := replaceFile(tmpPath, targetPath); err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				return err
			}

			fileMu.Lock()
			completedFiles++
			curCompletedCount := completedFiles
			fileMu.Unlock()

			if onProgress != nil {
				curBytes := streamReader.GetTotalRead()
				percent := percentOf(curBytes, totalDownloadBytes)
				onProgress(StreamProgress{
					DownloadedBytes:    curBytes,
					TotalDownloadBytes: totalDownloadBytes,
					CompletedFiles:     curCompletedCount,
					CurrentFile:        fileName,
					SpeedBps:           getSpeed(),
					Percent:            percent,
					IsPaused:           false,
				})
			}
		}

		if (flag & 0x08) != 0 {
			var descBuf [16]byte
			if _, err := io.ReadFull(streamReader, descBuf[:4]); err != nil {
				stopTickerFunc()
				saveState(statePath, state)
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				return err
			}
			if binary.LittleEndian.Uint32(descBuf[:4]) == 0x08074b50 {
				_, _ = io.ReadFull(streamReader, descBuf[4:16])
			} else {
				_, _ = io.ReadFull(streamReader, descBuf[4:12])
			}
		}
	}

	if flateReader != nil {
		_ = flateReader.Close()
	}

	stopTickerFunc()

	if onProgress != nil {
		curRead := streamReader.GetTotalRead()
		fileMu.RLock()
		fName := curFileName
		cFiles := completedFiles
		fileMu.RUnlock()

		onProgress(StreamProgress{
			DownloadedBytes:    curRead,
			TotalDownloadBytes: totalDownloadBytes,
			CompletedFiles:     cFiles,
			CurrentFile:        fName,
			SpeedBps:           0,
			Percent:            100.0,
			IsPaused:           false,
		})
	}

	_ = os.Remove(statePath)
	return nil
}

// StreamApplyPatch downloads and applies a delta patch zip directly to destDir without storing the zip archive.
func StreamApplyPatch(ctx context.Context, client *http.Client, patchURL string, destDir string, onProgress func(StreamProgress)) error {
	packs := []PackInfo{
		{URL: patchURL, Size: 0},
	}
	return StreamInstallPacks(ctx, client, packs, destDir, "patch", onProgress)
}

func saveState(path string, state StreamState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}
