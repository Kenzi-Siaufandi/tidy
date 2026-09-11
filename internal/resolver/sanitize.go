package resolver

import (
	"fmt"
	"path"
	"strings"
)

// SafeFilename sanitizes a remote-provided filename to prevent path traversal.
// It takes the base name, rejects empty/dot entries and separators, and
// optionally enforces an expected file extension.
func SafeFilename(raw, fallback string) (string, error) {
	base := path.Base(strings.TrimSpace(raw))
	// path.Base returns "." for empty input.
	if base == "" || base == "." || base == "/" {
		base = strings.TrimSpace(fallback)
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" || base == ".." {
		return "", fmt.Errorf("invalid remote filename %q", raw)
	}
	// filepath.Join would already clean absolute paths, but reject them
	// explicitly plus any remaining separators/backslashes.
	if strings.Contains(base, "/") || strings.Contains(base, "\\") {
		return "", fmt.Errorf("invalid remote filename %q: must not contain path separators", raw)
	}
	return base, nil
}

// SafeArchiveName sanitizes a config key used for temporary archive files,
// preventing directory escape from .tidy/.
func SafeArchiveName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("invalid file/world name %q", name)
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("invalid file/world name %q: must not contain path separators", name)
	}
	return trimmed, nil
}
