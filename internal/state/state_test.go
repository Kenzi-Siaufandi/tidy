package state

import (
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
}
