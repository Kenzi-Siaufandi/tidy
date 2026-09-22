package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

const DefaultPurpurBaseURL = "https://api.purpurmc.org"

// PurpurVersionResponse is GET /v2/{project}/{version} (builds subset).
type PurpurVersionResponse struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Builds  struct {
		Latest string   `json:"latest"`
		All    []string `json:"all"`
	} `json:"builds"`
}

// PurpurBuildResponse is GET /v2/{project}/{version}/{build} (subset).
type PurpurBuildResponse struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Build   string `json:"build"`
	Result  string `json:"result"`
	MD5     string `json:"md5"`
}

// PurpurClient interacts with the PurpurMC Downloads API.
type PurpurClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewPurpurClient creates a new client for the PurpurMC Downloads API.
func NewPurpurClient(baseURL string, client *http.Client) *PurpurClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultPurpurBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &PurpurClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: client,
	}
}

// Name identifies the upstream API for log lines.
func (c *PurpurClient) Name() string { return "PurpurMC API" }

// getJSON performs an authenticated-less JSON GET against the PurpurMC API.
func (c *PurpurClient) getJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create PurpurMC API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tidy/"+version.Version+" (Pterodactyl Pre-Flight Orchestrator)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact PurpurMC API (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PurpurMC API returned HTTP %d %s for %s",
			resp.StatusCode, resp.Status, endpoint)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("failed to parse PurpurMC API response: %w", err)
	}
	return nil
}

// FetchLatestBuildID resolves the newest build number for a Purpur version.
func (c *PurpurClient) FetchLatestBuildID(ctx context.Context, serverCfg config.ServerConfig) (int, error) {
	endpoint := fmt.Sprintf("%s/v2/%s/%s",
		c.BaseURL,
		url.PathEscape(strings.ToLower(strings.TrimSpace(serverCfg.Project))),
		url.PathEscape(strings.TrimSpace(serverCfg.Version)))

	var verResp PurpurVersionResponse
	if err := c.getJSON(ctx, endpoint, &verResp); err != nil {
		return 0, err
	}
	latest := strings.TrimSpace(verResp.Builds.Latest)
	if latest == "" {
		return 0, fmt.Errorf("PurpurMC API returned no latest build for version %q", serverCfg.Version)
	}
	id, err := strconv.Atoi(latest)
	if err != nil {
		return 0, fmt.Errorf("PurpurMC API returned non-numeric latest build %q: %w", latest, err)
	}
	return id, nil
}

// fetchBuild fetches and validates a single build (latest or pinned).
func (c *PurpurClient) fetchBuild(ctx context.Context, serverCfg config.ServerConfig, build string) (*PurpurBuildResponse, error) {
	endpoint := fmt.Sprintf("%s/v2/%s/%s/%s",
		c.BaseURL,
		url.PathEscape(strings.ToLower(strings.TrimSpace(serverCfg.Project))),
		url.PathEscape(strings.TrimSpace(serverCfg.Version)),
		url.PathEscape(build))

	var buildResp PurpurBuildResponse
	if err := c.getJSON(ctx, endpoint, &buildResp); err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(buildResp.Result), "success") {
		return nil, fmt.Errorf("PurpurMC build %s for version %q did not succeed (result %q); refusing to download a broken build",
			build, serverCfg.Version, buildResp.Result)
	}
	if strings.TrimSpace(buildResp.MD5) == "" {
		return nil, fmt.Errorf("PurpurMC API provided no md5 checksum for build %s", build)
	}
	if _, err := strconv.Atoi(strings.TrimSpace(buildResp.Build)); err != nil {
		return nil, fmt.Errorf("PurpurMC API returned non-numeric build %q: %w", buildResp.Build, err)
	}
	return &buildResp, nil
}

// ResolveAndDownload fetches build metadata and downloads the Purpur jar.
// Upstream integrity is verified via the API-provided MD5; SHA-256 is then
// computed locally for state tracking and restart idempotency.
func (c *PurpurClient) ResolveAndDownload(ctx context.Context, serverCfg config.ServerConfig, workDir string) (*ServerDownloadResult, error) {
	build := strings.TrimSpace(serverCfg.Build)
	if build == "" || strings.EqualFold(build, "latest") {
		latestID, err := c.FetchLatestBuildID(ctx, serverCfg)
		if err != nil {
			return nil, err
		}
		build = strconv.Itoa(latestID)
	}

	buildResp, err := c.fetchBuild(ctx, serverCfg, build)
	if err != nil {
		return nil, err
	}
	buildID, _ := strconv.Atoi(strings.TrimSpace(buildResp.Build))

	filename, err := SafeFilename("", fmt.Sprintf("%s-%s-%d.jar", "purpur", serverCfg.Version, buildID))
	if err != nil {
		return nil, fmt.Errorf("invalid purpur filename: %w", err)
	}
	targetPath := filepath.Join(workDir, filename)
	downloadURL := fmt.Sprintf("%s/v2/%s/%s/%s/download",
		c.BaseURL,
		url.PathEscape(strings.ToLower(strings.TrimSpace(serverCfg.Project))),
		url.PathEscape(strings.TrimSpace(serverCfg.Version)),
		url.PathEscape(strings.TrimSpace(buildResp.Build)))

	downloadClient := c.HTTPClient
	if downloadClient == nil || downloadClient.Timeout < 5*time.Minute {
		downloadClient = &http.Client{Timeout: 5 * time.Minute}
	}

	if _, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           downloadURL,
		TargetPath:    targetPath,
		ExpectedHash:  strings.TrimSpace(buildResp.MD5),
		HashAlgorithm: "md5",
		Client:        downloadClient,
		ProgressLabel: filename,
	}); err != nil {
		return nil, fmt.Errorf("failed to download and verify purpur jar: %w", err)
	}

	sha256sum, size, err := sha256File(targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash downloaded purpur jar: %w", err)
	}

	return &ServerDownloadResult{
		Project:  serverCfg.Project,
		Version:  serverCfg.Version,
		BuildID:  buildID,
		Filename: filename,
		FilePath: targetPath,
		SHA256:   sha256sum,
		Size:     size,
	}, nil
}

// sha256File returns the hex SHA-256 and size of a local file.
func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
