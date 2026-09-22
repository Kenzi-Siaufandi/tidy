package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/git"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
)

func TestFormatDriftReport_Sections(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 30, 0, 0, time.UTC)
	out := formatDriftReport(
		[]string{"tidy.toml", "plugins/LuckPerms/config.yml"},
		[]string{"plugins/MyPlugin/data.yml"},
		now,
	)
	for _, want := range []string{
		"# Tidy drift report",
		"2026-09-22T08:30:00Z",
		"## Modified vs remote (2)",
		"tidy.toml",
		"plugins/LuckPerms/config.yml",
		"## Untracked — not in remote (1)",
		"plugins/MyPlugin/data.yml",
		"reset --hard",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestFormatDriftReport_Clean(t *testing.T) {
	out := formatDriftReport(nil, nil, time.Now())
	if !strings.Contains(out, "## Modified vs remote (0)") || !strings.Contains(out, "(none)") {
		t.Errorf("clean report should show empty sections:\n%s", out)
	}
}

func TestFormatDriftReport_CapsSections(t *testing.T) {
	var many []string
	for i := 0; i < driftReportCap+5; i++ {
		many = append(many, "file.yml")
	}
	out := formatDriftReport(many, nil, time.Now())
	if !strings.Contains(out, "(and 5 more)") {
		t.Errorf("expected overflow note, got:\n%s", out)
	}
	if got := strings.Count(out, "file.yml"); got != driftReportCap {
		t.Errorf("expected %d listed paths, got %d", driftReportCap, got)
	}
}

func TestConfigRelPath(t *testing.T) {
	workDir := string(filepath.Separator) + "home" + string(filepath.Separator) + "container"
	tests := []struct {
		name string
		flag string
		want string
	}{
		{"relative", "tidy.toml", "tidy.toml"},
		{"nested relative", "conf/tidy.toml", "conf/tidy.toml"},
		{"absolute inside", filepath.Join(workDir, "tidy.toml"), "tidy.toml"},
		{"absolute outside", string(filepath.Separator) + filepath.Join("etc", "tidy.toml"), ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configRelPath(workDir, tt.flag); got != tt.want {
				t.Errorf("configRelPath(%q) = %q, want %q", tt.flag, got, tt.want)
			}
		})
	}
}

func TestReportDrift_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed, skipping test")
	}

	workDir := t.TempDir()
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
	}
	runCmd("init")
	runCmd("config", "user.email", "test@test.com")
	runCmd("config", "user.name", "Test User")
	runCmd("checkout", "-b", "main")

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("tidy.toml", "version = 1\n")
	write("templated.yml", "key: {{SOME_VAR}}\n")
	runCmd("add", "tidy.toml", "templated.yml")
	runCmd("commit", "-m", "initial")

	// Simulate last boot's templating plus a panel edit and a new file.
	write("templated.yml", "key: substituted-value\n")
	write("tidy.toml", "version = 2\n")
	write("extra.yml", "brand new\n")

	client, err := git.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	prev := &state.State{TemplatedFiles: []string{"templated.yml"}}
	reportDrift(context.Background(), client, workDir, "tidy.toml", prev)

	raw, err := os.ReadFile(filepath.Join(workDir, driftReportName))
	if err != nil {
		t.Fatalf("drift report was not written: %v", err)
	}
	report := string(raw)
	for _, want := range []string{"tidy.toml", "extra.yml", "Untracked"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "templated.yml") {
		t.Errorf("templated leftovers must be filtered out:\n%s", report)
	}
}
