package storage

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

// SyncResult contains details about the file or world sync operation.
type SyncResult struct {
	Name      string
	Path      string
	SHA256    string
	Skipped   bool
	Reason    string
	Extracted bool
	Files     int
}

// Manager orchestrates large files and world downloads with mandatory SHA-256 verification.
type Manager struct {
	HTTPClient *http.Client
}

// NewManager creates a new storage manager.
func NewManager(client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Manager{HTTPClient: client}
}

// resolveDestPath resolves a [files.*]/[worlds.*] destination against workDir
// and guarantees the result stays inside workDir. Absolute destinations are
// allowed only when they resolve inside workDir; parent traversals escape and
// are rejected, mirroring resolver.ResolveLocalPath.
func resolveDestPath(workDir, configuredPath string) (string, error) {
	raw := configuredPath
	if raw == "" {
		return "", fmt.Errorf("destination path cannot be empty")
	}
	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workdir: %w", err)
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workAbs, raw)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(workAbs, abs)
	if err != nil {
		return "", fmt.Errorf("destination path %q cannot be resolved: %w", configuredPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("destination path %q must stay inside the workdir", configuredPath)
	}
	// Symlink-aware containment: resolve the nearest existing ancestor and
	// ensure it (plus the lexical remainder) stays inside the real workdir.
	realWork := workAbs
	if rw, err := filepath.EvalSymlinks(workAbs); err == nil {
		realWork = rw
	}
	existing := abs
	for {
		if _, lerr := os.Lstat(existing); lerr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
		if existing == workAbs || existing == filepath.Dir(workAbs) {
			// Reached (or passed) the workdir; stop walking.
			if _, lerr := os.Lstat(existing); lerr == nil {
				break
			}
		}
	}
	if _, lerr := os.Lstat(existing); lerr == nil {
		if realExisting, err := filepath.EvalSymlinks(existing); err == nil {
			if rrel, rerr := filepath.Rel(realWork, realExisting); rerr != nil || rrel == ".." || strings.HasPrefix(rrel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rrel) {
				if rrel != "." {
					return "", fmt.Errorf("destination path %q must stay inside the workdir", configuredPath)
				}
			}
			// Re-anchor the non-existent remainder onto the resolved ancestor.
			if rem, rerr := filepath.Rel(existing, abs); rerr == nil && rem != "." {
				realAbs := filepath.Join(realExisting, rem)
				if rrel2, rerr := filepath.Rel(realWork, realAbs); rerr != nil || rrel2 == ".." || strings.HasPrefix(rrel2, ".."+string(os.PathSeparator)) || filepath.IsAbs(rrel2) {
					return "", fmt.Errorf("destination path %q must stay inside the workdir", configuredPath)
				}
			}
		}
	}
	return abs, nil
}

// SyncFile synchronizes a large file or world asset to the target destination.
func (m *Manager) SyncFile(ctx context.Context, name string, fCfg config.FileConfig, workDir string) (*SyncResult, error) {
	return m.SyncFileWithState(ctx, name, fCfg, workDir, nil)
}

// SyncFileWithState is SyncFile with previous state for idempotency:
// once=false skips when local hash already matches; once=true re-syncs when
// url/sha256 changed versus previous state, otherwise protects existing data.
func (m *Manager) SyncFileWithState(ctx context.Context, name string, fCfg config.FileConfig, workDir string, prev *state.FileState) (*SyncResult, error) {
	destPath, err := resolveDestPath(workDir, fCfg.Path)
	if err != nil {
		return nil, fmt.Errorf("file/world %q: %w", name, err)
	}

	if info, err := os.Stat(destPath); err == nil {
		if fCfg.Extract && !info.IsDir() {
			return nil, fmt.Errorf("file/world %q: extract=true requires a directory path, got file %q", name, fCfg.Path)
		}
		if !fCfg.Extract && info.IsDir() && !fCfg.IsOnce() {
			return nil, fmt.Errorf("file/world %q: extract=false requires a file path, got directory %q", name, fCfg.Path)
		}
	}

	// 1. Idempotency against previous state and local disk.
	wantSHA := strings.ToLower(strings.TrimSpace(fCfg.SHA256))
	wantURL := strings.TrimSpace(fCfg.URL)
	if _, statErr := os.Stat(destPath); statErr == nil {
		if fCfg.IsOnce() {
			// once=true protects existing data, but a url/sha256 change
			// versus previous state must take effect (otherwise config
			// edits are silently ignored).
			if prev != nil && strings.TrimSpace(prev.SHA256) != "" {
				prevSHA := strings.ToLower(strings.TrimSpace(prev.SHA256))
				prevURL := strings.TrimSpace(prev.URL)
				if prevSHA == wantSHA && (prevURL == "" || prevURL == wantURL) {
					return &SyncResult{
						Name:    name,
						Path:    destPath,
						SHA256:  prev.SHA256,
						Skipped: true,
						Reason:  "target path already exists and matches state (once=true)",
					}, nil
				}
				// Config changed vs state: fall through to re-download.
			} else {
				// No state (first install with pre-existing files): protect.
				return &SyncResult{
					Name:    name,
					Path:    destPath,
					SHA256:  "",
					Skipped: true,
					Reason:  "target path already exists on disk (once=true, unverified)",
				}, nil
			}
		} else {
			// once=false: skip when local content already matches wantSHA.
			if !fCfg.Extract {
				if computed, err := state.ComputeFileSHA256(destPath); err == nil && strings.ToLower(computed) == wantSHA {
					return &SyncResult{
						Name:    name,
						Path:    destPath,
						SHA256:  computed,
						Skipped: true,
						Reason:  "local file already matches sha256 (verified)",
					}, nil
				}
			} else if prev != nil && strings.EqualFold(strings.TrimSpace(prev.SHA256), wantSHA) {
				return &SyncResult{
					Name:    name,
					Path:    destPath,
					SHA256:  prev.SHA256,
					Skipped: true,
					Reason:  "world already matches state sha256 (once=false)",
				}, nil
			}
		}
	}

	// 2. Download and verify with mandatory SHA-256
	if fCfg.Extract {
		// Download to a temporary archive file first
		tidyDir := filepath.Join(workDir, ".tidy")
		if err := os.MkdirAll(tidyDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create .tidy directory: %w", err)
		}

		safeName, err := resolver.SafeArchiveName(name)
		if err != nil {
			return nil, err
		}
		tmpArchive := filepath.Join(tidyDir, safeName+".archive.tmp")
		staging := filepath.Join(tidyDir, safeName+".staging.tmp")

		dlRes, err := resolver.DownloadAndVerify(ctx, resolver.DownloadOptions{
			URL:           fCfg.URL,
			TargetPath:    tmpArchive,
			ExpectedHash:  fCfg.SHA256,
			HashAlgorithm: "sha256",
			Client:        m.HTTPClient,
			ProgressLabel: name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed downloading archive for %s: %w", name, err)
		}

		// Atomic extraction: unpack into a staging dir inside .tidy/, then
		// promote to the final destination only after success. A failed or
		// interrupted extraction never leaves a half-written world behind.
		_ = os.RemoveAll(staging)
		count, extErr := ExtractArchive(dlRes.Path, staging)
		_ = os.Remove(dlRes.Path) // Clean up downloaded temporary archive

		if extErr != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("failed extracting archive for %s into %s: %w", name, destPath, extErr)
		}

		// Replace any existing destination only after a successful extract.
		if _, serr := os.Lstat(destPath); serr == nil {
			if rerr := os.RemoveAll(destPath); rerr != nil {
				_ = os.RemoveAll(staging)
				return nil, fmt.Errorf("failed removing existing destination for %s at %s: %w", name, destPath, rerr)
			}
		}
		if merr := os.MkdirAll(filepath.Dir(destPath), 0755); merr != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("failed creating parent for %s at %s: %w", name, destPath, merr)
		}
		if rerr := os.Rename(staging, destPath); rerr != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("failed promoting staged extraction for %s into %s: %w", name, destPath, rerr)
		}

		// Automatically remediate stale session.lock files in extracted world directories
		_, _ = CleanStaleSessionLock(destPath)

		return &SyncResult{
			Name:      name,
			Path:      destPath,
			SHA256:    dlRes.ComputedHash,
			Extracted: true,
			Files:     count,
		}, nil
	}

	// Single file download directly to destination path
	dlRes, err := resolver.DownloadAndVerify(ctx, resolver.DownloadOptions{
		URL:           fCfg.URL,
		TargetPath:    destPath,
		ExpectedHash:  fCfg.SHA256,
		HashAlgorithm: "sha256",
		Client:        m.HTTPClient,
		ProgressLabel: name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed downloading file for %s: %w", name, err)
	}

	return &SyncResult{
		Name:      name,
		Path:      destPath,
		SHA256:    dlRes.ComputedHash,
		Extracted: false,
		Files:     1,
	}, nil
}
