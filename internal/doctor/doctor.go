package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// JarInfo describes a jar file in the workdir.
type JarInfo struct {
	Name       string
	Size       int64
	IsSymlink  bool
	LinkTarget string
}

// JavaVersion runs `<binary> -version` and returns the first line of its
// combined output (java prints to stderr). Binary is a param for tests.
func JavaVersion(ctx context.Context, binary string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = "java"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run %s -version: %w: %s", binary, err, strings.TrimSpace(out.String()))
	}
	first := strings.TrimSpace(out.String())
	if i := strings.Index(first, "\n"); i >= 0 {
		first = first[:i]
	}
	return first, nil
}

// ListJars returns *.jar entries in workDir (top level only).
func ListJars(workDir string) ([]JarInfo, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil, err
	}
	var jars []JarInfo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".jar") {
			continue
		}
		full := filepath.Join(workDir, name)
		info := JarInfo{Name: name}
		if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			info.IsSymlink = true
			if target, err := os.Readlink(full); err == nil {
				info.LinkTarget = target
			}
		}
		if fi, err := os.Stat(full); err == nil {
			info.Size = fi.Size()
		}
		jars = append(jars, info)
	}
	return jars, nil
}

// Executable returns the path of the running tidy binary. Printed in the
// doctor section so a PATH-vs-local mismatch is visible in the panel log,
// where no interactive shell exists to run `command -v tidy`.
func Executable() (string, error) {
	return os.Executable()
}

// EulaStatus reports eula.txt state: "ok", "missing", or "not-accepted".
func EulaStatus(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "eula.txt"))
	if err != nil {
		return "missing"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx >= 0 {
			k := strings.ToLower(strings.TrimSpace(line[:idx]))
			v := strings.ToLower(strings.TrimSpace(line[idx+1:]))
			if k == "eula" {
				if v == "true" {
					return "ok"
				}
				return "not-accepted"
			}
		}
	}
	return "not-accepted"
}
