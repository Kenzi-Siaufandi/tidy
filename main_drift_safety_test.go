package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/git"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

func TestDriftNeverPersistsHistoricalTemplateSecrets(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrecorded", true: "recorded"}[recorded], func(t *testing.T) {
			root := t.TempDir()
			run := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %s: %v", out, err)
				}
			}
			run("init")
			run("config", "user.email", "test@example.com")
			run("config", "user.name", "Test")
			path := filepath.Join(root, "config.yml")
			if err := os.WriteFile(path, []byte("password: {{DRIFT_TEST_PASSWORD}}\nport: 1234\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run("add", "config.yml")
			run("commit", "-m", "template")
			if err := os.WriteFile(path, []byte("password: historical-secret\nport: 5678\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DRIFT_TEST_PASSWORD", "rotated-secret")
			previous := &state.State{}
			if recorded {
				previous.TemplatedFiles = []string{"config.yml"}
			}
			client, err := git.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			reportDrift(context.Background(), client, root, "tidy.toml", previous)
			reports, err := filepath.Glob(filepath.Join(root, ".tidy", "drift", "*.txt"))
			if err != nil || len(reports) != 1 {
				t.Fatalf("reports: %v %v", reports, err)
			}
			data, err := os.ReadFile(reports[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "historical-secret") || strings.Contains(string(data), "rotated-secret") {
				t.Fatal("report leaked secret")
			}
			if !strings.Contains(string(data), "config.yml") || !strings.Contains(string(data), "diff omitted") {
				t.Fatalf("missing safe drift warning: %s", data)
			}
			info, err := os.Stat(reports[0])
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("report mode: %v", info.Mode())
			}
		})
	}
}
