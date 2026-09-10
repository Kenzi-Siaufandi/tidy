package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

// ResolveAndDownloadURL downloads a plugin from a direct HTTP/HTTPS URL and verifies its SHA256 checksum.
func ResolveAndDownloadURL(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string, client *http.Client) (*PluginDownloadResult, error) {
	if strings.TrimSpace(pCfg.URL) == "" {
		return nil, fmt.Errorf("plugin %q: direct URL cannot be empty", pluginName)
	}

	expectedSHA256 := strings.TrimSpace(pCfg.SHA256)
	if expectedSHA256 == "" {
		return nil, fmt.Errorf("plugin %q: direct URL source requires 'sha256' checksum", pluginName)
	}

	u, err := url.Parse(pCfg.URL)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: invalid URL %q: %w", pluginName, pCfg.URL, err)
	}

	// Extract filename from URL path or fallback to <pluginName>.jar
	filename := path.Base(u.Path)
	if filename == "" || filename == "/" || filename == "." || !strings.HasSuffix(strings.ToLower(filename), ".jar") {
		filename = pluginName + ".jar"
	}

	targetPath := filepath.Join(workDir, "plugins", filename)

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           pCfg.URL,
		TargetPath:    targetPath,
		ExpectedHash:  expectedSHA256,
		HashAlgorithm: "sha256",
		Client:        client,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download and verify plugin %s from URL: %w", pluginName, err)
	}

	return &PluginDownloadResult{
		PluginName: pluginName,
		Source:     "url",
		Filename:   filename,
		FilePath:   res.Path,
		HashAlgo:   "sha256",
		Hash:       res.ComputedHash,
		Size:       res.Size,
	}, nil
}
