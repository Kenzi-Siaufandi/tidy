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
	Server    ServerConfig            `toml:"server"`
	Plugins   map[string]PluginConfig `toml:"plugins"`
	Templates TemplateConfig          `toml:"templates"`
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
	SHA256    string `toml:"sha256"`     // mandatory for url, optional for others
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
		case "url":
			if strings.TrimSpace(p.URL) == "" {
				return fmt.Errorf("plugin %q: url source requires 'url'", name)
			}
			sha := strings.TrimSpace(p.SHA256)
			if sha == "" {
				return fmt.Errorf("plugin %q: url source MUST provide 'sha256' for verification", name)
			}
			if len(sha) != 64 {
				return fmt.Errorf("plugin %q: 'sha256' must be a 64-character hex string", name)
			}
			if _, err := hex.DecodeString(sha); err != nil {
				return fmt.Errorf("plugin %q: invalid hex format in 'sha256': %w", name, err)
			}
		default:
			return fmt.Errorf("plugin %q: unsupported source %q (supported: 'modrinth', 'url')", name, p.Source)
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
