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
	"github.com/Kenzi-Siaufandi/tidy/internal/templating"
)

const Version = "0.1.0"

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
		fmt.Printf("Tidy v%s - Pterodactyl Pre-Flight Orchestrator\n", Version)
		os.Exit(0)
	}

	workDir, err := filepath.Abs(*workDirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Fatal: failed to determine absolute path for workdir: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("==================================================================")
	fmt.Printf("  Tidy v%s - Minecraft Pre-Flight Orchestrator (Java 25 Target)\n", Version)
	fmt.Println("==================================================================")

	firstInstall := state.IsFirstInstall(workDir)
	if firstInstall {
		fmt.Println("[*] Status: First install detected in container.")
	} else {
		fmt.Println("[*] Status: Existing installation found.")
	}

	// 1. Git Synchronization
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

	var commitSHA string
	if !*skipGitFlag && gitRepo != "" {
		fmt.Printf("[*] Git: Synchronizing configs from %s (branch: %s)...\n", git.MaskURL(gitRepo), gitBranch)
		gitClient, err := git.NewClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[!] Fatal: %v\n", err)
			os.Exit(1)
		}

		commit, err := gitClient.Sync(git.Options{
			RepoURL:   gitRepo,
			Branch:    gitBranch,
			Token:     gitToken,
			Username:  gitUser,
			TargetDir: workDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[!] Fatal: Git sync failed: %v\n", err)
			os.Exit(1)
		}
		commitSHA = commit
		fmt.Printf("[+] Git: Synced successfully (HEAD: %s)\n", commitSHA)
	} else if gitRepo == "" {
		fmt.Println("[*] Git: No GIT_REPO specified; using local directory configs.")
	}

	// 2. Read tidy.toml
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

	// 3. Resolve and Download Server Software (PaperMC Fill API)
	fmt.Printf("[*] Server: Resolving %s %s (PaperMC Fill API)...\n", cfg.Server.Project, cfg.Server.Version)
	paperClient := resolver.NewPaperClient("", nil)
	serverRes, err := paperClient.ResolveAndDownload(ctx, cfg.Server, workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to resolve/download server software: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[+] Server: Downloaded %s (Build #%d, SHA256: %s, %d bytes)\n",
		serverRes.Filename, serverRes.BuildID, serverRes.SHA256[:12]+"...", serverRes.Size)

	// 4. Resolve and Download Plugins
	installedPlugins := make(map[string]state.PluginState)
	modClient := resolver.NewModrinthClient("", "", nil)

	for pluginName, pCfg := range cfg.Plugins {
		source := strings.ToLower(pCfg.Source)
		switch source {
		case "modrinth":
			fmt.Printf("[*] Plugin [%s]: Resolving from Modrinth (%s v%s)...\n", pluginName, pCfg.ProjectID, pCfg.Version)
			res, err := modClient.ResolveAndDownload(ctx, pluginName, pCfg, workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to download plugin %q: %v\n", pluginName, err)
				os.Exit(1)
			}
			fmt.Printf("[+] Plugin [%s]: Saved %s (%s: %s, %d bytes)\n",
				pluginName, res.Filename, strings.ToUpper(res.HashAlgo), res.Hash[:12]+"...", res.Size)

			installedPlugins[pluginName] = state.PluginState{
				Source:   "modrinth",
				Filename: res.Filename,
				Version:  pCfg.Version,
				HashAlgo: res.HashAlgo,
				Hash:     res.Hash,
			}

		case "url":
			fmt.Printf("[*] Plugin [%s]: Downloading from direct URL...\n", pluginName)
			res, err := resolver.ResolveAndDownloadURL(ctx, pluginName, pCfg, workDir, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[!] Fatal: Failed to download plugin %q: %v\n", pluginName, err)
				os.Exit(1)
			}
			fmt.Printf("[+] Plugin [%s]: Saved %s (SHA256: %s, %d bytes)\n",
				pluginName, res.Filename, res.Hash[:12]+"...", res.Size)

			installedPlugins[pluginName] = state.PluginState{
				Source:   "url",
				Filename: res.Filename,
				HashAlgo: "sha256",
				Hash:     res.Hash,
			}
		}
	}

	// 5. Template Variable Replacement (Mustache {{VAR_NAME}})
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

	// 6. Save State Metadata
	currentState := &state.State{
		InstalledAt:  time.Now().UTC(),
		LastSyncedAt: time.Now().UTC(),
		GitCommit:    commitSHA,
		Server: state.ServerState{
			Project:  serverRes.Project,
			Version:  serverRes.Version,
			BuildID:  serverRes.BuildID,
			Filename: serverRes.Filename,
			SHA256:   serverRes.SHA256,
		},
		Plugins: installedPlugins,
	}

	if err := state.SaveState(workDir, currentState); err != nil {
		fmt.Fprintf(os.Stderr, "[!] Warning: Failed to save .tidy/state.json: %v\n", err)
	} else {
		fmt.Println("[+] State: Saved installation state to .tidy/state.json")
	}

	fmt.Println("==================================================================")
	fmt.Println("  [✓] Pre-flight orchestration completed successfully!")
	fmt.Printf("  Container is ready for Java 25 startup: java -jar %s\n", serverRes.Filename)
	fmt.Println("==================================================================")
}
