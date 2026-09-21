package resolver

import (
	"context"
	"crypto/sha512"
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

func TestModrinthClient_ResolveAndDownload(t *testing.T) {
	jarContent := []byte("mock-fawe-plugin-content")
	h512 := sha512.Sum512(jarContent)
	expectedSHA512 := hex.EncodeToString(h512[:])

	var receivedUserAgent string

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUserAgent = r.Header.Get("User-Agent")

		switch r.URL.Path {
		case "/v2/project/fastasyncworldedit/version":
			versions := []ModrinthVersion{
				{
					ID:            "ver-2.11.2",
					ProjectID:     "fastasyncworldedit",
					Name:          "FastAsyncWorldEdit 2.11.2",
					VersionNumber: "2.11.2",
					Files: []ModrinthFile{
						{
							Filename: "FastAsyncWorldEdit-Bukkit-2.11.2.jar",
							Primary:  true,
							Size:     int64(len(jarContent)),
							URL:      server.URL + "/download/fawe.jar",
							Hashes: map[string]string{
								"sha512": expectedSHA512,
							},
						},
					},
				},
				{
					ID:            "ver-2.11.1",
					ProjectID:     "fastasyncworldedit",
					Name:          "FastAsyncWorldEdit 2.11.1",
					VersionNumber: "2.11.1",
					Files: []ModrinthFile{
						{
							Filename: "FastAsyncWorldEdit-Bukkit-2.11.1.jar",
							Primary:  true,
							Size:     10,
							URL:      server.URL + "/download/fawe-old.jar",
							Hashes: map[string]string{
								"sha512": "dummy",
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(versions)
		case "/download/fawe.jar":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	customUA := "Kenzi-Siaufandi/tidy/0.1.0 (https://github.com/Kenzi-Siaufandi)"
	client := NewModrinthClient(server.URL, customUA, server.Client())
	tmpDir := t.TempDir()

	pCfg := config.PluginConfig{
		Source:    "modrinth",
		ProjectID: "fastasyncworldedit",
		Version:   "2.11.2",
	}

	result, err := client.ResolveAndDownload(context.Background(), "FastAsyncWorldEdit", pCfg, tmpDir)
	if err != nil {
		t.Fatalf("modrinth resolve and download failed: %v", err)
	}

	if !strings.Contains(receivedUserAgent, "Kenzi-Siaufandi") {
		t.Errorf("expected User-Agent with 'Kenzi-Siaufandi', got %q", receivedUserAgent)
	}
	if result.Filename != "FastAsyncWorldEdit-Bukkit-2.11.2.jar" {
		t.Errorf("expected filename FastAsyncWorldEdit-Bukkit-2.11.2.jar, got %s", result.Filename)
	}
	if result.HashAlgo != "sha512" {
		t.Errorf("expected hash algo sha512, got %s", result.HashAlgo)
	}
	if result.Hash != expectedSHA512 {
		t.Errorf("expected hash %s, got %s", expectedSHA512, result.Hash)
	}

	// Verify plugin jar inside plugins directory
	expectedFile := filepath.Join(tmpDir, "plugins", "FastAsyncWorldEdit-Bukkit-2.11.2.jar")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("failed to read downloaded plugin: %v", err)
	}
	if string(data) != string(jarContent) {
		t.Errorf("content does not match")
	}
}

// latestTestServer serves a version list (newest first: beta then release)
// plus a direct version endpoint and jar downloads.
func latestTestServer(t *testing.T, jarContent []byte, sha512Hex string) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotGameVersions, gotLoaders string
	var server *httptest.Server
	newFile := func(name, url string) ModrinthFile {
		return ModrinthFile{
			Filename: name,
			Primary:  true,
			Size:     int64(len(jarContent)),
			URL:      url,
			Hashes:   map[string]string{"sha512": sha512Hex},
		}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/project/test/version":
			gotGameVersions = r.URL.Query().Get("game_versions")
			gotLoaders = r.URL.Query().Get("loaders")
			versions := []ModrinthVersion{
				{
					ID: "ver-beta", ProjectID: "test", Name: "Test 2.0",
					VersionNumber: "2.0", VersionType: "beta", Status: "listed",
					GameVersions: []string{"1.21.1"}, Loaders: []string{"paper"},
					Files: []ModrinthFile{newFile("Test-2.0.jar", server.URL+"/download/test-2.0.jar")},
				},
				{
					ID: "ver-release", ProjectID: "test", Name: "Test 1.0",
					VersionNumber: "1.0", VersionType: "release", Status: "listed",
					GameVersions: []string{"1.21.1", "1.20.1"}, Loaders: []string{"paper"},
					Files: []ModrinthFile{newFile("Test-1.0.jar", server.URL+"/download/test-1.0.jar")},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(versions)
		case r.URL.Path == "/v2/project/test/version/1.0":
			ver := ModrinthVersion{
				ID: "ver-release", ProjectID: "test", Name: "Test 1.0",
				VersionNumber: "1.0", VersionType: "release", Status: "listed",
				GameVersions: []string{"1.21.1", "1.20.1"}, Loaders: []string{"paper"},
				Files: []ModrinthFile{newFile("Test-1.0.jar", server.URL+"/download/test-1.0.jar")},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ver)
		case strings.HasPrefix(r.URL.Path, "/download/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(jarContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &gotGameVersions, &gotLoaders
}

func TestModrinthClient_ResolveLatest_SkipsBetaByDefault(t *testing.T) {
	jarContent := []byte("mock-test-plugin")
	h := sha512.Sum512(jarContent)
	server, gotGV, gotLoaders := latestTestServer(t, jarContent, hex.EncodeToString(h[:]))

	client := NewModrinthClient(server.URL, "test-agent", server.Client())
	ver, _, err := client.Resolve(context.Background(), config.PluginConfig{
		Source: "modrinth", ProjectID: "test", Version: "latest",
		GameVersion: "1.21.1", Loader: "paper",
	})
	if err != nil {
		t.Fatalf("resolve latest failed: %v", err)
	}
	if ver.VersionNumber != "1.0" {
		t.Errorf("expected newest release 1.0 (beta skipped), got %s", ver.VersionNumber)
	}
	if *gotGV != `["1.21.1"]` {
		t.Errorf("expected game_versions filter %q, got %q", `["1.21.1"]`, *gotGV)
	}
	if *gotLoaders != `["paper"]` {
		t.Errorf("expected loaders filter %q, got %q", `["paper"]`, *gotLoaders)
	}
}

func TestModrinthClient_ResolveLatest_BetaChannel(t *testing.T) {
	jarContent := []byte("mock-test-plugin")
	h := sha512.Sum512(jarContent)
	server, _, _ := latestTestServer(t, jarContent, hex.EncodeToString(h[:]))

	client := NewModrinthClient(server.URL, "test-agent", server.Client())
	ver, _, err := client.Resolve(context.Background(), config.PluginConfig{
		Source: "modrinth", ProjectID: "test", Version: "latest",
		GameVersion: "1.21.1", Loader: "paper", Channel: "beta",
	})
	if err != nil {
		t.Fatalf("resolve latest failed: %v", err)
	}
	if ver.VersionNumber != "2.0" {
		t.Errorf("expected newest beta 2.0 with channel=beta, got %s", ver.VersionNumber)
	}
}

func TestModrinthClient_ResolveLatest_NoCompatibleVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := NewModrinthClient(server.URL, "test-agent", server.Client())
	_, _, err := client.Resolve(context.Background(), config.PluginConfig{
		Source: "modrinth", ProjectID: "test", Version: "latest",
		GameVersion: "1.19", Loader: "paper",
	})
	if err == nil {
		t.Fatal("expected error for unsupported game version, got nil")
	}
	if !strings.Contains(err.Error(), "1.19") {
		t.Errorf("expected error to mention game version, got: %v", err)
	}
}

func TestModrinthClient_ResolvePinned_GameVersionMismatch(t *testing.T) {
	jarContent := []byte("mock-test-plugin")
	h := sha512.Sum512(jarContent)
	server, _, _ := latestTestServer(t, jarContent, hex.EncodeToString(h[:]))

	client := NewModrinthClient(server.URL, "test-agent", server.Client())
	_, _, err := client.Resolve(context.Background(), config.PluginConfig{
		Source: "modrinth", ProjectID: "test", Version: "1.0",
		GameVersion: "1.19.4",
	})
	if err == nil {
		t.Fatal("expected game_version mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not support game version") {
		t.Errorf("expected incompatibility error, got: %v", err)
	}
}

func TestModrinthClient_ResolveAndDownload_Latest(t *testing.T) {
	jarContent := []byte("mock-test-plugin-latest")
	h := sha512.Sum512(jarContent)
	server, _, _ := latestTestServer(t, jarContent, hex.EncodeToString(h[:]))

	client := NewModrinthClient(server.URL, "test-agent", server.Client())
	tmpDir := t.TempDir()
	res, err := client.ResolveAndDownload(context.Background(), "Test", config.PluginConfig{
		Source: "modrinth", ProjectID: "test", Version: "latest",
		GameVersion: "1.21.1", Loader: "paper",
	}, tmpDir)
	if err != nil {
		t.Fatalf("resolve and download latest failed: %v", err)
	}
	if res.VersionID != "ver-release" || res.VersionNumber != "1.0" {
		t.Errorf("expected resolved ver-release/1.0, got %s/%s", res.VersionID, res.VersionNumber)
	}
	if res.GameVersion != "1.21.1" || res.Loader != "paper" {
		t.Errorf("expected game_version/loader metadata, got %s/%s", res.GameVersion, res.Loader)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "plugins", "Test-1.0.jar")); err != nil {
		t.Errorf("expected downloaded jar: %v", err)
	}
}
