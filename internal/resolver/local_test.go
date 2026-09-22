package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func writeLocalJar(t *testing.T, dir, rel string, content []byte) string {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, content, 0644); err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestVerifyLocalPlugin_Success(t *testing.T) {
	tmpDir := t.TempDir()
	content := []byte("mock-manual-plugin")
	h := sha256.Sum256(content)
	sha := hex.EncodeToString(h[:])
	writeLocalJar(t, tmpDir, "plugins/MyPlugin.jar", content)

	pCfg := config.PluginConfig{Source: "local", Path: "plugins/MyPlugin.jar", SHA256: sha}
	res, err := VerifyLocalPlugin("MyPlugin", pCfg, tmpDir)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if res.Filename != "MyPlugin.jar" {
		t.Errorf("expected filename MyPlugin.jar, got %s", res.Filename)
	}
	if res.SHA256 != sha {
		t.Errorf("expected sha %s, got %s", sha, res.SHA256)
	}
	if res.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), res.Size)
	}
}

func TestVerifyLocalPlugin_HashMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	writeLocalJar(t, tmpDir, "plugins/MyPlugin.jar", []byte("real-content"))

	pCfg := config.PluginConfig{
		Source: "local",
		Path:   "plugins/MyPlugin.jar",
		SHA256: strings.Repeat("0", 64),
	}
	if _, err := VerifyLocalPlugin("MyPlugin", pCfg, tmpDir); err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
}

func TestVerifyLocalPlugin_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	pCfg := config.PluginConfig{
		Source: "local",
		Path:   "plugins/Missing.jar",
		SHA256: strings.Repeat("a", 64),
	}
	if _, err := VerifyLocalPlugin("MyPlugin", pCfg, tmpDir); err == nil {
		t.Fatal("expected missing-file error, got nil")
	}
}

func TestVerifyLocalPlugin_MissingSHA(t *testing.T) {
	tmpDir := t.TempDir()
	writeLocalJar(t, tmpDir, "plugins/MyPlugin.jar", []byte("x"))
	pCfg := config.PluginConfig{Source: "local", Path: "plugins/MyPlugin.jar"}
	if _, err := VerifyLocalPlugin("MyPlugin", pCfg, tmpDir); err == nil {
		t.Fatal("expected missing-sha error, got nil")
	}
}

func TestResolveLocalPath_EscapesWorkdir(t *testing.T) {
	tmpDir := t.TempDir()
	pCfg := config.PluginConfig{Source: "local", Path: "../escape.jar", SHA256: strings.Repeat("a", 64)}
	if _, _, err := ResolveLocalPath(pCfg, tmpDir); err == nil {
		t.Fatal("expected workdir-escape error, got nil")
	}
}

func TestResolveLocalPath_NonJar(t *testing.T) {
	tmpDir := t.TempDir()
	pCfg := config.PluginConfig{Source: "local", Path: "plugins/readme.txt", SHA256: strings.Repeat("a", 64)}
	if _, _, err := ResolveLocalPath(pCfg, tmpDir); err == nil {
		t.Fatal("expected non-jar error, got nil")
	}
}
