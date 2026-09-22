package resolver

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
)

func purpurMock(t *testing.T, jarContent []byte, mutate func(build map[string]any)) *httptest.Server {
	t.Helper()
	md5sum := md5.Sum(jarContent)
	md5hex := hex.EncodeToString(md5sum[:])

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/purpur/1.21.8":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"project": "purpur",
				"version": "1.21.8",
				"builds":  map[string]any{"latest": "2497", "all": []string{"2496", "2497"}},
			})
		case "/v2/purpur/1.21.8/2497":
			build := map[string]any{
				"project": "purpur",
				"version": "1.21.8",
				"build":   "2497",
				"result":  "success",
				"md5":     md5hex,
			}
			if mutate != nil {
				mutate(build)
			}
			_ = json.NewEncoder(w).Encode(build)
		case "/v2/purpur/1.21.8/2497/download":
			w.Header().Set("Content-Type", "application/java-archive")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server
}

func TestPurpurClient_ResolveLatest(t *testing.T) {
	jarContent := []byte("mock-purpur-1.21.8-2497-jar-content")
	sha := sha256.Sum256(jarContent)
	wantSHA := hex.EncodeToString(sha[:])

	server := purpurMock(t, jarContent, nil)
	defer server.Close()

	client := NewPurpurClient(server.URL, server.Client())
	res, err := client.ResolveAndDownload(context.Background(), config.ServerConfig{
		Project: "purpur",
		Version: "1.21.8",
		Build:   "latest",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("resolve and download failed: %v", err)
	}
	if res.Filename != "purpur-1.21.8-2497.jar" {
		t.Errorf("filename = %q, want purpur-1.21.8-2497.jar", res.Filename)
	}
	if res.BuildID != 2497 {
		t.Errorf("build = %d, want 2497", res.BuildID)
	}
	if res.SHA256 != wantSHA {
		t.Errorf("sha256 = %s, want %s", res.SHA256, wantSHA)
	}
	if data, err := os.ReadFile(res.FilePath); err != nil || string(data) != string(jarContent) {
		t.Errorf("downloaded file mismatch: %v", err)
	}

	latest, err := client.FetchLatestBuildID(context.Background(), config.ServerConfig{
		Project: "purpur",
		Version: "1.21.8",
	})
	if err != nil {
		t.Fatalf("FetchLatestBuildID failed: %v", err)
	}
	if latest != 2497 {
		t.Errorf("latest = %d, want 2497", latest)
	}
}

func TestPurpurClient_PinnedBuild(t *testing.T) {
	jarContent := []byte("mock-purpur-pinned")
	server := purpurMock(t, jarContent, nil)
	defer server.Close()

	client := NewPurpurClient(server.URL, server.Client())
	res, err := client.ResolveAndDownload(context.Background(), config.ServerConfig{
		Project: "purpur",
		Version: "1.21.8",
		Build:   "2497",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("pinned resolve failed: %v", err)
	}
	if res.BuildID != 2497 || !strings.HasSuffix(res.Filename, "-2497.jar") {
		t.Errorf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(res.FilePath), res.Filename)); err != nil {
		t.Errorf("jar missing on disk: %v", err)
	}
}

func TestPurpurClient_RejectsFailedBuild(t *testing.T) {
	server := purpurMock(t, []byte("x"), func(build map[string]any) {
		build["result"] = "failure"
	})
	defer server.Close()

	client := NewPurpurClient(server.URL, server.Client())
	_, err := client.ResolveAndDownload(context.Background(), config.ServerConfig{
		Project: "purpur",
		Version: "1.21.8",
		Build:   "2497",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "did not succeed") {
		t.Fatalf("expected broken-build refusal, got: %v", err)
	}
}

func TestPurpurClient_MD5Mismatch(t *testing.T) {
	advertised := []byte("advertised-content")
	served := []byte("different-served-content")
	md5sum := md5.Sum(advertised)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/purpur/1.21.8/2497":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"project": "purpur", "version": "1.21.8",
				"build": "2497", "result": "success",
				"md5": hex.EncodeToString(md5sum[:]),
			})
		case "/v2/purpur/1.21.8/2497/download":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(served)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewPurpurClient(server.URL, server.Client())
	_, err := client.ResolveAndDownload(context.Background(), config.ServerConfig{
		Project: "purpur", Version: "1.21.8", Build: "2497",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "hash verification failed") {
		t.Fatalf("expected md5 verification failure, got: %v", err)
	}
}

func TestPurpurClient_UnknownVersion(t *testing.T) {
	server := purpurMock(t, []byte("x"), nil)
	defer server.Close()

	client := NewPurpurClient(server.URL, server.Client())
	_, err := client.ResolveAndDownload(context.Background(), config.ServerConfig{
		Project: "purpur", Version: "9.9.9", Build: "latest",
	}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
}

func TestNewServerResolver_Routing(t *testing.T) {
	if _, ok := NewServerResolver("purpur").(*PurpurClient); !ok {
		t.Error("purpur should route to PurpurClient")
	}
	if _, ok := NewServerResolver("PURPUR").(*PurpurClient); !ok {
		t.Error("PURPUR should route to PurpurClient")
	}
	if _, ok := NewServerResolver("paper").(*PaperClient); !ok {
		t.Error("paper should route to PaperClient")
	}
	if _, ok := NewServerResolver("folia").(*PaperClient); !ok {
		t.Error("other Fill projects should route to PaperClient")
	}
}
