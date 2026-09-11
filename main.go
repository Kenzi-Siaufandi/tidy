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
	"github.com/Kenzi-Siaufandi/tidy/internal/resolver"
	"github.com/Kenzi-Siaufandi/tidy/internal/state"
	"github.com/Kenzi-Siaufandi/tidy/internal/storage"
	"github.com/Kenzi-Siaufandi/tidy/internal/templating"
	"github.com/Kenzi-Siaufandi/tidy/internal/version"
)

const Version = version.Version

func main() {
	var (
		configFlag    = flag.String("config", "tidy.toml", "Path to tidy.toml configuration file")
		workDirFlag   = flag.String("workdir", ".", "Working directory for Minecraft server root")
		gitRepoFlag   = flag.String("git-repo", "", "Git repository URL to synchronize declarative configs (or env GIT_REPO)")
		gitBranchFlag = flag.String("git-branch", "", "Git branch to synchronize (or env GIT_BRANCH, default 'main')")
		gitTokenFlag  = flag.String("git-token", "", "Git authentication token (or env GIT_TOKEN / GITHUB_TOKEN)")
		gitUserFlag   = flag.String("git-user", "", "Git username (or env GIT_USER)")
		skipGitFlag   = flag.Bool("skip-git", false, "Skip Git synchronization")
		skipTplFlag   = flag.Bool("skip-templates", false, "Skip Mustache template variable replacement")
		versionFlag   = flag.Bool("version", false, "Print Tidy version and exit")
	)

	flag.Parse()

	if *versionFlag {
		fmt.Printf("Tidy v%s\n", Version)
		os.Exit(0)
	}

	workDir, err := filepath.Abs(*workDirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Fatal: failed to determine absolute path for workdir: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("==================================================================")
	fmt.Printf("  Tidy v%s \n", Version)
	fmt.Println("==================================================================")

	firstInstall := state.IsFirstInstall(workDir)
	var previousState *state.State
	if firstInstall {
		fmt.Println("[*] Status: First install detected in container.")
	} else {
		fmt.Println("[*] Status: Existing installation found. Performing incremental reconciliation.")
		previousState, err = state.LoadState(workDir)
		if err != nil {
			fmt.Printf("[!] Warning: failed to load existing state file: %v. Proceeding as fresh sync.\n", err)
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
			fmt.Fprintf(os.Stderr, "[!] Fatal: %v\n", err)
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
			fmt.Printf("[*] Git: Initializing clone from %s (branch: %s)...\n", git.MaskURL(gitRepo), gitBranch)
			if err := gitClient.Clone(ctx, gitOpts); err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Git clone failed: %v\n", err)
				os.Exit(1)
			}
			headCommit, headErr := gitClient.GetHeadCommit(ctx, workDir)
			if headErr != nil {
				fmt.Fprintf(os.Stderr, "[!] Warning: failed to get HEAD commit after clone: %v\n", headErr)
				currentCommit = ""
			} else {
				currentCommit = headCommit
			}
			fmt.Printf("[+] Git: Initial clone complete (HEAD: %s)\n", git.ShortSHA(currentCommit))
		} else {
			fmt.Printf("[*] Git: Checking for upstream updates from %s (branch: %s)...\n", git.MaskURL(gitRepo), gitBranch)
			pullRes, err := gitClient.Pull(ctx, gitOpts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Git pull failed: %v\n", err)
				os.Exit(1)
			}
			currentCommit = pullRes.NewCommit

			if pullRes.HasUpdates {
				fmt.Printf("[+] Git: Pulled %d new commits (%s..%s):\n",
					len(pullRes.Commits), git.ShortSHA(pullRes.OldCommit), git.ShortSHA(pullRes.NewCommit))
				for _, cMsg := range pullRes.Commits {
					fmt.Printf("    - %s\n", cMsg)
				}
				fmt.Printf("[*] Git: Modified files:\n")
				for _, ch := range pullRes.ChangedFiles {
					fmt.Printf("    [%s] %s\n", ch.Status, ch.Path)
				}
			} else {
				fmt.Printf("[=] Git: Repository is up to date (commit %s)\n", git.ShortSHA(currentCommit))
			}
		}
	} else if gitRepo == "" {
		fmt.Println("[*] Git: No GIT_REPO specified; using local directory configs.")
	}

	// 2. Read and Validate tidy.toml
	configFile := *configFlag
	if !filepath.IsAbs(configFile) {
		configFile = filepath.Join(workDir, configFile)
	}

	if _, err := os.Stat(configFile); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Fatal: Configuration file %q not found: %v\n", configFile, err)
		os.Exit(1)
	}

	fmt.Printf("[*] Config: Loading %s...\n", filepath.Base(configFile))
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to load config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 3. Reconcile Server Software (PaperMC Fill API with SHA-256)
	var activeServerFilename string
	var activeServerSHA256 string
	var activeServerBuildID int

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
				paperCheck := resolver.NewPaperClient("", nil)
				upstreamResp, _, fetchErr := paperCheck.FetchBuild(ctx, cfg.Server)
				if fetchErr != nil {
					fmt.Printf("[!] Warning: failed to check upstream build for updates, keeping local %s: %v\n", previousState.Server.Filename, fetchErr)
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				} else if upstreamResp.ID == previousState.Server.BuildID && previousState.Server.BuildID != 0 {
					fmt.Printf("[=] Server: %s is up-to-date (build #%d, SHA-256 verified, skipped download)\n", previousState.Server.Filename, previousState.Server.BuildID)
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				} else {
					fmt.Printf("[*] Server: New upstream build #%d available (local #%d). Updating...\n", upstreamResp.ID, previousState.Server.BuildID)
				}
			} else {
				// Pinned build: skip only if stored build ID matches request.
				if fmt.Sprintf("%d", previousState.Server.BuildID) == requestedBuild {
					fmt.Printf("[=] Server: %s is up-to-date (SHA-256 verified, skipped download)\n", previousState.Server.Filename)
					activeServerFilename = previousState.Server.Filename
					activeServerSHA256 = previousState.Server.SHA256
					activeServerBuildID = previousState.Server.BuildID
					serverNeedsDownload = false
				} else {
					fmt.Printf("[*] Server: Build changed (%d -> %s). Updating...\n", previousState.Server.BuildID, requestedBuild)
				}
			}
		} else {
			fmt.Printf("[*] Server: %s is missing or checksum changed. Re-downloading...\n", previousState.Server.Filename)
		}
	} else if previousState != nil && (previousState.Server.Project != cfg.Server.Project || previousState.Server.Version != cfg.Server.Version) {
		fmt.Printf("[*] Server: Version changed from %s %s to %s %s. Upgrading...\n",
			previousState.Server.Project, previousState.Server.Version, cfg.Server.Project, cfg.Server.Version)
		// Old jar is removed only after the new download succeeds (see below).
	}

	if serverNeedsDownload {
		fmt.Printf("[*] Server: Resolving %s %s (PaperMC Fill API)...\n", cfg.Server.Project, cfg.Server.Version)
		paperClient := resolver.NewPaperClient("", nil)
		prevFilename := ""
		if previousState != nil {
			prevFilename = previousState.Server.Filename
		}
		serverRes, err := paperClient.ResolveAndDownload(ctx, cfg.Server, workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to resolve/download server software: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[+] Server: Downloaded %s (Build #%d, SHA256: %s, %d bytes)\n",
			serverRes.Filename, serverRes.BuildID, serverRes.SHA256[:12]+"...", serverRes.Size)
		activeServerFilename = serverRes.Filename
		activeServerSHA256 = serverRes.SHA256
		activeServerBuildID = serverRes.BuildID
		// Atomic upgrade: remove stale jar only after success and only if name changed.
		if prevFilename != "" && prevFilename != activeServerFilename {
			_ = os.Remove(filepath.Join(workDir, prevFilename))
		}
	}

	// 4. Reconcile Plugins (Added, Updated, Removed, Unchanged with SHA-256)
	installedPlugins := make(map[string]state.PluginState)
	modClient := resolver.NewModrinthClient("", "", nil)

	// A. Detect removed plugins (present in previous state but removed from tidy.toml)
	if previousState != nil {
		for oldName, oldP := range previousState.Plugins {
			if _, stillPresent := cfg.Plugins[oldName]; !stillPresent {
				fmt.Printf("[-] Plugin [%s]: Removed from tidy.toml. Deleting %s...\n", oldName, oldP.Filename)
				oldPath := filepath.Join(workDir, "plugins", oldP.Filename)
				if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
					fmt.Printf("[!] Warning: failed to delete removed plugin %s: %v\n", oldPath, err)
				}
			}
		}
	}

	// B. Reconcile active plugins in tidy.toml
	for pluginName, pCfg := range cfg.Plugins {
		source := strings.ToLower(strings.TrimSpace(pCfg.Source))
		var oldPlugin *state.PluginState
		var configChanged bool

		// Check if plugin is already in state and unchanged
		if previousState != nil {
			if oldP, exists := previousState.Plugins[pluginName]; exists && strings.ToLower(strings.TrimSpace(oldP.Source)) == source {
				cp := oldP
				oldPlugin = &cp
				switch source {
				case "modrinth":
					if oldP.Version != pCfg.Version {
						configChanged = true
					} else if oldP.ProjectID != "" && oldP.ProjectID != pCfg.ProjectID {
						// ProjectID changed with same version string (e.g. fork swap).
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
				}

				if !configChanged {
					localPath := filepath.Join(workDir, "plugins", oldP.Filename)
					// Check local file existence and SHA-256
					if oldP.SHA256 != "" && state.VerifyLocalSHA256(localPath, oldP.SHA256) {
						fmt.Printf("[=] Plugin [%s]: %s is up-to-date (SHA-256 verified, skipped download)\n", pluginName, oldP.Filename)
						installedPlugins[pluginName] = oldP
						continue
					}
				} else {
					fmt.Printf("[*] Plugin [%s]: Configuration changed, re-downloading (keeping old jar until success)...\n", pluginName)
				}
			}
		}

		switch source {
		case "modrinth":
			fmt.Printf("[*] Plugin [%s]: Resolving from Modrinth (%s v%s)...\n", pluginName, pCfg.ProjectID, pCfg.Version)
			res, err := modClient.ResolveAndDownload(ctx, pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to download plugin %q: %v\n", pluginName, err)
				os.Exit(1)
			}
			fmt.Printf("[+] Plugin [%s]: Saved %s (SHA256: %s, %d bytes)\n",
				pluginName, res.Filename, res.SHA256[:12]+"...", res.Size)

			installedPlugins[pluginName] = state.PluginState{
				Source:    "modrinth",
				Filename:  res.Filename,
				Version:   pCfg.Version,
				ProjectID: pCfg.ProjectID,
				HashAlgo:  res.HashAlgo,
				Hash:      res.Hash,
				SHA256:    res.SHA256,
			}
			if oldPlugin != nil && oldPlugin.Filename != "" && oldPlugin.Filename != res.Filename {
				_ = os.Remove(filepath.Join(workDir, "plugins", oldPlugin.Filename))
			}

		case "url":
			fmt.Printf("[*] Plugin [%s]: Downloading from direct URL with mandatory SHA-256...\n", pluginName)
			res, err := resolver.ResolveAndDownloadURL(ctx, pluginName, pCfg, workDir, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to download plugin %q: %v\n", pluginName, err)
				os.Exit(1)
			}
			fmt.Printf("[+] Plugin [%s]: Saved %s (SHA256: %s, %d bytes)\n",
				pluginName, res.Filename, res.SHA256[:12]+"...", res.Size)

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
		default:
			fmt.Fprintf(os.Stderr, "[!] Fatal: plugin %q has unsupported source %q (supported: 'modrinth', 'url')\n", pluginName, pCfg.Source)
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
				fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to synchronize file/world %q: %v\n", name, err)
				os.Exit(1)
			}

			if syncRes.Skipped {
				fmt.Printf("[=] World/File [%s]: %s (%s)\n", name, filepath.Base(syncRes.Path), syncRes.Reason)
			} else {
				if syncRes.Extracted {
					fmt.Printf("[+] World/File [%s]: Extracted %d files to %s (SHA-256 verified)\n",
						name, syncRes.Files, fCfg.Path)
				} else {
					fmt.Printf("[+] World/File [%s]: Saved to %s (SHA-256 verified)\n",
						name, fCfg.Path)
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
	if !*skipTplFlag && !cfg.Templates.Disabled {
		fmt.Println("[*] Templates: Scanning config files for {{VAR_NAME}} variable replacements...")
		tplResult, err := templating.ProcessDirectory(workDir, cfg.Templates.Paths)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[!] Warning: Template processing encountered an issue: %v\n", err)
		} else {
			fmt.Printf("[+] Templates: Processed %d configs with %d variable replacements across %d modified files\n",
				tplResult.FilesProcessed, tplResult.Replacements, len(tplResult.ModifiedFiles))
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
		Plugins: installedPlugins,
		Files:   installedFiles,
	}

	if err := state.SaveState(workDir, currentState); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: Failed to save .tidy/state.json: %v\n", err)
	} else {
		fmt.Println("[+] State: Saved updated state to .tidy/state.json")
	}

	fmt.Println("==================================================================")
	fmt.Println("  [✓] Pre-flight orchestration completed successfully!")
	fmt.Printf("  Container is ready for Java 25 startup: java -jar %s\n", activeServerFilename)
	fmt.Println("==================================================================")
}
