package templating

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// If an environment variable is not present, it leaves the placeholder unchanged and records it.
func ReplaceString(content string) (string, int, []string) {
	replacements := 0
	var missingVars []string

	result := MustacheVariableRegex.ReplaceAllStringFunc(content, func(match string) string {
		submatches := MustacheVariableRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		varName := submatches[1]
		val, exists := os.LookupEnv(varName)
		if !exists {
			missingVars = append(missingVars, varName)
			return match
		}
		replacements++
		return val
	})

	return result, replacements, missingVars
}

// IsSensitiveVar checks whether a variable name looks like a secret/credential.
func IsSensitiveVar(name string) bool {
	upper := strings.ToUpper(name)
	for _, keyword := range []string{"PASSWORD", "SECRET", "TOKEN", "KEY", "AUTH", "PASS"} {
		if strings.Contains(upper, keyword) {
			return true
		}
	}
	return false
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
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	newContent, count, missing := ReplaceString(string(data))
	if count > 0 {
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return 0, nil, fmt.Errorf("failed to write templated file %q: %w", path, err)
		}
	}

	return count, missing, nil
}

// ProcessDirectory scans the directory and processes config files.
func ProcessDirectory(rootDir string, customPaths []string) (*ReplaceResult, error) {
	result := &ReplaceResult{}

	// If no custom paths provided, walk the rootDir and find all supported config files
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

		relPath, _ := filepath.Rel(rootDir, path)
		count, missing, procErr := ProcessFile(path)
		if procErr != nil {
			return procErr
		}

		result.FilesProcessed++
		if count > 0 {
			result.Replacements += count
			result.ModifiedFiles = append(result.ModifiedFiles, relPath)
		}

		for _, mv := range missing {
			fmt.Printf(" [!] Config warning: environment variable {{%s}} in %s is not set\n", mv, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error processing templates in %q: %w", rootDir, err)
	}

	return result, nil
}
