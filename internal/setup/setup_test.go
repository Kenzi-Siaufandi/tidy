package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFindServerRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "server")
	nested := filepath.Join(root, "plugins", "Essentials")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "server.properties"), "motd=test\n")

	if got := FindServerRoot(nested); got != root {
		t.Fatalf("FindServerRoot(nested) = %q, want %q", got, root)
	}

	plain := filepath.Join(base, "empty")
	if err := os.MkdirAll(plain, 0755); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(plain)
	if got := FindServerRoot(plain); got != abs {
		t.Fatalf("FindServerRoot(plain) = %q, want %q", got, abs)
	}
}

func TestScanRepository(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "server.properties"), "motd=test\n")
	writeFile(t, filepath.Join(root, "bukkit.yml"), "settings: {}\n")
	writeFile(t, filepath.Join(root, "config", "paper-global.yml"), "x: 1\n")
	writeFile(t, filepath.Join(root, "plugins", "Essentials", "config.yml"), "a: b\n")
	writeFile(t, filepath.Join(root, "plugins", "Essentials", "data.db"), "binary")
	writeFile(t, filepath.Join(root, "plugins", "Vault", "Vault.jar"), "binary")
	writeFile(t, filepath.Join(root, "world", "region", "r.0.0.mca"), "binary")
	writeFile(t, filepath.Join(root, "world_nether", "level.dat"), "binary")
	writeFile(t, filepath.Join(root, "logs", "latest.log"), "log")
	writeFile(t, filepath.Join(root, "cache", "foo.yml"), "x: 1\n")
	writeFile(t, filepath.Join(root, "usercache.json"), "[]\n")

	res, err := ScanRepository(root)
	if err != nil {
		t.Fatal(err)
	}

	hasSuffix := func(files []string, suffix string) bool {
		for _, f := range files {
			if strings.HasSuffix(filepath.ToSlash(f), suffix) {
				return true
			}
		}
		return false
	}

	for _, want := range []string{"server.properties", "bukkit.yml", "config/paper-global.yml"} {
		if !hasSuffix(res.RootConfigs, want) {
			t.Errorf("RootConfigs missing %s: %v", want, res.RootConfigs)
		}
	}
	if files := res.PluginConfigs["Essentials"]; !hasSuffix(files, "plugins/Essentials/config.yml") {
		t.Errorf("Essentials configs missing config.yml: %v", files)
	}
	for _, f := range res.RootConfigs {
		for _, bad := range []string{".db", ".jar", ".mca", "usercache.json", "cache/foo.yml"} {
			if strings.HasSuffix(f, bad) {
				t.Errorf("RootConfigs contains forbidden %s", f)
			}
		}
	}
	if res.Ignored.Jars < 1 {
		t.Errorf("Ignored.Jars = %d, want >= 1", res.Ignored.Jars)
	}
	if res.Ignored.Databases < 1 {
		t.Errorf("Ignored.Databases = %d, want >= 1", res.Ignored.Databases)
	}
	if res.Ignored.WorldDirs < 1 {
		t.Errorf("Ignored.WorldDirs = %d, want >= 1", res.Ignored.WorldDirs)
	}
}

func TestIsForbiddenTracked(t *testing.T) {
	cases := map[string]bool{
		"world/level.dat":               true,
		"world_nether/level.dat":        true,
		"plugins/Foo.jar":               true,
		"libraries/paper.jar":           true,
		"plugins/Essentials/data.db":    true,
		"plugins/Core/map.sqlite3":      true,
		"plugins/Core/map.sqlite":       true,
		"usercache.json":                true,
		"worlds-hub.zip":                true,
		"plugins/Essentials/config.yml": false,
		"server.properties":             false,
		"bukkit.yml":                    false,
		"config/paper-global.yml":       false,
	}
	for path, want := range cases {
		if got := IsForbiddenTracked(path); got != want {
			t.Errorf("IsForbiddenTracked(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCmdInitWritesFiles(t *testing.T) {
	root := t.TempDir()
	if code := CmdInit(root, false); code != 0 {
		t.Fatalf("CmdInit = %d, want 0", code)
	}
	for _, name := range []string{".gitignore", ".gitattributes"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
	content, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !strings.Contains(string(content), "*.jar") || !strings.Contains(string(content), "world/") {
		t.Error(".gitignore missing expected *.jar/world rules")
	}

	custom := "# custom\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	if code := CmdInit(root, false); code != 0 {
		t.Fatalf("CmdInit (existing) = %d, want 0", code)
	}
	kept, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(kept) != custom {
		t.Error("CmdInit without --force overwrote .gitignore")
	}
	if code := CmdInit(root, true); code != 0 {
		t.Fatalf("CmdInit --force = %d, want 0", code)
	}
	overwritten, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !strings.Contains(string(overwritten), "*.jar") {
		t.Error("CmdInit --force did not restore default .gitignore")
	}
}

func TestCmdCheckDetectsForbidden(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "server.properties"), "motd=test\n")
	if code := CmdInit(root, false); code != 0 {
		t.Fatalf("CmdInit = %d", code)
	}
	// Track a forbidden JAR (force-add simulates a file committed before .gitignore existed).
	writeFile(t, filepath.Join(root, "plugins", "Bad.jar"), "binary")
	runGit(root, "add", "-f", "plugins/Bad.jar")

	if code := CmdCheck(root); code == 0 {
		t.Error("CmdCheck = 0 with forbidden tracked JAR, want non-zero")
	}

	runGit(root, "rm", "--cached", "-q", "plugins/Bad.jar")
	if code := CmdCheck(root); code != 0 {
		t.Errorf("CmdCheck = %d after untracking JAR, want 0", code)
	}
}
