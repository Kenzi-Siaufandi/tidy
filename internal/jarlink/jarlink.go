package jarlink

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
)

// AliasEnv is the Pterodactyl panel variable naming the jar used in
// `java -jar {{SERVER_JARFILE}}`. Default matches egg-tidy-paper.json.
const AliasEnv = "SERVER_JARFILE"

// DefaultAlias is used when SERVER_JARFILE is unset (local runs).
const DefaultAlias = "server.jar"

// AliasFromEnv resolves the stable jar name, defaulting to server.jar.
func AliasFromEnv(getenv func(string) string) string {
	alias := strings.TrimSpace(getenv(AliasEnv))
	if alias == "" {
		return DefaultAlias
	}
	return alias
}

// Sync ensures workDir/alias points at target (the Fill-resolved jar).
// It uses a symlink with a file-copy fallback, atomically replacing any
// stale alias. Returns the alias filename, or "" when no sync was needed.
func Sync(workDir, target, alias string) (string, error) {
	target = strings.TrimSpace(target)
	alias = strings.TrimSpace(alias)
	if target == "" || alias == "" || alias == target {
		return "", nil
	}

	safeAlias, err := resolver.SafeArchiveName(alias)
	if err != nil {
		return "", fmt.Errorf("invalid %s %q: %w", AliasEnv, alias, err)
	}
	aliasPath := filepath.Join(workDir, safeAlias)
	targetPath := filepath.Join(workDir, strings.TrimSpace(target))

	if st, err := os.Lstat(aliasPath); err == nil {
		if st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("%s %q is a directory, leaving it alone", AliasEnv, safeAlias)
		}
		// Remove stale symlink or regular file before relinking.
		_ = os.Remove(aliasPath)
	}

	// Atomic symlink swap: link into temp name then rename.
	tmp, err := os.CreateTemp(workDir, ".tidy-jarlink-*")
	if err != nil {
		return "", fmt.Errorf("failed to stage jar link: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpName)

	if err := os.Symlink(strings.TrimSpace(target), tmpName); err != nil {
		// Privilege-restricted filesystems (e.g. Windows): fall back to copy.
		if copyErr := copyFile(targetPath, tmpName); copyErr != nil {
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("failed to link %s -> %s: %w", safeAlias, target, err)
		}
	}
	if err := os.Rename(tmpName, aliasPath); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("failed to activate jar link %s: %w", safeAlias, err)
	}
	return safeAlias, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
