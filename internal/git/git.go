package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Options specifies the Git synchronization parameters.
type Options struct {
	RepoURL   string
	Branch    string
	Token     string
	Username  string
	TargetDir string
}

// FileChange represents a modified, added, or deleted file from git diff.
type FileChange struct {
	Status string // "M", "A", "D", "R"
	Path   string
}

// PullResult contains details about the incremental git pull.
type PullResult struct {
	OldCommit    string
	NewCommit    string
	HasUpdates   bool
	Commits      []string
	ChangedFiles []FileChange
}

// Client wraps git operations.
type Client struct {
	GitPath string
}

// NewClient returns a new Git client, verifying that the git binary exists.
func NewClient() (*Client, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("git binary not found in PATH; system 'git' is required")
	}
	return &Client{GitPath: path}, nil
}

// IsGitRepo checks if the specified directory is already a git repository.
func (c *Client) IsGitRepo(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	return err == nil && info.IsDir()
}

// BuildAuthURL constructs an authenticated HTTPS URL if a token is provided.
func BuildAuthURL(rawURL, username, token string) (string, error) {
	if token == "" {
		return rawURL, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid repository URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		// Do not modify non-HTTP URLs (e.g. ssh, file)
		return rawURL, nil
	}

	if username != "" {
		u.User = url.UserPassword(username, token)
	} else {
		u.User = url.User(token)
	}

	return u.String(), nil
}

// MaskURL masks sensitive credentials in a URL for safe logging.
func MaskURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	_, hasPassword := u.User.Password()
	if hasPassword {
		u.User = url.UserPassword(u.User.Username(), "redacted")
	} else {
		u.User = url.User("redacted")
	}
	return u.String()
}

// ResetWorkingTree resets all tracked files in the repository to HEAD,
// discarding any local template variable replacements so Git merges cleanly.
func (c *Client) ResetWorkingTree(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, c.GitPath, "reset", "--hard", "HEAD")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git reset failed in %s: %s (%w)", dir, strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// isDirEmpty returns true if a directory does not exist or has no entries.
func isDirEmpty(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return true
	}
	defer f.Close()

	names, err := f.Readdirnames(1)
	return err != nil || len(names) == 0
}

// cloneInPlace handles cloning into an existing non-empty directory (e.g. /home/container in Pterodactyl).
// It initializes a git repository in place, fetches the target branch, and force checks out the tree.
func (c *Client) cloneInPlace(ctx context.Context, authURL string, opts Options) error {
	if err := os.MkdirAll(opts.TargetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", opts.TargetDir, err)
	}

	// 1. git init
	initCmd := exec.CommandContext(ctx, c.GitPath, "init")
	initCmd.Dir = opts.TargetDir
	var stderr bytes.Buffer
	initCmd.Stderr = &stderr
	if err := initCmd.Run(); err != nil {
		return fmt.Errorf("git init failed in %s: %s (%w)", opts.TargetDir, strings.TrimSpace(stderr.String()), err)
	}

	// 2. git remote add or set-url
	remoteCmd := exec.CommandContext(ctx, c.GitPath, "remote", "add", "origin", authURL)
	remoteCmd.Dir = opts.TargetDir
	stderr.Reset()
	remoteCmd.Stderr = &stderr
	if err := remoteCmd.Run(); err != nil {
		setUrlCmd := exec.CommandContext(ctx, c.GitPath, "remote", "set-url", "origin", authURL)
		setUrlCmd.Dir = opts.TargetDir
		stderr.Reset()
		setUrlCmd.Stderr = &stderr
		if err := setUrlCmd.Run(); err != nil {
			errOut := stderr.String()
			if opts.Token != "" {
				errOut = strings.ReplaceAll(errOut, opts.Token, "******")
			}
			return fmt.Errorf("git remote set-url failed: %s (%w)", strings.TrimSpace(errOut), err)
		}
	}

	// 3. git fetch --depth 1 origin <branch>
	fetchCmd := exec.CommandContext(ctx, c.GitPath, "fetch", "--depth", "1", "origin", opts.Branch)
	fetchCmd.Dir = opts.TargetDir
	stderr.Reset()
	fetchCmd.Stderr = &stderr
	if err := fetchCmd.Run(); err != nil {
		errOut := stderr.String()
		if opts.Token != "" {
			errOut = strings.ReplaceAll(errOut, opts.Token, "******")
		}
		return fmt.Errorf("git fetch failed for branch %q: %s (%w)", opts.Branch, strings.TrimSpace(errOut), err)
	}

	// 4. git checkout -f -B <branch> FETCH_HEAD
	checkoutCmd := exec.CommandContext(ctx, c.GitPath, "checkout", "-f", "-B", opts.Branch, "FETCH_HEAD")
	checkoutCmd.Dir = opts.TargetDir
	stderr.Reset()
	checkoutCmd.Stderr = &stderr
	if err := checkoutCmd.Run(); err != nil {
		return fmt.Errorf("git checkout failed: %s (%w)", strings.TrimSpace(stderr.String()), err)
	}

	// 5. Best-effort branch upstream tracking
	trackCmd := exec.CommandContext(ctx, c.GitPath, "branch", "-u", fmt.Sprintf("origin/%s", opts.Branch), opts.Branch)
	trackCmd.Dir = opts.TargetDir
	_ = trackCmd.Run()

	// 6. Mask secrets in stored config if token was used
	if opts.Token != "" && opts.RepoURL != "" {
		maskRemoteCmd := exec.CommandContext(ctx, c.GitPath, "remote", "set-url", "origin", opts.RepoURL)
		maskRemoteCmd.Dir = opts.TargetDir
		_ = maskRemoteCmd.Run()
	}

	return nil
}

// Clone performs a shallow clone of the repository into targetDir.
func (c *Client) Clone(ctx context.Context, opts Options) error {
	if opts.RepoURL == "" {
		return errors.New("git repository URL is required")
	}
	if opts.Branch == "" {
		opts.Branch = "main"
	}
	if opts.TargetDir == "" {
		opts.TargetDir = "."
	}

	authURL, err := BuildAuthURL(opts.RepoURL, opts.Username, opts.Token)
	if err != nil {
		return err
	}

	// If the target directory already exists and contains files (e.g. /home/container in Pterodactyl),
	// standard git clone will fail. Use cloneInPlace to initialize and checkout cleanly.
	if !isDirEmpty(opts.TargetDir) {
		return c.cloneInPlace(ctx, authURL, opts)
	}

	args := []string{
		"clone",
		"--depth", "1",
		"--branch", opts.Branch,
		authURL,
		opts.TargetDir,
	}

	cmd := exec.CommandContext(ctx, c.GitPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOut := stderr.String()
		// Fallback in case directory became non-empty concurrently
		if strings.Contains(errOut, "already exists and is not an empty directory") {
			return c.cloneInPlace(ctx, authURL, opts)
		}
		if opts.Token != "" {
			errOut = strings.ReplaceAll(errOut, opts.Token, "******")
		}
		return fmt.Errorf("git clone failed for %s: %s (%w)", MaskURL(opts.RepoURL), strings.TrimSpace(errOut), err)
	}

	return nil
}

// GetHeadCommit returns the latest commit SHA of the repository in dir.
func (c *Client) GetHeadCommit(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, c.GitPath, "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git HEAD commit: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Pull performs an incremental git fetch and fast-forward pull, returning diff details.
func (c *Client) Pull(ctx context.Context, opts Options) (*PullResult, error) {
	branch := opts.Branch
	if branch == "" {
		branch = "main"
	}

	// 1. Reset local template modifications to ensure clean merge
	if err := c.ResetWorkingTree(ctx, opts.TargetDir); err != nil {
		return nil, fmt.Errorf("failed to clean working tree before pull: %w", err)
	}

	oldCommit, err := c.GetHeadCommit(ctx, opts.TargetDir)
	if err != nil {
		return nil, err
	}

	authURL, err := BuildAuthURL(opts.RepoURL, opts.Username, opts.Token)
	if err != nil {
		return nil, err
	}

	// 2. Fetch updates
	fetchArgs := []string{"fetch", "--depth", "10"}
	if opts.RepoURL != "" {
		fetchArgs = append(fetchArgs, authURL, branch)
	} else {
		fetchArgs = append(fetchArgs, "origin", branch)
	}

	fetchCmd := exec.CommandContext(ctx, c.GitPath, fetchArgs...)
	fetchCmd.Dir = opts.TargetDir
	var stderr bytes.Buffer
	fetchCmd.Stderr = &stderr
	if err := fetchCmd.Run(); err != nil {
		errOut := stderr.String()
		if opts.Token != "" {
			errOut = strings.ReplaceAll(errOut, opts.Token, "******")
		}
		return nil, fmt.Errorf("git fetch failed: %s (%w)", strings.TrimSpace(errOut), err)
	}

	// Determine FETCH_HEAD commit
	revCmd := exec.CommandContext(ctx, c.GitPath, "rev-parse", "FETCH_HEAD")
	revCmd.Dir = opts.TargetDir
	revOut, err := revCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get FETCH_HEAD commit: %w", err)
	}
	newCommit := strings.TrimSpace(string(revOut))

	if oldCommit == newCommit {
		return &PullResult{
			OldCommit:  oldCommit,
			NewCommit:  newCommit,
			HasUpdates: false,
		}, nil
	}

	// 3. Inspect commit messages
	logCmd := exec.CommandContext(ctx, c.GitPath, "log", "--oneline", fmt.Sprintf("%s..%s", oldCommit, newCommit))
	logCmd.Dir = opts.TargetDir
	logOut, _ := logCmd.Output()
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(logOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commits = append(commits, line)
		}
	}

	// 4. Inspect changed files
	diffCmd := exec.CommandContext(ctx, c.GitPath, "diff", "--name-status", oldCommit, newCommit)
	diffCmd.Dir = opts.TargetDir
	diffOut, _ := diffCmd.Output()
	var changedFiles []FileChange
	for _, line := range strings.Split(strings.TrimSpace(string(diffOut)), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			changedFiles = append(changedFiles, FileChange{
				Status: parts[0],
				Path:   parts[1],
			})
		}
	}

	// 5. Fast-forward / reset working tree to FETCH_HEAD
	resetCmd := exec.CommandContext(ctx, c.GitPath, "reset", "--hard", "FETCH_HEAD")
	resetCmd.Dir = opts.TargetDir
	var resetErr bytes.Buffer
	resetCmd.Stderr = &resetErr
	if err := resetCmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to update to FETCH_HEAD: %s (%w)", strings.TrimSpace(resetErr.String()), err)
	}

	return &PullResult{
		OldCommit:    oldCommit,
		NewCommit:    newCommit,
		HasUpdates:   true,
		Commits:      commits,
		ChangedFiles: changedFiles,
	}, nil
}

// Sync performs the first-boot clone or detects existing repository and pulls.
func (c *Client) Sync(opts Options) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if c.IsGitRepo(opts.TargetDir) {
		// Existing installation: pull remote updates
		pullRes, err := c.Pull(ctx, opts)
		if err != nil {
			return "", err
		}
		return pullRes.NewCommit, nil
	}

	// Clean/first install clone
	if err := c.Clone(ctx, opts); err != nil {
		return "", err
	}

	commit, err := c.GetHeadCommit(ctx, opts.TargetDir)
	if err != nil {
		return "unknown", nil
	}
	return commit, nil
}
