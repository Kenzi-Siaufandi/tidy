package config

import (
	"testing"
)

func TestParseConfig_Valid(t *testing.T) {
	raw := `
[server]
project = "paper"
version = "26.2"

[plugins.FastAsyncWorldEdit]
source = "modrinth"
project_id = "fastasyncworldedit"
version = "2.11.2"

[plugins.Vault]
source = "url"
url = "https://github.com/MilkBowl/Vault/releases/download/1.7.3/Vault.jar"
sha256 = "4b281b7e41e8c783dbd63f58a3a0e69888be62d6cb35661d9962a9ec2b10a26b"
`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}

	if cfg.Server.Project != "paper" {
		t.Errorf("expected server project paper, got %s", cfg.Server.Project)
	}
	if cfg.Server.Version != "26.2" {
		t.Errorf("expected server version 26.2, got %s", cfg.Server.Version)
	}
	if cfg.Server.Build != "latest" {
		t.Errorf("expected default build 'latest', got %s", cfg.Server.Build)
	}

	fawe, ok := cfg.Plugins["FastAsyncWorldEdit"]
	if !ok {
		t.Fatalf("expected plugin FastAsyncWorldEdit")
	}
	if fawe.Source != "modrinth" || fawe.ProjectID != "fastasyncworldedit" || fawe.Version != "2.11.2" {
		t.Errorf("unexpected FAWE plugin config: %+v", fawe)
	}

	vault, ok := cfg.Plugins["Vault"]
	if !ok {
		t.Fatalf("expected plugin Vault")
	}
	if vault.Source != "url" || vault.SHA256 != "4b281b7e41e8c783dbd63f58a3a0e69888be62d6cb35661d9962a9ec2b10a26b" {
		t.Errorf("unexpected Vault plugin config: %+v", vault)
	}
}

func TestParseConfig_ServerURLDisallowed(t *testing.T) {
	raw := `
[server]
project = "paper"
version = "26.2"
url = "https://example.com/paper.jar"
`
	_, err := ParseConfig([]byte(raw))
	if err == nil {
		t.Fatal("expected error when server url is provided, got nil")
	}
}

func TestParseConfig_ServerMissingFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "missing project",
			raw: `
[server]
version = "26.2"
`,
		},
		{
			name: "missing version",
			raw: `
[server]
project = "paper"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.raw))
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestParseConfig_PluginValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "modrinth missing project_id",
			raw: `
[server]
project = "paper"
version = "26.2"

[plugins.Test]
source = "modrinth"
version = "1.0.0"
`,
		},
		{
			name: "modrinth missing version",
			raw: `
[server]
project = "paper"
version = "26.2"

[plugins.Test]
source = "modrinth"
project_id = "test"
`,
		},
		{
			name: "url missing sha256",
			raw: `
[server]
project = "paper"
version = "26.2"

[plugins.Test]
source = "url"
url = "https://example.com/test.jar"
`,
		},
		{
			name: "url invalid sha256 length",
			raw: `
[server]
project = "paper"
version = "26.2"

[plugins.Test]
source = "url"
url = "https://example.com/test.jar"
sha256 = "tooshort"
`,
		},
		{
			name: "unsupported source",
			raw: `
[server]
project = "paper"
version = "26.2"

[plugins.Test]
source = "spiget"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.raw))
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}
