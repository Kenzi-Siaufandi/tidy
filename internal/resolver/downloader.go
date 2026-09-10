package resolver

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadOptions contains options for downloading and verifying a file.
type DownloadOptions struct {
	URL           string
	TargetPath    string
	ExpectedHash  string
	HashAlgorithm string // "sha256", "sha512", "sha1"
	Headers       map[string]string
	Client        *http.Client
}

// DownloadResult contains details about the completed download.
type DownloadResult struct {
	Path         string
	ComputedHash string
	Size         int64
}

// DownloadAndVerify downloads a file from URL to TargetPath, streaming through a hasher,
// verifying the checksum before finalizing the file into place.
func DownloadAndVerify(ctx context.Context, opts DownloadOptions) (*DownloadResult, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("download URL cannot be empty")
	}
	if opts.TargetPath == "" {
		return nil, fmt.Errorf("target path cannot be empty")
	}
	if opts.ExpectedHash == "" {
		return nil, fmt.Errorf("expected hash cannot be empty")
	}

	algo := strings.ToLower(strings.TrimSpace(opts.HashAlgorithm))
	var hasher hash.Hash
	switch algo {
	case "sha256":
		hasher = sha256.New()
	case "sha512":
		hasher = sha512.New()
	case "sha1":
		hasher = sha1.New()
	default:
		return nil, fmt.Errorf("unsupported hash algorithm %q (supported: sha256, sha512, sha1)", opts.HashAlgorithm)
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request for %s: %w", opts.URL, err)
	}

	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP download request failed for %s: %w", opts.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed for %s: HTTP %d %s", opts.URL, resp.StatusCode, resp.Status)
	}

	dir := filepath.Dir(opts.TargetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	// Write to temporary file first
	tmpPath := opts.TargetPath + ".tidy-tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file %q: %w", tmpPath, err)
	}

	// Stream write while computing hash
	tee := io.TeeReader(resp.Body, hasher)
	written, copyErr := io.Copy(tmpFile, tee)
	closeErr := tmpFile.Close()

	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("download stream error for %s: %w", opts.URL, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to close temporary file %q: %w", tmpPath, closeErr)
	}

	computedHex := hex.EncodeToString(hasher.Sum(nil))
	expectedHex := strings.ToLower(strings.TrimSpace(opts.ExpectedHash))

	if subtle.ConstantTimeCompare([]byte(computedHex), []byte(expectedHex)) != 1 {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("hash verification failed for %s: expected %s (%s), computed %s",
			filepath.Base(opts.TargetPath), expectedHex, algo, computedHex)
	}

	// Move into destination atomically
	if err := os.Rename(tmpPath, opts.TargetPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to move verified file into %q: %w", opts.TargetPath, err)
	}

	return &DownloadResult{
		Path:         opts.TargetPath,
		ComputedHash: computedHex,
		Size:         written,
	}, nil
}
