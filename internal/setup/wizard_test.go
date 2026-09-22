package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func TestBuildTOML(t *testing.T) {
	spec := WizardSpec{
		Server: WizardServer{Project: "purpur", Version: "1.21.8", Build: "latest"},
		Plugins: []WizardPlugin{
			{Name: "LuckPerms", Source: "modrinth", ProjectID: "luckperms", Version: "latest", GameVersion: "1.21.8", Loader: "paper", Channel: "release"},
			{Name: "Vault", Source: "url", URL: "https://example.com/Vault.jar", SHA256: strings.Repeat("a", 64)},
		},
	}
	out := BuildTOML(spec)
	for _, want := range []string{
		`project = "purpur"`, `version = "1.21.8"`,
		"[plugins.LuckPerms]", `project_id = "luckperms"`, `game_version = "1.21.8"`,
		"[plugins.Vault]", `url = "https://example.com/Vault.jar"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("BuildTOML missing %q:\n%s", want, out)
		}
	}
	if _, err := config.ParseConfig([]byte(out)); err != nil {
		t.Errorf("generated TOML does not validate: %v\n%s", err, out)
	}
}

func TestBuildTOML_SkippedPlugins(t *testing.T) {
	out := BuildTOML(WizardSpec{Server: WizardServer{Project: "paper", Version: "26.2"}})
	if strings.Contains(out, "[plugins.") {
		t.Errorf("expected no plugin sections:\n%s", out)
	}
	if _, err := config.ParseConfig([]byte(out)); err != nil {
		t.Errorf("server-only TOML does not validate: %v", err)
	}
}

func TestBuildTOML_QuotedKey(t *testing.T) {
	out := BuildTOML(WizardSpec{
		Server:  WizardServer{Project: "paper", Version: "26.2"},
		Plugins: []WizardPlugin{{Name: "My Cool Plugin!", Source: "modrinth", ProjectID: "x"}},
	})
	if !strings.Contains(out, `[plugins."My Cool Plugin!"]`) {
		t.Errorf("expected quoted plugin key:\n%s", out)
	}
	if _, err := config.ParseConfig([]byte(out)); err != nil {
		t.Errorf("quoted-key TOML does not validate: %v", err)
	}
}

func TestCmdNew_Scripted(t *testing.T) {
	root := t.TempDir()
	// Server answers, one modrinth plugin, then finish + confirm.
	script := strings.Join([]string{
		"purpur",    // project
		"",          // version -> suggested 1.21.8
		"",          // build -> latest
		"LuckPerms", // plugin name
		"",          // source -> modrinth
		"",          // project_id -> luckperms
		"",          // version -> latest
		"",          // game_version -> skip
		"",          // loader -> paper
		"",          // channel -> release
		"",          // finish plugins
		"Y",         // confirm write
	}, "\n") + "\n"

	var stdout strings.Builder
	code := CmdNew(root, "", false, strings.NewReader(script), &stdout)
	if code != 0 {
		t.Fatalf("CmdNew = %d, output:\n%s", code, stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "tidy.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseConfig(raw)
	if err != nil {
		t.Fatalf("written tidy.toml invalid: %v", err)
	}
	if cfg.Server.Project != "purpur" || cfg.Server.Version != "1.21.8" {
		t.Errorf("unexpected server: %+v", cfg.Server)
	}
	if p, ok := cfg.Plugins["LuckPerms"]; !ok || p.ProjectID != "luckperms" {
		t.Errorf("unexpected plugins: %+v", cfg.Plugins)
	}

	// Second run without --force must refuse.
	if code := CmdNew(root, "", false, strings.NewReader(""), &strings.Builder{}); code == 0 {
		t.Error("expected refusal when tidy.toml exists without --force")
	}
}

func TestBuildTOML_LocalPlugin(t *testing.T) {
	spec := WizardSpec{
		Server:  WizardServer{Project: "paper", Version: "26.2", Build: "latest"},
		Plugins: []WizardPlugin{{Name: "Custom", Source: "local", Path: "plugins/Custom.jar", SHA256: strings.Repeat("b", 64)}},
	}
	out := BuildTOML(spec)
	for _, want := range []string{
		`[plugins.Custom]`, `source = "local"`, `path = "plugins/Custom.jar"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("BuildTOML missing %q:\n%s", want, out)
		}
	}
	cfg, err := config.ParseConfig([]byte(out))
	if err != nil {
		t.Fatalf("generated local TOML does not validate: %v\n%s", err, out)
	}
	if p := cfg.Plugins["Custom"]; p.Path != "plugins/Custom.jar" {
		t.Errorf("unexpected local path: %+v", p)
	}
}
