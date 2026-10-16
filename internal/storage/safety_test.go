package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

func TestReservedDestinations(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tidy"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".tidy"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	once := false
	for _, path := range []string{".", "sub/..", ".tidy", ".tidy/nested", ".git/config", "alias/nested"} {
		_, err := NewManager(nil).SyncFile(context.Background(), "test", config.FileConfig{Path: path, Extract: true, Once: &once}, root)
		if err == nil {
			t.Errorf("accepted reserved destination %q", path)
		}
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("original data lost: %v", err)
	}
}

func TestOncePreservesExistingDataAfterConfigChanges(t *testing.T) {
	for _, extract := range []bool{false, true} {
		root := t.TempDir()
		dest := filepath.Join(root, "data")
		marker := dest
		if extract {
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			marker = filepath.Join(dest, "player-progress.dat")
		}
		if err := os.WriteFile(marker, []byte("live data"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, prev := range []*state.FileState{nil, {Path: "data", URL: "https://old.invalid", SHA256: "old-hash"}} {
			result, err := NewManager(nil).SyncFileWithState(context.Background(), "test", config.FileConfig{Path: "data", URL: "https://new.invalid", SHA256: "new-hash", Extract: extract}, root, prev)
			if err != nil || !result.Skipped {
				t.Fatalf("once=true must skip config changes: %v", err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "live data" {
				t.Fatalf("live data lost: %v", err)
			}
		}
	}
}

func TestPromotionRestoresDestinationOnFailure(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "world")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "level.dat")
	if err := os.WriteFile(marker, []byte("live world"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := promoteStagedDirectory(filepath.Join(root, "missing"), dest); err == nil {
		t.Fatal("expected promotion failure")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "live world" {
		t.Fatalf("original world lost: %v", err)
	}
}
