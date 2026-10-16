package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

func buildTestCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tidy")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	return binary
}

func seedTestServer(t *testing.T, root string, plugins map[string]state.PluginState) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("server")
	if err := os.WriteFile(filepath.Join(root, "paper.jar"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(root, &state.State{Server: state.ServerState{Project: "paper", Version: "1.21.1", BuildID: 1, Filename: "paper.jar", SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, Plugins: plugins}); err != nil {
		t.Fatal(err)
	}
}

const testServerConfig = "[server]\nproject='paper'\nversion='1.21.1'\nbuild='1'\n[templates]\ndisabled=true\n"

func runTestCLI(t *testing.T, binary, root string) error {
	t.Helper()
	cmd := exec.Command(binary, "--skip-git", "--no-color", "--no-progress", "--workdir", root)
	cmd.Env = append(os.Environ(), "SERVER_JAR=server.jar", "SERVER_JARFILE=server.jar")
	out, err := cmd.CombinedOutput()
	t.Logf("CLI: %s", out)
	return err
}

func TestPluginSourceMigrationRetiresOnlyManagedJar(t *testing.T) {
	binary := buildTestCLI(t)
	data := []byte("new plugin")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	for _, source := range []string{"github", "local"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			seedTestServer(t, root, map[string]state.PluginState{"demo": {Source: source, Filename: "old.jar", Path: "plugins/old.jar"}})
			old := filepath.Join(root, "plugins", "old.jar")
			if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			cfg := testServerConfig + fmt.Sprintf("[plugins.demo]\nsource='url'\nurl=%q\nsha256='%x'\n", server.URL+"/new.jar", sha256.Sum256(data))
			if err := os.WriteFile(filepath.Join(root, "tidy.toml"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			if err := runTestCLI(t, binary, root); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(old)
			if source == "local" && err != nil {
				t.Fatal("local jar removed")
			}
			if source != "local" && !os.IsNotExist(err) {
				t.Fatal("managed old jar remains")
			}
			raw, err := os.ReadFile(filepath.Join(root, state.StateFilename))
			if err != nil {
				t.Fatal(err)
			}
			var saved state.State
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Plugins["demo"].Filename != "new.jar" {
				t.Fatal("replacement missing from state")
			}
		})
	}
}
