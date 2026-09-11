package storage

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
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

// SyncFile synchronizes a large file or world asset to the target destination.
func (m *Manager) SyncFile(ctx context.Context, name string, fCfg config.FileConfig, workDir string) (*SyncResult, error) {
	destPath := fCfg.Path
	if !filepath.IsAbs(destPath) {
		destPath = filepath.Join(workDir, destPath)
	}

	if info, err := os.Stat(destPath); err == nil {
		if fCfg.Extract && !info.IsDir() {
			return nil, fmt.Errorf("file/world %q: extract=true requires a directory path, got file %q", name, fCfg.Path)
		}
		if !fCfg.Extract && info.IsDir() && !fCfg.IsOnce() {
			return nil, fmt.Errorf("file/world %q: extract=false requires a file path, got directory %q", name, fCfg.Path)
		}
	}

	// 1. Check if target exists and once=true (protect existing world/file data).
	// Skipped targets are unverified: SHA256 stays empty so state never
	// records the config hash as if it were checked.
	if fCfg.IsOnce() {
		if _, err := os.Stat(destPath); err == nil {
			return &SyncResult{
				Name:    name,
				Path:    destPath,
				SHA256:  "",
				Skipped: true,
				Reason:  "target path already exists on disk (once=true, unverified)",
			}, nil
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

		dlRes, err := resolver.DownloadAndVerify(ctx, resolver.DownloadOptions{
			URL:           fCfg.URL,
			TargetPath:    tmpArchive,
			ExpectedHash:  fCfg.SHA256,
			HashAlgorithm: "sha256",
			Client:        m.HTTPClient,
		})
		if err != nil {
			return nil, fmt.Errorf("failed downloading archive for %s: %w", name, err)
		}

		// Extract into target directory
		count, extErr := ExtractArchive(dlRes.Path, destPath)
		_ = os.Remove(dlRes.Path) // Clean up downloaded temporary archive

		if extErr != nil {
			return nil, fmt.Errorf("failed extracting archive for %s into %s: %w", name, destPath, extErr)
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
