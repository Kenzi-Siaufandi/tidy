package resolver

import (
	"fmt"
	"path"
	"strings"
)

// SafeFilename sanitizes a remote-provided filename to prevent path traversal
// and terminal escape injection. It takes the base name, rejects empty/dot
// entries, separators, and control characters (including ANSI escapes and
// newlines that could inject into panel logs).
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
	for _, r := range base {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid remote filename %q: must not contain control characters", raw)
		}
	}
	if strings.Contains(base, "\x1b") {
		return "", fmt.Errorf("invalid remote filename %q: must not contain escape sequences", raw)
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
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid file/world name %q: must not contain control characters", name)
		}
	}
	return trimmed, nil
}
