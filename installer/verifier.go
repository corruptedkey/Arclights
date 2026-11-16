package installer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// VerifyProgress represents live verification status.
type VerifyProgress struct {
	CheckedFiles int     `json:"checked_files"`
	TotalFiles   int     `json:"total_files"`
	CheckedBytes uint64  `json:"checked_bytes"`
	TotalBytes   uint64  `json:"total_bytes"`
	DiffCount    int     `json:"diff_count"`
	CurrentFile  string  `json:"current_file"`
	Percent      float64 `json:"percent"`
}

// VerifyGameFiles verifies local files in gameDir against the provided manifest.
// Missing or corrupted files are returned as a slice of GameFileEntry (diffs).
func VerifyGameFiles(ctx context.Context, gameDir string, manifest *Manifest, concurrency int, onProgress func(VerifyProgress)) ([]GameFileEntry, error) {
	if manifest == nil || len(manifest.Entries) == 0 {
		return nil, nil
	}

	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
		if concurrency < 2 {
			concurrency = 2
		} else if concurrency > 16 {
			concurrency = 16
		}
	}

	totalFiles := len(manifest.Entries)
	totalBytes := manifest.TotalSize

	var (
		checkedFiles int64
		checkedBytes uint64
		diffCount    int64
		diffMu       sync.Mutex
		diffs        []GameFileEntry
	)

	jobs := make(chan GameFileEntry, concurrency*4)
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hashBuf := make([]byte, 128*1024)

			for entry := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				targetPath := filepath.Join(gameDir, filepath.FromSlash(entry.Path))
				isDiff := false

				fi, err := os.Stat(targetPath)
				if err != nil {
					isDiff = true
				} else if uint64(fi.Size()) != entry.Size {
					isDiff = true
				} else if entry.Size > 0 {
					fileMD5, err := calculateFileMD5(targetPath, hashBuf)
					if err != nil || !stringsEqualFold(fileMD5, entry.MD5) {
						isDiff = true
					}
				}

				curChecked := atomic.AddInt64(&checkedFiles, 1)
				curBytes := atomic.AddUint64(&checkedBytes, entry.Size)

				if isDiff {
					atomic.AddInt64(&diffCount, 1)
					diffMu.Lock()
					diffs = append(diffs, entry)
					diffMu.Unlock()
				}

				if onProgress != nil {
					percent := 0.0
					if totalBytes > 0 {
						percent = percentOf(int64(curBytes), int64(totalBytes))
					} else if totalFiles > 0 {
						percent = percentOf(curChecked, int64(totalFiles))
					}
					onProgress(VerifyProgress{
						CheckedFiles: int(curChecked),
						TotalFiles:   totalFiles,
						CheckedBytes: curBytes,
						TotalBytes:   totalBytes,
						DiffCount:    int(atomic.LoadInt64(&diffCount)),
						CurrentFile:  entry.Path,
						Percent:      percent,
					})
				}
			}
		}()
	}

sendLoop:
	for _, entry := range manifest.Entries {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- entry:
		}
	}
	close(jobs)
	wg.Wait()

	if ctx.Err() != nil {
		return diffs, ctx.Err()
	}

	return diffs, nil
}

func calculateFileMD5(path string, buf []byte) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if len(buf) == 0 {
		buf = make([]byte, 1<<20)
	}

	hasher := md5.New()
	if _, err := io.CopyBuffer(hasher, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func stringsEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
