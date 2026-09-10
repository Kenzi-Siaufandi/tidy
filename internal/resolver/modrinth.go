package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

const (
	DefaultModrinthBaseURL   = "https://api.modrinth.com"
	DefaultModrinthUserAgent = "Kenzi-Siaufandi/tidy/0.1.0 (https://github.com/Kenzi-Siaufandi)"
)

// ModrinthVersion represents a version item in Modrinth API.
type ModrinthVersion struct {
	ID            string         `json:"id"`
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	VersionNumber string         `json:"version_number"`
	Files         []ModrinthFile `json:"files"`
}

// ModrinthFile represents a downloadable asset of a version.
type ModrinthFile struct {
	Hashes   map[string]string `json:"hashes"`
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Primary  bool              `json:"primary"`
	Size     int64             `json:"size"`
}

// ModrinthClient interacts with the Modrinth v2 API.
type ModrinthClient struct {
	BaseURL    string
	UserAgent  string
	HTTPClient *http.Client
}

// NewModrinthClient creates a new client for Modrinth API.
func NewModrinthClient(baseURL, userAgent string, client *http.Client) *ModrinthClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultModrinthBaseURL
	}
	if strings.TrimSpace(userAgent) == "" {
		if envUA := os.Getenv("MODRINTH_USER_AGENT"); strings.TrimSpace(envUA) != "" {
			userAgent = envUA
		} else {
			userAgent = DefaultModrinthUserAgent
		}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ModrinthClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		UserAgent:  userAgent,
		HTTPClient: client,
	}
}

// PluginDownloadResult contains details about a downloaded plugin.
type PluginDownloadResult struct {
	PluginName string
	Source     string
	Filename   string
	FilePath   string
	HashAlgo   string
	Hash       string
	Size       int64
}

// ResolveAndDownload fetches version details from Modrinth API and downloads the plugin JAR.
func (c *ModrinthClient) ResolveAndDownload(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string) (*PluginDownloadResult, error) {
	endpoint := fmt.Sprintf("%s/v2/project/%s/version", c.BaseURL, pCfg.ProjectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Modrinth API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact Modrinth API (%s): %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Modrinth API returned HTTP %d %s for project %q", resp.StatusCode, resp.Status, pCfg.ProjectID)
	}

	var versions []ModrinthVersion
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("failed to parse Modrinth API response: %w", err)
	}

	var targetVer *ModrinthVersion
	for i := range versions {
		v := &versions[i]
		if strings.EqualFold(v.VersionNumber, pCfg.Version) || strings.EqualFold(v.ID, pCfg.Version) {
			targetVer = v
			break
		}
	}

	if targetVer == nil {
		return nil, fmt.Errorf("version %q not found on Modrinth for project %q", pCfg.Version, pCfg.ProjectID)
	}

	if len(targetVer.Files) == 0 {
		return nil, fmt.Errorf("no files associated with version %q for project %q on Modrinth", pCfg.Version, pCfg.ProjectID)
	}

	// Select primary file, or fall back to first file
	selectedFile := targetVer.Files[0]
	for _, f := range targetVer.Files {
		if f.Primary {
			selectedFile = f
			break
		}
	}

	// Determine hash algorithm
	var hashAlgo, expectedHash string
	if sha512Hash, ok := selectedFile.Hashes["sha512"]; ok && sha512Hash != "" {
		hashAlgo = "sha512"
		expectedHash = sha512Hash
	} else if sha1Hash, ok := selectedFile.Hashes["sha1"]; ok && sha1Hash != "" {
		hashAlgo = "sha1"
		expectedHash = sha1Hash
	} else {
		return nil, fmt.Errorf("no supported cryptographic hash (sha512/sha1) found for file %q in Modrinth version %s",
			selectedFile.Filename, targetVer.VersionNumber)
	}

	filename := selectedFile.Filename
	if strings.TrimSpace(filename) == "" {
		filename = fmt.Sprintf("%s-%s.jar", pluginName, targetVer.VersionNumber)
	}

	targetPath := filepath.Join(workDir, "plugins", filename)

	headers := map[string]string{
		"User-Agent": c.UserAgent,
	}

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           selectedFile.URL,
		TargetPath:    targetPath,
		ExpectedHash:  expectedHash,
		HashAlgorithm: hashAlgo,
		Headers:       headers,
		Client:        c.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download and verify plugin %s from Modrinth: %w", pluginName, err)
	}

	return &PluginDownloadResult{
		PluginName: pluginName,
		Source:     "modrinth",
		Filename:   filename,
		FilePath:   res.Path,
		HashAlgo:   hashAlgo,
		Hash:       res.ComputedHash,
		Size:       res.Size,
	}, nil
}
