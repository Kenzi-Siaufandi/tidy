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

[files.overworld]
source = "http"
path = "world"
url = "https://example.com/world.tar.gz"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
extract = true

[worlds.nether]
source = "http"
path = "world_nether"
url = "https://example.com/nether.zip"
sha256 = "b8a8b167520e5c9a0bc43fdfb0d4c1b48b6f3c1b1842e47856d117a56114a1e9"
extract = true
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

	// Verify files and worlds
	world, ok := cfg.Files["overworld"]
	if !ok {
		t.Fatalf("expected file overworld")
	}
	if world.Path != "world" || !world.Extract || !world.IsOnce() {
		t.Errorf("unexpected overworld config: %+v", world)
	}

	allFiles := cfg.GetAllFiles()
	if len(allFiles) != 2 {
		t.Errorf("expected 2 files in GetAllFiles, got %d", len(allFiles))
	}
	if _, ok := allFiles["nether"]; !ok {
		t.Errorf("expected nether in GetAllFiles")
	}
}

func TestParseConfig_FileValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "files missing sha256",
			raw: `
[server]
project = "paper"
version = "26.2"

[files.test]
path = "world"
url = "https://example.com/test.zip"
`,
		},
		{
			name: "files invalid sha256 length",
			raw: `
[server]
project = "paper"
version = "26.2"

[files.test]
path = "world"
url = "https://example.com/test.zip"
sha256 = "12345"
`,
		},
		{
			name: "files missing path",
			raw: `
[server]
project = "paper"
version = "26.2"

[files.test]
url = "https://example.com/test.zip"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
`,
		},
		{
			name: "files missing url",
			raw: `
[server]
project = "paper"
version = "26.2"

[files.test]
path = "world"
sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.raw))
			if err == nil {
				t.Errorf("expected validation error for %s, got nil", tt.name)
			}
		})
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
