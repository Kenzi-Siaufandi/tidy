package storage

import (
	"archive/zip"
	"bytes"
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

func TestManager_SkipExistingWorld(t *testing.T) {
	tmpDir := t.TempDir()
	worldDir := filepath.Join(tmpDir, "world")
	if err := os.MkdirAll(worldDir, 0755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(nil)
	onceTrue := true
	cfg := config.FileConfig{
		Path:    "world",
		URL:     "https://example.com/world.tar.gz",
		SHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Extract: true,
		Once:    &onceTrue,
	}

	res, err := mgr.SyncFile(context.Background(), "overworld", cfg, tmpDir)
	if err != nil {
		t.Fatalf("SyncFile failed: %v", err)
	}

	if !res.Skipped {
		t.Errorf("expected download to be skipped when world directory already exists")
	}
}

func TestManager_DownloadAndExtractWorld(t *testing.T) {
	// Create mock world zip containing level.dat and session.lock
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f1, _ := zw.Create("level.dat")
	_, _ = f1.Write([]byte("mock level nbt"))
	f2, _ := zw.Create("session.lock")
	_, _ = f2.Write([]byte("old lock"))
	_ = zw.Close()

	archiveBytes := buf.Bytes()
	h := sha256.Sum256(archiveBytes)
	expectedSHA := hex.EncodeToString(h[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archiveBytes)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	mgr := NewManager(server.Client())

	cfg := config.FileConfig{
		Path:    "world",
		URL:     server.URL + "/world.zip",
		SHA256:  expectedSHA,
		Extract: true,
	}

	res, err := mgr.SyncFile(context.Background(), "overworld", cfg, tmpDir)
	if err != nil {
		t.Fatalf("SyncFile failed: %v", err)
	}

	if res.Skipped {
		t.Errorf("expected download not to be skipped")
	}
	if !res.Extracted {
		t.Errorf("expected Extracted to be true")
	}
	if res.SHA256 != expectedSHA {
		t.Errorf("expected hash %s, got %s", expectedSHA, res.SHA256)
	}

	// Verify level.dat extracted
	levelPath := filepath.Join(tmpDir, "world", "level.dat")
	if data, err := os.ReadFile(levelPath); err != nil || string(data) != "mock level nbt" {
		t.Errorf("level.dat not extracted correctly: %v", err)
	}

	// Verify session.lock was cleaned up
	lockPath := filepath.Join(tmpDir, "world", "session.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("session.lock should have been cleaned up automatically")
	}
}
