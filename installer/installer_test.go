package installer

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCrypto(t *testing.T) {
	key := DefaultAESKey
	iv := DefaultAESIV

	if _, err := DecryptAES256CBC([]byte("too-short"), key, iv); err == nil {
		t.Fatal("expected error for non-multiple block size")
	}
}

func TestManifestAndVerifier(t *testing.T) {
	tmpDir := t.TempDir()

	file1Path := filepath.Join(tmpDir, "file1.txt")
	file1Content := []byte("hello file 1")
	if err := os.WriteFile(file1Path, file1Content, 0644); err != nil {
		t.Fatal(err)
	}

	hasher := md5.New()
	hasher.Write(file1Content)
	file1MD5 := hex.EncodeToString(hasher.Sum(nil))

	manifestData := fmt.Sprintf(`{"path":"file1.txt","md5":"%s","size":%d}
{"path":"sub/missing.txt","md5":"00112233445566778899aabbccddeeff","size":100}
`, file1MD5, len(file1Content))

	manifest, err := ParseManifest([]byte(manifestData))
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if len(manifest.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(manifest.Entries))
	}

	diffs, err := VerifyGameFiles(context.Background(), tmpDir, manifest, 2, nil)
	if err != nil {
		t.Fatalf("VerifyGameFiles failed: %v", err)
	}

	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff (missing.txt), got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "sub/missing.txt" {
		t.Fatalf("unexpected diff path: %s", diffs[0].Path)
	}
}

func TestStreamExtractorAndResume(t *testing.T) {
	var zipBuf bytes.Buffer

	data1 := bytes.Repeat([]byte("File1 content test "), 40)
	var deflated1 bytes.Buffer
	fw1, _ := flate.NewWriter(&deflated1, flate.DefaultCompression)
	fw1.Write(data1)
	fw1.Close()
	h1 := crc32.NewIEEE()
	h1.Write(data1)
	writeLocalFileHeader(&zipBuf, "dir/file1.txt", 8, h1.Sum32(), uint32(deflated1.Len()), uint32(len(data1)))
	zipBuf.Write(deflated1.Bytes())

	data2 := bytes.Repeat([]byte("File2 second part "), 40)
	var deflated2 bytes.Buffer
	fw2, _ := flate.NewWriter(&deflated2, flate.DefaultCompression)
	fw2.Write(data2)
	fw2.Close()
	h2 := crc32.NewIEEE()
	h2.Write(data2)
	writeLocalFileHeader(&zipBuf, "dir/file2.txt", 8, h2.Sum32(), uint32(deflated2.Len()), uint32(len(data2)))
	zipBuf.Write(deflated2.Bytes())

	zipBuf.Write([]byte{0x50, 0x4b, 0x01, 0x02})

	zipBytes := zipBuf.Bytes()

	splitPoint := len(zipBytes) / 2
	part1 := zipBytes[:splitPoint]
	part2 := zipBytes[splitPoint:]

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		if r.URL.Path == "/part1.zip" {
			data = part1
		} else if r.URL.Path == "/part2.zip" {
			data = part2
		} else {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "pack.zip", time.Now(), bytes.NewReader(data))
	}))
	defer ts.Close()

	destDir := t.TempDir()
	packs := []PackInfo{
		{URL: ts.URL + "/part1.zip", Size: int64(len(part1))},
		{URL: ts.URL + "/part2.zip", Size: int64(len(part2))},
	}

	ctx, cancel := context.WithCancel(context.Background())
	fileCount := 0
	err := StreamInstallPacks(ctx, ts.Client(), packs, destDir, "1.0.0", func(p StreamProgress) {
		fileCount++
		if fileCount == 1 {
			cancel()
		}
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error on pause: %v", err)
	}

	statePath := filepath.Join(destDir, ".arclights.progress.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file to exist after pause: %v", err)
	}

	err = StreamInstallPacks(context.Background(), ts.Client(), packs, destDir, "1.0.0", nil)
	if err != nil {
		t.Fatalf("failed to resume and finish: %v", err)
	}

	f1, err := os.ReadFile(filepath.Join(destDir, "dir", "file1.txt"))
	if err != nil || !bytes.Equal(f1, data1) {
		t.Fatalf("file1 verification failed: err=%v", err)
	}
	f2, err := os.ReadFile(filepath.Join(destDir, "dir", "file2.txt"))
	if err != nil || !bytes.Equal(f2, data2) {
		t.Fatalf("file2 verification failed: err=%v", err)
	}

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected state file to be removed after completion")
	}
}

func TestLiveManifestFetch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}

	cdnURL := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.6/update/6/6/Windows/1.6.4_O0xruPqxQxZ9aEYl/files"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	manifest, err := FetchManifest(ctx, nil, cdnURL)
	if err != nil {
		t.Fatalf("FetchManifest failed: %v", err)
	}

	if len(manifest.Entries) < 1000 {
		t.Fatalf("expected >= 1000 files in manifest, got %d", len(manifest.Entries))
	}
	t.Logf("Successfully fetched and decrypted live manifest: %d files, total size: %.2f GB",
		manifest.TotalCount, float64(manifest.TotalSize)/1024/1024/1024)
}

func TestLiveRepairSingleFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}

	cdnURL := "https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.6/update/6/6/Windows/1.6.4_O0xruPqxQxZ9aEYl/files"
	tmpGameDir := t.TempDir()

	testDiffs := []GameFileEntry{
		{
			Path: "api-ms-win-core-console-l1-1-0.dll",
			MD5:  "07ebe4d5cef3301ccf07430f4c3e32d8",
			Size: 12240,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := RepairGameFiles(ctx, nil, cdnURL, tmpGameDir, testDiffs, 1, nil)
	if err != nil {
		t.Fatalf("RepairGameFiles failed: %v", err)
	}

	downloadedPath := filepath.Join(tmpGameDir, "api-ms-win-core-console-l1-1-0.dll")
	fi, err := os.Stat(downloadedPath)
	if err != nil {
		t.Fatalf("repaired file missing on disk: %v", err)
	}
	if fi.Size() != 12240 {
		t.Fatalf("expected size 12240, got %d", fi.Size())
	}
}

func TestInstallManifestFilesMock(t *testing.T) {
	file1Data := []byte("hello world this is file 1")
	file2Data := []byte("another file content with more data for file 2")

	h1 := md5.Sum(file1Data)
	h2 := md5.Sum(file2Data)

	md5_1 := hex.EncodeToString(h1[:])
	md5_2 := hex.EncodeToString(h2[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sub/file1.txt":
			http.ServeContent(w, r, "file1.txt", time.Now(), bytes.NewReader(file1Data))
		case "/file2.txt":
			http.ServeContent(w, r, "file2.txt", time.Now(), bytes.NewReader(file2Data))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manifest := &Manifest{
		TotalSize:  uint64(len(file1Data) + len(file2Data)),
		TotalCount: 2,
		Entries: []GameFileEntry{
			{Path: "sub/file1.txt", Size: uint64(len(file1Data)), MD5: md5_1},
			{Path: "file2.txt", Size: uint64(len(file2Data)), MD5: md5_2},
		},
	}

	tmpDir := t.TempDir()
	var lastProgress StreamProgress
	err := InstallManifestFiles(context.Background(), server.Client(), server.URL, tmpDir, manifest, 2, 0, func(p StreamProgress) {
		lastProgress = p
	})
	if err != nil {
		t.Fatalf("InstallManifestFiles failed: %v", err)
	}

	if lastProgress.Percent != 100.0 {
		t.Fatalf("expected percent 100, got %f", lastProgress.Percent)
	}

	f1, err := os.ReadFile(filepath.Join(tmpDir, "sub", "file1.txt"))
	if err != nil || string(f1) != string(file1Data) {
		t.Fatalf("file1 content mismatch: %v", err)
	}
	f2, err := os.ReadFile(filepath.Join(tmpDir, "file2.txt"))
	if err != nil || string(f2) != string(file2Data) {
		t.Fatalf("file2 content mismatch: %v", err)
	}

	err = InstallManifestFiles(context.Background(), server.Client(), server.URL, tmpDir, manifest, 2, 0, nil)
	if err != nil {
		t.Fatalf("re-running InstallManifestFiles failed: %v", err)
	}
}

func writeLocalFileHeader(buf *bytes.Buffer, name string, method uint16, crc, compSize, uncompSize uint32) {
	buf.Write([]byte{0x50, 0x4b, 0x03, 0x04})
	buf.Write([]byte{20, 0})
	buf.Write([]byte{0, 0})
	buf.Write([]byte{byte(method), 0})
	buf.Write([]byte{0, 0, 0, 0})
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], crc)
	buf.Write(b[:])
	binary.LittleEndian.PutUint32(b[:], compSize)
	buf.Write(b[:])
	binary.LittleEndian.PutUint32(b[:], uncompSize)
	buf.Write(b[:])
	var l [2]byte
	binary.LittleEndian.PutUint16(l[:], uint16(len(name)))
	buf.Write(l[:])
	binary.LittleEndian.PutUint16(l[:], 0)
	buf.Write(l[:])
	buf.WriteString(name)
}

func TestExtractFile(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	storedContent := []byte("plain stored file content")
	storedHdr := &zip.FileHeader{
		Name:   "plain.txt",
		Method: zip.Store,
	}
	sw, err := zw.CreateHeader(storedHdr)
	if err != nil {
		t.Fatalf("failed to create stored zip entry: %v", err)
	}
	if _, err := sw.Write(storedContent); err != nil {
		t.Fatalf("failed to write stored zip entry: %v", err)
	}

	deflatedContent := bytes.Repeat([]byte("deflated payload content to test range extraction "), 30)
	deflatedHdr := &zip.FileHeader{
		Name:   "nested/deflated.txt",
		Method: zip.Deflate,
	}
	dw, err := zw.CreateHeader(deflatedHdr)
	if err != nil {
		t.Fatalf("failed to create deflated zip entry: %v", err)
	}
	if _, err := dw.Write(deflatedContent); err != nil {
		t.Fatalf("failed to write deflated zip entry: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to finalize zip: %v", err)
	}

	zipBytes := zipBuf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive.zip" {
			http.ServeContent(w, r, "archive.zip", time.Now(), bytes.NewReader(zipBytes))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	archiveURL := server.URL + "/archive.zip"
	destDir := t.TempDir()

	destStored := filepath.Join(destDir, "plain.txt")
	if err := ExtractFile(archiveURL, "plain.txt", destStored); err != nil {
		t.Fatalf("ExtractFile plain.txt failed: %v", err)
	}
	gotStored, err := os.ReadFile(destStored)
	if err != nil || !bytes.Equal(gotStored, storedContent) {
		t.Fatalf("stored file content mismatch: err=%v, got=%q", err, string(gotStored))
	}

	destDeflated := filepath.Join(destDir, "nested_out", "deflated.txt")
	if err := ExtractFileWithContext(context.Background(), server.Client(), archiveURL, "nested/deflated.txt", destDeflated); err != nil {
		t.Fatalf("ExtractFileWithContext deflated failed: %v", err)
	}
	gotDeflated, err := os.ReadFile(destDeflated)
	if err != nil || !bytes.Equal(gotDeflated, deflatedContent) {
		t.Fatalf("deflated file content mismatch: err=%v", err)
	}

	destMissing := filepath.Join(destDir, "missing.txt")
	if err := ExtractFile(archiveURL, "not_in_zip.txt", destMissing); err == nil {
		t.Fatalf("expected error for non-existent file, got nil")
	}

	destUnsafe := filepath.Join(destDir, "unsafe.txt")
	if err := ExtractFile(archiveURL, "../evil.txt", destUnsafe); err == nil {
		t.Fatalf("expected error for unsafe remote path, got nil")
	}

	if err := ExtractFile(server.URL+"/404.zip", "plain.txt", filepath.Join(destDir, "err404.txt")); err == nil {
		t.Fatalf("expected error for 404 archive URL, got nil")
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ExtractFileWithContext(cancelledCtx, server.Client(), archiveURL, "plain.txt", filepath.Join(destDir, "canc.txt")); err == nil {
		t.Fatalf("expected error for cancelled context, got nil")
	}
}
