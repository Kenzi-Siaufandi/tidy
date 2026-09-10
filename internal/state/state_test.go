package state

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestState_FirstInstallAndSave(t *testing.T) {
	tmpDir := t.TempDir()

	if !IsFirstInstall(tmpDir) {
		t.Errorf("expected IsFirstInstall to return true for empty directory")
	}

	initialState := &State{
		InstalledAt:  time.Now().UTC(),
		LastSyncedAt: time.Now().UTC(),
		GitCommit:    "abcdef123456",
		Server: ServerState{
			Project:  "paper",
			Version:  "26.2",
			BuildID:  123,
			Filename: "paper-26.2-123.jar",
			SHA256:   "7b7b3b43c009103e1971a0576c26f655a7dd9b56a0a2a4438e352c03a7fecd08",
		},
		Plugins: map[string]PluginState{
			"FastAsyncWorldEdit": {
				Source:   "modrinth",
				Filename: "FastAsyncWorldEdit-Bukkit-2.11.2.jar",
				Version:  "2.11.2",
				HashAlgo: "sha512",
				Hash:     "abc512",
				SHA256:   "def256",
			},
		},
		Files: map[string]FileState{
			"overworld": {
				Path:      "world",
				URL:       "https://example.com/world.tar.gz",
				SHA256:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				SyncedAt:  time.Now().UTC(),
				Extracted: true,
			},
		},
	}

	if err := SaveState(tmpDir, initialState); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	if IsFirstInstall(tmpDir) {
		t.Errorf("expected IsFirstInstall to return false after saving state")
	}

	loaded, err := LoadState(tmpDir)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	if loaded.Server.Filename != "paper-26.2-123.jar" {
		t.Errorf("expected server filename paper-26.2-123.jar, got %s", loaded.Server.Filename)
	}
	if loaded.GitCommit != "abcdef123456" {
		t.Errorf("expected commit abcdef123456, got %s", loaded.GitCommit)
	}
	if p, ok := loaded.Plugins["FastAsyncWorldEdit"]; !ok || p.Version != "2.11.2" {
		t.Errorf("plugin state not loaded accurately: %+v", loaded.Plugins)
	}
	if f, ok := loaded.Files["overworld"]; !ok || f.Path != "world" {
		t.Errorf("file state not loaded accurately: %+v", loaded.Files)
	}
}

func TestVerifyLocalSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.jar")
	content := []byte("sample binary content")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(h[:])

	computed, err := ComputeFileSHA256(filePath)
	if err != nil {
		t.Fatalf("ComputeFileSHA256 failed: %v", err)
	}
	if computed != expectedHex {
		t.Errorf("expected %s, got %s", expectedHex, computed)
	}

	if !VerifyLocalSHA256(filePath, expectedHex) {
		t.Errorf("expected VerifyLocalSHA256 to return true")
	}

	if VerifyLocalSHA256(filePath, "wronghashwronghashwronghashwronghashwronghashwronghashwronghashwron") {
		t.Errorf("expected VerifyLocalSHA256 to return false for mismatching hash")
	}

	if VerifyLocalSHA256(filepath.Join(tmpDir, "nonexistent.jar"), expectedHex) {
		t.Errorf("expected VerifyLocalSHA256 to return false for missing file")
	}
}
