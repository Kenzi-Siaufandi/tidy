package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runGit executes git in dir, returning exit code, stdout, stderr.
func runGit(dir string, args ...string) (int, string, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return -1, "", "git executable not found in PATH"
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	outBuf := &strings.Builder{}
	errBuf := &strings.Builder{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	err := cmd.Run()
	out, errOut := outBuf.String(), errBuf.String()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), out, errOut
		}
		return -1, out, errOut
	}
	return 0, out, errOut
}

// IsGitRepo reports whether dir is inside a git work tree.
func IsGitRepo(root string) bool {
	code, out, _ := runGit(root, "rev-parse", "--is-inside-work-tree")
	return code == 0 && strings.TrimSpace(out) == "true"
}

// GitStatusMap returns relative path -> porcelain status code.
func GitStatusMap(root string) map[string]string {
	m := map[string]string{}
	if !IsGitRepo(root) {
		return m
	}
	code, out, _ := runGit(root, "status", "--porcelain", "-uall")
	if code != 0 {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		st := strings.TrimSpace(line[:2])
		p := strings.TrimSpace(line[3:])
		if p != "" {
			m[p] = st
		}
	}
	return m
}

// LsFiles lists tracked files, optionally with extra git ls-files args.
func LsFiles(root string, extra ...string) []string {
	args := append([]string{"ls-files"}, extra...)
	code, out, _ := runGit(root, args...)
	if code != 0 {
		return nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			files = append(files, p)
		}
	}
	return files
}

// IsGitIgnored reports whether absPath is ignored via hardcoded rules or
// git check-ignore (when inside a repo).
func IsGitIgnored(root, absPath string) bool {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, part := range parts[:len(parts)-1] {
		if IgnoredDirectories[part] {
			return true
		}
	}
	if AlwaysIgnoreFilenames[strings.ToLower(filepath.Base(absPath))] {
		return true
	}
	if IsGitRepo(root) {
		code, _, _ := runGit(root, "check-ignore", "-q", filepath.ToSlash(rel))
		if code == 0 {
			return true
		}
	}
	return false
}

// worldLikeDir matches world data directories that must never be tracked.
func worldLikeDir(seg string) bool {
	switch seg {
	case "world", "world_nether", "world_the_end", "world_the_end_nether":
		return true
	default:
		return false
	}
}

// IsForbiddenTracked reports whether a git-relative path must not be tracked
// (worlds, JARs, runtime databases, usercache, archives).
func IsForbiddenTracked(relPath string) bool {
	slash := filepath.ToSlash(relPath)
	lower := strings.ToLower(slash)
	base := lower[strings.LastIndex(lower, "/")+1:]

	for _, seg := range strings.Split(lower, "/") {
		if worldLikeDir(seg) {
			return true
		}
	}
	if base == "usercache.json" {
		return true
	}
	switch {
	case strings.HasSuffix(lower, ".jar"),
		strings.HasSuffix(lower, ".db"),
		strings.HasSuffix(lower, ".sqlite"),
		strings.HasSuffix(lower, ".sqlite3"),
		strings.HasSuffix(lower, ".h2.db"),
		strings.HasSuffix(lower, ".mv.db"),
		strings.HasSuffix(lower, ".zip"):
		return true
	}
	_ = base
	return false
}
