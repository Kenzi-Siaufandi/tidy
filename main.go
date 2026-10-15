package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// driftReportDir holds the panel-viewable drift reports: one file per drift
// event (clean runs write nothing), never pruned, so old reports remain as
// history without cluttering .tidy itself.
// rejectStateTraversal rejects absolute paths, parent traversals and
// subdirectory separators so state-tracked filenames stay bare names.
func rejectStateTraversal(raw string) error {
	trimmed := raw
	if filepath.IsAbs(trimmed) {
		return fmt.Errorf("must not be absolute")
	}
	for _, part := range strings.Split(filepath.ToSlash(trimmed), "/") {
		if part == ".." {
			return fmt.Errorf("must not contain parent traversals")
		}
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return fmt.Errorf("must be a bare filename")
	}
	return nil
}

// stateWorkdirPath joins a state-tracked bare filename under workDir after
// sanitizing it, so a tampered .tidy/state.json can never drive deletions
// outside the workdir.
// stateWorkdirPath joins a state-tracked bare filename under workDir after
// sanitizing it, so a tampered .tidy/state.json can never drive deletions
// outside the workdir.
func stateWorkdirPath(workDir, raw string) (string, error) {
	if err := rejectStateTraversal(raw); err != nil {
		return "", fmt.Errorf("invalid state filename %q: %w", raw, err)
	}
	safe, err := resolver.SafeFilename(raw, "")
	if err != nil {
		return "", err
	}
	return filepath.Join(workDir, safe), nil
}

// statePluginPath joins a state-tracked plugin filename under plugins/ after
// sanitizing it.
// statePluginPath joins a state-tracked plugin filename under plugins/ after
// sanitizing it.
func statePluginPath(workDir, raw string) (string, error) {
	if err := rejectStateTraversal(raw); err != nil {
		return "", fmt.Errorf("invalid state filename %q: %w", raw, err)
	}
	safe, err := resolver.SafeFilename(raw, "")
	if err != nil {
		return "", err
	}
	return filepath.Join(workDir, "plugins", safe), nil
}

// removeStateWorkdirFile removes a state-tracked file under workDir after
// sanitizing. Invalid names are skipped with a warning instead of deleting
// arbitrary paths.
// removeStateWorkdirFile removes a state-tracked file under workDir after
// sanitizing. Invalid names are skipped with a warning instead of deleting
// arbitrary paths.
func removeStateWorkdirFile(workDir, raw string) {
	p, err := stateWorkdirPath(workDir, raw)
	if err != nil {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: skipping removal of suspicious state filename %q: %v", raw, err)))
		return
	}
	_ = os.Remove(p)
}

// removeStatePluginFile removes a state-tracked plugin jar after sanitizing.
// removeStatePluginFile removes a state-tracked plugin jar after sanitizing.
func removeStatePluginFile(workDir, raw string) {
	p, err := statePluginPath(workDir, raw)
	if err != nil {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: skipping removal of suspicious state filename %q: %v", raw, err)))
		return
	}
	_ = os.Remove(p)
}

// reportDrift snapshots working-tree drift versus the git remote into a
// dated .tidy/drift-*.txt report and warns on console. It runs before Pull's
// reset --hard, while the evidence still exists. Files substituted by the
// previous run's templating are expected-dirty and filtered out. A report is
// only written when drift exists, so clean boots leave no files behind and
// history stays bounded to real events (never pruned).
// Best-effort: never fails the boot.
// sameFileOnDisk reports whether two absolute paths resolve to the same file
func sameFileOnDisk(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil {
		// Fall back to case-insensitive name comparison when stats fail
		// (e.g. old was already replaced): Vault.jar vs vault.jar must not
		// trigger a delete of the new file.
		return strings.EqualFold(a, b)
	}
	return os.SameFile(ai, bi)
}

// safeRemoveOldFile removes oldAbs only when it is not the same file as
// newAbs (SameFile or case-only name match). Returns true when a delete was
// attempted.
// safeRemoveOldFile removes oldAbs only when it is not the same file as
// newAbs (SameFile or case-only name match). Returns true when a delete was
// attempted.
func safeRemoveOldFile(workDir, oldRaw, newAbs string, isPlugin bool) {
	if oldRaw == "" || newAbs == "" {
		return
	}
	var oldAbs string
	var err error
	if isPlugin {
		oldAbs, err = statePluginPath(workDir, oldRaw)
	} else {
		oldAbs, err = stateWorkdirPath(workDir, oldRaw)
	}
	if err != nil {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: skipping removal of suspicious state filename %q: %v", oldRaw, err)))
		return
	}
	if oldAbs == newAbs || sameFileOnDisk(oldAbs, newAbs) {
		return
	}
	// Extra guard for case-only renames on case-insensitive filesystems:
	// Vault.jar -> vault.jar would otherwise delete the just-downloaded jar.
	if strings.EqualFold(oldAbs, newAbs) {
		return
	}
	if isPlugin {
		removeStatePluginFile(workDir, oldRaw)
	} else {
		removeStateWorkdirFile(workDir, oldRaw)
	}
}

// activePluginFilename returns a best-effort filename for an active plugin
// config (local source only; remote sources resolve filenames at download
// time so "" is returned). Used to avoid deleting a file still referenced
// after a key rename.
// activePluginFilename returns a best-effort filename for an active plugin
// config (local source only; remote sources resolve filenames at download
// time so "" is returned). Used to avoid deleting a file still referenced
// after a key rename.
func activePluginFilename(pCfg config.PluginConfig, workDir string) string {
	if strings.ToLower(strings.TrimSpace(pCfg.Source)) != "local" {
		return ""
	}
	if _, fn, err := resolver.ResolveLocalPath(pCfg, workDir); err == nil {
		return fn
	}
	return ""
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if abs, err := filepath.Abs(setupWorkdir(os.Args[2:])); err == nil {
			progress.ForceLive = isCharDevice(os.Stderr)
			startRunLog(abs, time.Now())
		}
		exitWithLog(setup.Run(os.Args[2:]))
	}
	var (
		configFlag     = flag.String("config", "tidy.toml", "Path to tidy.toml configuration file")
		workDirFlag    = flag.String("workdir", ".", "Working directory for Minecraft server root")
		gitRepoFlag    = flag.String("git-repo", "", "Git repository URL to synchronize declarative configs (or env GIT_REPO)")
		gitBranchFlag  = flag.String("git-branch", "", "Git branch to synchronize (or env GIT_BRANCH, default 'main')")
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

	workDir, err := filepath.Abs(*workDirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: failed to determine absolute path for workdir: %v", err)))
		exitWithLog(1)
	}

	// --version must not create .tidy/log/ entries (entrypoint runs
	// `tidy --version` on every boot; logging it would litter logs and
	// fill disk/inodes in crash loops).
	if *versionFlag {
		fmt.Printf("Tidy v%s\n", Version)
		os.Exit(0)
	}

	// Log every run under .tidy/log/ from here on (banner included).
	progress.ForceLive = isCharDevice(os.Stderr)
	startRunLog(workDir, time.Now())

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

	var gitToken string
	for _, envKey := range []string{"GIT_TOKEN", "GIT_AUTH_TOKEN", "GITHUB_TOKEN"} {
		if val := os.Getenv(envKey); strings.TrimSpace(val) != "" {
			gitToken = strings.TrimSpace(val)
			break
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
			exitWithLog(1)
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
				exitWithLog(1)
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
				exitWithLog(1)
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
		exitWithLog(1)
	}

	fmt.Printf("%s\n", ui.Cyan(fmt.Sprintf("[*] Config: Loading %s...", filepath.Base(configFile))))
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to load config: %v", err)))
		exitWithLog(1)
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
		// Sanitize the state-tracked filename so tampered state cannot probe outside files.
		localJar, jarErr := stateWorkdirPath(workDir, previousState.Server.Filename)
		if jarErr != nil {
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: suspicious server filename in state %q: %v", previousState.Server.Filename, jarErr)))
			localJar = ""
		}
		if localJar != "" && state.VerifyLocalSHA256(localJar, previousState.Server.SHA256) {
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
				// BuildID 0 means unknown (e.g. legacy state): never treat as
				// matching a "0" pin, force re-resolution instead.
				if previousState.Server.BuildID != 0 && fmt.Sprintf("%d", previousState.Server.BuildID) == requestedBuild {
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
			exitWithLog(1)
		}
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Server: Downloaded %s (Build #%d, SHA256: %s, %d bytes)",
			serverRes.Filename, serverRes.BuildID, serverRes.SHA256[:12]+"...", serverRes.Size)))
		activeServerFilename = serverRes.Filename
		activeServerSHA256 = serverRes.SHA256
		activeServerBuildID = serverRes.BuildID
		// Atomic upgrade: remove stale jar only after success and only if name changed.
		// Case-only renames (Vault.jar -> vault.jar) on case-insensitive
		// filesystems overwrote in place via Rename: never delete the new jar.
		if prevFilename != "" && prevFilename != activeServerFilename {
			newPath := filepath.Join(workDir, activeServerFilename)
			oldPath, olderr := stateWorkdirPath(workDir, prevFilename)
			if olderr != nil {
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: skipping removal of suspicious state filename %q: %v", prevFilename, olderr)))
			} else if !sameFileOnDisk(oldPath, newPath) && !strings.EqualFold(oldPath, newPath) {
				removeStateWorkdirFile(workDir, prevFilename)
			}
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
	// GitHub release downloads reuse the same PAT as git sync
	// GitHub downloads reuse the git PAT from env.
	ghClient := resolver.NewGitHubClient("", gitToken, nil)

	// A. Detect removed plugins (present in previous state but removed from tidy.toml)
	// source="local" jars are manually uploaded: never delete them from disk.
	// Renames (Foo -> Bar with same path) must not delete the file Step 4B
	// still needs to verify.
	if previousState != nil {
		activeLocalPaths := map[string]bool{}
		activeFilenames := map[string]bool{}
		for _, pCfg := range cfg.Plugins {
			if strings.ToLower(strings.TrimSpace(pCfg.Source)) == "local" && strings.TrimSpace(pCfg.Path) != "" {
				if abs, _, err := resolver.ResolveLocalPath(pCfg, workDir); err == nil {
					activeLocalPaths[abs] = true
				}
			}
			if fn := activePluginFilename(pCfg, workDir); fn != "" {
				activeFilenames[fn] = true
			}
		}
		for oldName, oldP := range previousState.Plugins {
			if _, stillPresent := cfg.Plugins[oldName]; !stillPresent {
				oldSource := strings.ToLower(strings.TrimSpace(oldP.Source))
				if oldSource == "local" {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[-] Plugin [%s]: Removed from tidy.toml. Keeping manually-uploaded local file %s (never auto-deleted)...", oldName, oldP.Filename)))
					continue
				}
				oldPath, pathErr := statePluginPath(workDir, oldP.Filename)
				if pathErr != nil {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Warning: skipping removal of suspicious state filename %q: %v", oldP.Filename, pathErr)))
					continue
				}
				// Same file still referenced by an active plugin (e.g. rename
				// Foo -> Bar keeping path): never delete out from under Step 4B.
				if activeFilenames[oldP.Filename] {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[-] Plugin [%s]: Removed from tidy.toml but %s is still referenced by active config. Keeping...", oldName, oldP.Filename)))
					continue
				}
				if abs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: oldP.Filename}, workDir); err == nil && activeLocalPaths[abs] {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[-] Plugin [%s]: Removed from tidy.toml but path still referenced. Keeping %s...", oldName, oldP.Filename)))
					continue
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
		isLatest := (source == "modrinth" && pCfg.IsLatest()) ||
			(source == "github" && pCfg.IsGitHubLatest())

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
				case "github":
					// GitHub source: any repo, tag selector, asset or SHA change must trigger re-download.
					if oldP.Repo != "" && oldP.Repo != strings.TrimSpace(pCfg.Repo) {
						configChanged = true
					} else if oldP.Tag != pCfg.NormalizedTag() {
						configChanged = true
					} else if oldP.Asset != "" && oldP.Asset != strings.TrimSpace(pCfg.Asset) {
						// Asset changed (e.g. renamed jar between releases).
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
					localPath, pathErr := statePluginPath(workDir, oldP.Filename)
					if pathErr != nil {
						// Suspicious state filename: force re-download/verify.
						localPath = ""
					} else if source == "local" && strings.TrimSpace(oldP.Path) != "" {
						if abs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: oldP.Path}, workDir); err == nil {
							localPath = abs
						} else {
							localPath = ""
						}
					}
					// Check local file existence and SHA-256
					if localPath != "" && oldP.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldP.SHA256) {
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
					safeRemoveOldFile(workDir, oldPlugin.Filename, res.FilePath, true)
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
					exitWithLog(1)
				}
				if ver.ID == oldPlugin.VersionID {
					localPath, pathErr := statePluginPath(workDir, oldPlugin.Filename)
					if pathErr == nil && oldPlugin.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldPlugin.SHA256) {
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
					exitWithLog(1)
				}
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Downloaded %s (%s, SHA256: %s, %d bytes)",
					pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))
				storeModrinth(res)
				continue
			}

			res, err := modClient.ResolveAndDownload(ctx, pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
				exitWithLog(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Downloaded %s (%s, SHA256: %s, %d bytes)",
				pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))

			storeModrinth(res)

		case "url":
			res, err := resolver.ResolveAndDownloadURL(ctx, pluginName, pCfg, workDir, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
				exitWithLog(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Downloaded %s (SHA256: %s, %d bytes)",
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
				safeRemoveOldFile(workDir, oldPlugin.Filename, res.FilePath, true)
			}
		case "github":
			storeGitHub := func(res *resolver.PluginDownloadResult) {
				installedPlugins[pluginName] = state.PluginState{
					Source:          "github",
					Filename:        res.Filename,
					Version:         pCfg.NormalizedTag(),
					VersionID:       res.VersionID,
					ResolvedVersion: res.VersionNumber,
					Repo:            strings.TrimSpace(pCfg.Repo),
					Tag:             pCfg.NormalizedTag(),
					Asset:           strings.TrimSpace(pCfg.Asset),
					HashAlgo:        res.HashAlgo,
					Hash:            res.Hash,
					SHA256:          res.SHA256,
				}
				if oldPlugin != nil && oldPlugin.Filename != "" && oldPlugin.Filename != res.Filename {
					safeRemoveOldFile(workDir, oldPlugin.Filename, res.FilePath, true)
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

			// Fast path for tag="latest": resolve metadata first and skip the
			// download when the upstream release ID matches state and the
			// local jar still verifies.
			if isLatest && !configChanged && oldPlugin != nil && strings.TrimSpace(oldPlugin.VersionID) != "" {
				rel, asset, resolveErr := ghClient.Resolve(ctx, pCfg)
				if resolveErr != nil {
					fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to resolve plugin %q: %v", pluginName, resolveErr)))
					exitWithLog(1)
				}
				if strconv.FormatInt(rel.ID, 10) == oldPlugin.VersionID {
					localPath, pathErr := statePluginPath(workDir, oldPlugin.Filename)
					if pathErr == nil && oldPlugin.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldPlugin.SHA256) {
						fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] Plugin [%s]: %s is up-to-date (latest %s verified, skipped download)", pluginName, oldPlugin.Filename, rel.TagName)))
						installedPlugins[pluginName] = *oldPlugin
						continue
					}
				}
				// A new release (or failed local hash) falls through silently;
				// the result prints below.
				res, err := ghClient.Download(ctx, pluginName, pCfg, workDir, rel, asset)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
					exitWithLog(1)
				}
				fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Downloaded %s (%s, SHA256: %s, %d bytes)",
					pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))
				storeGitHub(res)
				continue
			}

			res, err := ghClient.ResolveAndDownload(ctx, pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to download plugin %q: %v", pluginName, err)))
				exitWithLog(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Downloaded %s (%s, SHA256: %s, %d bytes)",
				pluginName, res.Filename, res.VersionNumber, res.SHA256[:12]+"...", res.Size)))

			storeGitHub(res)
		case "local":
			res, err := resolver.VerifyLocalPlugin(pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to verify local plugin %q: %v", pluginName, err)))
				exitWithLog(1)
			}
			fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: Verified local %s (SHA256: %s, %d bytes, no download)",
				pluginName, res.Filename, res.SHA256[:12]+"...", res.Size)))

			installedPlugins[pluginName] = state.PluginState{
				Source:   "local",
				Filename: res.Filename,
				Path:     strings.TrimSpace(pCfg.Path),
				HashAlgo: "sha256",
				Hash:     res.Hash,
				SHA256:   res.SHA256,
			}
			// Local jars are manually uploaded: never auto-delete the old
				// path on renames. Step 4A already keeps them; deleting here
				// would permanently brick source="local" plugins.
				if oldPlugin != nil && strings.ToLower(strings.TrimSpace(oldPlugin.Source)) == "local" {
					if oldAbs, _, err := resolver.ResolveLocalPath(config.PluginConfig{Path: strings.TrimSpace(oldPlugin.Path)}, workDir); err == nil && oldAbs != res.FilePath {
						if _, statErr := os.Stat(oldAbs); statErr == nil {
							fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] Plugin [%s]: previous local file kept at %s (manual cleanup if unneeded)", pluginName, oldPlugin.Path)))
						}
					}
			}
		default:
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: plugin %q has unsupported source %q (supported: 'modrinth', 'url', 'github', 'local')", pluginName, pCfg.Source)))
			exitWithLog(1)
		}
	}

	// 5. Reconcile Large Files & Worlds ([files.*] and [worlds.*]) with Mandatory SHA-256
	allFiles := cfg.GetAllFiles()
	installedFiles := make(map[string]state.FileState)
	if len(allFiles) > 0 {
		storageMgr := storage.NewManager(nil)
		for name, fCfg := range allFiles {
			var prevFile *state.FileState
			if previousState != nil {
				if pf, ok := previousState.Files[name]; ok {
					cp := pf
					prevFile = &cp
				}
			}
			syncRes, err := storageMgr.SyncFileWithState(ctx, name, fCfg, workDir, prevFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: Failed to synchronize file/world %q: %v", name, err)))
				exitWithLog(1)
			}

			if syncRes.Skipped {
				fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[=] World/File [%s]: %s (%s)", name, filepath.Base(syncRes.Path), syncRes.Reason)))
			} else {
				if syncRes.Extracted {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] World/File [%s]: Downloaded + extracted %d files to %s (SHA-256 verified)",
						name, syncRes.Files, fCfg.Path)))
				} else {
					fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[+] World/File [%s]: Downloaded to %s (SHA-256 verified)",
						name, fCfg.Path)))
				}
			}

			effectiveSHA := syncRes.SHA256
			if effectiveSHA == "" && prevFile != nil {
				effectiveSHA = prevFile.SHA256
			}
			installedFiles[name] = state.FileState{
				Path:      fCfg.Path,
				URL:       fCfg.URL,
				SHA256:    effectiveSHA,
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
	finishRunLog()
}
