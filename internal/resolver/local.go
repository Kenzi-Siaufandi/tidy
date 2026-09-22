package resolver

import (
	"crypto/subtle"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

// ResolveLocalPath resolves a local plugin's configured path against workDir
// and guarantees the result stays inside workDir. It returns the absolute
// path and the sanitized base filename.
func ResolveLocalPath(pCfg config.PluginConfig, workDir string) (string, string, error) {
	raw := strings.TrimSpace(pCfg.Path)
	if raw == "" {
		return "", "", fmt.Errorf("local source requires 'path' to the manually-uploaded jar")
	}
	if !strings.HasSuffix(strings.ToLower(raw), ".jar") {
		return "", "", fmt.Errorf("local source 'path' %q must point to a .jar file", pCfg.Path)
	}

	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve workdir: %w", err)
	}

	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workAbs, raw)
	}
	abs = filepath.Clean(abs)

	rel, err := filepath.Rel(workAbs, abs)
	if err != nil {
		return "", "", fmt.Errorf("local source 'path' %q cannot be resolved: %w", pCfg.Path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("local source 'path' %q must stay inside the workdir", pCfg.Path)
	}

	filename, err := SafeFilename(filepath.Base(abs), "")
	if err != nil {
		return "", "", fmt.Errorf("invalid local plugin filename: %w", err)
	}
	return abs, filename, nil
}

// VerifyLocalPlugin checks that a manually-uploaded jar exists at the
// configured path and matches the mandatory SHA-256 pin. Nothing is
// downloaded; a missing or tampered file is a fatal error.
func VerifyLocalPlugin(pluginName string, pCfg config.PluginConfig, workDir string) (*PluginDownloadResult, error) {
	expectedSHA256 := strings.TrimSpace(pCfg.SHA256)
	if expectedSHA256 == "" {
		return nil, fmt.Errorf("plugin %q: local source requires 'sha256' checksum", pluginName)
	}

	absPath, filename, err := ResolveLocalPath(pCfg, workDir)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", pluginName, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("plugin %q: local jar not found at %q (upload it manually, then re-run; verify with: sha256sum %s)",
				pluginName, pCfg.Path, filename)
		}
		return nil, fmt.Errorf("plugin %q: cannot stat local jar %q: %w", pluginName, pCfg.Path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("plugin %q: local 'path' %q is a directory, expected a .jar file", pluginName, pCfg.Path)
	}

	computed, err := state.ComputeFileSHA256(absPath)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: failed to hash local jar %q: %w", pluginName, pCfg.Path, err)
	}

	if subtle.ConstantTimeCompare([]byte(strings.ToLower(computed)), []byte(strings.ToLower(expectedSHA256))) != 1 {
		return nil, fmt.Errorf("plugin %q: SHA-256 verification failed for local jar %q: expected %s, computed %s",
			pluginName, pCfg.Path, strings.ToLower(expectedSHA256), computed)
	}

	return &PluginDownloadResult{
		PluginName: pluginName,
		Source:     "local",
		Filename:   filename,
		FilePath:   absPath,
		HashAlgo:   "sha256",
		Hash:       computed,
		SHA256:     computed,
		Size:       info.Size(),
	}, nil
}
