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
)

func TestDownloadAndVerify_Success(t *testing.T) {
	content := []byte("paper minecraft server jar payload bytes")
	h := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(h[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "server.jar")

	res, err := DownloadAndVerify(context.Background(), DownloadOptions{
		URL:           server.URL + "/server.jar",
		TargetPath:    targetPath,
		ExpectedHash:  expectedHash,
		HashAlgorithm: "sha256",
		Client:        server.Client(),
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	if res.ComputedHash != expectedHash {
		t.Errorf("expected hash %s, got %s", expectedHash, res.ComputedHash)
	}
	if res.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), res.Size)
	}

	// Verify file was written
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch")
	}

	// Verify tmp file does not remain
	if _, err := os.Stat(targetPath + ".tidy-tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file was not cleaned up")
	}
}

func TestDownloadAndVerify_HashMismatch(t *testing.T) {
	content := []byte("corrupted payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "corrupted.jar")

	// Provide incorrect hash
	_, err := DownloadAndVerify(context.Background(), DownloadOptions{
		URL:           server.URL + "/corrupted.jar",
		TargetPath:    targetPath,
		ExpectedHash:  "0000000000000000000000000000000000000000000000000000000000000000",
		HashAlgorithm: "sha256",
		Client:        server.Client(),
	})
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}

	// Verify target file was NOT created
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("target file should not exist on hash failure")
	}

	// Verify tmp file was cleaned up
	if _, err := os.Stat(targetPath + ".tidy-tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file was not cleaned up on error")
	}
}

func TestDownloadAndVerify_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "404.jar")

	_, err := DownloadAndVerify(context.Background(), DownloadOptions{
		URL:           server.URL + "/notfound.jar",
		TargetPath:    targetPath,
		ExpectedHash:  "abc",
		HashAlgorithm: "sha256",
		Client:        server.Client(),
	})
	if err == nil {
		t.Fatal("expected error on HTTP 404, got nil")
	}
}
