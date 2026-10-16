package resolver

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func TestModrinthRejectsPinBeforeReplacingInstalledJar(t *testing.T) {
	old, newer := []byte("old pinned plugin"), []byte("new plugin")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(newer) }))
	defer server.Close()
	for _, match := range []bool{false, true} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "plugins"), 0755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "plugins", "plugin.jar")
		if err := os.WriteFile(target, old, 0644); err != nil {
			t.Fatal(err)
		}
		pin := fmt.Sprintf("%x", sha256.Sum256(old))
		want := old
		if match {
			pin = fmt.Sprintf("%x", sha256.Sum256(newer))
			want = newer
		}
		_, err := NewModrinthClient("", "", nil).Download(context.Background(), "demo", config.PluginConfig{SHA256: pin}, root, &ModrinthVersion{ID: "new", VersionNumber: "2"}, &ModrinthFile{URL: server.URL, Filename: "plugin.jar", Hashes: map[string]string{"sha512": fmt.Sprintf("%x", sha512.Sum512(newer))}})
		if (err == nil) != match {
			t.Fatalf("pin match=%v: %v", match, err)
		}
		data, readErr := os.ReadFile(target)
		if readErr != nil || string(data) != string(want) {
			t.Fatalf("pin match=%v: jar=%q err=%v", match, data, readErr)
		}
		if _, err := os.Stat(target + ".tidy-tmp"); !os.IsNotExist(err) {
			t.Fatal("temporary download remains")
		}
	}
}
