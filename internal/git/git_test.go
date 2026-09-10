package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBuildAuthURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		username string
		token    string
		expected string
	}{
		{
			name:     "no token",
			rawURL:   "https://github.com/example/repo.git",
			username: "",
			token:    "",
			expected: "https://github.com/example/repo.git",
		},
		{
			name:     "token only",
			rawURL:   "https://github.com/example/repo.git",
			username: "",
			token:    "mytoken123",
			expected: "https://mytoken123@github.com/example/repo.git",
		},
		{
			name:     "username and token",
			rawURL:   "https://github.com/example/repo.git",
			username: "gituser",
			token:    "mytoken123",
			expected: "https://gituser:mytoken123@github.com/example/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildAuthURL(tt.rawURL, tt.username, tt.token)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestMaskURL(t *testing.T) {
	raw := "https://mytoken123@github.com/example/repo.git"
	masked := MaskURL(raw)
	if masked != "https://redacted@github.com/example/repo.git" {
		t.Errorf("unexpected masked URL: %s", masked)
	}

	rawWithUser := "https://user:password@github.com/example/repo.git"
	maskedUser := MaskURL(rawWithUser)
	if maskedUser != "https://user:redacted@github.com/example/repo.git" {
		t.Errorf("unexpected masked URL: %s", maskedUser)
	}
}

func TestGitClient_CloneAndIncrementalPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed, skipping test")
	}

	tmpDir := t.TempDir()
	originDir := filepath.Join(tmpDir, "origin")
	targetDir := filepath.Join(tmpDir, "target")

	if err := os.MkdirAll(originDir, 0755); err != nil {
		t.Fatal(err)
	}

	runCmd := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
	}

	runCmd(originDir, "init")
	runCmd(originDir, "config", "user.email", "test@test.com")
	runCmd(originDir, "config", "user.name", "Test User")
	runCmd(originDir, "checkout", "-b", "main")

	testFile := filepath.Join(originDir, "tidy.toml")
	if err := os.WriteFile(testFile, []byte("[server]\nproject='paper'\nversion='26.2'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(originDir, "add", "tidy.toml")
	runCmd(originDir, "commit", "-m", "initial commit")

	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{
		RepoURL:   originDir,
		Branch:    "main",
		TargetDir: targetDir,
	}

	// 1. Initial Clone
	commit1, err := client.Sync(opts)
	if err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	if len(commit1) != 40 {
		t.Errorf("expected 40-char commit SHA, got %s", commit1)
	}

	// 2. Simulate local modification (as done by template engine)
	clonedFile := filepath.Join(targetDir, "tidy.toml")
	if err := os.WriteFile(clonedFile, []byte("[server]\nproject='paper-templated'\nversion='26.2'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Commit a new change in origin
	newFile := filepath.Join(originDir, "config.yml")
	if err := os.WriteFile(newFile, []byte("key: value\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("[server]\nproject='paper'\nversion='26.3'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(originDir, "add", "config.yml", "tidy.toml")
	runCmd(originDir, "commit", "-m", "update version and add config")

	// 4. Test Pull with local dirty changes - should reset and pull cleanly
	pullRes, err := client.Pull(context.Background(), opts)
	if err != nil {
		t.Fatalf("incremental pull failed: %v", err)
	}

	if !pullRes.HasUpdates {
		t.Errorf("expected HasUpdates to be true")
	}
	if pullRes.OldCommit != commit1 {
		t.Errorf("expected old commit %s, got %s", commit1, pullRes.OldCommit)
	}
	if len(pullRes.Commits) == 0 {
		t.Errorf("expected at least 1 commit message in pull result")
	}

	// Check changed files
	foundTidy := false
	foundConfig := false
	for _, ch := range pullRes.ChangedFiles {
		if ch.Path == "tidy.toml" && ch.Status == "M" {
			foundTidy = true
		}
		if ch.Path == "config.yml" && ch.Status == "A" {
			foundConfig = true
		}
	}
	if !foundTidy {
		t.Errorf("expected tidy.toml to be marked as Modified [M]")
	}
	if !foundConfig {
		t.Errorf("expected config.yml to be marked as Added [A]")
	}

	// Verify local target directory has updated content
	updatedTidy, err := os.ReadFile(clonedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedTidy) != "[server]\nproject='paper'\nversion='26.3'\n" {
		t.Errorf("cloned tidy.toml did not match pulled content: %s", string(updatedTidy))
	}

	// 5. Test Pull again when no new updates exist
	pullRes2, err := client.Pull(context.Background(), opts)
	if err != nil {
		t.Fatalf("second pull failed: %v", err)
	}
	if pullRes2.HasUpdates {
		t.Errorf("expected HasUpdates to be false for up-to-date repo")
	}
}
