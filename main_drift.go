package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/git"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
	"github.com/Kenzi-Siaufandi/tidy/internal/templating"
	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
)

// driftReportDir holds the panel-viewable drift reports: one file per drift
// event (clean runs write nothing), never pruned, so old reports remain as
// history without cluttering .tidy itself.
const driftReportDir = ".tidy/drift"

// driftReportPrefix names the drift reports inside driftReportDir.
// driftReportPrefix names the drift reports inside driftReportDir.
const driftReportPrefix = ".tidy/drift/drift-"

// runLogDir holds one console-output log per tidy run, never pruned.
// driftReportCap bounds each report section so messy servers stay readable.
const driftReportCap = 50

// driftDiffFilesCap bounds how many files get full diffs in a report. The
// name lists above always cover every file; diffs are the troubleshooting
// payload and the expensive part, so only the first N (sorted) are shown.
// driftDiffFilesCap bounds how many files get full diffs in a report. The
// name lists above always cover every file; diffs are the troubleshooting
// payload and the expensive part, so only the first N (sorted) are shown.
const driftDiffFilesCap = 10

// driftDiffLinesCap bounds each file's diff so one huge config can't flood
// the panel disk (reports are never pruned).
// driftDiffLinesCap bounds each file's diff so one huge config can't flood
// the panel disk (reports are never pruned).
const driftDiffLinesCap = 80

// maxRetainedRunLogs caps .tidy/log/ retention (N=50): every run writes one
// file, crash loops would otherwise fill disk/inodes.
// maxRetainedDriftReports caps .tidy/drift/ retention (N=50).
const maxRetainedDriftReports = 50

// driftReportName returns the report path for a run timestamp, e.g.
// .tidy/drift/drift-2026-09-23_05-30-24.txt. Hyphens keep it shell/panel-safe.
// driftReportName returns the report path for a run timestamp, e.g.
// .tidy/drift/drift-2026-09-23_05-30-24.txt. Hyphens keep it shell/panel-safe.
func driftReportName(now time.Time) string {
	return driftReportPrefix + now.Format("2006-01-02_15-04-05") + ".txt"
}

// runLogName returns the run-log path for a timestamp, e.g.
// .tidy/log/tidy-2026-09-23_05-30-24.log.
// maxDriftReportAttempts bounds the same-second suffix probe so a .tidy
// path conflict (file vs directory, permission errors, crash loops) can
// never hang the boot sequence. Exhaustion falls back to a nanosecond
// suffix below. Shared by drift reports and run logs.
const maxDriftReportAttempts = 100

// uniqueDatedName keeps the agreed datetime format but avoids losing
// history when two runs land in the same second (crash loops): the second
// run gets ...-1.txt, then -2, and so on.
// uniqueDatedName keeps the agreed datetime format but avoids losing
// history when two runs land in the same second (crash loops): the second
// run gets ...-1.txt, then -2, and so on.
func uniqueDatedName(workDir, prefix, base, ext string, now time.Time) string {
	if _, err := os.Stat(filepath.Join(workDir, base)); err != nil {
		if os.IsNotExist(err) {
			return base
		}
		// Stat failed for another reason (permissions, .tidy is a file,
		// I/O error): do not spin probing. Return a nanosecond-suffixed
		// name so the boot proceeds; the write itself is best-effort.
		return fmt.Sprintf("%s%s-%d%s", prefix,
			strings.TrimSuffix(strings.TrimPrefix(base, prefix), ext),
			now.UnixNano()%1000000, ext)
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(base, prefix), ext)
	for i := 1; i <= maxDriftReportAttempts; i++ {
		candidate := fmt.Sprintf("%s%s-%d%s", prefix, trimmed, i, ext)
		if _, err := os.Stat(filepath.Join(workDir, candidate)); err != nil {
			if os.IsNotExist(err) {
				return candidate
			}
			// Non-NotExist stat error: stop probing, use this candidate.
			return candidate
		}
	}
	// Suffix space exhausted: nanosecond fallback guarantees progress.
	return fmt.Sprintf("%s%s-%d%s", prefix, trimmed, now.UnixNano(), ext)
}

// uniqueDriftReportName keeps the agreed datetime format but avoids losing
// history when two runs land in the same second (crash loops): the second
// run gets drift-...-1.txt, then -2, and so on.
// uniqueDriftReportName keeps the agreed datetime format but avoids losing
// history when two runs land in the same second (crash loops): the second
// run gets drift-...-1.txt, then -2, and so on.
func uniqueDriftReportName(workDir string, now time.Time) string {
	return uniqueDatedName(workDir, driftReportPrefix, driftReportName(now), ".txt", now)
}

// uniqueRunLogName is the run-log counterpart: same-second runs get
// tidy-...-1.log, then -2, and so on.
// volatileDateComment matches the timestamp line java.util.Properties.store()
// rewrites atop server.properties on every save, e.g.
// "#Sun Sep 20 14:03:06 WIB 2026". It changes on every boot with no real
// config change, so drift detection ignores it (vanilla behavior, not
// Pterodactyl — and no server option disables it).
var volatileDateComment = regexp.MustCompile(`^#[A-Z][a-z]{2} [A-Z][a-z]{2} \d{1,2} \d{2}:\d{2}:\d{2} \S+ \d{4}$`)

// hasRealDiff reports whether a unified diff contains any change beyond
// volatile date comments. Headers (+++ / --- / @@ / diff --git / index) and
// context lines never count; a binary-change marker always counts.
// hasRealDiff reports whether a unified diff contains any change beyond
// volatile date comments. Headers (+++ / --- / @@ / diff --git / index) and
// context lines never count; a binary-change marker always counts.
func hasRealDiff(diff string) bool {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case l == "",
			strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"),
			strings.HasPrefix(l, "@@"), strings.HasPrefix(l, "diff --git"),
			strings.HasPrefix(l, "index "):
			continue
		case strings.HasPrefix(l, "Binary files "):
			return true
		case strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-"):
			if volatileDateComment.MatchString(strings.TrimSpace(l[1:])) {
				continue
			}
			return true
		}
	}
	return false
}

// isTidyInternal reports whether a repo-relative path is Tidy's own runtime
// state (.tidy/). Those files exist only locally and never on the remote, so
// they are pollution in a drift report, not drift.
// isTidyInternal reports whether a repo-relative path is Tidy's own runtime
// state (.tidy/). Those files exist only locally and never on the remote, so
// they are pollution in a drift report, not drift.
func isTidyInternal(p string) bool {
	p = strings.TrimPrefix(p, "./")
	return p == ".tidy" || strings.HasPrefix(p, ".tidy/")
}

// rejectStateTraversal rejects absolute paths, parent traversals and
// subdirectory separators so state-tracked filenames stay bare names.
// reportDrift snapshots working-tree drift versus the git remote into a
// dated .tidy/drift-*.txt report and warns on console. It runs before Pull's
// reset --hard, while the evidence still exists. Files substituted by the
// previous run's templating are expected-dirty and filtered out. A report is
// only written when drift exists, so clean boots leave no files behind and
// history stays bounded to real events (never pruned).
// Best-effort: never fails the boot.
func reportDrift(ctx context.Context, gitClient *git.Client, workDir, configFlag string, previousState *state.State) {
	drift, err := gitClient.StatusDrift(ctx, workDir)
	if err != nil {
		return
	}
	templated := make(map[string]bool)
	if previousState != nil {
		for _, p := range previousState.TemplatedFiles {
			if s := strings.TrimSpace(p); s != "" {
				templated[s] = true
			}
		}
	}
	var modified []string
	var churnSkipped int
	diffs := make(map[string]string)
	for _, p := range drift.Modified {
		if isTidyInternal(p) {
			continue
		}
		d, err := gitClient.DiffTracked(ctx, workDir, p)
		if err != nil {
			modified = append(modified, p)
			continue
		}
		if !hasRealDiff(d) {
			// Timestamp-only churn (e.g. the server.properties date
			// header the server rewrites on every boot): not drift.
			churnSkipped++
			continue
		}
		// Templated files need masking, not skipping: re-apply template
		// substitution to the HEAD blob in memory and compare against the
		// working file. Pure templating differences are expected-dirty;
		// real user edits are reported with secrets masked.
		if templated[p] || previousState == nil {
			masked, isOnlyTemplate := maskedTemplatedDiff(ctx, gitClient, workDir, p, d)
			if isOnlyTemplate {
				churnSkipped++
				continue
			}
			if masked != "" {
				d = masked
			}
			// previousState==nil means we cannot know templated set:
			// still mask any file whose HEAD contains {{VAR}}.
			if previousState == nil && masked == "" && !templated[p] {
				if m, ok := maskSecretsInDiff(ctx, gitClient, workDir, p, d); ok {
					d = m
				}
			}
		}
		modified = append(modified, p)
		diffs[p] = d
	}
	var untracked []string
	for _, p := range drift.Untracked {
		if !isTidyInternal(p) {
			untracked = append(untracked, p)
		}
	}
	if len(modified)+len(untracked) == 0 {
		return
	}
	now := time.Now()
	reportName := uniqueDriftReportName(workDir, now)
	writeDriftReport(workDir, reportName, now, modified, untracked, diffs, churnSkipped)
	if rel := configRelPath(workDir, configFlag); rel != "" {
		for _, p := range modified {
			if p == rel {
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Git: local %s differs from the remote and will be discarded by sync — commit it to keep it", rel)))
				break
			}
		}
	}
	fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Git: %d file(s) differ from the remote — see %s",
		len(modified)+len(untracked), reportName)))
}

// maskedTemplatedDiff compares the working file against the HEAD blob with
// template substitution re-applied in memory. It returns (maskedDiff, true)
// when the only differences are templating + volatile date comments
// (expected-dirty, skip), or (maskedDiff, false) when real user edits exist.
// maskedDiff has secret env values replaced with {{VAR}} so drift reports
// never leak plaintext secrets into world-readable files.
// maskedTemplatedDiff compares the working file against the HEAD blob with
// template substitution re-applied in memory. It returns (maskedDiff, true)
// when the only differences are templating + volatile date comments
// (expected-dirty, skip), or (maskedDiff, false) when real user edits exist.
// maskedDiff has secret env values replaced with {{VAR}} so drift reports
// never leak plaintext secrets into world-readable files.
func maskedTemplatedDiff(ctx context.Context, gitClient *git.Client, workDir, relPath, rawDiff string) (string, bool) {
	head, err := gitClient.ShowHeadFile(ctx, workDir, relPath)
	if err != nil || head == "" {
		// Cannot mask without HEAD: keep raw diff (caller reports it).
		return "", false
	}
	if !strings.Contains(head, "{{") {
		return "", false
	}
	expected, _, _ := templating.ReplaceString(head)
	workBytes, err := os.ReadFile(filepath.Join(workDir, filepath.FromSlash(relPath)))
	if err != nil {
		return "", false
	}
	if contentsEqualIgnoringVolatile(expected, string(workBytes)) {
		return "", true
	}
	// Previous run's env may differ from current (or test env unset):
	// if working matches the HEAD template shape (literals in order),
	// it is pure templating output, not a panel edit.
	if templateShapeMatches(head, string(workBytes)) {
		return "", true
	}
	return maskValuesInText(rawDiff, placeholdersIn(head)), false
}

// maskSecretsInDiff masks secret values in a diff for files whose HEAD
// contains {{VAR}} placeholders. ok=false when no masking applied.
// maskSecretsInDiff masks secret values in a diff for files whose HEAD
// contains {{VAR}} placeholders. ok=false when no masking applied.
func maskSecretsInDiff(ctx context.Context, gitClient *git.Client, workDir, relPath, rawDiff string) (string, bool) {
	head, err := gitClient.ShowHeadFile(ctx, workDir, relPath)
	if err != nil || head == "" || !strings.Contains(head, "{{") {
		return "", false
	}
	return maskValuesInText(rawDiff, placeholdersIn(head)), true
}

// placeholdersIn returns distinct {{VAR}} names found in content.
// placeholdersIn returns distinct {{VAR}} names found in content.
func placeholdersIn(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range templating.MustacheVariableRegex.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

// maskValuesInText replaces non-empty env values for vars with {{VAR}} so
// reports show the placeholder instead of the secret.
// maskValuesInText replaces non-empty env values for vars with {{VAR}} so
// reports show the placeholder instead of the secret.
func maskValuesInText(text string, vars []string) string {
	for _, v := range vars {
		if val, ok := os.LookupEnv(v); ok && val != "" && len(val) >= 3 {
			text = strings.ReplaceAll(text, val, "{{"+v+"}}")
		}
	}
	return text
}

// templateShapeMatches reports whether working could be head with every
// {{VAR}} replaced by some value: all literal segments between placeholders
// appear in order. Distinguishes pure templating output (previous run's env,
// possibly unset now) from real panel edits elsewhere.
// templateShapeMatches reports whether working could be head with every
// {{VAR}} replaced by some value: all literal segments between placeholders
// appear in order. Distinguishes pure templating output (previous run's env,
// possibly unset now) from real panel edits elsewhere.
func templateShapeMatches(head, working string) bool {
	locs := templating.MustacheVariableRegex.FindAllStringIndex(head, -1)
	if len(locs) == 0 {
		return false
	}
	var lits []string
	prev := 0
	for _, l := range locs {
		lits = append(lits, head[prev:l[0]])
		prev = l[1]
	}
	lits = append(lits, head[prev:])
	pos := 0
	for i, lit := range lits {
		if lit == "" {
			continue
		}
		if i == 0 {
			if strings.HasPrefix(working[pos:], lit) {
				pos += len(lit)
				continue
			}
			idx := strings.Index(working[pos:], lit)
			if idx < 0 {
				return false
			}
			pos += idx + len(lit)
			continue
		}
		if i == len(lits)-1 {
			if lit == "\n" {
				return true
			}
			return strings.HasSuffix(working, lit) || strings.Contains(working[pos:], lit)
		}
		idx := strings.Index(working[pos:], lit)
		if idx < 0 {
			return false
		}
		pos += idx + len(lit)
	}
	return true
}

// contentsEqualIgnoringVolatile compares two file contents ignoring volatile
// date-comment lines (server.properties header rewritten every boot).
// contentsEqualIgnoringVolatile compares two file contents ignoring volatile
// date-comment lines (server.properties header rewritten every boot).
func contentsEqualIgnoringVolatile(a, b string) bool {
	strip := func(s string) string {
		var kept []string
		for _, l := range strings.Split(s, "\n") {
			t := strings.TrimRight(l, "\r")
			if volatileDateComment.MatchString(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "+"), "-"))) {
				continue
			}
			if volatileDateComment.MatchString(strings.TrimSpace(t)) {
				continue
			}
			kept = append(kept, t)
		}
		return strings.Join(kept, "\n")
	}
	return strip(a) == strip(b)
}

// configRelPath returns the config path repo-relative in slash form, or ""
// when it lives outside the workdir (no remote to diff against).
// configRelPath returns the config path repo-relative in slash form, or ""
// when it lives outside the workdir (no remote to diff against).
func configRelPath(workDir, configFlag string) string {
	rel := strings.TrimSpace(configFlag)
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		r, err := filepath.Rel(workDir, rel)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return ""
		}
		rel = r
	}
	return filepath.ToSlash(rel)
}

// formatDriftReport renders the panel-viewable drift report body.
// Timestamps are local so they match the panel console and the filename.
// diffs holds per-file `git diff HEAD` output for troubleshooting what the
// sync discarded; files without an entry render as "(diff unavailable)".
// formatDriftReport renders the panel-viewable drift report body.
// Timestamps are local so they match the panel console and the filename.
// diffs holds per-file `git diff HEAD` output for troubleshooting what the
// sync discarded; files without an entry render as "(diff unavailable)".
func formatDriftReport(modified, untracked []string, diffs map[string]string, churnSkipped int, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tidy drift report — %s\n", now.Format("2006-01-02 15:04:05 -0700"))
	b.WriteString("# Written before the git sync.\n")
	b.WriteString("# 'Discarded' files are tracked and were reset (reset --hard): commit them to keep them.\n")
	b.WriteString("# 'Kept' files are untracked: git left them alone, but the remote does not know them.\n")
	b.WriteString("# Files templated ({{VAR}}) show masked diffs (secrets as {{VAR}}); pure templating is filtered.\n")
	b.WriteString("# Diffs below are what the sync discarded (unified diff vs HEAD).\n")
	if churnSkipped > 0 {
		fmt.Fprintf(&b, "# %d file(s) differed only by volatile date comments (e.g. the server.properties header) and were ignored.\n", churnSkipped)
	}
	writeDriftSection(&b, "Discarded on sync (tracked, locally modified)", modified)
	writeDriftSection(&b, "Kept locally (untracked, not in git)", untracked)
	writeDiffSection(&b, modified, diffs)
	return b.String()
}

func writeDriftSection(b *strings.Builder, title string, paths []string) {
	fmt.Fprintf(b, "\n## %s (%d)\n", title, len(paths))
	if len(paths) == 0 {
		b.WriteString("(none)\n")
		return
	}
	shown := paths
	extra := ""
	if len(shown) > driftReportCap {
		shown = shown[:driftReportCap]
		extra = fmt.Sprintf("(and %d more)\n", len(paths)-driftReportCap)
	}
	for _, p := range shown {
		b.WriteString(p + "\n")
	}
	b.WriteString(extra)
}

// writeDiffSection renders the troubleshooting payload: what the sync
// discarded, per tracked file. Only the first driftDiffFilesCap files get
// diffs; each diff is capped at driftDiffLinesCap lines.
// writeDiffSection renders the troubleshooting payload: what the sync
// discarded, per tracked file. Only the first driftDiffFilesCap files get
// diffs; each diff is capped at driftDiffLinesCap lines.
func writeDiffSection(b *strings.Builder, modified []string, diffs map[string]string) {
	fmt.Fprintf(b, "\n## Changes that will be lost (diff vs HEAD, first %d files)\n", driftDiffFilesCap)
	if len(modified) == 0 {
		b.WriteString("(none)\n")
		return
	}
	shown := modified
	if len(shown) > driftDiffFilesCap {
		shown = shown[:driftDiffFilesCap]
	}
	for _, p := range shown {
		fmt.Fprintf(b, "\n### %s\n", p)
		d, ok := diffs[p]
		if !ok {
			b.WriteString("(diff unavailable)\n")
			continue
		}
		d = strings.TrimRight(d, "\n")
		if d == "" {
			b.WriteString("(no textual diff)\n")
			continue
		}
		lines := strings.Split(d, "\n")
		if len(lines) > driftDiffLinesCap {
			for _, l := range lines[:driftDiffLinesCap] {
				b.WriteString(l + "\n")
			}
			fmt.Fprintf(b, "(... truncated, %d more lines)\n", len(lines)-driftDiffLinesCap)
		} else {
			for _, l := range lines {
				b.WriteString(l + "\n")
			}
		}
	}
	if len(modified) > len(shown) {
		fmt.Fprintf(b, "\n(and diffs for %d more file(s) omitted)\n", len(modified)-len(shown))
	}
}

// writeDriftReport persists the dated drift report under .tidy/drift.
// Best-effort. Retention is capped at maxRetainedDriftReports (N=50).
// writeDriftReport persists the dated drift report under .tidy/drift.
// Best-effort. Retention is capped at maxRetainedDriftReports (N=50).
func writeDriftReport(workDir, reportName string, now time.Time, modified, untracked []string, diffs map[string]string, churnSkipped int) {
	if err := os.MkdirAll(filepath.Join(workDir, driftReportDir), 0755); err != nil {
		return
	}
	content := formatDriftReport(modified, untracked, diffs, churnSkipped, now)
	_ = os.WriteFile(filepath.Join(workDir, reportName), []byte(content), 0644)
	pruneOldFiles(filepath.Join(workDir, driftReportDir), "drift-", ".txt", maxRetainedDriftReports)
}

// sameFileOnDisk reports whether two absolute paths resolve to the same file
