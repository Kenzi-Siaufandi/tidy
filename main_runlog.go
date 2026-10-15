package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// runLogDir holds one console-output log per tidy run, never pruned.
const runLogDir = ".tidy/log"

// runLogPrefix names the per-run logs inside runLogDir.
// runLogPrefix names the per-run logs inside runLogDir.
const runLogPrefix = ".tidy/log/tidy-"

// driftReportCap bounds each report section so messy servers stay readable.
// maxRetainedRunLogs caps .tidy/log/ retention (N=50): every run writes one
// file, crash loops would otherwise fill disk/inodes.
const maxRetainedRunLogs = 50

// maxRetainedDriftReports caps .tidy/drift/ retention (N=50).
// runLogName returns the run-log path for a timestamp, e.g.
// .tidy/log/tidy-2026-09-23_05-30-24.log.
func runLogName(now time.Time) string {
	return runLogPrefix + now.Format("2006-01-02_15-04-05") + ".log"
}

// maxDriftReportAttempts bounds the same-second suffix probe so a .tidy
// path conflict (file vs directory, permission errors, crash loops) can
// never hang the boot sequence. Exhaustion falls back to a nanosecond
// suffix below. Shared by drift reports and run logs.
// uniqueRunLogName is the run-log counterpart: same-second runs get
// tidy-...-1.log, then -2, and so on.
func uniqueRunLogName(workDir string, now time.Time) string {
	return uniqueDatedName(workDir, runLogPrefix, runLogName(now), ".log", now)
}

// volatileDateComment matches the timestamp line java.util.Properties.store()
// rewrites atop server.properties on every save, e.g.
// "#Sun Sep 20 14:03:06 WIB 2026". It changes on every boot with no real
// config change, so drift detection ignores it (vanilla behavior, not
// Pterodactyl — and no server option disables it).
// ansiSGR matches SGR color codes (see internal/ui) so run logs stay plain
// text while the panel console keeps its colors.
var (
	runLogFile  *os.File
	runLogPipes []*os.File
	runLogWait  sync.WaitGroup
)

var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

// startRunLog tees this run's stdout/stderr into
// .tidy/log/tidy-YYYY-MM-DD_HH-MM-SS.log while leaving the console output
// untouched. Best-effort: when the log cannot be created the run proceeds
// unlogged. The log carries plain text (color codes stripped, \r progress
// redraws folded to newlines) so it reads well in the panel file manager.
// Retention is capped at maxRetainedRunLogs (N=50).
func startRunLog(workDir string, now time.Time) {
	if err := os.MkdirAll(filepath.Join(workDir, runLogDir), 0755); err != nil {
		return
	}
	name := uniqueRunLogName(workDir, now)
	f, err := os.OpenFile(filepath.Join(workDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return
	}
	runLogFile = f
	os.Stdout = teeToLog(os.Stdout, f)
	os.Stderr = teeToLog(os.Stderr, f)
	pruneOldFiles(filepath.Join(workDir, runLogDir), "tidy-", ".log", maxRetainedRunLogs)
}

// pruneOldFiles keeps only the newest keep files matching prefix+*+ext in
// dir, sorted by name (datetime names sort chronologically). Best-effort.
// pruneOldFiles keeps only the newest keep files matching prefix+*+ext in
// dir, sorted by name (datetime names sort chronologically). Best-effort.
func pruneOldFiles(dir, prefix, ext string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || keep <= 0 {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ext) {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// teeToLog swaps dst for a pipe writer and streams everything written to it
// to both dst and the run log. ANSI stripping buffers a trailing partial
// escape (e.g. "\x1b[" split across 32KiB reads) so split sequences never
// leak raw bytes into the log.
// teeToLog swaps dst for a pipe writer and streams everything written to it
// to both dst and the run log. ANSI stripping buffers a trailing partial
// escape (e.g. "\x1b[" split across 32KiB reads) so split sequences never
// leak raw bytes into the log.
func teeToLog(dst *os.File, log *os.File) *os.File {
	r, w, err := os.Pipe()
	if err != nil {
		return dst
	}
	runLogPipes = append(runLogPipes, w)
	runLogWait.Add(1)
	go func() {
		defer runLogWait.Done()
		defer r.Close()
		buf := make([]byte, 32*1024)
		var pending []byte
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				_, _ = dst.Write(chunk)
				combined := chunk
				if len(pending) > 0 {
					combined = append(append([]byte(nil), pending...), chunk...)
					pending = nil
				}
				// Hold a trailing partial SGR prefix for the next chunk.
				if idx := bytes.LastIndexByte(combined, 0x1b); idx >= 0 {
					tail := combined[idx:]
					if isPartialSGR(tail) {
						pending = append([]byte(nil), tail...)
						combined = combined[:idx]
					}
				}
				plain := ansiSGR.ReplaceAll(combined, nil)
				plain = bytes.ReplaceAll(plain, []byte("\r"), []byte("\n"))
				_, _ = log.Write(plain)
			}
			if rerr != nil {
				// Flush any held partial sequence as plain text.
				if len(pending) > 0 {
					plain := ansiSGR.ReplaceAll(pending, nil)
					_, _ = log.Write(plain)
					pending = nil
				}
				return
			}
		}
	}()
	return w
}

// isPartialSGR reports whether tail looks like a truncated SGR escape:
// "\x1b" or "\x1b[" followed only by digits/semicolons with no final byte.
// isPartialSGR reports whether tail looks like a truncated SGR escape:
// "\x1b" or "\x1b[" followed only by digits/semicolons with no final byte.
func isPartialSGR(tail []byte) bool {
	if len(tail) == 0 || tail[0] != 0x1b {
		return false
	}
	if len(tail) == 1 {
		return true
	}
	if tail[1] != '[' {
		return false
	}
	for _, c := range tail[2:] {
		if (c >= '0' && c <= '9') || c == ';' {
			continue
		}
		// A final letter completes the sequence: not partial.
		return false
	}
	return true
}

// finishRunLog drains the tee goroutines and closes the run log. It must
// run before every process exit or the log tail is lost (deferred calls do
// not run on os.Exit).
// finishRunLog drains the tee goroutines and closes the run log. It must
// run before every process exit or the log tail is lost (deferred calls do
// not run on os.Exit).
func finishRunLog() {
	for _, p := range runLogPipes {
		_ = p.Close()
	}
	runLogPipes = nil
	runLogWait.Wait()
	if runLogFile != nil {
		_ = runLogFile.Close()
		runLogFile = nil
	}
}

// exitWithLog flushes the run log, then exits.
// exitWithLog flushes the run log, then exits.
func exitWithLog(code int) {
	finishRunLog()
	os.Exit(code)
}

// isCharDevice reports whether f is a terminal (used to preserve live
// progress bars: teeing through pipes would otherwise downgrade them to
// piped milestone lines even on a real terminal).
// isCharDevice reports whether f is a terminal (used to preserve live
// progress bars: teeing through pipes would otherwise downgrade them to
// piped milestone lines even on a real terminal).
func isCharDevice(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// setupWorkdir pre-scans setup args for --workdir/--root (both dash forms,
// with space or =) so setup runs log under the same workdir setup itself
// will use. Defaults to "." like setup.Run.
// setupWorkdir pre-scans setup args for --workdir/--root (both dash forms,
// with space or =) so setup runs log under the same workdir setup itself
// will use. Defaults to "." like setup.Run.
func setupWorkdir(args []string) string {
	dir := "."
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		name = strings.TrimLeft(name, "-")
		if name != "workdir" && name != "root" {
			continue
		}
		if hasValue {
			if strings.TrimSpace(value) != "" {
				dir = value
			}
			continue
		}
		if i+1 < len(args) {
			dir = args[i+1]
			i++
		}
	}
	return dir
}

