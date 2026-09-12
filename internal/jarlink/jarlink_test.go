package jarlink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncCreatesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := "paper-26.2-123.jar"
	if err := os.WriteFile(filepath.Join(dir, target), []byte("jar-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	alias, err := Sync(dir, target, "server.jar")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if alias != "server.jar" {
		t.Fatalf("expected alias server.jar, got %q", alias)
	}
	linkTarget, err := os.Readlink(filepath.Join(dir, "server.jar"))
	if err != nil {
		// Copy fallback on filesystems without symlink privs.
		data, readErr := os.ReadFile(filepath.Join(dir, "server.jar"))
		if readErr != nil || string(data) != "jar-bytes" {
			t.Fatalf("expected symlink or copied content, got linkErr=%v readErr=%v", err, readErr)
		}
		return
	}
	if linkTarget != target {
		t.Fatalf("expected link -> %q, got %q", target, linkTarget)
	}
}

func TestSyncSameNameNoop(t *testing.T) {
	dir := t.TempDir()
	if alias, err := Sync(dir, "server.jar", "server.jar"); err != nil || alias != "" {
		t.Fatalf("expected noop, got alias=%q err=%v", alias, err)
	}
}

func TestSyncRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := Sync(dir, "paper.jar", "../evil.jar"); err == nil {
		t.Fatal("expected error on traversal alias, got nil")
	}
}

func TestSyncRefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "server.jar"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(dir, "paper.jar", "server.jar"); err == nil {
		t.Fatal("expected error when alias is a directory, got nil")
	}
}

func TestAliasFromEnv(t *testing.T) {
	if got := AliasFromEnv(func(string) string { return "" }); got != "server.jar" {
		t.Fatalf("expected default server.jar, got %q", got)
	}
	// Active egg variable (egg-tidy.json) wins over the legacy one.
	getenv := func(k string) string {
		switch k {
		case "SERVER_JAR":
			return "  current.jar "
		case "SERVER_JARFILE":
			return "legacy.jar"
		}
		return ""
	}
	if got := AliasFromEnv(getenv); got != "current.jar" {
		t.Fatalf("expected current.jar precedence, got %q", got)
	}
	legacyOnly := func(k string) string {
		if k == "SERVER_JARFILE" {
			return "legacy.jar"
		}
		return ""
	}
	if got := AliasFromEnv(legacyOnly); got != "legacy.jar" {
		t.Fatalf("expected legacy.jar fallback, got %q", got)
	}
}
