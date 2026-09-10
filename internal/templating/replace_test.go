package templating

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceString(t *testing.T) {
	t.Setenv("DB_HOST", "10.0.0.5")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_PASS", "super_secret_pw")

	input := `
database:
  host: {{DB_HOST}}
  port: {{ DB_PORT }}
  password: {{DB_PASS}}
  database: {{UNSET_DATABASE_NAME}}
`
	res, count, missing := ReplaceString(input)

	if count != 3 {
		t.Errorf("expected 3 replacements, got %d", count)
	}
	if len(missing) != 1 || missing[0] != "UNSET_DATABASE_NAME" {
		t.Errorf("expected UNSET_DATABASE_NAME in missing, got %v", missing)
	}

	expectedSubstring1 := "host: 10.0.0.5"
	expectedSubstring2 := "port: 3306"
	expectedSubstring3 := "password: super_secret_pw"
	expectedSubstring4 := "database: {{UNSET_DATABASE_NAME}}"

	for _, sub := range []string{expectedSubstring1, expectedSubstring2, expectedSubstring3, expectedSubstring4} {
		if !contains(res, sub) {
			t.Errorf("expected result to contain %q, but got:\n%s", sub, res)
		}
	}
}

func TestProcessDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MYSQL_USER", "mc_user")
	t.Setenv("MYSQL_PASSWORD", "secret123")

	pluginDir := filepath.Join(tmpDir, "plugins", "LuckPerms")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	configFile := filepath.Join(pluginDir, "config.yml")
	configContent := `
data:
  address: "localhost:3306"
  username: "{{MYSQL_USER}}"
  password: "{{MYSQL_PASSWORD}}"
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Jar file that shouldn't be touched
	jarFile := filepath.Join(pluginDir, "LuckPerms.jar")
	if err := os.WriteFile(jarFile, []byte("binary jar data {{MYSQL_USER}}"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ProcessDirectory(tmpDir, nil)
	if err != nil {
		t.Fatalf("ProcessDirectory failed: %v", err)
	}

	if res.Replacements != 2 {
		t.Errorf("expected 2 replacements, got %d", res.Replacements)
	}

	// Verify YAML content was replaced
	updatedData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(updatedData), `username: "mc_user"`) {
		t.Errorf("config.yml username not replaced: %s", string(updatedData))
	}
	if !contains(string(updatedData), `password: "secret123"`) {
		t.Errorf("config.yml password not replaced: %s", string(updatedData))
	}

	// Verify jar file was not modified
	jarData, err := os.ReadFile(jarFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(jarData) != "binary jar data {{MYSQL_USER}}" {
		t.Errorf("jar file was unexpectedly modified")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && len(substr) > 0 && searchSubstr(s, substr)))
}

func searchSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
