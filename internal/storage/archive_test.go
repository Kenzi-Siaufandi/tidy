package storage

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	destDir := filepath.Join(tmpDir, "extracted")

	// Create test zip
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f1, err := zw.Create("level.dat")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f1.Write([]byte("mock level.dat nbt data"))

	f2, err := zw.Create("region/r.0.0.mca")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f2.Write([]byte("mock region chunk data"))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	count, err := ExtractArchive(zipPath, destDir)
	if err != nil {
		t.Fatalf("ExtractArchive failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 extracted files, got %d", count)
	}

	datContent, err := os.ReadFile(filepath.Join(destDir, "level.dat"))
	if err != nil || string(datContent) != "mock level.dat nbt data" {
		t.Errorf("level.dat not extracted properly: %v", err)
	}

	mcaContent, err := os.ReadFile(filepath.Join(destDir, "region", "r.0.0.mca"))
	if err != nil || string(mcaContent) != "mock region chunk data" {
		t.Errorf("r.0.0.mca not extracted properly: %v", err)
	}
}

func TestExtractTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	tarGzPath := filepath.Join(tmpDir, "world.tar.gz")
	destDir := filepath.Join(tmpDir, "world_dest")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	data := []byte("world data")
	hdr := &tar.Header{
		Name: "world/level.dat",
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(data)

	_ = tw.Close()
	_ = gw.Close()

	if err := os.WriteFile(tarGzPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	count, err := ExtractArchive(tarGzPath, destDir)
	if err != nil {
		t.Fatalf("tar.gz extraction failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 file, got %d", count)
	}

	extracted, err := os.ReadFile(filepath.Join(destDir, "world", "level.dat"))
	if err != nil || string(extracted) != "world data" {
		t.Errorf("tar.gz content mismatch: %v", err)
	}
}

func TestExtractZip_ZipSlipProtection(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "malicious.zip")
	destDir := filepath.Join(tmpDir, "safe_dir")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("malicious content"))
	_ = zw.Close()

	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	_, err = ExtractArchive(zipPath, destDir)
	if err == nil {
		t.Fatal("expected error on zip traversal (ZipSlip), got nil")
	}
}

func TestExtractZip_DirsCountTowardLimit(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "dirs.zip")
	destDir := filepath.Join(tmpDir, "out")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Explicit directory entries plus one file.
	for _, d := range []string{"a/", "a/b/", "c/"} {
		hdr := &zip.FileHeader{Name: d}
		hdr.SetMode(0755 | 0040000)
		if _, err := zw.CreateHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := zw.Create("a/b/file.txt")
	_, _ = f.Write([]byte("hi"))
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	count, err := ExtractArchive(zipPath, destDir)
	if err != nil {
		t.Fatalf("ExtractArchive failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file (dirs excluded from return), got %d", count)
	}
}

func TestExtractTar_DirsCountTowardLimit(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "dirs.tar")
	destDir := filepath.Join(tmpDir, "out")

	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, d := range []string{"x/", "x/y/"} {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("hi")
	if err := tw.WriteHeader(&tar.Header{Name: "x/y/file.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = f.Close()
	count, err := ExtractArchive(tarPath, destDir)
	if err != nil {
		t.Fatalf("ExtractArchive failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file (dirs excluded from return), got %d", count)
	}
}

func TestCleanStaleSessionLock(t *testing.T) {
	tmpDir := t.TempDir()
	worldDir := filepath.Join(tmpDir, "world")
	if err := os.MkdirAll(worldDir, 0755); err != nil {
		t.Fatal(err)
	}

	lockFile := filepath.Join(worldDir, "session.lock")
	if err := os.WriteFile(lockFile, []byte("lock"), 0644); err != nil {
		t.Fatal(err)
	}

	removed, err := CleanStaleSessionLock(worldDir)
	if err != nil {
		t.Fatalf("CleanStaleSessionLock failed: %v", err)
	}
	if !removed {
		t.Errorf("expected removed to be true")
	}

	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Errorf("session.lock should be deleted")
	}
}

func TestExtractZip_DirBombHitsLimit(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "bomb.zip")
	destDir := filepath.Join(tmpDir, "out")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < MaxArchiveFiles+1; i++ {
		hdr := &zip.FileHeader{Name: "d" + itoa(i) + "/"}
		hdr.SetMode(0755 | 0040000)
		if _, err := zw.CreateHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractArchive(zipPath, destDir); err == nil {
		t.Fatal("expected dir bomb to hit file limit, got nil")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [32]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
