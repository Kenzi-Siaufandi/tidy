package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func TestPaperClient_ResolveAndDownload(t *testing.T) {
	jarContent := []byte("mock-paper-26.2-123-jar-content")
	h := sha256.Sum256(jarContent)
	expectedSHA256 := hex.EncodeToString(h[:])

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/projects/paper/versions/26.2/builds/latest":
			resp := FillBuildResponse{
				ID:      123,
				Channel: "STABLE",
				Downloads: map[string]FillDownload{
					"server:default": {
						Name: "paper-26.2-123.jar",
						Size: int64(len(jarContent)),
						URL:  server.URL + "/download/paper-26.2-123.jar",
						Checksums: FillChecksums{
							SHA256: expectedSHA256,
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/download/paper-26.2-123.jar":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewPaperClient(server.URL, server.Client())
	tmpDir := t.TempDir()

	cfg := config.ServerConfig{
		Project: "paper",
		Version: "26.2",
		Build:   "latest",
	}

	result, err := client.ResolveAndDownload(context.Background(), cfg, tmpDir)
	if err != nil {
		t.Fatalf("resolve and download failed: %v", err)
	}

	if result.Filename != "paper-26.2-123.jar" {
		t.Errorf("expected filename paper-26.2-123.jar, got %s", result.Filename)
	}
	if result.BuildID != 123 {
		t.Errorf("expected build 123, got %d", result.BuildID)
	}
	if result.SHA256 != expectedSHA256 {
		t.Errorf("expected hash %s, got %s", expectedSHA256, result.SHA256)
	}

	// Verify file on disk
	data, err := os.ReadFile(filepath.Join(tmpDir, "paper-26.2-123.jar"))
	if err != nil {
		t.Fatalf("failed to read downloaded jar: %v", err)
	}
	if string(data) != string(jarContent) {
		t.Errorf("downloaded content does not match expected")
	}
}
