package storage

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractArchive unpacks a .zip, .tar.gz, .tgz, or .tar archive into destDir.
func ExtractArchive(archivePath, destDir string) (int, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create destination directory %q: %w", destDir, err)
	}

	// Read first 512 bytes to inspect magic header
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open archive %q: %w", archivePath, err)
	}
	header := make([]byte, 512)
	n, _ := f.Read(header)
	_ = f.Close()

	if n >= 4 && header[0] == 0x50 && header[1] == 0x4B && (header[2] == 0x03 || header[2] == 0x05 || header[2] == 0x07) {
		return extractZip(archivePath, destDir)
	}
	if n >= 2 && header[0] == 0x1F && header[1] == 0x8B {
		return extractTarGz(archivePath, destDir)
	}

	lower := strings.ToLower(archivePath)
	if strings.HasSuffix(lower, ".zip") {
		return extractZip(archivePath, destDir)
	} else if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return extractTarGz(archivePath, destDir)
	} else if strings.HasSuffix(lower, ".tar") || (n >= 262 && string(header[257:262]) == "ustar") {
		return extractTar(archivePath, destDir)
	}

	return 0, fmt.Errorf("unsupported archive format for %q (supported: .zip, .tar.gz, .tgz, .tar)", archivePath)
}

func extractZip(zipPath, destDir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open zip file %q: %w", zipPath, err)
	}
	defer r.Close()

	cleanDest := filepath.Clean(destDir)
	count := 0

	for _, f := range r.File {
		targetPath := filepath.Join(cleanDest, f.Name)
		// ZipSlip protection
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDest+string(os.PathSeparator)) && filepath.Clean(targetPath) != cleanDest {
			return count, fmt.Errorf("illegal file path in zip archive: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return count, err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return count, err
		}

		rc, err := f.Open()
		if err != nil {
			return count, fmt.Errorf("failed to open file %q in zip: %w", f.Name, err)
		}

		mode := sanitizeFileMode(f.Mode())
		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return count, fmt.Errorf("failed to create output file %q: %w", targetPath, err)
		}

		_, copyErr := io.Copy(outFile, rc)
		closeErr := outFile.Close()
		rc.Close()

		if copyErr != nil {
			return count, fmt.Errorf("failed to write file %q: %w", targetPath, copyErr)
		}
		if closeErr != nil {
			return count, fmt.Errorf("failed to close file %q: %w", targetPath, closeErr)
		}

		count++
	}

	return count, nil
}

func extractTarGz(tarGzPath, destDir string) (int, error) {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open tar.gz file: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("failed to initialize gzip reader: %w", err)
	}
	defer gzr.Close()

	return extractTarReader(tar.NewReader(gzr), destDir)
}

func extractTar(tarPath, destDir string) (int, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open tar file: %w", err)
	}
	defer f.Close()

	return extractTarReader(tar.NewReader(f), destDir)
}

func extractTarReader(tr *tar.Reader, destDir string) (int, error) {
	cleanDest := filepath.Clean(destDir)
	count := 0

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, fmt.Errorf("failed reading tar stream: %w", err)
		}

		targetPath := filepath.Join(cleanDest, header.Name)
		// ZipSlip / TarSlip protection
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDest+string(os.PathSeparator)) && filepath.Clean(targetPath) != cleanDest {
			return count, fmt.Errorf("illegal file path in tar archive: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return count, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return count, err
			}

			mode := sanitizeFileMode(os.FileMode(header.Mode))
			if mode == 0 {
				mode = 0644
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return count, fmt.Errorf("failed to create output file %q: %w", targetPath, err)
			}

			_, copyErr := io.Copy(outFile, tr)
			closeErr := outFile.Close()
			if copyErr != nil {
				return count, fmt.Errorf("failed to write tar entry %q: %w", targetPath, copyErr)
			}
			if closeErr != nil {
				return count, fmt.Errorf("failed to close output file %q: %w", targetPath, closeErr)
			}
			count++
		}
	}

	return count, nil
}

// sanitizeFileMode strips setuid/setgid/sticky bits and limits to owner/group/other
// permission bits, preventing privilege escalation via crafted archives.
func sanitizeFileMode(m os.FileMode) os.FileMode {
	perm := m.Perm() & 0777
	if perm == 0 {
		return 0644
	}
	return os.FileMode(perm)
}

// CleanStaleSessionLock looks for session.lock in world directories and removes them
// to prevent "World is locked by another server" startup crashes.
func CleanStaleSessionLock(worldDir string) (bool, error) {
	removed := false

	// Check direct worldDir/session.lock
	directLock := filepath.Join(worldDir, "session.lock")
	if _, err := os.Stat(directLock); err == nil {
		if err := os.Remove(directLock); err != nil {
			return false, fmt.Errorf("failed to remove stale session.lock at %q: %w", directLock, err)
		}
		removed = true
	}

	// Also check child directories (e.g. if worldDir contains sub-dimensions like DIM-1 or world_nether)
	entries, err := os.ReadDir(worldDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				childLock := filepath.Join(worldDir, e.Name(), "session.lock")
				if _, err := os.Stat(childLock); err == nil {
					if err := os.Remove(childLock); err == nil {
						removed = true
					}
				}
			}
		}
	}

	return removed, nil
}
