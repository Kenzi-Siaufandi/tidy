package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func TestResolveAndDownloadURL(t *testing.T) {
	jarContent := []byte("mock-vault-plugin-content")
	h256 := sha256.Sum256(jarContent)
	expectedSHA256 := hex.EncodeToString(h256[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Vault.jar" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	pCfg := config.PluginConfig{
		Source: "url",
		URL:    server.URL + "/Vault.jar",
		SHA256: expectedSHA256,
	}

	result, err := ResolveAndDownloadURL(context.Background(), "Vault", pCfg, tmpDir, server.Client())
	if err != nil {
		t.Fatalf("url download failed: %v", err)
	}

	if result.Filename != "Vault.jar" {
		t.Errorf("expected filename Vault.jar, got %s", result.Filename)
	}
	if result.Hash != expectedSHA256 {
		t.Errorf("expected hash %s, got %s", expectedSHA256, result.Hash)
	}

	expectedFile := filepath.Join(tmpDir, "plugins", "Vault.jar")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(data) != string(jarContent) {
		t.Errorf("content does not match")
	}
}

func TestResolveAndDownloadURL_MissingSHA256(t *testing.T) {
	pCfg := config.PluginConfig{
		Source: "url",
		URL:    "https://example.com/test.jar",
	}

	_, err := ResolveAndDownloadURL(context.Background(), "Vault", pCfg, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error when sha256 is missing, got nil")
	}
}
