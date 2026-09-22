package resolver

import (
	"context"
	"crypto/md5"
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
	"strconv"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/progress"
)

// DownloadOptions contains options for downloading and verifying a file.
type DownloadOptions struct {
	URL           string
	TargetPath    string
	ExpectedHash  string
	HashAlgorithm string // "sha256", "sha512", "sha1", "md5"
	Headers       map[string]string
	Client        *http.Client
	// ProgressLabel enables a stderr progress bar for this download (see
	// internal/progress). Empty disables it. Rendering is globally gated by
	// progress.Enabled, which the CLI sets and tests leave off.
	ProgressLabel string
}

// DownloadResult contains details about the completed download.
type DownloadResult struct {
	Path         string
	ComputedHash string
	Size         int64
}

// DownloadAndVerify downloads a file from URL to TargetPath, streaming through a hasher,
// verifying the checksum before finalizing the file into place.
//
// Interrupted downloads resume: a leftover "<target>.tidy-tmp" from a killed
// run is continued via an HTTP Range request (when the server honors it), so
// slow links converge across restarts instead of restarting from zero. The
// partial file is kept on stream errors and discarded on hash mismatch.
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
	newHasher := func() (hash.Hash, error) {
		switch algo {
		case "sha256":
			return sha256.New(), nil
		case "sha512":
			return sha512.New(), nil
		case "sha1":
			return sha1.New(), nil
		case "md5":
			// Legacy APIs (e.g. PurpurMC) publish only MD5. Verified upstream,
			// then re-hashed locally as SHA-256 for state tracking.
			return md5.New(), nil
		default:
			return nil, fmt.Errorf("unsupported hash algorithm %q (supported: sha256, sha512, sha1, md5)", opts.HashAlgorithm)
		}
	}
	if _, err := newHasher(); err != nil {
		return nil, err
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	dir := filepath.Dir(opts.TargetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	// Write to temporary file first
	tmpPath := opts.TargetPath + ".tidy-tmp"
	label := strings.TrimSpace(opts.ProgressLabel)

	// A partial tmp file means a previous run was interrupted (killed or
	// timed out); try to continue it instead of starting over.
	var resumeOffset int64
	if info, err := os.Stat(tmpPath); err == nil && info.Size() > 0 {
		resumeOffset = info.Size()
	}

	// At most two attempts: the initial request, plus one clean restart if
	// the server answers unexpectedly (ignored Range, unsatisfiable offset).
	for attempt := 0; ; attempt++ {
		clean := attempt > 0
		offset := resumeOffset
		if clean {
			offset = 0
			_ = os.Remove(tmpPath)
		}

		hasher, err := newHasher()
		if err != nil {
			return nil, err
		}
		if offset > 0 {
			// Seed the hash with the already-downloaded prefix.
			if !seedHasherWithFile(hasher, tmpPath) {
				offset = 0
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP request for %s: %w", opts.URL, err)
		}
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP download request failed for %s: %w", opts.URL, err)
		}

		appendMode := false
		switch resp.StatusCode {
		case http.StatusPartialContent:
			if offset <= 0 {
				resp.Body.Close()
				return nil, fmt.Errorf("download failed for %s: unexpected HTTP 206 without a Range request", opts.URL)
			}
			if start, ok := parseRangeStart(resp.Header.Get("Content-Range")); !ok || start != offset {
				resp.Body.Close()
				if attempt == 0 {
					continue // stale partial file; retry from scratch
				}
				return nil, fmt.Errorf("download failed for %s: server resumed from byte %d, requested %d", opts.URL, start, offset)
			}
			appendMode = true
		case http.StatusOK:
			if offset > 0 {
				// Server ignored the Range request; restart from scratch.
				offset = 0
				_ = os.Remove(tmpPath)
				hasher, err = newHasher()
				if err != nil {
					resp.Body.Close()
					return nil, err
				}
			}
		case http.StatusRequestedRangeNotSatisfiable:
			resp.Body.Close()
			_ = os.Remove(tmpPath)
			if attempt == 0 {
				continue // stale partial file (e.g. newer remote); retry from scratch
			}
			return nil, fmt.Errorf("download failed for %s: HTTP %d %s", opts.URL, resp.StatusCode, resp.Status)
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("download failed for %s: HTTP %d %s", opts.URL, resp.StatusCode, resp.Status)
		}

		total := resp.ContentLength
		if appendMode && total >= 0 {
			total += offset
		}
		announceDownload(label, offset, total)

		flags := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		tmpFile, err := os.OpenFile(tmpPath, flags, 0644)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to create temporary file %q: %w", tmpPath, err)
		}
		if appendMode {
			if info, err := tmpFile.Stat(); err != nil || info.Size() != offset {
				tmpFile.Close()
				resp.Body.Close()
				_ = os.Remove(tmpPath)
				if attempt == 0 {
					continue // partial file changed under us; retry from scratch
				}
				return nil, fmt.Errorf("failed to resume download into %q: partial file size changed", tmpPath)
			}
		}

		// Stream write while computing hash, with an optional progress bar.
		bar := progress.New(label, total)
		bar.ResumeFrom(offset)
		tee := io.TeeReader(resp.Body, io.MultiWriter(hasher, bar))
		written, copyErr := io.Copy(tmpFile, tee)
		closeErr := tmpFile.Close()
		resp.Body.Close()

		if copyErr != nil {
			bar.Abort()
			// Keep the partial file so the next run resumes instead of
			// restarting.
			return nil, fmt.Errorf("download stream error for %s: %w", opts.URL, copyErr)
		}
		if closeErr != nil {
			bar.Abort()
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to close temporary file %q: %w", tmpPath, closeErr)
		}

		computedHex := hex.EncodeToString(hasher.Sum(nil))
		expectedHex := strings.ToLower(strings.TrimSpace(opts.ExpectedHash))

		if subtle.ConstantTimeCompare([]byte(computedHex), []byte(expectedHex)) != 1 {
			bar.Abort()
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("hash verification failed for %s: expected %s (%s), computed %s",
				filepath.Base(opts.TargetPath), expectedHex, algo, computedHex)
		}
		bar.Finish()

		// Move into destination atomically
		if err := os.Rename(tmpPath, opts.TargetPath); err != nil {
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("failed to move verified file into %q: %w", opts.TargetPath, err)
		}

		return &DownloadResult{
			Path:         opts.TargetPath,
			ComputedHash: computedHex,
			Size:         offset + written,
		}, nil
	}
}

// seedHasherWithFile streams an existing partial download through the hasher.
// It reports false when the file is unreadable, in which case the caller
// restarts from scratch.
func seedHasherWithFile(hasher hash.Hash, tmpPath string) bool {
	f, err := os.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return false
	}
	_, err = io.Copy(hasher, f)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		return false
	}
	return true
}

// parseRangeStart extracts the start offset from a Content-Range header such
// as "bytes 100-199/1000".
func parseRangeStart(header string) (int64, bool) {
	v := strings.TrimSpace(header)
	const prefix = "bytes "
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return 0, false
	}
	rest := strings.TrimSpace(v[len(prefix):])
	dash := strings.Index(rest, "-")
	if dash <= 0 {
		return 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(rest[:dash]), 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	return start, true
}

// announceDownload logs the download size (and resume point) once headers
// arrive, so a stalled transfer is diagnosable instead of a silent hang.
// It only prints for CLI runs with a labeled download.
func announceDownload(label string, offset, total int64) {
	if !progress.Enabled || label == "" {
		return
	}
	size := "unknown size"
	if total >= 0 {
		size = progress.FormatBytes(total)
	}
	if offset > 0 {
		fmt.Printf("  %s (resuming from %s of %s)\n", label, progress.FormatBytes(offset), size)
	} else {
		fmt.Printf("  %s (%s)\n", label, size)
	}
}
