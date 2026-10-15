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
	// Repo, Tag and Asset carry GitHub release resolution (source "github").
	// Tag is the resolved tag_name; the configured selector lives in state.
	Repo  string
	Tag   string
	Asset string
}

// fetchVersions queries the version list endpoint with optional server-side
// filters. An empty gameVersion disables the game_versions filter; loader is
// always sent (defaults to "paper" via NormalizedLoader).
func (c *ModrinthClient) fetchVersions(ctx context.Context, projectID, gameVersion, loader string, limit, offset int) ([]ModrinthVersion, error) {
	endpoint := fmt.Sprintf("%s/v2/project/%s/version", c.BaseURL, url.PathEscape(projectID))

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
	endpoint := fmt.Sprintf("%s/v2/project/%s/version/%s", c.BaseURL, url.PathEscape(projectID), url.PathEscape(pin))

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
// Compat: empty version_type (unrecognized payloads) is treated as
// release-compatible but callers log it. Unknown non-empty types fail closed.
func allowVersionType(versionType, channel string) bool {
	t := strings.ToLower(strings.TrimSpace(versionType))
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "release":
		return t == "" || t == "release"
	case "beta":
		return t == "" || t == "release" || t == "beta"
	case "alpha":
		return t == "" || t == "release" || t == "beta" || t == "alpha"
	default:
		return t == "" || t == "release"
	}
}

// isListed reports whether a version is publicly listed. Compat: empty status
// (unrecognized payloads) is treated as listed but callers log it.
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

// latestMaxPages bounds the paged "latest" scan. The API returns newest
// first with server-side game_versions/loaders filters, but channel filtering
// is client-side, so large projects may need several pages to find a release.
const latestMaxPages = 5

// resolveLatest returns the newest version matching the game_version/loader
// filters and channel policy. The API returns newest first; pagination
// continues until a short page or a match so compatible releases beyond the
// first window are still found.
func (c *ModrinthClient) resolveLatest(ctx context.Context, pCfg config.PluginConfig) (*ModrinthVersion, *ModrinthFile, error) {
	gameVersion := pCfg.NormalizedGameVersion()
	loader := pCfg.NormalizedLoader()
	channel := pCfg.NormalizedChannel()

	for page := 0; page < latestMaxPages; page++ {
		versions, err := c.fetchVersions(ctx, pCfg.ProjectID, gameVersion, loader, latestFetchLimit, page*latestFetchLimit)
		if err != nil {
			return nil, nil, err
		}

		for i := range versions {
			v := &versions[i]
			if strings.TrimSpace(v.Status) == "" || strings.TrimSpace(v.VersionType) == "" {
				fmt.Fprintf(os.Stderr, "warning: Modrinth project %q version %q has empty status/type, allowing for compat\n", pCfg.ProjectID, v.VersionNumber)
			}
			if !isListed(v.Status) || !allowVersionType(v.VersionType, channel) {
				continue
			}
			file, err := selectPrimaryFile(v)
			if err != nil {
				continue
			}
			return v, file, nil
		}
		if len(versions) < latestFetchLimit {
			break
		}
	}

	if strings.TrimSpace(gameVersion) != "" {
		return nil, nil, fmt.Errorf("no %s version of Modrinth project %q supports game version %q (loader %q, channel %q)",
			channel, pCfg.ProjectID, gameVersion, loader, channel)
	}
	return nil, nil, fmt.Errorf("no %s versions found on Modrinth for project %q (loader %q, channel %q)",
		channel, pCfg.ProjectID, loader, channel)
}

// loaderCompatible reports whether a Modrinth version's loader list satisfies
// the requested loader. Empty lists pass (unverifiable). Matching is
// case-insensitive. The bukkit/spigot/paper family is interchangeable because
// Paper servers run bukkit/spigot jars that many projects publish without a
// "paper" loader tag.
func loaderCompatible(loaders []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" || len(loaders) == 0 {
		return true
	}
	for _, l := range loaders {
		if strings.EqualFold(strings.TrimSpace(l), want) {
			return true
		}
	}
	family := map[string]bool{"bukkit": true, "spigot": true, "paper": true}
	if family[want] {
		for _, l := range loaders {
			if family[strings.ToLower(strings.TrimSpace(l))] {
				return true
			}
		}
	}
	return false
}

// resolvePinned returns the exact version_number or version ID. An optional
// game_version is enforced: the pin must list it or resolution fails. Loader
// is intentionally NOT enforced for pins because Paper servers run
// bukkit/spigot jars that many projects publish without a "paper" loader tag.
func (c *ModrinthClient) resolvePinned(ctx context.Context, pCfg config.PluginConfig) (*ModrinthVersion, *ModrinthFile, error) {
	pin := strings.TrimSpace(pCfg.Version)
	gameVersion := pCfg.NormalizedGameVersion()
	loader := pCfg.NormalizedLoader()

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
		if loaderCompatible(ver.Loaders, loader) {
			return ver, file, nil
		}
		// Direct hit has the wrong loader: scan the list for the same pin
		// with a compatible loader before falling back to the direct hit.
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
					continue
				}
				if !loaderCompatible(v.Loaders, loader) {
					continue
				}
				better, err := selectPrimaryFile(v)
				if err != nil {
					continue
				}
				return v, better, nil
			}
			if len(versions) < pinnedPageLimit {
				break
			}
		}
		fmt.Fprintf(os.Stderr, "warning: Modrinth version %q of project %q does not list loader %q (loaders: %s), using it anyway for bukkit compat\n",
			pin, pCfg.ProjectID, loader, strings.Join(ver.Loaders, ", "))
		return ver, file, nil
	}

	// Fallback: scan the unfiltered list (covers version IDs and servers
	// that 404 the direct endpoint). Prefer a loader-compatible match for
	// the same pin; keep the first pin+game_version match as a fallback so
	// bukkit-on-paper pins keep resolving.
	var fallbackVer *ModrinthVersion
	var fallbackFile *ModrinthFile
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
			if loaderCompatible(v.Loaders, loader) {
				return v, file, nil
			}
			if fallbackVer == nil {
				cpVer := *v
				cpFile := *file
				fallbackVer = &cpVer
				fallbackFile = &cpFile
			}
		}
		if len(versions) < pinnedPageLimit {
			break
		}
	}
	if fallbackVer != nil {
		fmt.Fprintf(os.Stderr, "warning: Modrinth version %q of project %q does not list loader %q (loaders: %s), using it anyway for bukkit compat\n",
			pin, pCfg.ProjectID, loader, strings.Join(fallbackVer.Loaders, ", "))
		return fallbackVer, fallbackFile, nil
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

	// Use a long-timeout client for the (large) jar download; the API client
	// default of 30s is too short for slow networks.
	downloadClient := c.HTTPClient
	if downloadClient == nil || downloadClient.Timeout < 5*time.Minute {
		downloadClient = &http.Client{Timeout: 5 * time.Minute}
	}

	res, err := DownloadAndVerify(ctx, DownloadOptions{
		URL:           file.URL,
		TargetPath:    targetPath,
		ExpectedHash:  expectedHash,
		HashAlgorithm: hashAlgo,
		Headers:       headers,
		Client:        downloadClient,
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
