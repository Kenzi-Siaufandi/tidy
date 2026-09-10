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

func TestGitClient_LocalCloneAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed, skipping test")
	}

	tmpDir := t.TempDir()
	originDir := filepath.Join(tmpDir, "origin")
	targetDir := filepath.Join(tmpDir, "target")

	if err := os.MkdirAll(originDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Initialize origin repository
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

	// Test Sync to targetDir
	opts := Options{
		RepoURL:   originDir,
		Branch:    "main",
		TargetDir: targetDir,
	}

	commit, err := client.Sync(opts)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if len(commit) != 40 {
		t.Errorf("expected 40-char commit SHA, got %s", commit)
	}

	if !client.IsGitRepo(targetDir) {
		t.Error("expected targetDir to be a git repo")
	}

	// Read cloned tidy.toml
	clonedFile := filepath.Join(targetDir, "tidy.toml")
	if _, err := os.Stat(clonedFile); err != nil {
		t.Errorf("tidy.toml was not cloned: %v", err)
	}

	// Test calling Sync again when already cloned
	commit2, err := client.Sync(opts)
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if commit2 != commit {
		t.Errorf("expected same commit SHA %s, got %s", commit, commit2)
	}

	_, _ = client.GetHeadCommit(context.Background(), targetDir)
}
