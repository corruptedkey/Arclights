package installer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GameFileEntry represents a file verification record from game_files manifest.
type GameFileEntry struct {
	Path string `json:"path"`
	MD5  string `json:"md5"`
	Size uint64 `json:"size"`
}

// Manifest represents the complete game integrity specification.
type Manifest struct {
	Entries    []GameFileEntry
	TotalSize  uint64
	TotalCount int
}

// FetchManifest downloads and decrypts the game_files manifest from the CDN endpoint.
// filePath should be the base URL to loose files, e.g. "https://beyond.hg-cdn.com/.../files".
func FetchManifest(ctx context.Context, client *http.Client, filePath string) (*Manifest, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	manifestURL := strings.TrimRight(filePath, "/") + "/game_files"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifest request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Arclights/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch game_files from %s: %w", manifestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d for %s", resp.StatusCode, manifestURL)
	}

	encryptedData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read encrypted manifest body: %w", err)
	}

	decrypted, err := DecryptAES256CBC(encryptedData, DefaultAESKey, DefaultAESIV)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt game_files manifest: %w", err)
	}

	return ParseManifest(decrypted)
}

// ParseManifest parses newline-delimited JSON (NDJSON) containing GameFileEntry records.
func ParseManifest(data []byte) (*Manifest, error) {
	var entries []GameFileEntry
	var totalSize uint64

	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry GameFileEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse manifest line %d failed: %w", lineNum, err)
		}

		entries = append(entries, entry)
		totalSize += entry.Size
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning manifest lines: %w", err)
	}

	return &Manifest{
		Entries:    entries,
		TotalSize:  totalSize,
		TotalCount: len(entries),
	}, nil
}
