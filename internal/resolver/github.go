package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/version"
)

// DefaultGitHubBaseURL is the upstream GitHub REST API endpoint. Tests point
// it at an httptest server via NewGitHubClient.
const DefaultGitHubBaseURL = "https://api.github.com"

// DefaultGitHubUserAgent is built from the single version source. GitHub
// rejects API requests without a User-Agent.
var DefaultGitHubUserAgent = "Kenzi-Siaufandi/tidy/" + version.Version + " (https://github.com/Kenzi-Siaufandi/tidy)"

// githubTokenEnvKeys mirrors the git auth aliases in main.go, so private
// release downloads reuse the same PAT (flag --git-token or env). The token
// never belongs in tidy.toml.
var githubTokenEnvKeys = []string{"GIT_TOKEN", "GIT_AUTH_TOKEN", "GITHUB_TOKEN"}

// GitHubTokenFromEnv returns the first non-empty PAT from the shared git auth
// env aliases, or "" when none is set (public repos work tokenless, subject
// to the unauthenticated rate limit).
func GitHubTokenFromEnv() string {
	for _, key := range githubTokenEnvKeys {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			return val
		}
	}
	return ""
}

// GitHubRelease represents the subset of a GitHub release used for resolution.
type GitHubRelease struct {
	ID         int64         `json:"id"`
	TagName    string        `json:"tag_name"`
	Name       string        `json:"name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []GitHubAsset `json:"assets"`
}

// GitHubAsset represents one downloadable file attached to a release.
type GitHubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
	// NeedsAuth records whether Resolve required the PAT to see this
	// release (i.e. the repo is private). Download sends Authorization
	// only then, so public repos stay anonymous. Excluded from JSON.
	NeedsAuth bool `json:"-"`
}

// GitHubClient resolves and downloads plugin jars from GitHub releases.
type GitHubClient struct {
	BaseURL    string
	Token      string
	UserAgent  string
	HTTPClient *http.Client
}

// NewGitHubClient creates a client for the GitHub REST API. An empty baseURL
// selects the upstream API; an empty token falls back to GitHubTokenFromEnv
// (shared git PAT aliases). A nil http client gets a sane default with
// redirect handling that strips Authorization on host change (the API 302s
// asset downloads to signed object URLs that must not receive the PAT).
func NewGitHubClient(baseURL, token string, client *http.Client) *GitHubClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultGitHubBaseURL
	}
	if strings.TrimSpace(token) == "" {
		token = GitHubTokenFromEnv()
	}
	if client == nil {
		client = &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: stripAuthOnRedirect,
		}
	} else if client.CheckRedirect == nil {
		cp := *client
		cp.CheckRedirect = stripAuthOnRedirect
		client = &cp
	}
	return &GitHubClient{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:      token,
		UserAgent:  DefaultGitHubUserAgent,
		HTTPClient: client,
	}
}

// stripAuthOnRedirect is an http.Client.CheckRedirect func that drops the
// Authorization header when a redirect leaves the original host. GitHub asset
// downloads 302 from api.github.com to signed objects.githubusercontent.com
// URLs; forwarding the PAT there would leak it and breaks the signed URL.
func stripAuthOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		req.Header.Del("Authorization")
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// setAPIHeaders applies the headers every GitHub API request needs. Auth is
// opt-in per request: callers try anonymously first and only set useAuth on
// retry, so public repos never receive the PAT.
func (c *GitHubClient) setAPIHeaders(req *http.Request, useAuth bool) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if useAuth && strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	}
}

// doFetchRelease performs one release-endpoint GET, returning the decoded
// release on 200 or the bare status otherwise (no error for HTTP statuses;
// the caller decides whether to retry with auth).
func (c *GitHubClient) doFetchRelease(ctx context.Context, endpoint string, useAuth bool) (*GitHubRelease, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create GitHub API request: %w", err)
	}
	c.setAPIHeaders(req, useAuth)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to contact GitHub API (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, 0, fmt.Errorf("failed to parse GitHub API response: %w", err)
	}
	return &rel, http.StatusOK, nil
}

// fetchRelease resolves one release endpoint, trying anonymously first so the
// PAT is only sent when needed. GitHub answers anonymous requests for private
// repos with 404, so a 401/403/404 with a configured token triggers exactly
// one authenticated retry (a 403 can also mean the anonymous rate limit ran
// out). It reports whether the winning attempt was authenticated.
func (c *GitHubClient) fetchRelease(ctx context.Context, endpoint string) (*GitHubRelease, bool, error) {
	rel, status, err := c.doFetchRelease(ctx, endpoint, false)
	if err != nil {
		return nil, false, err
	}
	usedAuth := false
	if strings.TrimSpace(c.Token) != "" &&
		(status == http.StatusNotFound || status == http.StatusUnauthorized || status == http.StatusForbidden) {
		rel, status, err = c.doFetchRelease(ctx, endpoint, true)
		if err != nil {
			return nil, false, err
		}
		usedAuth = true
	}

	switch status {
	case http.StatusOK:
		return rel, usedAuth, nil
	case http.StatusNotFound:
		if usedAuth {
			return nil, false, fmt.Errorf("GitHub API returned 404 for %s (release/tag not found, or the repo is private and the token lacks access)", endpoint)
		}
		return nil, false, fmt.Errorf("GitHub API returned 404 for %s (release/tag not found, or the repo is private and no token with access was provided via --git-token / GIT_TOKEN / GIT_AUTH_TOKEN / GITHUB_TOKEN)", endpoint)
	case http.StatusUnauthorized:
		return nil, false, fmt.Errorf("GitHub API authentication failed (401): check the PAT in --git-token / GIT_TOKEN / GIT_AUTH_TOKEN / GITHUB_TOKEN")
	case http.StatusForbidden:
		return nil, false, fmt.Errorf("GitHub API access forbidden (403): token lacks access or the rate limit was exceeded")
	default:
		return nil, false, fmt.Errorf("GitHub API returned HTTP %d for %s", status, endpoint)
	}
}

// splitRepo validates and splits "owner/repo" (config validation already
// enforces the shape; this guards direct callers).
func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("expected \"owner/repo\", got %q", repo)
	}
	return parts[0], parts[1], nil
}

// selectAsset picks the release asset with the exact configured filename.
// GitHub asset names are case-sensitive.
func selectAsset(rel *GitHubRelease, asset string) (*GitHubAsset, error) {
	for i := range rel.Assets {
		if rel.Assets[i].Name == asset {
			return &rel.Assets[i], nil
		}
	}
	var names []string
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("release %q of %q has no assets (wanted %q)", rel.TagName, rel.Name, asset)
	}
	return nil, fmt.Errorf("asset %q not found in release %q (available: %s)", asset, rel.TagName, strings.Join(names, ", "))
}

// Resolve fetches release metadata without downloading, so callers tracking
// "latest" can compare the resolved release ID against state and skip the
// download when nothing changed upstream.
func (c *GitHubClient) Resolve(ctx context.Context, pCfg config.PluginConfig) (*GitHubRelease, *GitHubAsset, error) {
	repo := strings.TrimSpace(pCfg.Repo)
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("github source requires 'repo': %w", err)
	}
	asset := strings.TrimSpace(pCfg.Asset)
	if asset == "" {
		return nil, nil, fmt.Errorf("github source requires 'asset' (exact release asset filename)")
	}

	var endpoint string
	if pCfg.IsGitHubLatest() {
		endpoint = fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.BaseURL, url.PathEscape(owner), url.PathEscape(name))
	} else {
		endpoint = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", c.BaseURL, url.PathEscape(owner), url.PathEscape(name), url.PathEscape(strings.TrimSpace(pCfg.Tag)))
	}

	rel, usedAuth, err := c.fetchRelease(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	if rel.Draft {
		return nil, nil, fmt.Errorf("GitHub release %q of %q is a draft, not a published release", rel.TagName, repo)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, nil, fmt.Errorf("GitHub API returned a release without a tag name for %q", repo)
	}

	file, err := selectAsset(rel, asset)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(file.URL) == "" {
		return nil, nil, fmt.Errorf("GitHub asset %q has no API download URL", asset)
	}
	// Remember whether the PAT was needed so Download only sends it for
	// private repos.
	file.NeedsAuth = usedAuth
	return rel, file, nil
}

// Download fetches the already-resolved asset, verifies its mandatory
// SHA-256 pin, and records resolved release metadata in the result.
func (c *GitHubClient) Download(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string, rel *GitHubRelease, file *GitHubAsset) (*PluginDownloadResult, error) {
	expectedSHA256 := strings.TrimSpace(pCfg.SHA256)
	if expectedSHA256 == "" {
		return nil, fmt.Errorf("plugin %q: github source requires 'sha256' checksum", pluginName)
	}

	filename, err := SafeFilename(file.Name, pluginName+".jar")
	if err != nil {
		return nil, fmt.Errorf("plugin %q: invalid asset filename from GitHub: %w", pluginName, err)
	}

	targetPath := filepath.Join(workDir, "plugins", filename)

	headers := map[string]string{
		"Accept":     "application/octet-stream",
		"User-Agent": c.UserAgent,
	}
	// The PAT goes out only when resolution needed it (private repo);
	// public assets download anonymously.
	if file != nil && file.NeedsAuth && strings.TrimSpace(c.Token) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(c.Token)
	}

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           file.URL,
		TargetPath:    targetPath,
		ExpectedHash:  expectedSHA256,
		HashAlgorithm: "sha256",
		Headers:       headers,
		Client:        c.HTTPClient,
		ProgressLabel: filename,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download and verify plugin %s from GitHub: %w", pluginName, err)
	}

	return &PluginDownloadResult{
		PluginName:    pluginName,
		Source:        "github",
		Filename:      filename,
		FilePath:      res.Path,
		HashAlgo:      "sha256",
		Hash:          res.ComputedHash,
		SHA256:        res.ComputedHash,
		Size:          res.Size,
		VersionID:     strconv.FormatInt(rel.ID, 10),
		VersionNumber: rel.TagName,
		Repo:          strings.TrimSpace(pCfg.Repo),
		Tag:           rel.TagName,
		Asset:         file.Name,
	}, nil
}

// ResolveAndDownload resolves the configured tag ("latest" or a pin) and
// downloads the exact asset file with SHA-256 verification.
func (c *GitHubClient) ResolveAndDownload(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string) (*PluginDownloadResult, error) {
	if strings.TrimSpace(pCfg.SHA256) == "" {
		return nil, fmt.Errorf("plugin %q: github source requires 'sha256' checksum", pluginName)
	}
	rel, file, err := c.Resolve(ctx, pCfg)
	if err != nil {
		return nil, err
	}
	return c.Download(ctx, pluginName, pCfg, workDir, rel, file)
}
