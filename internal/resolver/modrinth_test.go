package resolver

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func TestModrinthClient_ResolveAndDownload(t *testing.T) {
	jarContent := []byte("mock-fawe-plugin-content")
	h512 := sha512.Sum512(jarContent)
	expectedSHA512 := hex.EncodeToString(h512[:])

	var receivedUserAgent string

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUserAgent = r.Header.Get("User-Agent")

		switch r.URL.Path {
		case "/v2/project/fastasyncworldedit/version":
			versions := []ModrinthVersion{
				{
					ID:            "ver-2.11.2",
					ProjectID:     "fastasyncworldedit",
					Name:          "FastAsyncWorldEdit 2.11.2",
					VersionNumber: "2.11.2",
					Files: []ModrinthFile{
						{
							Filename: "FastAsyncWorldEdit-Bukkit-2.11.2.jar",
							Primary:  true,
							Size:     int64(len(jarContent)),
							URL:      server.URL + "/download/fawe.jar",
							Hashes: map[string]string{
								"sha512": expectedSHA512,
							},
						},
					},
				},
				{
					ID:            "ver-2.11.1",
					ProjectID:     "fastasyncworldedit",
					Name:          "FastAsyncWorldEdit 2.11.1",
					VersionNumber: "2.11.1",
					Files: []ModrinthFile{
						{
							Filename: "FastAsyncWorldEdit-Bukkit-2.11.1.jar",
							Primary:  true,
							Size:     10,
							URL:      server.URL + "/download/fawe-old.jar",
							Hashes: map[string]string{
								"sha512": "dummy",
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(versions)
		case "/download/fawe.jar":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	customUA := "Kenzi-Siaufandi/tidy/0.1.0 (https://github.com/Kenzi-Siaufandi)"
	client := NewModrinthClient(server.URL, customUA, server.Client())
	tmpDir := t.TempDir()

	pCfg := config.PluginConfig{
		Source:    "modrinth",
		ProjectID: "fastasyncworldedit",
		Version:   "2.11.2",
	}

	result, err := client.ResolveAndDownload(context.Background(), "FastAsyncWorldEdit", pCfg, tmpDir)
	if err != nil {
		t.Fatalf("modrinth resolve and download failed: %v", err)
	}

	if !strings.Contains(receivedUserAgent, "Kenzi-Siaufandi") {
		t.Errorf("expected User-Agent with 'Kenzi-Siaufandi', got %q", receivedUserAgent)
	}
	if result.Filename != "FastAsyncWorldEdit-Bukkit-2.11.2.jar" {
		t.Errorf("expected filename FastAsyncWorldEdit-Bukkit-2.11.2.jar, got %s", result.Filename)
	}
	if result.HashAlgo != "sha512" {
		t.Errorf("expected hash algo sha512, got %s", result.HashAlgo)
	}
	if result.Hash != expectedSHA512 {
		t.Errorf("expected hash %s, got %s", expectedSHA512, result.Hash)
	}

	// Verify plugin jar inside plugins directory
	expectedFile := filepath.Join(tmpDir, "plugins", "FastAsyncWorldEdit-Bukkit-2.11.2.jar")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("failed to read downloaded plugin: %v", err)
	}
	if string(data) != string(jarContent) {
		t.Errorf("content does not match")
	}
}
