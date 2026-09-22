package resolver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resumeFixture(content []byte) (*httptest.Server, *string) {
	var sawRange string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		http.ServeContent(w, r, "file.jar", time.Now(), bytes.NewReader(content))
	}))
	return server, &sawRange
}

func downloadOpts(server *httptest.Server, dir string, content []byte) DownloadOptions {
	h := sha256.Sum256(content)
	return DownloadOptions{
		URL:           server.URL + "/file.jar",
		TargetPath:    filepath.Join(dir, "file.jar"),
		ExpectedHash:  hex.EncodeToString(h[:]),
		HashAlgorithm: "sha256",
		Client:        server.Client(),
	}
}

func TestDownloadAndVerify_ResumesPartialFile(t *testing.T) {
	content := bytes.Repeat([]byte("purpur-jar-payload-"), 16*1024) // ~288 KB
	server, sawRange := resumeFixture(content)
	defer server.Close()

	tmpDir := t.TempDir()
	opts := downloadOpts(server, tmpDir, content)

	// Simulate a killed run: half the bytes already in the tmp file.
	if err := os.WriteFile(opts.TargetPath+".tidy-tmp", content[:len(content)/2], 0644); err != nil {
		t.Fatal(err)
	}

	res, err := DownloadAndVerify(context.Background(), opts)
	if err != nil {
		t.Fatalf("resumed download failed: %v", err)
	}
	if *sawRange == "" {
		t.Error("expected a Range request for the partial file, got none")
	}
	if res.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), res.Size)
	}
	data, err := os.ReadFile(opts.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Error("resumed file content does not match")
	}
	if _, err := os.Stat(opts.TargetPath + ".tidy-tmp"); !os.IsNotExist(err) {
		t.Error("temporary file was not cleaned up after success")
	}
}

func TestDownloadAndVerify_RestartsWhenServerIgnoresRange(t *testing.T) {
	content := []byte("full-fresh-content-from-server")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately ignore Range: always 200 with the full body.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	opts := downloadOpts(server, tmpDir, content)

	// Stale partial file from an unrelated download.
	if err := os.WriteFile(opts.TargetPath+".tidy-tmp", []byte("stale-garbage-prefix"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := DownloadAndVerify(context.Background(), opts); err != nil {
		t.Fatalf("download with ignored Range failed: %v", err)
	}
	data, err := os.ReadFile(opts.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("expected server content, got %q", data)
	}
}

func TestDownloadAndVerify_InterruptedStreamResumesNextRun(t *testing.T) {
	content := bytes.Repeat([]byte("slow-link-payload-"), 8*1024)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// First attempt dies mid-stream: declare the full length but
			// close after half, simulating a killed connection.
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			_, _ = w.Write(content[:len(content)/2])
			return
		}
		http.ServeContent(w, r, "file.jar", time.Now(), bytes.NewReader(content))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	opts := downloadOpts(server, tmpDir, content)

	_, err := DownloadAndVerify(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "download stream error") {
		t.Fatalf("expected stream error on interrupted download, got %v", err)
	}
	// The partial file must survive for the next run to resume.
	info, err := os.Stat(opts.TargetPath + ".tidy-tmp")
	if err != nil {
		t.Fatalf("partial file should be kept for resume: %v", err)
	}
	if info.Size() != int64(len(content)/2) {
		t.Errorf("expected %d partial bytes, got %d", len(content)/2, info.Size())
	}

	res, err := DownloadAndVerify(context.Background(), opts)
	if err != nil {
		t.Fatalf("resume after interruption failed: %v", err)
	}
	if res.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), res.Size)
	}
	data, err := os.ReadFile(opts.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Error("resumed file content does not match")
	}
}

func TestDownloadAndVerify_OversizedStaleTmpRestartsClean(t *testing.T) {
	content := []byte("small-fresh-file")
	server, _ := resumeFixture(content)
	defer server.Close()

	tmpDir := t.TempDir()
	opts := downloadOpts(server, tmpDir, content)

	// Partial file larger than the remote: Range is unsatisfiable (416).
	if err := os.WriteFile(opts.TargetPath+".tidy-tmp", bytes.Repeat([]byte("x"), 1024), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := DownloadAndVerify(context.Background(), opts); err != nil {
		t.Fatalf("download with oversized stale tmp failed: %v", err)
	}
	data, err := os.ReadFile(opts.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("expected server content, got %q", data)
	}
}

func TestParseRangeStart(t *testing.T) {
	cases := []struct {
		header string
		start  int64
		ok     bool
	}{
		{"bytes 100-199/1000", 100, true},
		{"bytes 0-0/1", 0, true},
		{"bytes 67413040-", 67413040, true},
		{"", 0, false},
		{"none", 0, false},
		{"bytes -199/1000", 0, false},
		{"bytes abc-def/1000", 0, false},
	}
	for _, tc := range cases {
		start, ok := parseRangeStart(tc.header)
		if ok != tc.ok || start != tc.start {
			t.Errorf("parseRangeStart(%q) = (%d, %v), want (%d, %v)", tc.header, start, ok, tc.start, tc.ok)
		}
	}
}
