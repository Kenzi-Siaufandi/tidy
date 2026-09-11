package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/version"
)

const DefaultFillBaseURL = "https://fill.papermc.io"

// FillBuildResponse represents the response from PaperMC Fill API.
type FillBuildResponse struct {
	ID        int                     `json:"id"`
	Channel   string                  `json:"channel"`
	Downloads map[string]FillDownload `json:"downloads"`
}

// FillDownload represents download information for an artifact.
type FillDownload struct {
	Name      string        `json:"name"`
	Size      int64         `json:"size"`
	URL       string        `json:"url"`
	Checksums FillChecksums `json:"checksums"`
}

// FillChecksums holds checksum data.
type FillChecksums struct {
	SHA256 string `json:"sha256"`
}

// PaperClient interacts with PaperMC's Fill API.
type PaperClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewPaperClient creates a new client for PaperMC Fill API.
func NewPaperClient(baseURL string, client *http.Client) *PaperClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultFillBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &PaperClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: client,
	}
}

// ServerDownloadResult contains details of the downloaded server software.
type ServerDownloadResult struct {
	Project  string
	Version  string
	BuildID  int
	Filename string
	FilePath string
	SHA256   string
	Size     int64
}

// ResolveAndDownload fetches build metadata from PaperMC Fill API and downloads the server JAR.
func (c *PaperClient) ResolveAndDownload(ctx context.Context, serverCfg config.ServerConfig, workDir string) (*ServerDownloadResult, error) {
	buildResp, download, err := c.FetchBuild(ctx, serverCfg)
	if err != nil {
		return nil, err
	}

	filename, err := SafeFilename(download.Name, fmt.Sprintf("%s-%s-%d.jar", serverCfg.Project, serverCfg.Version, buildResp.ID))
	if err != nil {
		return nil, fmt.Errorf("invalid server filename from PaperMC Fill API: %w", err)
	}

	targetPath := filepath.Join(workDir, filename)

	// Use a long-timeout client for the (large) jar download; the API client
	// default of 30s is too short for slow networks.
	downloadClient := c.HTTPClient
	if downloadClient == nil || downloadClient.Timeout < 5*time.Minute {
		downloadClient = &http.Client{Timeout: 5 * time.Minute}
	}

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           download.URL,
		TargetPath:    targetPath,
		ExpectedHash:  strings.TrimSpace(download.Checksums.SHA256),
		HashAlgorithm: "sha256",
		Client:        downloadClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download and verify server jar: %w", err)
	}

	return &ServerDownloadResult{
		Project:  serverCfg.Project,
		Version:  serverCfg.Version,
		BuildID:  buildResp.ID,
		Filename: filename,
		FilePath: res.Path,
		SHA256:   res.ComputedHash,
		Size:     res.Size,
	}, nil
}

// FetchBuild resolves build metadata from the Fill API without downloading.
// It returns the build response plus the deterministically selected server artifact.
func (c *PaperClient) FetchBuild(ctx context.Context, serverCfg config.ServerConfig) (*FillBuildResponse, *FillDownload, error) {
	build := serverCfg.Build
	if strings.TrimSpace(build) == "" {
		build = "latest"
	}

	endpoint := fmt.Sprintf("%s/v3/projects/%s/versions/%s/builds/%s",
		c.BaseURL,
		url.PathEscape(serverCfg.Project),
		url.PathEscape(serverCfg.Version),
		url.PathEscape(build))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create PaperMC API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tidy/"+version.Version+" (Pterodactyl Pre-Flight Orchestrator)")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to contact PaperMC Fill API (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("PaperMC Fill API returned HTTP %d %s for project %q version %q build %q",
			resp.StatusCode, resp.Status, serverCfg.Project, serverCfg.Version, build)
	}

	var buildResp FillBuildResponse
	if err := json.NewDecoder(resp.Body).Decode(&buildResp); err != nil {
		return nil, nil, fmt.Errorf("failed to parse PaperMC Fill API response: %w", err)
	}

	// Select server download artifact deterministically.
	dl, ok := buildResp.Downloads["server:default"]
	if !ok {
		// Deterministic fallback: sorted keys instead of random map iteration.
		keys := make([]string, 0, len(buildResp.Downloads))
		for k := range buildResp.Downloads {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			dl = buildResp.Downloads[k]
			ok = true
			break
		}
	}

	if !ok || strings.TrimSpace(dl.URL) == "" {
		return nil, nil, fmt.Errorf("no server download URL found in PaperMC Fill API response for %s %s build %d",
			serverCfg.Project, serverCfg.Version, buildResp.ID)
	}

	sha256Hash := strings.TrimSpace(dl.Checksums.SHA256)
	if sha256Hash == "" {
		return nil, nil, fmt.Errorf("no sha256 checksum provided by PaperMC Fill API for build %d", buildResp.ID)
	}

	dlCopy := dl
	return &buildResp, &dlCopy, nil
}
