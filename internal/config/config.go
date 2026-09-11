package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	Source    string `toml:"source"`     // "modrinth" or "url"
	ProjectID string `toml:"project_id"` // required for modrinth
	Version   string `toml:"version"`    // required for modrinth
	URL       string `toml:"url"`        // required for url
	SHA256    string `toml:"sha256"`     // mandatory for url, optional for modrinth
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
		return errors.New("invalid [server] configuration: direct 'url' is not supported; use project and version for PaperMC Fill API resolution")
	}
	if strings.TrimSpace(c.Server.Project) == "" {
		return errors.New("invalid [server] configuration: 'project' is required (e.g. 'paper')")
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
			if strings.TrimSpace(p.Version) == "" {
				return fmt.Errorf("plugin %q: modrinth source requires 'version'", name)
			}
			if sha := strings.TrimSpace(p.SHA256); sha != "" {
				if err := validateSHA256Hex(sha); err != nil {
					return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
				}
			}
		case "url":
			if strings.TrimSpace(p.URL) == "" {
				return fmt.Errorf("plugin %q: url source requires 'url'", name)
			}
			sha := strings.TrimSpace(p.SHA256)
			if sha == "" {
				return fmt.Errorf("plugin %q: url source MUST provide 'sha256' for verification", name)
			}
			if err := validateSHA256Hex(sha); err != nil {
				return fmt.Errorf("plugin %q: invalid 'sha256': %w", name, err)
			}
		default:
			return fmt.Errorf("plugin %q: unsupported source %q (supported: 'modrinth', 'url')", name, p.Source)
		}
	}

	// Validate Files and Worlds (All must have mandatory SHA-256)
	validateFileSection := func(sectionName string, items map[string]FileConfig) error {
		for name, f := range items {
			if strings.TrimSpace(f.Path) == "" {
				return fmt.Errorf("%s %q: 'path' destination directory or file path is required", sectionName, name)
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

	// Default template paths if none configured
	if len(c.Templates.Paths) == 0 {
		c.Templates.Paths = []string{
			"plugins/**/*.yml",
			"plugins/**/*.yaml",
			"plugins/**/*.properties",
			"plugins/**/*.json",
			"plugins/**/*.conf",
			"config/**/*.yml",
			"config/**/*.yaml",
			"*.properties",
			"*.yml",
			"*.yaml",
			"*.json",
			"*.conf",
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
