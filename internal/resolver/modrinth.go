package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
	"github.com/Kenzi-Siaufandi/tidy/internal/version"
)

const DefaultModrinthBaseURL = "https://api.modrinth.com"

// latestFetchLimit bounds the filtered "latest" query. The API returns newest
// first, so a small window suffices after channel filtering.
const latestFetchLimit = 50

// pinnedPageLimit bounds each page of the unfiltered list scan used for exact
// pins. Pagination continues until a short page or a match.
const pinnedPageLimit = 200
const pinnedMaxPages = 10

// DefaultModrinthUserAgent is built from the single version source.
var DefaultModrinthUserAgent = "Kenzi-Siaufandi/tidy/" + version.Version + " (https://github.com/Kenzi-Siaufandi/tidy)"

// ModrinthVersion represents a version item in Modrinth API.
type ModrinthVersion struct {
	ID            string         `json:"id"`
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	VersionNumber string         `json:"version_number"`
	VersionType   string         `json:"version_type"`
	Status        string         `json:"status"`
	GameVersions  []string       `json:"game_versions"`
	Loaders       []string       `json:"loaders"`
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
	PluginName    string
	Source        string
	Filename      string
	FilePath      string
	HashAlgo      string
	Hash          string
	SHA256        string
	Size          int64
	VersionID     string
	VersionNumber string
	GameVersion   string
	Loader        string
}

// fetchVersions queries the version list endpoint with optional server-side
// filters. An empty gameVersion disables the game_versions filter; loader is
// always sent (defaults to "paper" via NormalizedLoader).
func (c *ModrinthClient) fetchVersions(ctx context.Context, projectID, gameVersion, loader string, limit, offset int) ([]ModrinthVersion, error) {
	endpoint := fmt.Sprintf("%s/v2/project/%s/version", c.BaseURL, projectID)

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Modrinth API endpoint: %w", err)
	}
	q := u.Query()
	if strings.TrimSpace(loader) != "" {
		if loadersJSON, err := json.Marshal([]string{strings.ToLower(strings.TrimSpace(loader))}); err == nil {
			q.Set("loaders", string(loadersJSON))
		}
	}
	if strings.TrimSpace(gameVersion) != "" {
		if gvJSON, err := json.Marshal([]string{strings.TrimSpace(gameVersion)}); err == nil {
			q.Set("game_versions", string(gvJSON))
		}
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Modrinth API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact Modrinth API (%s): %w", u.Redacted(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Modrinth API returned HTTP %d %s for project %q", resp.StatusCode, resp.Status, projectID)
	}

	var versions []ModrinthVersion
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("failed to parse Modrinth API response: %w", err)
	}
	return versions, nil
}

// fetchPinnedVersion tries the direct version endpoint first
// (GET /v2/project/{id}/version/{number}), falling back to "" when the pin is
// a version ID or the endpoint 404s.
func (c *ModrinthClient) fetchPinnedVersion(ctx context.Context, projectID, pin string) (*ModrinthVersion, error) {
	endpoint := fmt.Sprintf("%s/v2/project/%s/version/%s", c.BaseURL, projectID, url.PathEscape(pin))

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

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Modrinth API returned HTTP %d %s for project %q version %q", resp.StatusCode, resp.Status, projectID, pin)
	}

	var ver ModrinthVersion
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return nil, fmt.Errorf("failed to parse Modrinth API response: %w", err)
	}
	return &ver, nil
}

// allowVersionType reports whether a version_type passes the channel filter.
// Empty version_type (unrecognized payloads) is treated as release-compatible.
func allowVersionType(versionType, channel string) bool {
	t := strings.ToLower(strings.TrimSpace(versionType))
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "release":
		return t == "" || t == "release"
	case "beta":
		return t == "" || t == "release" || t == "beta"
	case "alpha":
		return true
	default:
		return t == "" || t == "release"
	}
}

// isListed reports whether a version is publicly listed. Empty status
// (unrecognized payloads) is treated as listed.
func isListed(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s == "" || s == "listed"
}

// selectPrimaryFile picks the primary file, falling back to the first file.
func selectPrimaryFile(ver *ModrinthVersion) (*ModrinthFile, error) {
	if len(ver.Files) == 0 {
		return nil, fmt.Errorf("no files associated with version %q for project %q on Modrinth", ver.VersionNumber, ver.ProjectID)
	}
	selected := &ver.Files[0]
	for i := range ver.Files {
		if ver.Files[i].Primary {
			selected = &ver.Files[i]
			break
		}
	}
	return selected, nil
}

// supportsGameVersion checks the version's game_versions list. An empty list
// (unrecognized payload) cannot be verified, so it passes.
func supportsGameVersion(ver *ModrinthVersion, gameVersion string) bool {
	if strings.TrimSpace(gameVersion) == "" {
		return true
	}
	if len(ver.GameVersions) == 0 {
		return true
	}
	for _, gv := range ver.GameVersions {
		if gv == strings.TrimSpace(gameVersion) {
			return true
		}
	}
	return false
}

// resolveLatest returns the newest version matching the game_version/loader
// filters and channel policy. The API returns newest first.
func (c *ModrinthClient) resolveLatest(ctx context.Context, pCfg config.PluginConfig) (*ModrinthVersion, *ModrinthFile, error) {
	gameVersion := pCfg.NormalizedGameVersion()
	loader := pCfg.NormalizedLoader()
	channel := pCfg.NormalizedChannel()

	versions, err := c.fetchVersions(ctx, pCfg.ProjectID, gameVersion, loader, latestFetchLimit, 0)
	if err != nil {
		return nil, nil, err
	}

	for i := range versions {
		v := &versions[i]
		if !isListed(v.Status) || !allowVersionType(v.VersionType, channel) {
			continue
		}
		file, err := selectPrimaryFile(v)
		if err != nil {
			continue
		}
		return v, file, nil
	}

	if strings.TrimSpace(gameVersion) != "" {
		return nil, nil, fmt.Errorf("no %s version of Modrinth project %q supports game version %q (loader %q, channel %q)",
			channel, pCfg.ProjectID, gameVersion, loader, channel)
	}
	return nil, nil, fmt.Errorf("no %s versions found on Modrinth for project %q (loader %q, channel %q)",
		channel, pCfg.ProjectID, loader, channel)
}

// resolvePinned returns the exact version_number or version ID. An optional
// game_version is enforced: the pin must list it or resolution fails. Loader
// is intentionally NOT enforced for pins because Paper servers run
// bukkit/spigot jars that many projects publish without a "paper" loader tag.
func (c *ModrinthClient) resolvePinned(ctx context.Context, pCfg config.PluginConfig) (*ModrinthVersion, *ModrinthFile, error) {
	pin := strings.TrimSpace(pCfg.Version)
	gameVersion := pCfg.NormalizedGameVersion()

	if ver, err := c.fetchPinnedVersion(ctx, pCfg.ProjectID, pin); err != nil {
		return nil, nil, err
	} else if ver != nil && (strings.EqualFold(ver.VersionNumber, pin) || strings.EqualFold(ver.ID, pin)) {
		if !supportsGameVersion(ver, gameVersion) {
			return nil, nil, fmt.Errorf("Modrinth version %q of project %q does not support game version %q (supports: %s)",
				pin, pCfg.ProjectID, gameVersion, strings.Join(ver.GameVersions, ", "))
		}
		file, err := selectPrimaryFile(ver)
		if err != nil {
			return nil, nil, err
		}
		return ver, file, nil
	}

	// Fallback: scan the unfiltered list (covers version IDs and servers
	// that 404 the direct endpoint).
	for page := 0; page < pinnedMaxPages; page++ {
		versions, err := c.fetchVersions(ctx, pCfg.ProjectID, "", "", pinnedPageLimit, page*pinnedPageLimit)
		if err != nil {
			return nil, nil, err
		}
		if len(versions) == 0 {
			break
		}
		for i := range versions {
			v := &versions[i]
			if !strings.EqualFold(v.VersionNumber, pin) && !strings.EqualFold(v.ID, pin) {
				continue
			}
			if !supportsGameVersion(v, gameVersion) {
				return nil, nil, fmt.Errorf("Modrinth version %q of project %q does not support game version %q (supports: %s)",
					pin, pCfg.ProjectID, gameVersion, strings.Join(v.GameVersions, ", "))
			}
			file, err := selectPrimaryFile(v)
			if err != nil {
				return nil, nil, err
			}
			return v, file, nil
		}
		if len(versions) < pinnedPageLimit {
			break
		}
	}

	return nil, nil, fmt.Errorf("version %q not found on Modrinth for project %q", pin, pCfg.ProjectID)
}

// Resolve fetches version metadata without downloading, so callers tracking
// "latest" can compare the resolved version ID against state and skip the
// download when nothing changed upstream.
func (c *ModrinthClient) Resolve(ctx context.Context, pCfg config.PluginConfig) (*ModrinthVersion, *ModrinthFile, error) {
	if strings.TrimSpace(pCfg.ProjectID) == "" {
		return nil, nil, fmt.Errorf("modrinth source requires 'project_id'")
	}
	if pCfg.IsLatest() {
		return c.resolveLatest(ctx, pCfg)
	}
	return c.resolvePinned(ctx, pCfg)
}

// Download fetches the already-resolved file, verifies its hash, enforces an
// optional sha256 pin, and records resolved version metadata in the result.
func (c *ModrinthClient) Download(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string, ver *ModrinthVersion, file *ModrinthFile) (*PluginDownloadResult, error) {
	// Determine hash algorithm, strongest first.
	var hashAlgo, expectedHash string
	if h, ok := file.Hashes["sha512"]; ok && strings.TrimSpace(h) != "" {
		hashAlgo = "sha512"
		expectedHash = strings.TrimSpace(h)
	} else if h, ok := file.Hashes["sha256"]; ok && strings.TrimSpace(h) != "" {
		hashAlgo = "sha256"
		expectedHash = strings.TrimSpace(h)
	} else if h, ok := file.Hashes["sha1"]; ok && strings.TrimSpace(h) != "" {
		hashAlgo = "sha1"
		expectedHash = strings.TrimSpace(h)
	} else {
		return nil, fmt.Errorf("no supported cryptographic hash (sha512/sha256/sha1) found for file %q in Modrinth version %s",
			file.Filename, ver.VersionNumber)
	}

	filename, err := SafeFilename(file.Filename, fmt.Sprintf("%s-%s.jar", pluginName, ver.VersionNumber))
	if err != nil {
		return nil, fmt.Errorf("invalid plugin filename from Modrinth for %s: %w", pluginName, err)
	}

	targetPath := filepath.Join(workDir, "plugins", filename)

	headers := map[string]string{
		"User-Agent": c.UserAgent,
	}

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           file.URL,
		TargetPath:    targetPath,
		ExpectedHash:  expectedHash,
		HashAlgorithm: hashAlgo,
		Headers:       headers,
		Client:        c.HTTPClient,
		ProgressLabel: filename,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download and verify plugin %s from Modrinth: %w", pluginName, err)
	}

	// Compute SHA-256 for universal cache tracking
	computedSHA256, err := state.ComputeFileSHA256(res.Path)
	if err != nil {
		_ = os.Remove(res.Path)
		return nil, fmt.Errorf("failed to compute SHA-256 for plugin %s: %w", pluginName, err)
	}

	// If user explicitly configured sha256 in tidy.toml, enforce it
	if expectedSHA := strings.TrimSpace(pCfg.SHA256); expectedSHA != "" {
		if !strings.EqualFold(computedSHA256, expectedSHA) {
			_ = os.Remove(res.Path)
			return nil, fmt.Errorf("SHA-256 verification failed for Modrinth plugin %s: expected %s, computed %s",
				pluginName, expectedSHA, computedSHA256)
		}
	}

	return &PluginDownloadResult{
		PluginName:    pluginName,
		Source:        "modrinth",
		Filename:      filename,
		FilePath:      res.Path,
		HashAlgo:      hashAlgo,
		Hash:          res.ComputedHash,
		SHA256:        computedSHA256,
		Size:          res.Size,
		VersionID:     ver.ID,
		VersionNumber: ver.VersionNumber,
		GameVersion:   pCfg.NormalizedGameVersion(),
		Loader:        pCfg.NormalizedLoader(),
	}, nil
}

// ResolveAndDownload fetches version details from Modrinth API and downloads the plugin JAR.
func (c *ModrinthClient) ResolveAndDownload(ctx context.Context, pluginName string, pCfg config.PluginConfig, workDir string) (*PluginDownloadResult, error) {
	ver, file, err := c.Resolve(ctx, pCfg)
	if err != nil {
		return nil, err
	}
	return c.Download(ctx, pluginName, pCfg, workDir, ver, file)
}
