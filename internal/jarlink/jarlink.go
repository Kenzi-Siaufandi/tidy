package jarlink

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
)

// AliasEnv is the legacy Pterodactyl panel variable naming the jar used in
// `java -jar {{SERVER_JARFILE}}` (egg-tidy-paper.json).
const AliasEnv = "SERVER_JARFILE"

// AliasEnvCurrent is the panel variable in the active egg (egg-tidy.json),
// whose startup is `java ... -jar {{SERVER_JAR}}`. It takes precedence.
const AliasEnvCurrent = "SERVER_JAR"

// DefaultAlias is used when neither variable is set (local runs).
const DefaultAlias = "server.jar"

// AliasFromEnv resolves the stable jar name, defaulting to server.jar.
func AliasFromEnv(getenv func(string) string) string {
	if alias := strings.TrimSpace(getenv(AliasEnvCurrent)); alias != "" {
		return alias
	}
	if alias := strings.TrimSpace(getenv(AliasEnv)); alias != "" {
		return alias
	}
	return DefaultAlias
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
		return "", fmt.Errorf("invalid jar alias %q: %w", alias, err)
	}
	aliasPath := filepath.Join(workDir, safeAlias)
	targetPath := filepath.Join(workDir, strings.TrimSpace(target))

	if st, err := os.Lstat(aliasPath); err == nil {
		if st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("jar alias %q is a directory, leaving it alone", safeAlias)
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
