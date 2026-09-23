package main

import (
	"context"
	"fmt"
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
		map[string]string{
			"tidy.toml": "diff --git a/tidy.toml b/tidy.toml\n-version = 1\n+version = 2\n",
		},
		now,
	)
	for _, want := range []string{
		"# Tidy drift report",
		"2026-09-22 08:30:00 +0000",
		"Discarded on sync (tracked, locally modified) (2)",
		"tidy.toml",
		"plugins/LuckPerms/config.yml",
		"Kept locally (untracked, not in git) (1)",
		"plugins/MyPlugin/data.yml",
		"reset --hard",
		"Changes that will be lost",
		"### tidy.toml",
		"-version = 1",
		"+version = 2",
		"(diff unavailable)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestDriftReportName_DatetimeNoPrune(t *testing.T) {
	now := time.Date(2026, 9, 23, 5, 30, 24, 0, time.UTC)
	want := ".tidy/drift-2026-09-23_05-30-24.txt"
	if got := driftReportName(now); got != want {
		t.Errorf("driftReportName = %q, want %q", got, want)
	}
	// Distinct timestamps get distinct names (history accumulates, never pruned).
	other := driftReportName(now.Add(time.Second))
	if other == want {
		t.Errorf("expected a new filename per timestamp, got %q twice", other)
	}
	if !strings.HasPrefix(other, driftReportPrefix) {
		t.Errorf("name %q must live under %q", other, driftReportPrefix)
	}
}

func TestUniqueDriftReportName_SameSecondCollision(t *testing.T) {
	workDir := t.TempDir()
	now := time.Date(2026, 9, 23, 5, 30, 24, 0, time.UTC)
	first := uniqueDriftReportName(workDir, now)
	if first != ".tidy/drift-2026-09-23_05-30-24.txt" {
		t.Fatalf("first name = %q, want base datetime name", first)
	}
	// Simulate a crash loop: base name already taken within the same second.
	if err := os.MkdirAll(filepath.Join(workDir, ".tidy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, first), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	second := uniqueDriftReportName(workDir, now)
	if second != ".tidy/drift-2026-09-23_05-30-24-1.txt" {
		t.Errorf("second name = %q, want -1 suffix", second)
	}
}

func TestIsTidyInternal(t *testing.T) {
	for _, p := range []string{".tidy", ".tidy/", ".tidy/state.json", ".tidy/drift-2026-09-23_05-30-24.txt", "./.tidy/state.json"} {
		if !isTidyInternal(p) {
			t.Errorf("isTidyInternal(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"tidy.toml", ".tidyrc", "plugins/.tidy/x.yml", "server.properties"} {
		if isTidyInternal(p) {
			t.Errorf("isTidyInternal(%q) = true, want false", p)
		}
	}
}

func TestFormatDriftReport_Clean(t *testing.T) {
	out := formatDriftReport(nil, nil, nil, time.Now())
	if !strings.Contains(out, "Discarded on sync (tracked, locally modified) (0)") ||
		!strings.Contains(out, "Kept locally (untracked, not in git) (0)") ||
		!strings.Contains(out, "(none)") {
		t.Errorf("clean report should show empty sections:\n%s", out)
	}
}

func TestFormatDriftReport_DiffTruncation(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < driftDiffLinesCap+10; i++ {
		fmt.Fprintf(&sb, " line %d\n", i)
	}
	out := formatDriftReport(
		[]string{"big.yml"},
		nil,
		map[string]string{"big.yml": sb.String()},
		time.Now(),
	)
	if !strings.Contains(out, "(... truncated, 10 more lines)") {
		t.Errorf("expected truncation note, got:\n%s", out)
	}
	if strings.Contains(out, fmt.Sprintf("line %d", driftDiffLinesCap+10-1)) {
		t.Errorf("lines past the cap must be cut:\n%s", out)
	}

	// More modified files than the diff cap: names list everything, diffs stop.
	var many []string
	for i := 0; i < driftDiffFilesCap+3; i++ {
		many = append(many, fmt.Sprintf("f%d.yml", i))
	}
	out = formatDriftReport(many, nil, map[string]string{"f0.yml": "x\n"}, time.Now())
	if !strings.Contains(out, "(and diffs for 3 more file(s) omitted)") {
		t.Errorf("expected omitted-diffs note, got:\n%s", out)
	}
	if !strings.Contains(out, "f12.yml") {
		t.Errorf("name list must still cover all files, got:\n%s", out)
	}
}

func TestFormatDriftReport_CapsSections(t *testing.T) {
	var many []string
	for i := 0; i < driftReportCap+5; i++ {
		many = append(many, "file.yml")
	}
	out := formatDriftReport(many, nil, nil, time.Now())
	if !strings.Contains(out, "(and 5 more)") {
		t.Errorf("expected overflow note, got:\n%s", out)
	}
	// Name appears driftReportCap times in the capped name list plus once
	// per diff header (driftDiffFilesCap, all unavailable here).
	if got := strings.Count(out, "file.yml"); got != driftReportCap+driftDiffFilesCap {
		t.Errorf("expected %d listed paths, got %d", driftReportCap+driftDiffFilesCap, got)
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
	// Pollute .tidy with runtime state from a previous run: it must never
	// surface as drift.
	if err := os.MkdirAll(filepath.Join(workDir, ".tidy"), 0755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(".tidy", "state.json"), "{}\n")
	// Fixed ancient name: can never collide with the fresh report's timestamp.
	write(filepath.Join(".tidy", "drift-2000-01-01_00-00-00.txt"), "old report\n")

	prev := &state.State{TemplatedFiles: []string{"templated.yml"}}
	reportDrift(context.Background(), client, workDir, "tidy.toml", prev)

	seeded := filepath.Join(workDir, ".tidy", "drift-2000-01-01_00-00-00.txt")
	matches, err := filepath.Glob(filepath.Join(workDir, driftReportPrefix+"*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 { // the seeded old report + the one just written
		t.Fatalf("expected 2 dated drift reports, got %v", matches)
	}
	var fresh string
	for _, m := range matches {
		if m == seeded {
			continue
		}
		raw, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("drift report was not written: %v", err)
		}
		fresh = string(raw)
	}
	if fresh == "" {
		t.Fatalf("fresh report not found among %v", matches)
	}
	report := fresh
	for _, want := range []string{"tidy.toml", "extra.yml", "Kept locally", "Changes that will be lost", "### tidy.toml", "-version = 1", "+version = 2"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "templated.yml") {
		t.Errorf("templated leftovers must be filtered out:\n%s", report)
	}
	for _, leaked := range []string{"state.json", "drift-2000-01-01_00-00-00.txt"} {
		if strings.Contains(report, leaked) {
			t.Errorf(".tidy/ runtime file %q must be filtered out:\n%s", leaked, report)
		}
	}
}

func TestReportDrift_CleanWritesNothing(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(workDir, "tidy.toml"), []byte("version = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd("add", "tidy.toml")
	runCmd("commit", "-m", "initial")

	client, err := git.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	reportDrift(context.Background(), client, workDir, "tidy.toml", nil)

	matches, err := filepath.Glob(filepath.Join(workDir, driftReportPrefix+"*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("clean runs must write no drift report, got %v", matches)
	}
}
