package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

func TestFailedSyncRetainsPluginOwnership(t *testing.T) {
	binary := buildTestCLI(t)
	data := []byte("installed before failure")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail.jar" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	root := t.TempDir()
	seedTestServer(t, root, map[string]state.PluginState{})
	plugin := func(name, path string) string {
		return fmt.Sprintf("[plugins.%s]\nsource='url'\nurl=%q\nsha256='%x'\n", name, server.URL+path, sha256.Sum256(data))
	}
	cfg := testServerConfig + plugin("aaa", "/new.jar") + plugin("zzz", "/fail.jar")
	configPath := filepath.Join(root, "tidy.toml")
	if err := os.WriteFile(configPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runTestCLI(t, binary, root); err == nil {
		t.Fatal("expected later plugin to fail")
	}
	saved, err := state.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Plugins["aaa"].Filename != "new.jar" {
		t.Fatal("successful installation lost ownership")
	}
	if err := os.WriteFile(configPath, []byte(testServerConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runTestCLI(t, binary, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "new.jar")); !os.IsNotExist(err) {
		t.Fatal("removed plugin jar remains")
	}
}

func TestPluginCheckpointPreservesUnprocessedState(t *testing.T) {
	root := t.TempDir()
	previous := &state.State{
		GitCommit:      "previous-commit",
		Plugins:        map[string]state.PluginState{"pending": {Source: "url", Filename: "pending.jar"}},
		Files:          map[string]state.FileState{"world": {Path: "world", SHA256: "previous-hash"}},
		TemplatedFiles: []string{"server.properties"},
	}
	save := newPluginCheckpoint(previous, state.ServerState{Filename: "paper.jar"}, "")
	if err := save(root, "installed", state.PluginState{Source: "url", Filename: "installed.jar"}); err != nil {
		t.Fatal(err)
	}
	saved, err := state.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Plugins["pending"].Filename != "pending.jar" || saved.Plugins["installed"].Filename != "installed.jar" {
		t.Fatal("checkpoint lost plugin records")
	}
	if saved.Files["world"].SHA256 != "previous-hash" || len(saved.TemplatedFiles) != 1 || saved.GitCommit != "previous-commit" {
		t.Fatal("checkpoint lost unrelated state")
	}
	if _, ok := previous.Plugins["installed"]; ok {
		t.Fatal("checkpoint mutated previous state")
	}
}
