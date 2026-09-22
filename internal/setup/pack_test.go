package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kenzi-Siaufandi/tidy/internal/storage"
)

func fixtureWorld(t *testing.T, root, name string) string {
	t.Helper()
	world := filepath.Join(root, name)
	writeFile(t, filepath.Join(world, "level.dat"), "level-data")
	writeFile(t, filepath.Join(world, "region", "r.0.0.mca"), "chunk-data")
	writeFile(t, filepath.Join(world, "entities", "r.0.0.mca"), "entity-data")
	writeFile(t, filepath.Join(world, "playerdata", "uuid-1.dat"), "player-inventory")
	writeFile(t, filepath.Join(world, "stats", "uuid-1.json"), "{}")
	writeFile(t, filepath.Join(world, "advancements", "uuid-1.json"), "{}")
	writeFile(t, filepath.Join(world, "session.lock"), "lock")
	writeFile(t, filepath.Join(world, "uid.dat"), "uid")
	return world
}

func TestPackWorldFormats(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip", "tar"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			fixtureWorld(t, root, "world")
			out := filepath.Join(t.TempDir(), "hub."+format)

			res, err := PackWorld(PackOptions{Root: root, World: "world", Output: out, Format: format})
			if err != nil {
				t.Fatal(err)
			}
			if res.Format != format {
				t.Errorf("Format = %q, want %q", res.Format, format)
			}
			if res.StrippedFiles == 0 {
				t.Error("StrippedFiles = 0, want > 0")
			}

			// Archive hash must match the reported SHA-256.
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != res.SHA256 {
				t.Error("reported SHA256 does not match archive content")
			}

			// Live world must be untouched.
			for _, p := range []string{"playerdata/uuid-1.dat", "stats/uuid-1.json", "session.lock", "uid.dat"} {
				if _, err := os.Stat(filepath.Join(root, "world", filepath.FromSlash(p))); err != nil {
					t.Errorf("live world file %s was modified/removed", p)
				}
			}

			// Roundtrip through tidy's own extractor.
			dest := filepath.Join(t.TempDir(), "extracted")
			n, err := storage.ExtractArchive(out, dest)
			if err != nil {
				t.Fatalf("tidy extractor rejected packed archive: %v", err)
			}
			if n != res.Files {
				t.Errorf("extracted %d files, packed %d", n, res.Files)
			}
			for _, want := range []string{"level.dat", "region/r.0.0.mca", "entities/r.0.0.mca"} {
				if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
					t.Errorf("extracted world missing %s", want)
				}
			}
			for _, gone := range []string{"playerdata", "stats", "advancements", "session.lock", "uid.dat"} {
				if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(gone))); err == nil {
					t.Errorf("extracted world still contains %s", gone)
				}
			}
		})
	}
}

func TestPackWorldDefaults(t *testing.T) {
	root := t.TempDir()
	fixtureWorld(t, root, "world_nether")
	cwd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	res, err := PackWorld(PackOptions{Root: root, World: "world_nether"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "tar.gz" {
		t.Errorf("default Format = %q, want tar.gz", res.Format)
	}
	if want := filepath.Join(tmp, "world_nether-template.tar.gz"); res.ArchivePath != want {
		t.Errorf("ArchivePath = %q, want %q", res.ArchivePath, want)
	}
}

func TestPackWorldMissing(t *testing.T) {
	if _, err := PackWorld(PackOptions{Root: t.TempDir(), World: "nope"}); err == nil {
		t.Error("expected error for missing world dir")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}
