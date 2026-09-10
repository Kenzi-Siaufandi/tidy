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
		// Mask any secrets that might appear in stderr
		errOut := stderr.String()
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

// Sync performs the first-boot clone or detects existing repository.
func (c *Client) Sync(opts Options) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if c.IsGitRepo(opts.TargetDir) {
		// Already cloned; retrieve current commit
		commit, err := c.GetHeadCommit(ctx, opts.TargetDir)
		if err != nil {
			return "unknown", nil
		}
		return commit, nil
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
