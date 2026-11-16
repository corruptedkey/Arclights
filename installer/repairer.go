package installer

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// CloudFront limits a single HTTP request/response body to 30 GB.
// Any file larger than maxCloudFrontRequestSize is downloaded in cloudFrontChunkSize Range windows.
const (
	maxCloudFrontRequestSize = int64(25 * 1024 * 1024 * 1024) // 25 GB safe margin under 30 GB limit
	cloudFrontChunkSize      = int64(5 * 1024 * 1024 * 1024)  // 5 GB per Range request for large files
)

const progressInterval = 100 * time.Millisecond

// RepairProgress reports the progress of repairing missing/corrupted files.
type RepairProgress struct {
	CompletedFiles  int     `json:"completed_files"`
	TotalFiles      int     `json:"total_files"`
	DownloadedBytes uint64  `json:"downloaded_bytes"`
	TotalBytes      uint64  `json:"total_bytes"`
	CurrentFile     string  `json:"current_file"`
	SpeedBps        float64 `json:"speed_bps"`
	Percent         float64 `json:"percent"`
}

func percentOf(done, total int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(done) / float64(total) * 100.0
	if p > 100.0 {
		p = 100.0
	}
	return p
}

// sortBySizeDesc orders entries biggest-first. Starting the largest files first keeps every
// worker busy until the end instead of leaving one slow multi-GB file for the tail.
func sortBySizeDesc(entries []GameFileEntry) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
}

// InstallManifestFiles downloads all game files concurrently from the CDN manifest directly to gameDir.
// Pass a nil client to get one tuned for bulk downloads (see NewDownloadClient).
func InstallManifestFiles(
	ctx context.Context,
	client *http.Client,
	filesURL string,
	gameDir string,
	manifest *Manifest,
	concurrency int,
	speedLimitBytesPerSec int64,
	onProgress func(StreamProgress),
) error {
	if manifest == nil || len(manifest.Entries) == 0 {
		return errors.New("empty manifest provided")
	}

	if err := os.MkdirAll(gameDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	statePath := filepath.Join(gameDir, ".arclights.progress.json")
	_ = os.WriteFile(statePath, []byte(`{"status":"downloading"}`), 0644)

	if concurrency <= 0 {
		concurrency = 16
	} else if concurrency > 32 {
		concurrency = 32
	}
	if client == nil {
		client = NewDownloadClient(concurrency)
	}

	totalDownloadBytes := int64(manifest.TotalSize)
	totalFilesCount := len(manifest.Entries)

	var (
		initialBytes int64
		initialFiles int64
		pending      []GameFileEntry
	)

	for _, entry := range manifest.Entries {
		targetPath := filepath.Join(gameDir, filepath.FromSlash(entry.Path))
		if fi, err := os.Stat(targetPath); err == nil && uint64(fi.Size()) == entry.Size {
			initialBytes += int64(entry.Size)
			initialFiles++
		} else {
			pending = append(pending, entry)
		}
	}

	finalProgress := StreamProgress{
		DownloadedBytes:    totalDownloadBytes,
		TotalDownloadBytes: totalDownloadBytes,
		CompletedFiles:     totalFilesCount,
		Percent:            100.0,
	}

	if len(pending) == 0 {
		_ = os.Remove(statePath)
		if onProgress != nil {
			onProgress(finalProgress)
		}
		return nil
	}

	sortBySizeDesc(pending)

	var (
		pc        progressCounter
		completed atomic.Int64
		curName   atomic.Value
	)
	pc.done.Store(initialBytes)
	completed.Store(initialFiles)
	curName.Store("")

	stop := func() {}
	if onProgress != nil {
		meter := newSpeedMeter(0)
		stop = startTicker(progressInterval, func(now time.Time) {
			done := pc.done.Load()
			onProgress(StreamProgress{
				DownloadedBytes:    done,
				TotalDownloadBytes: totalDownloadBytes,
				CompletedFiles:     int(completed.Load()),
				CurrentFile:        curName.Load().(string),
				SpeedBps:           meter.sample(now, pc.wire.Load()),
				Percent:            percentOf(done, totalDownloadBytes),
			})
		})
	}

	d := newDownloader(client, filesURL, gameDir, newRateLimiter(speedLimitBytesPerSec))
	err := runPool(ctx, concurrency, pending, func(ctx context.Context, e GameFileEntry) error {
		curName.Store(e.Path)
		if err := d.download(ctx, e, true, &pc); err != nil {
			return fmt.Errorf("failed to download %s: %w", e.Path, err)
		}
		completed.Add(1)
		return nil
	})
	stop()
	if err != nil {
		return err
	}

	_ = os.Remove(statePath)
	if onProgress != nil {
		onProgress(finalProgress)
	}
	return nil
}

// RepairGameFiles downloads and restores missing or corrupted files directly from the loose files CDN.
// Pass a nil client to get one tuned for bulk downloads (see NewDownloadClient).
func RepairGameFiles(ctx context.Context, client *http.Client, filePath string, gameDir string, diffs []GameFileEntry, concurrency int, onProgress func(RepairProgress)) error {
	if len(diffs) == 0 {
		return nil
	}

	if concurrency <= 0 {
		concurrency = 8
	} else if concurrency > 32 {
		concurrency = 32
	}
	if client == nil {
		client = NewDownloadClient(concurrency)
	}

	work := append([]GameFileEntry(nil), diffs...)
	sortBySizeDesc(work)

	var totalBytes int64
	for _, e := range work {
		totalBytes += int64(e.Size)
	}
	totalFiles := len(work)

	var (
		pc        progressCounter
		completed atomic.Int64
		curName   atomic.Value
	)
	curName.Store("")

	emit := func(speed float64, done int64, files int, name string) {
		if onProgress == nil {
			return
		}
		onProgress(RepairProgress{
			CompletedFiles:  files,
			TotalFiles:      totalFiles,
			DownloadedBytes: uint64(done),
			TotalBytes:      uint64(totalBytes),
			CurrentFile:     name,
			SpeedBps:        speed,
			Percent:         percentOf(done, totalBytes),
		})
	}

	stop := func() {}
	if onProgress != nil {
		meter := newSpeedMeter(0)
		stop = startTicker(progressInterval, func(now time.Time) {
			emit(meter.sample(now, pc.wire.Load()), pc.done.Load(), int(completed.Load()), curName.Load().(string))
		})
	}

	d := newDownloader(client, filePath, gameDir, nil)
	err := runPool(ctx, concurrency, work, func(ctx context.Context, e GameFileEntry) error {
		curName.Store(e.Path)
		if err := d.download(ctx, e, false, &pc); err != nil {
			return fmt.Errorf("failed to repair %s: %w", e.Path, err)
		}
		completed.Add(1)
		return nil
	})
	stop()
	if err != nil {
		return err
	}

	emit(0, totalBytes, totalFiles, "")
	return nil
}

type downloader struct {
	client  *http.Client
	baseURL string
	gameDir string
	limiter *rateLimiter
	dirs    dirCache
}

func newDownloader(client *http.Client, baseURL, gameDir string, limiter *rateLimiter) *downloader {
	return &downloader{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		gameDir: gameDir,
		limiter: limiter,
	}
}

func (d *downloader) fileURL(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return d.baseURL + "/" + strings.Join(parts, "/")
}

// download fetches one file into <target>.arclights.tmp, verifies its MD5 and moves it into place.
func (d *downloader) download(ctx context.Context, entry GameFileEntry, trustExisting bool, pc *progressCounter) error {
	rel := filepath.FromSlash(entry.Path)
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("refusing unsafe path in manifest: %q", entry.Path)
	}
	targetPath := filepath.Join(d.gameDir, rel)
	if err := d.dirs.ensure(filepath.Dir(targetPath)); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", entry.Path, err)
	}

	if trustExisting {
		if fi, err := os.Stat(targetPath); err == nil && uint64(fi.Size()) == entry.Size {
			return nil
		}
	}

	if entry.Size == 0 {
		f, err := os.Create(targetPath)
		if err != nil {
			return err
		}
		return f.Close()
	}

	var (
		tmpPath = targetPath + ".arclights.tmp"
		fileURL = d.fileURL(entry.Path)
		total   = int64(entry.Size)
		ep      = &entryProgress{pc: pc}
		h       hash.Hash
		hashed  int64
		bad     int
	)
	resetHash := func() {
		hashed = 0
		if entry.MD5 != "" {
			h = md5.New()
		} else {
			h = nil
		}
	}
	resetHash()

	err := retry(ctx, func(int) error {
		var size int64
		if fi, err := os.Stat(tmpPath); err == nil {
			size = fi.Size()
		}
		switch {
		case size > total:
			_ = os.Remove(tmpPath)
			size = 0
			resetHash()
		case h != nil && hashed != size:
			switch {
			case hashed == 0:
				if err := hashPrefix(h, tmpPath, size); err != nil {
					_ = os.Remove(tmpPath)
					size = 0
					resetHash()
				} else {
					hashed = size
				}
			case hashed < size:
				if err := os.Truncate(tmpPath, hashed); err != nil {
					return &permanentError{err}
				}
				size = hashed
			default:
				_ = os.Remove(tmpPath)
				size = 0
				resetHash()
			}
		}
		ep.align(size)

		if size < total {
			if err := d.fetch(ctx, fileURL, tmpPath, size, total, h, &hashed, ep); err != nil {
				return err
			}
		}

		if h != nil {
			got := hex.EncodeToString(h.Sum(nil))
			if hashed != total || !stringsEqualFold(got, entry.MD5) {
				_ = os.Remove(tmpPath)
				resetHash()
				ep.align(0)
				bad++
				mismatch := fmt.Errorf("MD5 mismatch for %s: got %s, expected %s", entry.Path, got, entry.MD5)
				if bad >= 2 {
					return &permanentError{mismatch}
				}
				return mismatch
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return replaceFile(tmpPath, targetPath)
}

var errRangeIgnored = errors.New("server ignored the Range header")

// fetch appends bytes [offset, total) to tmpPath, in windows if the file exceeds the CloudFront limit.
func (d *downloader) fetch(ctx context.Context, fileURL, tmpPath string, offset, total int64, h hash.Hash, hashed *int64, ep *entryProgress) error {
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return &permanentError{err}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return &permanentError{err}
	}

	remaining := total - offset
	bw := bufio.NewWriterSize(f, int(max(4096, min(remaining, 1<<20))))
	defer func() {
		_ = bw.Flush()
		_ = f.Close()
	}()
	readBuf := make([]byte, int(min(remaining, 256<<10)))

	window := total
	if total > maxCloudFrontRequestSize {
		window = cloudFrontChunkSize
	}

	for offset < total {
		end := min(total, offset+window)
		ranged := offset > 0 || end < total

		n, err := d.copyRange(ctx, fileURL, bw, readBuf, offset, end, ranged, h, hashed, ep)
		offset += n
		if errors.Is(err, errRangeIgnored) {
			if offset == 0 {
				return &permanentError{fmt.Errorf("server ignores Range requests, cannot fetch %s in windows", fileURL)}
			}
			bw.Reset(f)
			if err := f.Truncate(0); err != nil {
				return &permanentError{err}
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return &permanentError{err}
			}
			if h != nil {
				h.Reset()
			}
			*hashed = 0
			ep.align(0)
			offset = 0
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// copyRange streams bytes [start, end) of the file into bw and returns how many were written.
func (d *downloader) copyRange(ctx context.Context, fileURL string, bw *bufio.Writer, buf []byte, start, end int64, ranged bool, h hash.Hash, hashed *int64, ep *entryProgress) (int64, error) {
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fileURL, nil)
	if err != nil {
		return 0, &permanentError{err}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	if ranged {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, causeOr(reqCtx, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && !ranged:
	case resp.StatusCode == http.StatusOK:
		return 0, errRangeIgnored
	default:
		return 0, &httpStatusError{Code: resp.StatusCode, URL: fileURL}
	}

	wd := newWatchdog(cancel, stallTimeout)
	defer wd.Stop()

	want := end - start
	var got int64
	for got < want {
		chunk := buf
		if rem := want - got; rem < int64(len(chunk)) {
			chunk = chunk[:rem]
		}
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			wd.Stop()
			if err := d.limiter.Wait(ctx, n); err != nil {
				return got, err
			}
			wd.Kick()

			if _, werr := bw.Write(chunk[:n]); werr != nil {
				return got, &permanentError{fmt.Errorf("write failed: %w", werr)}
			}
			if h != nil {
				_, _ = h.Write(chunk[:n])
			}
			*hashed += int64(n)
			got += int64(n)
			ep.add(int64(n))
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				if got < want {
					return got, io.ErrUnexpectedEOF
				}
				break
			}
			return got, causeOr(reqCtx, rerr)
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return got, nil
}

func replaceFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err == nil {
		return nil
	}
	_ = os.Remove(target)
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tmp, target, err)
	}
	return nil
}

func hashPrefix(h hash.Hash, path string, n int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	copied, err := io.CopyBuffer(h, io.LimitReader(f, n), make([]byte, 1<<20))
	if err != nil {
		return err
	}
	if copied != n {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func computeFileMD5(path string) (string, error) {
	return calculateFileMD5(path, nil)
}

func verifyFileMD5(path, expectedMD5 string) bool {
	actual, err := computeFileMD5(path)
	if err != nil {
		return false
	}
	return stringsEqualFold(actual, expectedMD5)
}
