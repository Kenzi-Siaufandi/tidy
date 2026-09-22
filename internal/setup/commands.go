package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
)

// CmdInit initializes git and writes recommended ignore/attributes files.
func CmdInit(root string, force bool) int {
	fmt.Printf("%s %s\n", ui.Bold(ui.Cyan("Initializing tidy setup repository at:")), root)

	if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		if code, _, errOut := runGit(root, "init"); code != 0 {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("  [x] Git init failed: %s", firstLine(errOut))))
			return 1
		}
		fmt.Printf("  %s\n", ui.Green("[+] Initialized new Git repository"))
	} else {
		fmt.Printf("  %s\n", ui.Gray("(*) Existing Git repository found"))
	}

	gitignorePath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) || force {
		if err := os.WriteFile(gitignorePath, []byte(DefaultGitignore), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("  [x] Failed to write .gitignore: %v", err)))
			return 1
		}
		fmt.Printf("  %s\n", ui.Green("[+] Created optimized .gitignore"))
	} else {
		fmt.Printf("  %s\n", ui.Gray("(*) .gitignore already exists (use --force to overwrite)"))
	}

	attrsPath := filepath.Join(root, ".gitattributes")
	if _, err := os.Stat(attrsPath); os.IsNotExist(err) || force {
		if err := os.WriteFile(attrsPath, []byte(DefaultGitattributes), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("  [x] Failed to write .gitattributes: %v", err)))
			return 1
		}
		fmt.Printf("  %s\n", ui.Green("[+] Created .gitattributes with LF normalization"))
	} else {
		fmt.Printf("  %s\n", ui.Gray("(*) .gitattributes already exists"))
	}

	fmt.Printf("\n%s Run %s to inspect detectable configs.\n", ui.Green(ui.Bold("Done!")), ui.Cyan("tidy setup status"))
	return 0
}

// CmdStatus scans configs and prints git/ignored summary.
func CmdStatus(root string) int {
	res, err := ScanRepository(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Fatal: scan failed: %v", err)))
		return 1
	}
	gitMap := GitStatusMap(root)
	inGit := IsGitRepo(root)

	var totalSize int64
	for _, f := range res.RootConfigs {
		if info, err := os.Stat(f); err == nil {
			totalSize += info.Size()
		}
	}
	for _, files := range res.PluginConfigs {
		for _, f := range files {
			if info, err := os.Stat(f); err == nil {
				totalSize += info.Size()
			}
		}
	}
	totalConfigs := len(res.RootConfigs)
	for _, files := range res.PluginConfigs {
		totalConfigs += len(files)
	}

	fmt.Println(ui.Bold("=== tidy setup: Configuration Status ==="))
	fmt.Printf("Repository Root : %s\n", ui.Cyan(root))
	if inGit {
		fmt.Println("Git Initialized : Yes")
	} else {
		fmt.Printf("Git Initialized : %s\n", ui.Yellow("No (run tidy setup init)"))
	}
	fmt.Printf("Total Configs   : %s (%s)\n\n", ui.Green(itoa(int64(totalConfigs))), FormatSize(totalSize))

	printEntry := func(abs string) {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			rel = abs
		}
		size := ""
		if info, err := os.Stat(abs); err == nil {
			size = FormatSize(info.Size())
		}
		symbol, colorize := " ", ui.Gray
		if code, ok := gitMap[filepath.ToSlash(rel)]; ok {
			switch {
			case contains(code, "?"):
				symbol, colorize = "?", ui.Yellow
			case contains(code, "A"):
				symbol, colorize = "+", ui.Green
			case contains(code, "M"):
				symbol, colorize = "M", ui.Cyan
			default:
				symbol, colorize = code, ui.Yellow
			}
		} else if inGit {
			symbol, colorize = "✓", ui.Gray
		}
		fmt.Printf("  [%s] %s %s\n", colorize(symbol), filepath.ToSlash(rel), ui.Gray("("+size+")"))
	}

	fmt.Printf("%s (%d):\n", ui.Bold(ui.Cyan("Server Root Configs")), len(res.RootConfigs))
	for _, f := range res.RootConfigs {
		printEntry(f)
	}
	fmt.Println()

	plugins := make([]string, 0, len(res.PluginConfigs))
	for p := range res.PluginConfigs {
		plugins = append(plugins, p)
	}
	sort.Strings(plugins)
	fmt.Printf("%s (%d plugins):\n", ui.Bold(ui.Cyan("Plugin Configurations")), len(plugins))
	for _, p := range plugins {
		files := res.PluginConfigs[p]
		var pSize int64
		for _, f := range files {
			if info, err := os.Stat(f); err == nil {
				pSize += info.Size()
			}
		}
		fmt.Printf("  %s %s\n", ui.Bold(p), ui.Gray(fmt.Sprintf("(%d configs, %s)", len(files), FormatSize(pSize))))
		for _, f := range files {
			printEntry(f)
		}
	}
	fmt.Println()

	if inGit {
		if deleted := LsFiles(root, "--deleted"); len(deleted) > 0 {
			fmt.Printf("%s (%d):\n", ui.Bold(ui.Red("Deleted Files Pending Removal")), len(deleted))
			for _, d := range deleted {
				fmt.Printf("  [%s] %s\n", ui.Red("D"), d)
			}
			fmt.Println()
		}
	}

	fmt.Println(ui.Bold("Ignored / Filtered Files (Safe from Git):"))
	fmt.Printf("  - World directories : %s\n", ui.Gray(fmt.Sprintf("%d directories ignored (world/, etc.)", res.Ignored.WorldDirs)))
	fmt.Printf("  - Plugin JAR files  : %s\n", ui.Gray(fmt.Sprintf("%d .jar files ignored", res.Ignored.Jars)))
	fmt.Printf("  - Databases (.db)   : %s\n", ui.Gray(fmt.Sprintf("%d database files ignored", res.Ignored.Databases)))
	fmt.Printf("  - Archives (.zip)   : %s\n", ui.Gray(fmt.Sprintf("%d archive files ignored", res.Ignored.Archives)))
	fmt.Printf("  - Caches & Temp     : %s\n", ui.Gray(fmt.Sprintf("%d temp/cache files ignored", res.Ignored.CachesTemp)))
	if res.Ignored.OtherIgnored > 0 {
		fmt.Printf("  - Other ignored     : %s\n", ui.Gray(fmt.Sprintf("%d items matching .gitignore", res.Ignored.OtherIgnored)))
	}

	if inGit {
		fmt.Printf("\n%s\n", ui.Gray("Legend: [?] untracked, [+] staged, [M] modified, [D] deleted, [✓] clean"))
	}
	fmt.Printf("Next: %s\n", ui.Cyan("tidy setup check"))
	return 0
}

// CmdCheck audits hygiene: .gitignore coverage plus forbidden tracked files.
// Report-only: never deletes or untracks anything.
func CmdCheck(root string) int {
	fmt.Println(ui.Bold(ui.Cyan("Running repository hygiene audit...")))
	fmt.Println()
	if !IsGitRepo(root) {
		fmt.Printf("  %s\n", ui.Yellow("Git not initialized. Filesystem-only audit."))
	}

	issues := 0
	giPath := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(giPath)
	if err != nil {
		fmt.Printf("  %s\n", ui.Red("[x] Missing .gitignore! Worlds and binaries may be accidentally tracked."))
		issues++
	} else {
		if !contains(string(content), "world") {
			fmt.Printf("  %s\n", ui.Yellow("[!] .gitignore does not explicitly mention 'world/'."))
			issues++
		} else {
			fmt.Printf("  %s\n", ui.Green("[+] .gitignore excludes world directories."))
		}
		if !contains(string(content), "*.jar") {
			fmt.Printf("  %s\n", ui.Yellow("[!] .gitignore does not explicitly exclude '*.jar'."))
			issues++
		} else {
			fmt.Printf("  %s\n", ui.Green("[+] .gitignore excludes JAR files."))
		}
	}

	var forbidden []string
	if IsGitRepo(root) {
		for _, p := range LsFiles(root) {
			if IsForbiddenTracked(p) {
				fmt.Printf("  %s %s\n", ui.Red("[x] Forbidden tracked file in Git:"), p)
				forbidden = append(forbidden, p)
				issues++
			}
		}
	}

	if issues == 0 {
		fmt.Printf("\n%s No worlds, JARs, or runtime databases in Git.\n", ui.Green(ui.Bold("Audit passed cleanly!")))
		return 0
	}
	fmt.Printf("\n%s\n", ui.Red(ui.Bold(fmt.Sprintf("Audit found %d issue(s).", issues))))
	fmt.Printf("Fix .gitignore with: %s\n", ui.Cyan("tidy setup init --force"))
	if len(forbidden) > 0 {
		fmt.Printf("Untrack (keeps local file) with: %s\n", ui.Cyan("git rm --cached <path>"))
		for _, p := range forbidden {
			fmt.Printf("  git rm --cached %s\n", p)
		}
	}
	return 1
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
