package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the top-level tidy.toml specification.
type Config struct {
	Server    ServerConfig          `toml:"server"`
	Plugins   map[string]PluginConfig `toml:"plugins"`
	Files     map[string]FileConfig `toml:"files"`
	Worlds    map[string]FileConfig `toml:"worlds"`
	Templates TemplateConfig        `toml:"templates"`
}

// ServerConfig defines the server software jar configuration.
type ServerConfig struct {
	Project string `toml:"project"`
	Version string `toml:"version"`
	Build   string `toml:"build"`
	URL     string `toml:"url"` // Disallowed by design: enforces Fill API
}

// PluginConfig defines an individual plugin dependency.
type PluginConfig struct {
	Source      string `toml:"source"`       // "modrinth", "url", "github", or "local"
	ProjectID   string `toml:"project_id"`   // required for modrinth
	Version     string `toml:"version"`      // modrinth plugin version pin, or "latest" (default)
	GameVersion string `toml:"game_version"` // modrinth Minecraft version filter, e.g. "1.21.1"
	Loader      string `toml:"loader"`       // modrinth loader filter, defaults to "paper"
	Channel     string `toml:"channel"`      // "release" (default), "beta" (+beta), "alpha" (all)
	URL         string `toml:"url"`          // required for url
	Repo        string `toml:"repo"`         // required for github: "owner/repo"
	Tag         string `toml:"tag"`          // github release tag, or "latest" (default)
	Asset       string `toml:"asset"`        // required for github: exact release asset filename (e.g. "Vault.jar")
	Path        string `toml:"path"`         // required for local: path to manually-uploaded jar (relative to workdir or absolute)
	SHA256      string `toml:"sha256"`       // mandatory for url, github and local, optional for modrinth
}

// DefaultModrinthLoader is used when a modrinth plugin omits 'loader'.
const DefaultModrinthLoader = "paper"

// DefaultModrinthChannel is used when a modrinth plugin omits 'channel'.
const DefaultModrinthChannel = "release"

// IsLatest reports whether the plugin tracks the newest compatible Modrinth
// version instead of a pinned version number or version ID.
func (p PluginConfig) IsLatest() bool {
	v := strings.TrimSpace(p.Version)
	return v == "" || strings.EqualFold(v, "latest")
}

// NormalizedVersion returns the effective version selector ("latest" when unset).
func (p PluginConfig) NormalizedVersion() string {
	if p.IsLatest() {
		return "latest"
	}
	return strings.TrimSpace(p.Version)
}

// NormalizedLoader returns the effective loader filter (defaults to "paper").
func (p PluginConfig) NormalizedLoader() string {
	if l := strings.ToLower(strings.TrimSpace(p.Loader)); l != "" {
		return l
	}
	return DefaultModrinthLoader
}

// NormalizedChannel returns the effective release channel (defaults to "release").
func (p PluginConfig) NormalizedChannel() string {
	if ch := strings.ToLower(strings.TrimSpace(p.Channel)); ch != "" {
		return ch
	}
	return DefaultModrinthChannel
}

// NormalizedGameVersion returns the trimmed Minecraft version filter ("", if unset).
func (p PluginConfig) NormalizedGameVersion() string {
	return strings.TrimSpace(p.GameVersion)
}

// IsGitHubLatest reports whether the plugin tracks the newest GitHub release
// instead of a pinned tag.
func (p PluginConfig) IsGitHubLatest() bool {
	v := strings.TrimSpace(p.Tag)
	return v == "" || strings.EqualFold(v, "latest")
}

// NormalizedTag returns the effective release tag selector ("latest" when unset).
func (p PluginConfig) NormalizedTag() string {
	if p.IsGitHubLatest() {
		return "latest"
	}
	return strings.TrimSpace(p.Tag)
}

// FileConfig defines a large file or world asset download.
type FileConfig struct {
	Source  string `toml:"source"`  // "http" or "url"
	Path    string `toml:"path"`    // target directory or file destination
	URL     string `toml:"url"`     // direct HTTPS/HTTP URL
	SHA256  string `toml:"sha256"`  // MANDATORY: 64-char hex SHA-256
	Extract bool   `toml:"extract"` // whether to uncompress .zip / .tar.gz / .tgz / .tar
	Once    *bool  `toml:"once"`    // defaults to true (protects existing world/file data on reboot)
}

// IsOnce returns true if Once is unset (nil) or explicitly true.
func (f *FileConfig) IsOnce() bool {
	if f.Once == nil {
		return true
	}
	return *f.Once
}

// TemplateConfig defines template replacement settings.
type TemplateConfig struct {
	Paths    []string `toml:"paths"`
	Disabled bool     `toml:"disabled"`
}

// LoadConfig reads and parses a tidy.toml file from the given file path.
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}
	return ParseConfig(data)
}

// ParseConfig decodes TOML bytes and validates configuration rules.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid TOML syntax: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// GetAllFiles returns a combined map of all files and worlds.
func (c *Config) GetAllFiles() map[string]FileConfig {
	result := make(map[string]FileConfig)
	for k, v := range c.Files {
		result[k] = v
	}
	for k, v := range c.Worlds {
		// If worlds has an entry with the same name, files takes precedence
		if _, exists := result[k]; !exists {
			result[k] = v
		}
	}
	return result
}

// Validate verifies that the configuration conforms to all requirements.
func (c *Config) Validate() error {
	// Validate Server
	if strings.TrimSpace(c.Server.URL) != "" {
		return errors.New("invalid [server] configuration: direct 'url' is not supported; use 'project' and 'version' for server API resolution (PaperMC Fill or PurpurMC)")
	}
	if strings.TrimSpace(c.Server.Project) == "" {
		return errors.New("invalid [server] configuration: 'project' is required (e.g. 'paper' or 'purpur')")
	}
	if strings.TrimSpace(c.Server.Version) == "" {
		return errors.New("invalid [server] configuration: 'version' is required (e.g. '26.2')")
	}
	if strings.TrimSpace(c.Server.Build) == "" {
		c.Server.Build = "latest"
	}

	// Validate Plugins
	for name, p := range c.Plugins {
		source := strings.ToLower(strings.TrimSpace(p.Source))
		switch source {
		case "modrinth":
			if strings.TrimSpace(p.ProjectID) == "" {
				return fmt.Errorf("plugin %q: modrinth source requires 'project_id'", name)
			}
			if strings.TrimSpace(p.URL) != "" {
				return fmt.Errorf("plugin %q: 'url' only applies to url source", name)
			}
			if strings.TrimSpace(p.Repo) != "" || strings.TrimSpace(p.Tag) != "" || strings.TrimSpace(p.Asset) != "" {
				return fmt.Errorf("plugin %q: 'repo'/'tag'/'asset' only apply to github source", name)
			}
			if strings.TrimSpace(p.Path) != "" {
				return fmt.Errorf("plugin %q: 'path' only applies to local source", name)
			}
			// 'version' is optional and defaults to "latest" (track newest
			// compatible release). Apply defaults so downstream code and
			// state comparisons see normalized values.
			if p.IsLatest() {
				p.Version = "latest"
			} else {
				p.Version = strings.TrimSpace(p.Version)
			}
			p.GameVersion = strings.TrimSpace(p.GameVersion)
			p.Loader = p.NormalizedLoader()
			p.Channel = p.NormalizedChannel()
			switch p.Channel {
			case "release", "beta", "alpha":
			default:
				return fmt.Errorf("plugin %q: invalid 'channel' %q (supported: 'release', 'beta', 'alpha')", name, p.Channel)
			}
			if sha := strings.TrimSpace(p.SHA256); sha != "" {
				if err := validateSHA256Hex(sha); err != nil {
					return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
				}
				// Note: combining 'sha256' with version="latest" freezes the
				// jar, so a future upstream release fails verification by
				// design. Users tracking 'latest' should normally omit it.
			}
			c.Plugins[name] = p
		case "url":
			if strings.TrimSpace(p.URL) == "" {
				return fmt.Errorf("plugin %q: url source requires 'url'", name)
			}
			if strings.TrimSpace(p.Path) != "" {
				return fmt.Errorf("plugin %q: 'path' only applies to local source", name)
			}
			if strings.TrimSpace(p.GameVersion) != "" || strings.TrimSpace(p.Loader) != "" || strings.TrimSpace(p.Channel) != "" {
				return fmt.Errorf("plugin %q: 'game_version'/'loader'/'channel' only apply to modrinth source", name)
			}
			if strings.TrimSpace(p.ProjectID) != "" || strings.TrimSpace(p.Version) != "" {
				return fmt.Errorf("plugin %q: 'project_id'/'version' only apply to modrinth source", name)
			}
			if strings.TrimSpace(p.Repo) != "" || strings.TrimSpace(p.Tag) != "" || strings.TrimSpace(p.Asset) != "" {
				return fmt.Errorf("plugin %q: 'repo'/'tag'/'asset' only apply to github source", name)
			}
			sha := strings.TrimSpace(p.SHA256)
			if sha == "" {
				return fmt.Errorf("plugin %q: url source MUST provide 'sha256' for verification", name)
			}
			if err := validateSHA256Hex(sha); err != nil {
				return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
			}
		case "github":
			repo := strings.TrimSpace(p.Repo)
			if repo == "" {
				return fmt.Errorf("plugin %q: github source requires 'repo' (e.g. 'MilkBowl/Vault')", name)
			}
			if err := validateGitHubRepo(repo); err != nil {
				return fmt.Errorf("plugin %q: invalid 'repo': %w", name, err)
			}
			asset := strings.TrimSpace(p.Asset)
			if asset == "" {
				return fmt.Errorf("plugin %q: github source requires 'asset' (exact release asset filename, e.g. 'Vault.jar')", name)
			}
			if strings.Contains(asset, "/") || strings.Contains(asset, "\\") {
				return fmt.Errorf("plugin %q: invalid 'asset' %q: must be a filename without path separators", name, p.Asset)
			}
			if !strings.HasSuffix(strings.ToLower(asset), ".jar") {
				return fmt.Errorf("plugin %q: invalid 'asset' %q: must point to a .jar file", name, p.Asset)
			}
			// 'tag' is optional and defaults to "latest" (track newest
			// release). Apply defaults so downstream code and state
			// comparisons see normalized values.
			p.Repo = repo
			p.Tag = p.NormalizedTag()
			p.Asset = asset
			if strings.TrimSpace(p.URL) != "" {
				return fmt.Errorf("plugin %q: 'url' only applies to url source", name)
			}
			if strings.TrimSpace(p.Path) != "" {
				return fmt.Errorf("plugin %q: 'path' only applies to local source", name)
			}
			if strings.TrimSpace(p.ProjectID) != "" || strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.GameVersion) != "" || strings.TrimSpace(p.Loader) != "" || strings.TrimSpace(p.Channel) != "" {
				return fmt.Errorf("plugin %q: 'project_id'/'version'/'game_version'/'loader'/'channel' only apply to modrinth source", name)
			}
			sha := strings.TrimSpace(p.SHA256)
			if sha == "" {
				return fmt.Errorf("plugin %q: github source MUST provide 'sha256' for verification", name)
			}
			if err := validateSHA256Hex(sha); err != nil {
				return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
			}
			c.Plugins[name] = p
		case "local":
			if strings.TrimSpace(p.Path) == "" {
				return fmt.Errorf("plugin %q: local source requires 'path' to the manually-uploaded jar (e.g. 'plugins/MyPlugin.jar')", name)
			}
			if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(p.Path)), ".jar") {
				return fmt.Errorf("plugin %q: local source 'path' must point to a .jar file", name)
			}
			if strings.TrimSpace(p.URL) != "" {
				return fmt.Errorf("plugin %q: 'url' only applies to url source", name)
			}
			if strings.TrimSpace(p.ProjectID) != "" || strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.GameVersion) != "" || strings.TrimSpace(p.Loader) != "" || strings.TrimSpace(p.Channel) != "" {
				return fmt.Errorf("plugin %q: 'project_id'/'version'/'game_version'/'loader'/'channel' only apply to modrinth source", name)
			}
			if strings.TrimSpace(p.Repo) != "" || strings.TrimSpace(p.Tag) != "" || strings.TrimSpace(p.Asset) != "" {
				return fmt.Errorf("plugin %q: 'repo'/'tag'/'asset' only apply to github source", name)
			}
			sha := strings.TrimSpace(p.SHA256)
			if sha == "" {
				return fmt.Errorf("plugin %q: local source MUST provide 'sha256' for verification", name)
			}
			if err := validateSHA256Hex(sha); err != nil {
				return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
			}
			p.Path = strings.TrimSpace(p.Path)
			c.Plugins[name] = p
		default:
			return fmt.Errorf("plugin %q: unsupported source %q (supported: 'modrinth', 'url', 'github', 'local')", name, p.Source)
		}
	}

	// Validate Files and Worlds (All must have mandatory SHA-256)
	validateFileSection := func(sectionName string, items map[string]FileConfig) error {
		for name, f := range items {
			rawPath := strings.TrimSpace(f.Path)
			if rawPath == "" {
				return fmt.Errorf("%s %q: 'path' destination directory or file path is required", sectionName, name)
			}
			if filepath.IsAbs(rawPath) {
				return fmt.Errorf("%s %q: 'path' must be relative to the workdir, got absolute path %q", sectionName, name, f.Path)
			}
			for _, part := range strings.Split(filepath.ToSlash(rawPath), "/") {
				if part == ".." {
					return fmt.Errorf("%s %q: 'path' must not contain parent directory traversals (..)", sectionName, name)
				}
			}
			if strings.TrimSpace(f.URL) == "" {
				return fmt.Errorf("%s %q: 'url' is required", sectionName, name)
			}
			switch strings.ToLower(strings.TrimSpace(f.Source)) {
			case "", "http", "https", "url":
			default:
				return fmt.Errorf("%s %q: unsupported source %q (supported: 'http', 'https', 'url')", sectionName, name, f.Source)
			}
			sha := strings.TrimSpace(f.SHA256)
			if sha == "" {
				return fmt.Errorf("%s %q: MUST provide 'sha256' for verification", sectionName, name)
			}
			if err := validateSHA256Hex(sha); err != nil {
				return fmt.Errorf("%s %q: invalid 'sha256': %w", sectionName, name, err)
			}
		}
		return nil
	}

	if err := validateFileSection("files", c.Files); err != nil {
		return err
	}
	if err := validateFileSection("worlds", c.Worlds); err != nil {
		return err
	}

	for name := range c.Files {
		if _, exists := c.Worlds[name]; exists {
			return fmt.Errorf("duplicate file/world name %q: present in both [files] and [worlds]", name)
		}
	}

	// Default template paths if none configured. Must stay in sync with
	// templating.IsSupportedConfigFile (.yml/.yaml/.properties/.json/.conf/
	// .toml/.txt/.cfg/.env).
	if len(c.Templates.Paths) == 0 {
		c.Templates.Paths = []string{
			"plugins/**/*.yml",
			"plugins/**/*.yaml",
			"plugins/**/*.properties",
			"plugins/**/*.json",
			"plugins/**/*.conf",
			"plugins/**/*.toml",
			"plugins/**/*.txt",
			"plugins/**/*.cfg",
			"plugins/**/*.env",
			"config/**/*.yml",
			"config/**/*.yaml",
			"config/**/*.properties",
			"config/**/*.json",
			"config/**/*.conf",
			"config/**/*.toml",
			"config/**/*.txt",
			"config/**/*.cfg",
			"config/**/*.env",
			"*.properties",
			"*.yml",
			"*.yaml",
			"*.json",
			"*.conf",
			"*.toml",
			"*.txt",
			"*.cfg",
			"*.env",
		}
	}

	return nil
}

func validateSHA256Hex(sha string) error {
	if len(sha) != 64 {
		return fmt.Errorf("expected 64 hex characters, got %d", len(sha))
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return fmt.Errorf("invalid hexadecimal characters: %w", err)
	}
	return nil
}

// validateGitHubRepo verifies the "owner/repo" shape (exactly one slash,
// non-empty sides, no whitespace).
func validateGitHubRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("expected \"owner/repo\" (e.g. 'MilkBowl/Vault'), got %q", repo)
	}
	if strings.ContainsAny(repo, " \t\n\r") {
		return fmt.Errorf("expected \"owner/repo\" without whitespace, got %q", repo)
	}
	return nil
}
