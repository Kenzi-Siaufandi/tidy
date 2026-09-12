package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListJars(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "paper-26.2-123.jar"), []byte("12345678"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("paper-26.2-123.jar", filepath.Join(dir, "server.jar")); err != nil {
		t.Skipf("symlink not permitted: %v", err)
	}

	jars, err := ListJars(dir)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(jars) != 2 {
		t.Fatalf("expected 2 jars, got %d", len(jars))
	}
	byName := map[string]JarInfo{}
	for _, j := range jars {
		byName[j.Name] = j
	}
	srv, ok := byName["server.jar"]
	if !ok {
		t.Fatal("expected server.jar listed")
	}
	if !srv.IsSymlink || srv.LinkTarget != "paper-26.2-123.jar" {
		t.Fatalf("expected symlink -> paper jar, got %+v", srv)
	}
	if byName["paper-26.2-123.jar"].Size != 8 {
		t.Fatalf("expected size 8, got %+v", byName["paper-26.2-123.jar"])
	}
}

func TestEulaStatus(t *testing.T) {
	dir := t.TempDir()
	if got := EulaStatus(dir); got != "missing" {
		t.Fatalf("expected missing, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := EulaStatus(dir); got != "not-accepted" {
		t.Fatalf("expected not-accepted, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("# comment\neula=true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := EulaStatus(dir); got != "ok" {
		t.Fatalf("expected ok, got %q", got)
	}
}

func TestJavaVersionMissingBinary(t *testing.T) {
	if _, err := JavaVersion(t.Context(), "tidy-test-no-such-binary-xyz"); err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}
