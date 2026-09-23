package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kenzi-Siaufandi/tidy/internal/config"
	"github.com/Kenzi-Siaufandi/tidy/internal/git"
	"github.com/Kenzi-Siaufandi/tidy/internal/jarlink"
	"github.com/Kenzi-Siaufandi/tidy/internal/progress"
	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
	"github.com/Kenzi-Siaufandi/tidy/internal/setup"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
	"github.com/Kenzi-Siaufandi/tidy/internal/storage"
	"github.com/Kenzi-Siaufandi/tidy/internal/templating"
	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
	"github.com/Kenzi-Siaufandi/tidy/internal/version"
)

const Version = version.Version

// driftReportPrefix names the panel-viewable drift reports inside .tidy.
// A file is written per drift event (clean runs write nothing) as
// drift-YYYY-MM-DD_HH-MM-SS.txt, never pruned, so old reports remain
// as history.
const driftReportPrefix = ".tidy/drift-"

// driftReportCap bounds each report section so messy servers stay readable.
const driftReportCap = 50

// driftDiffFilesCap bounds how many files get full diffs in a report. The
// name lists above always cover every file; diffs are the troubleshooting
// payload and the expensive part, so only the first N (sorted) are shown.
const driftDiffFilesCap = 10

// driftDiffLinesCap bounds each file's diff so one huge config can't flood
// the panel disk (reports are never pruned).
const driftDiffLinesCap = 80

// driftReportName returns the report path for a run timestamp, e.g.
// .tidy/drift-2026-09-23_05-30-24.txt. Hyphens keep it shell/panel-safe.
func driftReportName(now time.Time) string {
	return driftReportPrefix + now.Format("2006-01-02_15-04-05") + ".txt"
}

// uniqueDriftReportName keeps the agreed datetime format but avoids losing
// history when two runs land in the same second (crash loops): the second
// run gets drift-...-1.txt, then -2, and so on.
func uniqueDriftReportName(workDir string, now time.Time) string {
	base := driftReportName(now)
	if _, err := os.Stat(filepath.Join(workDir, base)); os.IsNotExist(err) {
		return base
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(base, driftReportPrefix), ".txt")
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s%s-%d.txt", driftReportPrefix, trimmed, i)
		if _, err := os.Stat(filepath.Join(workDir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// isTidyInternal reports whether a repo-relative path is Tidy's own runtime
// state (.tidy/). Those files exist only locally and never on the remote, so
// they are pollution in a drift report, not drift.
func isTidyInternal(p string) bool {
	p = strings.TrimPrefix(p, "./")
	return p == ".tidy" || strings.HasPrefix(p, ".tidy/")
}

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
	for _, p := range drift.Modified {
		if !templated[p] && !isTidyInternal(p) {
			modified = append(modified, p)
		}
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
	// Capture what the sync is about to discard while the evidence still
	// exists. Best-effort per file: a failed diff must not lose the report.
	diffs := make(map[string]string)
	shown := modified
	if len(shown) > driftDiffFilesCap {
		shown = shown[:driftDiffFilesCap]
	}
	for _, p := range shown {
		if d, err := gitClient.DiffTracked(ctx, workDir, p); err == nil {
			diffs[p] = d
		}
	}
	now := time.Now()
	reportName := uniqueDriftReportName(workDir, now)
	writeDriftReport(workDir, reportName, now, modified, untracked, diffs)
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
func formatDriftReport(modified, untracked []string, diffs map[string]string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tidy drift report — %s\n", now.Format("2006-01-02 15:04:05 -0700"))
	b.WriteString("# Written before the git sync.\n")
	b.WriteString("# 'Discarded' files are tracked and were reset (reset --hard): commit them to keep them.\n")
	b.WriteString("# 'Kept' files are untracked: git left them alone, but the remote does not know them.\n")
	b.WriteString("# Files templated ({{VAR}}) by the previous run are expected-dirty and hidden.\n")
	b.WriteString("# Diffs below are what the sync discarded (unified diff vs HEAD).\n")
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

// writeDriftReport persists the dated drift report under .tidy. Best-effort.
func writeDriftReport(workDir, reportName string, now time.Time, modified, untracked []string, diffs map[string]string) {
	if err := os.MkdirAll(filepath.Join(workDir, ".tidy"), 0755); err != nil {
		return
	}
	content := formatDriftReport(modified, untracked, diffs, now)
	_ = os.WriteFile(filepath.Join(workDir, reportName), []byte(content), 0644)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		os.Exit(setup.Run(os.Args[2:]))
	}
	var (
		configFlag     = flag.String("config", "tidy.toml", "Path to tidy.toml configuration file")
		workDirFlag    = flag.String("workdir", ".", "Working directory for Minecraft server root")
		gitRepoFlag    = flag.String("git-repo", "", "Git repository URL to synchronize declarative configs (or env GIT_REPO)")
		gitBranchFlag  = flag.String("git-branch", "", "Git branch to synchronize (or env GIT_BRANCH, default 'main')")
		gitTokenFlag   = flag.String("git-token", "", "Git authentication token (or env GIT_TOKEN / GITHUB_TOKEN)")
		gitUserFlag    = flag.String("git-user", "", "Git username (or env GIT_USER)")
		skipGitFlag    = flag.Bool("skip-git", false, "Skip Git synchronization")
		skipTplFlag    = flag.Bool("skip-templates", false, "Skip Mustache template variable replacement")
		noColorFlag    = flag.Bool("no-color", false, "Disable colored output")
		noProgressFlag = flag.Bool("no-progress", false, "Disable download progress bars")
		versionFlag    = flag.Bool("version", false, "Print Tidy version and exit")
	)

	flag.Parse()

	if *noColorFlag {
		ui.SetEnabled(false)
	}
	progress.Enabled = !*noProgressFlag

	if *versionFlag {
		fmt.Printf("Tidy v%s\n", Version)
		os.Exit(0)
	}

	workDir, err := filepath.Abs(*workDirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: failed to determine absolute path for workdir: %v", err)))
		os.Exit(1)
	}

	fmt.Println(ui.Bold("=================================================================="))
	fmt.Printf("  Tidy v%s \n", Version)
	fmt.Println(ui.Bold("=================================================================="))

	firstInstall := state.IsFirstInstall(workDir)
	var previousState *state.State
	if firstInstall {
		fmt.Println(ui.Cyan("[*] Status: First install detected in container."))
	} else {
		previousState, err = state.LoadState(workDir)
		if err != nil {
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: failed to load existing state file: %v. Proceeding as fresh sync.", err)))
		}
	}

	// 1. Git Synchronization & Incremental Diff Engine
	gitRepo := *gitRepoFlag
	if gitRepo == "" {
		for _, envKey := range []string{"GIT_REPO", "GIT_URL", "TIDY_GIT_REPO"} {
			if val := os.Getenv(envKey); strings.TrimSpace(val) != "" {
				gitRepo = strings.TrimSpace(val)
				break
			}
		}
	}

	gitBranch := *gitBranchFlag
	if gitBranch == "" {
		for _, envKey := range []string{"GIT_BRANCH", "TIDY_GIT_BRANCH"} {
			if val := os.Getenv(envKey); strings.TrimSpace(val) != "" {
				gitBranch = strings.TrimSpace(val)
				break
			}
		}
		if gitBranch == "" {
			gitBranch = "main"
		}
	}

	gitToken := *gitTokenFlag
	if gitToken == "" {
		for _, envKey := range []string{"GIT_TOKEN", "GIT_AUTH_TOKEN", "GITHUB_TOKEN"} {
			if val := os.Getenv(envKey); strings.TrimSpace(val) != "" {
				gitToken = strings.TrimSpace(val)
				break
			}
		}
	}

	gitUser := *gitUserFlag
	if gitUser == "" {
		gitUser = os.Getenv("GIT_USER")
	}

	var currentCommit string
	if !*skipGitFlag && gitRepo != "" {
		gitClient, err := git.NewClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: %v", err)))
			os.Exit(1)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		gitOpts := git.Options{
			RepoURL:   gitRepo,
			Branch:    gitBranch,
			Token:     gitToken,
			Username:  gitUser,
			TargetDir: workDir,
		}

		if firstInstall || !gitClient.IsGitRepo(workDir) {
			fmt.Printf("%s\n", ui.Cyan(fmt.Sprintf("[*] Git: Initializing clone from %s (branch: %s)...", git.MaskURL(gitRepo), gitBranch)))
			if err := gitClient.Clone(ctx, gitOpts); err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Git clone failed: %v", err)))
				os.Exit(1)
			}
			headCommit, headErr := gitClient.GetHeadCommit(ctx, workDir)
			if headErr != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: failed to get HEAD commit after clone: %v", headErr)))
				currentCommit = ""
			} else {
				currentCommit = headCommit
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Git: Initial clone complete (HEAD: %s)", git.ShortSHA(currentCommit))))
		} else {
			fmt.Printf("%s\n", ui.Cyan(fmt.Sprintf("[*] Git: Checking for upstream updates from %s (branch: %s)...", git.MaskURL(gitRepo), gitBranch)))
			// Snapshot drift versus the remote before sync discards it, and
			// leave a panel-viewable report in .tidy. Best-effort; never
			// fails the boot.
			reportDrift(ctx, gitClient, workDir, *configFlag, previousState)
			pullRes, err := gitClient.Pull(ctx, gitOpts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Git pull failed: %v", err)))
				os.Exit(1)
			}
			currentCommit = pullRes.NewCommit

			if pullRes.HasUpdates {
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Git: Pulled %d new commits (%s..%s):",
					len(pullRes.Commits), git.ShortSHA(pullRes.OldCommit), git.ShortSHA(pullRes.NewCommit))))
				for _, cMsg := range pullRes.Commits {
					fmt.Printf("    - %s\n", cMsg)
				}
				fmt.Printf("%s\n", ui.Cyan("[*] Git: Modified files:"))
				for _, ch := range pullRes.ChangedFiles {
					fmt.Printf("    [%s] %s\n", ch.Status, ch.Path)
				}
			} else {
				fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Git: Repository is up to date (commit %s)", git.ShortSHA(currentCommit))))
			}
		}
	} else if gitRepo == "" {
		fmt.Println(ui.Cyan("[*] Git: No GIT_REPO specified; using local directory configs."))
	}

	// 2. Read and Validate tidy.toml
	configFile := *configFlag
	if !filepath.IsAbs(configFile) {
		configFile = filepath.Join(workDir, configFile)
	}

	if _, err := os.Stat(configFile); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Configuration file %q not found: %v", configFile, err)))
		os.Exit(1)
	}

	fmt.Printf("%s\n", ui.Cyan(fmt.Sprintf("[*] Config: Loading %s...", filepath.Base(configFile))))
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to load config: %v", err)))
		os.Exit(1)
	}

	ctx := context.Background()

	// 3. Reconcile Server Software (PaperMC Fill / PurpurMC API with SHA-256)
	var activeServerFilename string
	var activeServerSHA256 string
	var activeServerBuildID int

	serverResolver := resolver.NewServerResolver(cfg.Server.Project)

	serverNeedsDownload := true
	if previousState != nil && previousState.Server.Project == cfg.Server.Project && previousState.Server.Version == cfg.Server.Version {
		// Server project and version match previous state. Verify local file existence and SHA-256.
		localJar := filepath.Join(workDir, previousState.Server.Filename)
		if state.VerifyLocalSHA256(localJar, previousState.Server.SHA256) {
			requestedBuild := strings.TrimSpace(cfg.Server.Build)
			if requestedBuild == "" {
				requestedBuild = "latest"
			}
			if strings.EqualFold(requestedBuild, "latest") {
				// Auto-update: check upstream for a newer build.
				upstreamBuildID, fetchErr := serverResolver.FetchLatestBuildID(ctx, cfg.Server)
				if fetchErr != nil {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: failed to check upstream build for updates, keeping local %s: %v", previousState.Server.Filename, fetchErr)))
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				} else if upstreamBuildID == previousState.Server.BuildID && previousState.Server.BuildID != 0 {
					fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Server: %s is up-to-date (build #%d, SHA-256 verified, skipped download)", previousState.Server.Filename, previousState.Server.BuildID)))
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				}
				// A newer build falls through silently; the result prints below.
			} else {
				// Pinned build: skip only if stored build ID matches request.
				if fmt.Sprintf("%d", previousState.Server.BuildID) == requestedBuild {
					fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Server: %s is up-to-date (SHA-256 verified, skipped download)", previousState.Server.Filename)))
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				}
				// Build mismatch falls through silently; the result prints below.
			}
		}
		// Missing/checksum-changed/version-changed fall through silently;
		// the result prints below.
	}

	if serverNeedsDownload {
		prevFilename := ""
		if previousState != nil {
			prevFilename = previousState.Server.Filename
		}
		serverRes, err := serverResolver.ResolveAndDownload(ctx, cfg.Server, workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to resolve/download server software: %v", err)))
			os.Exit(1)
		}
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Server: Downloaded %s (Build #%d, SHA256: %s, %d bytes)",
			serverRes.Filename, serverRes.BuildID, serverRes.SHA256[:12]+"...", serverRes.Size)))
		activeServerFilename = serverRes.Filename
		activeServerSHA256 = serverRes.SHA256
		activeServerBuildID = serverRes.BuildID
		// Atomic upgrade: remove stale jar only after success and only if name changed.
		if prevFilename != "" && prevFilename != activeServerFilename {
			_ = os.Remove(filepath.Join(workDir, prevFilename))
		}
	}

	// Keep the panel's {{SERVER_JARFILE}} stable across Paper upgrades.
	jarAlias := jarlink.AliasFromEnv(os.Getenv)
	if linked, linkErr := jarlink.Sync(workDir, activeServerFilename, jarAlias); linkErr != nil {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: failed to link %s -> %s: %v", jarAlias, activeServerFilename, linkErr)))
	} else if linked != "" {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Server: Linked %s -> %s", linked, activeServerFilename)))
	}

	// 4. Reconcile Plugins (Added, Updated, Removed, Unchanged with SHA-256)
	installedPlugins := make(map[string]state.PluginState)
	modClient := resolver.NewModrinthClient("", "", nil)

	// A. Detect removed plugins (present in previous state but removed from tidy.toml)
	if previousState != nil {
		for oldName, oldP := range previousState.Plugins {
			if _, stillPresent := cfg.Plugins[oldName]; !stillPresent {
				oldSource := strings.ToLower(strings.TrimSpace(oldP.Source))
				oldPath := filepath.Join(workDir, "plugins", oldP.Filename)
				if oldSource == "local" && strings.TrimSpace(oldP.Path) != "" {
					if abs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: oldP.Path}, workDir); err == nil {
						oldPath = abs
					}
				}
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[-] Plugin [%s]: Removed from tidy.toml. Deleting %s...", oldName, oldP.Filename)))
				if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: failed to delete removed plugin %s: %v", oldPath, err)))
				}
			}
		}
	}

	// B. Reconcile active plugins in tidy.toml
	for pluginName, pCfg := range cfg.Plugins {
		source := strings.ToLower(strings.TrimSpace(pCfg.Source))
		var oldPlugin *state.PluginState
		var configChanged bool
		// "latest" plugins always resolve upstream below (even when config is
		// unchanged) to detect new releases; pins can skip via local SHA.
		isLatest := source == "modrinth" && pCfg.IsLatest()

		// Check if plugin is already in state and unchanged
		if previousState != nil {
			if oldP, exists := previousState.Plugins[pluginName]; exists && strings.ToLower(strings.TrimSpace(oldP.Source)) == source {
				cp := oldP
				oldPlugin = &cp
				switch source {
				case "modrinth":
					oldLoader := strings.ToLower(strings.TrimSpace(oldP.Loader))
					if oldLoader == "" {
						oldLoader = config.DefaultModrinthLoader
					}
					oldChannel := strings.ToLower(strings.TrimSpace(oldP.Channel))
					if oldChannel == "" {
						oldChannel = config.DefaultModrinthChannel
					}
					if oldP.Version != pCfg.NormalizedVersion() {
						configChanged = true
					} else if oldP.ProjectID != "" && oldP.ProjectID != strings.TrimSpace(pCfg.ProjectID) {
						// ProjectID changed with same version string (e.g. fork swap).
						configChanged = true
					} else if oldP.GameVersion != pCfg.NormalizedGameVersion() {
						configChanged = true
					} else if oldLoader != pCfg.NormalizedLoader() {
						configChanged = true
					} else if oldChannel != pCfg.NormalizedChannel() {
						configChanged = true
					} else if pin := strings.TrimSpace(pCfg.SHA256); pin != "" && !strings.EqualFold(oldP.SHA256, pin) {
						// Explicit SHA-256 pin changed.
						configChanged = true
					}
				case "url":
					// URL source has no version field: any URL or SHA change must trigger re-download.
					if oldP.URL != "" && oldP.URL != strings.TrimSpace(pCfg.URL) {
						configChanged = true
					} else if !strings.EqualFold(strings.TrimSpace(oldP.SHA256), strings.TrimSpace(pCfg.SHA256)) {
						configChanged = true
					}
				case "local":
					// Local source is manually uploaded: any path or SHA change must trigger re-verification.
					newPath := strings.TrimSpace(pCfg.Path)
					if oldP.Path != "" {
						if oldP.Path != newPath {
							configChanged = true
						} else if !strings.EqualFold(strings.TrimSpace(oldP.SHA256), strings.TrimSpace(pCfg.SHA256)) {
							configChanged = true
						}
					} else if oldP.SHA256 != "" {
						// Backward compat: pre-local states never carry source=local,
						// but guard anyway via filename + hash.
						if _, wantFile, err := resolver.ResolveLocalPath(pCfg, workDir); err != nil || oldP.Filename != wantFile {
							configChanged = true
						} else if !strings.EqualFold(strings.TrimSpace(oldP.SHA256), strings.TrimSpace(pCfg.SHA256)) {
							configChanged = true
						}
					} else {
						configChanged = true
					}
				}

				if !configChanged && !isLatest {
					localPath := filepath.Join(workDir, "plugins", oldP.Filename)
					if source == "local" && strings.TrimSpace(oldP.Path) != "" {
						if abs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: oldP.Path}, workDir); err == nil {
							localPath = abs
						}
					}
					// Check local file existence and SHA-256
					if oldP.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldP.SHA256) {
						fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Plugin [%s]: %s is up-to-date (SHA-256 verified, skipped download)", pluginName, oldP.Filename)))
						installedPlugins[pluginName] = oldP
						continue
					}
				}
				// NOTE: "latest" with unchanged config falls through to resolve
				// upstream and update only when a new release exists.
			}
		}

		switch source {
		case "modrinth":
			storeModrinth := func(res *resolver.PluginDownloadResult) {
				installedPlugins[pluginName] = state.PluginState{
					Source:          "modrinth",
					Filename:        res.Filename,
					Version:         pCfg.NormalizedVersion(),
					VersionID:       res.VersionID,
					ResolvedVersion: res.VersionNumber,
					GameVersion:     res.GameVersion,
					Loader:          res.Loader,
					Channel:         pCfg.NormalizedChannel(),
					ProjectID:       strings.TrimSpace(pCfg.ProjectID),
					HashAlgo:        res.HashAlgo,
					Hash:            res.Hash,
					SHA256:          res.SHA256,
				}
				if oldPlugin != nil && oldPlugin.Filename != "" && oldPlugin.Filename != res.Filename {
					_ = os.Remove(filepath.Join(workDir, "plugins", oldPlugin.Filename))
				}
				// The jar is swapped but the plugin's on-disk configs/data are
				// left untouched, so a breaking update can load stale files.
				// Surface that instead of failing silently or wiping data.
				if oldPlugin != nil && strings.TrimSpace(oldPlugin.ResolvedVersion) != "" &&
					strings.TrimSpace(res.VersionNumber) != "" &&
					oldPlugin.ResolvedVersion != res.VersionNumber {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Plugin [%s]: updated %s -> %s; existing configs were left untouched — review breaking changes",
						pluginName, oldPlugin.ResolvedVersion, res.VersionNumber)))
				}
			}

			// Fast path for "latest": resolve metadata first and skip the
			// download when the upstream version ID matches state and the
			// local jar still verifies.
			if isLatest && !configChanged && oldPlugin != nil && strings.TrimSpace(oldPlugin.VersionID) != "" {
				ver, file, resolveErr := modClient.Resolve(ctx, pCfg)
				if resolveErr != nil {
					fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to resolve plugin %q: %v", pluginName, resolveErr)))
					os.Exit(1)
				}
				if ver.ID == oldPlugin.VersionID {
					localPath := filepath.Join(workDir, "plugins", oldPlugin.Filename)
					if oldPlugin.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldPlugin.SHA256) {
						fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Plugin [%s]: %s is up-to-date (latest %s verified, skipped download)", pluginName, oldPlugin.Filename, ver.VersionNumber)))
						installedPlugins[pluginName] = *oldPlugin
						continue
					}
				}
				// A new release (or failed local hash) falls through silently;
				// the result prints below.
				res, err := modClient.Download(ctx, pluginName, pCfg, workDir, ver, file)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
					os.Exit(1)
				}
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Saved %s (%s, SHA256: %s, %d bytes)",
					pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))
				storeModrinth(res)
				continue
			}

			res, err := modClient.ResolveAndDownload(ctx, pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
				os.Exit(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Saved %s (%s, SHA256: %s, %d bytes)",
				pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))

			storeModrinth(res)

		case "url":
			res, err := resolver.ResolveAndDownloadURL(ctx, pluginName, pCfg, workDir, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
				os.Exit(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Saved %s (SHA256: %s, %d bytes)",
				pluginName, res.Filename, res.SHA256[:12]+"...", res.Size)))

			installedPlugins[pluginName] = state.PluginState{
				Source:   "url",
				Filename: res.Filename,
				URL:      strings.TrimSpace(pCfg.URL),
				HashAlgo: "sha256",
				Hash:     res.Hash,
				SHA256:   res.SHA256,
			}
			if oldPlugin != nil && oldPlugin.Filename != "" && oldPlugin.Filename != res.Filename {
				_ = os.Remove(filepath.Join(workDir, "plugins", oldPlugin.Filename))
			}
		case "local":
			res, err := resolver.VerifyLocalPlugin(pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to verify local plugin %q: %v", pluginName, err)))
				os.Exit(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Verified %s (SHA256: %s, %d bytes)",
				pluginName, res.Filename, res.SHA256[:12]+"...", res.Size)))

			installedPlugins[pluginName] = state.PluginState{
				Source:   "local",
				Filename: res.Filename,
				Path:     strings.TrimSpace(pCfg.Path),
				HashAlgo: "sha256",
				Hash:     res.Hash,
				SHA256:   res.SHA256,
			}
			if oldPlugin != nil && strings.ToLower(strings.TrimSpace(oldPlugin.Source)) == "local" {
				oldAbs := ""
				if strings.TrimSpace(oldPlugin.Path) != "" {
					if abs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: oldPlugin.Path}, workDir); err == nil {
						oldAbs = abs
					}
				} else if oldPlugin.Filename != "" {
					oldAbs = filepath.Join(workDir, "plugins", oldPlugin.Filename)
				}
				if oldAbs != "" && oldAbs != res.FilePath {
					_ = os.Remove(oldAbs)
				}
			}
		default:
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: plugin %q has unsupported source %q (supported: 'modrinth', 'url', 'local')", pluginName, pCfg.Source)))
			os.Exit(1)
		}
	}

	// 5. Reconcile Large Files & Worlds ([files.*] and [worlds.*]) with Mandatory SHA-256
	allFiles := cfg.GetAllFiles()
	installedFiles := make(map[string]state.FileState)
	if len(allFiles) > 0 {
		storageMgr := storage.NewManager(nil)
		for name, fCfg := range allFiles {
			syncRes, err := storageMgr.SyncFile(ctx, name, fCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to synchronize file/world %q: %v", name, err)))
				os.Exit(1)
			}

			if syncRes.Skipped {
				fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] World/File [%s]: %s (%s)", name, filepath.Base(syncRes.Path), syncRes.Reason)))
			} else {
				if syncRes.Extracted {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] World/File [%s]: Extracted %d files to %s (SHA-256 verified)",
						name, syncRes.Files, fCfg.Path)))
				} else {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] World/File [%s]: Saved to %s (SHA-256 verified)",
						name, fCfg.Path)))
				}
			}

			installedFiles[name] = state.FileState{
				Path:      fCfg.Path,
				URL:       fCfg.URL,
				SHA256:    syncRes.SHA256,
				SyncedAt:  time.Now().UTC(),
				Extracted: fCfg.Extract,
			}
		}
	}

	// 6. Template Variable Replacement (Mustache {{VAR_NAME}})
	// This run's substituted files are recorded in state so the next boot's
	// drift report can tell expected templating apart from external edits.
	var templatedFiles []string
	if !*skipTplFlag && !cfg.Templates.Disabled {
		fmt.Println(ui.Cyan("[*] Templates: Scanning config files for {{VAR_NAME}} variable replacements..."))
		tplResult, err := templating.ProcessDirectory(workDir, cfg.Templates.Paths)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: Template processing encountered an issue: %v", err)))
		} else {
			fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[+] Templates: Processed %d configs with %d variable replacements across %d modified files",
				tplResult.FilesProcessed, tplResult.Replacements, len(tplResult.ModifiedFiles))))
			for _, p := range tplResult.ModifiedFiles {
				templatedFiles = append(templatedFiles, filepath.ToSlash(p))
			}
		}
	}

	// 7. Save State Metadata
	installedAt := time.Now().UTC()
	if previousState != nil && !previousState.InstalledAt.IsZero() {
		installedAt = previousState.InstalledAt
	}
	// Preserve previous commit when this run did not sync git (skip-git / local-only).
	effectiveCommit := currentCommit
	if effectiveCommit == "" && previousState != nil {
		effectiveCommit = previousState.GitCommit
	}

	currentState := &state.State{
		InstalledAt:  installedAt,
		LastSyncedAt: time.Now().UTC(),
		GitCommit:    effectiveCommit,
		Server: state.ServerState{
			Project:  cfg.Server.Project,
			Version:  cfg.Server.Version,
			BuildID:  activeServerBuildID,
			Filename: activeServerFilename,
			SHA256:   activeServerSHA256,
		},
		Plugins:        installedPlugins,
		Files:          installedFiles,
		TemplatedFiles: templatedFiles,
	}

	if err := state.SaveState(workDir, currentState); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: Failed to save .tidy/state.json: %v", err)))
	}

	fmt.Println(ui.Bold("=================================================================="))
	fmt.Println(ui.Green("  [✓] Pre-flight orchestration completed successfully!"))
	fmt.Printf("  Container is ready for Java 25 startup: java -jar %s\n", jarlink.AliasFromEnv(os.Getenv))
	fmt.Println(ui.Bold("=================================================================="))
}
