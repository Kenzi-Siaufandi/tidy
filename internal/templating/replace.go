package templating

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
)

// MustacheVariableRegex matches {{VAR_NAME}} or {{ VAR_NAME }}.
var MustacheVariableRegex = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// ReplaceResult holds statistics of the template processing.
type ReplaceResult struct {
	FilesProcessed int
	Replacements   int
	ModifiedFiles  []string
}

// ReplaceString replaces {{VAR_NAME}} placeholders with environment variables.
// If an environment variable is not present, it leaves the placeholder unchanged and records it once.
func ReplaceString(content string) (string, int, []string) {
	replacements := 0
	seen := make(map[string]struct{})
	var missingVars []string

	result := MustacheVariableRegex.ReplaceAllStringFunc(content, func(match string) string {
		submatches := MustacheVariableRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		varName := submatches[1]
		val, exists := os.LookupEnv(varName)
		if !exists {
			if _, ok := seen[varName]; !ok {
				seen[varName] = struct{}{}
				missingVars = append(missingVars, varName)
			}
			return match
		}
		replacements++
		return val
	})

	return result, replacements, missingVars
}

// IsSupportedConfigFile checks if a file extension is typically a text configuration file.
func IsSupportedConfigFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yml", ".yaml", ".properties", ".json", ".conf", ".toml", ".txt", ".cfg", ".env":
		return true
	default:
		return false
	}
}

// ProcessFile applies template replacements to a single file.
func ProcessFile(path string) (int, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to stat file %q: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	newContent, count, missing := ReplaceString(string(data))
	if count > 0 {
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".tidy-tpl-*")
		if err != nil {
			return 0, nil, fmt.Errorf("failed to create temp file for %q: %w", path, err)
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write([]byte(newContent)); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return 0, nil, fmt.Errorf("failed to write templated file %q: %w", path, err)
		}
		if err := tmp.Chmod(mode); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			return 0, nil, fmt.Errorf("failed to chmod templated file %q: %w", path, err)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			return 0, nil, fmt.Errorf("failed to close templated file %q: %w", path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			_ = os.Remove(tmpName)
			return 0, nil, fmt.Errorf("failed to replace templated file %q: %w", path, err)
		}
	}

	return count, missing, nil
}

// ProcessDirectory scans the directory and processes config files.
// If customPaths is non-empty, only files matching those glob patterns
// (relative to rootDir, supporting ** for recursive match) are processed.
// If empty, it falls back to walking rootDir for all supported config files.
func ProcessDirectory(rootDir string, customPaths []string) (*ReplaceResult, error) {
	result := &ReplaceResult{}

	patterns := make([]string, 0, len(customPaths))
	for _, p := range customPaths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}

	if len(patterns) == 0 {
		return result, walkAll(rootDir, result)
	}

	seen := make(map[string]struct{})
	for _, pat := range patterns {
		files, err := expandPattern(rootDir, pat)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			clean := filepath.Clean(f)
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			if err := processOneFile(rootDir, clean, result); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// walkAll preserves the legacy full-tree behavior when no custom paths are set.
func walkAll(rootDir string, result *ReplaceResult) error {
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".tidy" || name == "cache" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip jar files and other non-configs
		if !IsSupportedConfigFile(path) {
			return nil
		}

		return processOneFileByAbs(rootDir, path, result)
	})

	if err != nil {
		return fmt.Errorf("error processing templates in %q: %w", rootDir, err)
	}
	return nil
}

// processOneFile validates skips and applies templating to an absolute path
// that may have come from glob expansion.
func processOneFile(rootDir, absPath string, result *ReplaceResult) error {
	// Resolve relative for skip checks.
	rel, err := filepath.Rel(rootDir, absPath)
	if err != nil {
		return nil
	}
	// Never touch .git/.tidy internals even if a custom glob matches them.
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".git" || part == ".tidy" {
			return nil
		}
	}
	info, err := os.Stat(absPath)
	if err != nil {
		// Dangling glob match: ignore.
		return nil
	}
	if info.IsDir() {
		// If a pattern matched a directory, walk it for supported files.
		return filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".tidy" || d.Name() == "cache" {
					return filepath.SkipDir
				}
				return nil
			}
			if !IsSupportedConfigFile(p) {
				return nil
			}
			return processOneFileByAbs(rootDir, p, result)
		})
	}
	if !IsSupportedConfigFile(absPath) {
		return nil
	}
	return processOneFileByAbs(rootDir, absPath, result)
}

func processOneFileByAbs(rootDir, absPath string, result *ReplaceResult) error {
	relPath, _ := filepath.Rel(rootDir, absPath)
	count, missing, procErr := ProcessFile(absPath)
	if procErr != nil {
		return procErr
	}

	result.FilesProcessed++
	if count > 0 {
		result.Replacements += count
		result.ModifiedFiles = append(result.ModifiedFiles, relPath)
	}

	for _, mv := range missing {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf(" [!] Config warning: environment variable {{%s}} in %s is not set", mv, relPath)))
	}
	return nil
}

// expandPattern resolves a single glob (absolute or relative to rootDir)
// with ** support into absolute file paths.
func expandPattern(rootDir, pattern string) ([]string, error) {
	// Absolute pattern: use directly.
	if filepath.IsAbs(pattern) {
		if strings.Contains(pattern, "**") {
			return matchDoublestarAbs(pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid template pattern %q: %w", pattern, err)
		}
		return matches, nil
	}
	// Relative pattern: try Glob against joined path first (covers *, ?).
	joined := filepath.Join(rootDir, pattern)
	if !strings.Contains(pattern, "**") {
		matches, err := filepath.Glob(joined)
		if err != nil {
			return nil, fmt.Errorf("invalid template pattern %q: %w", pattern, err)
		}
		return matches, nil
	}
	// ** pattern: walk rootDir and regex-match relative slash paths.
	re, err := globToRegex(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	_ = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".tidy" || d.Name() == "cache" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(rootDir, p)
		if err != nil {
			return nil
		}
		if re.MatchString(filepath.ToSlash(rel)) {
			out = append(out, p)
		}
		return nil
	})
	return out, nil
}

// matchDoublestarAbs handles absolute patterns containing **.
func matchDoublestarAbs(pattern string) ([]string, error) {
	// Walk from the longest static prefix to limit I/O.
	prefix := pattern
	if i := strings.Index(pattern, "**"); i >= 0 {
		prefix = pattern[:i]
		if j := strings.LastIndex(prefix, string(os.PathSeparator)); j >= 0 {
			prefix = prefix[:j]
		} else {
			prefix = string(os.PathSeparator)
		}
	}
	if prefix == "" {
		prefix = string(os.PathSeparator)
	}
	re, err := globToRegex(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	_ = filepath.WalkDir(prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".tidy" || d.Name() == "cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if re.MatchString(filepath.ToSlash(p)) || re.MatchString(p) {
			out = append(out, p)
		}
		return nil
	})
	return out, nil
}

// globToRegex converts a glob with *, ?, ** into a regex.
func globToRegex(pattern string) (*regexp.Regexp, error) {
	slashPat := filepath.ToSlash(pattern)
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(slashPat); {
		if strings.HasPrefix(slashPat[i:], "**/") {
			b.WriteString("(.*/)?")
			i += 3
			continue
		}
		if strings.HasPrefix(slashPat[i:], "**") {
			b.WriteString(".*")
			i += 2
			continue
		}
		c := slashPat[i]
		switch c {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
		i++
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
