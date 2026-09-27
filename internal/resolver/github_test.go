package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func githubTestJar() (content []byte, sha string) {
	content = []byte("mock-github-plugin-content")
	h := sha256.Sum256(content)
	return content, hex.EncodeToString(h[:])
}

// githubAPIServer returns a mock GitHub API serving one pinned tag and a
// latest release. assetURL is where the "P.jar" asset bytes are served. When
// private is true it behaves like a private repo: requests without an
// Authorization header get 404, exactly like the real API.
func githubAPIServer(t *testing.T, assetURL string, private bool, gotAuth *string) *httptest.Server {
	t.Helper()
	rel := func(id int64, tag string) GitHubRelease {
		return GitHubRelease{
			ID:      id,
			TagName: tag,
			Name:    "Release " + tag,
			Assets: []GitHubAsset{
				{ID: 11, Name: "P.jar", URL: assetURL, BrowserDownloadURL: assetURL + "-browser", Size: 4},
				{ID: 12, Name: "P-sources.jar", URL: assetURL + "-sources", Size: 4},
			},
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if private && r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("GitHub API request missing User-Agent")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/releases/tags/v1.0":
			_ = json.NewEncoder(w).Encode(rel(100, "v1.0"))
		case "/repos/o/r/releases/latest":
			_ = json.NewEncoder(w).Encode(rel(200, "v2.0"))
		case "/repos/o/r/releases/tags/draft-tag":
			_ = json.NewEncoder(w).Encode(GitHubRelease{ID: 300, TagName: "draft-tag", Draft: true})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
}

func TestGitHubResolveAndDownloadPinned(t *testing.T) {
	content, sha := githubTestJar()
	var gotAuth, gotAssetAuth string

	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAssetAuth = r.Header.Get("Authorization")
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Errorf("asset download missing Accept: application/octet-stream, got %q", r.Header.Get("Accept"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer assetSrv.Close()

	api := githubAPIServer(t, assetSrv.URL+"/dl", true, &gotAuth)
	defer api.Close()

	tmpDir := t.TempDir()
	pCfg := config.PluginConfig{
		Source: "github",
		Repo:   "o/r",
		Tag:    "v1.0",
		Asset:  "P.jar",
		SHA256: sha,
	}

	client := NewGitHubClient(api.URL, "test-token", api.Client())
	res, err := client.ResolveAndDownload(context.Background(), "P", pCfg, tmpDir)
	if err != nil {
		t.Fatalf("github download failed: %v", err)
	}
	if res.Filename != "P.jar" {
		t.Errorf("expected filename P.jar, got %s", res.Filename)
	}
	if res.SHA256 != sha {
		t.Errorf("expected sha %s, got %s", sha, res.SHA256)
	}
	if res.VersionID != "100" || res.VersionNumber != "v1.0" {
		t.Errorf("expected release tracking 100/v1.0, got %s/%s", res.VersionID, res.VersionNumber)
	}
	if res.Repo != "o/r" || res.Tag != "v1.0" || res.Asset != "P.jar" {
		t.Errorf("expected repo/tag/asset metadata, got %+v", res)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("API request missing PAT auth, got %q", gotAuth)
	}
	if gotAssetAuth != "Bearer test-token" {
		t.Errorf("private asset download missing PAT auth, got %q", gotAssetAuth)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "plugins", "P.jar"))
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content does not match")
	}
}

func TestGitHubResolveLatest(t *testing.T) {
	_, sha := githubTestJar()
	api := githubAPIServer(t, "http://example.com/dl", false, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "x", api.Client())
	rel, file, err := client.Resolve(context.Background(), config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "latest", Asset: "P.jar", SHA256: sha,
	})
	if err != nil {
		t.Fatalf("latest resolve failed: %v", err)
	}
	if rel.TagName != "v2.0" || rel.ID != 200 {
		t.Errorf("expected latest v2.0/200, got %s/%d", rel.TagName, rel.ID)
	}
	if file.Name != "P.jar" {
		t.Errorf("expected P.jar, got %s", file.Name)
	}
}

func TestGitHubAssetNotFound(t *testing.T) {
	_, sha := githubTestJar()
	api := githubAPIServer(t, "http://example.com/dl", false, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "x", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v1.0", Asset: "Missing.jar", SHA256: sha,
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), `asset "Missing.jar" not found`) {
		t.Fatalf("expected asset-not-found error listing alternatives, got %v", err)
	}
}

func TestGitHubDraftRefused(t *testing.T) {
	_, sha := githubTestJar()
	api := githubAPIServer(t, "http://example.com/dl", false, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "x", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "draft-tag", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("expected draft refusal, got %v", err)
	}
}

func TestGitHubTagNotFound(t *testing.T) {
	_, sha := githubTestJar()
	api := githubAPIServer(t, "http://example.com/dl", false, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "x", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "nope", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

func TestGitHubMissingSHA256(t *testing.T) {
	client := NewGitHubClient("http://example.com", "x", nil)
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v1.0", Asset: "P.jar",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected sha256 error, got %v", err)
	}
}

func TestGitHubHashMismatch(t *testing.T) {
	content := []byte("mock-github-plugin-content")
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer assetSrv.Close()
	api := githubAPIServer(t, assetSrv.URL+"/dl", false, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "x", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v1.0", Asset: "P.jar",
		SHA256: strings.Repeat("0", 64),
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "hash verification failed") {
		t.Fatalf("expected hash verification failure, got %v", err)
	}
}

// TestGitHubRedirectStripsAuth proves the PAT is not forwarded when the API
// 302s the asset download to another host (signed object URL). The API mock
// is private so the asset download really is authenticated — otherwise there
// would be nothing to strip.
func TestGitHubRedirectStripsAuth(t *testing.T) {
	content, sha := githubTestJar()
	var leakedAuth, originAuth string

	// Origin host: serves a 302 to the "signed" host.
	signed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakedAuth = r.Header.Get("Authorization")
		_, _ = w.Write(content)
	}))
	defer signed.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, signed.URL+"/obj", http.StatusFound)
	}))
	defer redirecting.Close()

	api := githubAPIServer(t, redirecting.URL+"/dl", true, nil)
	defer api.Close()

	client := NewGitHubClient(api.URL, "super-secret", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v1.0", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("redirected download failed: %v", err)
	}
	if originAuth != "Bearer super-secret" {
		t.Errorf("private asset origin missing PAT auth, got %q", originAuth)
	}
	if leakedAuth != "" {
		t.Errorf("PAT leaked to redirected host: %q", leakedAuth)
	}
}

// TestGitHubPublicRepoSkipsToken proves a configured PAT is never sent when
// the repo is public — neither to the API nor to the asset download.
func TestGitHubPublicRepoSkipsToken(t *testing.T) {
	content, sha := githubTestJar()
	var gotAPI, gotAsset string

	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAsset = r.Header.Get("Authorization")
		_, _ = w.Write(content)
	}))
	defer assetSrv.Close()

	api := githubAPIServer(t, assetSrv.URL+"/dl", false, &gotAPI)
	defer api.Close()

	client := NewGitHubClient(api.URL, "should-stay-secret", api.Client())
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v1.0", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("public download failed: %v", err)
	}
	if gotAPI != "" {
		t.Errorf("PAT sent to public repo API: %q", gotAPI)
	}
	if gotAsset != "" {
		t.Errorf("PAT sent to public asset download: %q", gotAsset)
	}
}

// TestGitHubRateLimitFallsBackToToken proves an anonymous 403 (exhausted IP
// quota) retries once with the PAT instead of failing.
func TestGitHubRateLimitFallsBackToToken(t *testing.T) {
	content, sha := githubTestJar()
	var calls int
	var gotAuth string

	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer assetSrv.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotAuth = r.Header.Get("Authorization")
		if gotAuth == "" {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
			return
		}
		rel := GitHubRelease{ID: 9, TagName: "v9", Assets: []GitHubAsset{
			{ID: 1, Name: "P.jar", URL: assetSrv.URL + "/asset"},
		}}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer api.Close()

	client := NewGitHubClient(api.URL, "quota-pat", api.Client())
	res, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v9", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("rate-limited download failed: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 1 anonymous + 1 authenticated API call, got %d", calls)
	}
	if res.VersionNumber != "v9" {
		t.Errorf("expected tag v9, got %s", res.VersionNumber)
	}
}

func TestGitHubTokenFromEnv(t *testing.T) {
	t.Setenv("GIT_TOKEN", "")
	t.Setenv("GIT_AUTH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "env-pat")
	if got := GitHubTokenFromEnv(); got != "env-pat" {
		t.Errorf("expected env-pat, got %q", got)
	}
	// Explicit token wins over env.
	c := NewGitHubClient("", "explicit", nil)
	if c.Token != "explicit" {
		t.Errorf("expected explicit token, got %q", c.Token)
	}
	if c.BaseURL != DefaultGitHubBaseURL {
		t.Errorf("expected default base URL, got %q", c.BaseURL)
	}
}

func TestGitHubBadRepo(t *testing.T) {
	client := NewGitHubClient("http://example.com", "x", nil)
	for _, repo := range []string{"", "noslash", "a/b/c", "/r", "o/"} {
		_, _, err := client.Resolve(context.Background(), config.PluginConfig{
			Source: "github", Repo: repo, Tag: "v1.0", Asset: "P.jar",
			SHA256: strings.Repeat("a", 64),
		})
		if err == nil {
			t.Errorf("expected error for repo %q, got nil", repo)
		}
	}
}

func TestGitHubPrivateRepoSendsAuth(t *testing.T) {
	// End-to-end with the same env alias main.go uses for git sync.
	t.Setenv("GIT_TOKEN", "")
	t.Setenv("GIT_AUTH_TOKEN", "shared-git-pat")
	t.Setenv("GITHUB_TOKEN", "")

	var gotAPI, gotAsset string
	content, sha := githubTestJar()
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAsset = r.Header.Get("Authorization")
		_, _ = w.Write(content)
	}))
	defer assetSrv.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPI = r.Header.Get("Authorization")
		if gotAPI == "" {
			// Private repo: anonymous callers see 404.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		rel := GitHubRelease{ID: 7, TagName: "v7", Assets: []GitHubAsset{
			{ID: 1, Name: "P.jar", URL: assetSrv.URL + "/asset"},
		}}
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer api.Close()

	client := NewGitHubClient(api.URL, "", api.Client()) // empty -> env fallback
	if client.Token != "shared-git-pat" {
		t.Fatalf("expected shared git PAT from env, got %q", client.Token)
	}
	_, err := client.ResolveAndDownload(context.Background(), "P", config.PluginConfig{
		Source: "github", Repo: "o/r", Tag: "v7", Asset: "P.jar", SHA256: sha,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("private download failed: %v", err)
	}
	if gotAPI != "Bearer shared-git-pat" {
		t.Errorf("API request missing PAT auth, got %q", gotAPI)
	}
	if gotAsset != "Bearer shared-git-pat" {
		t.Errorf("asset download missing PAT auth, got %q", gotAsset)
	}
}
