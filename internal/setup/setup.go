package setup

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
)

// Usage for `tidy setup`.
const Usage = `Usage:
  tidy setup [--workdir DIR] [--root DIR] [--no-color] <init|status|check> [flags]

Subcommands:
  init       Initialize Git and create .gitignore & .gitattributes
  status     Scan configs, inspect git status, and view ignored files (default)
  check      Audit repo for forbidden tracked files (report-only, no deletions)
  pack-world Strip player data from a world and pack a tidy-ready archive
  new        Interactive wizard to create tidy.toml (plugins skippable)

Flags:
  --workdir DIR   Minecraft server root (default ".")
  --root DIR      Alias for --workdir
  --force         Overwrite .gitignore/.gitattributes (init) or tidy.toml (new)
  --no-color      Disable colored output
  --world NAME    World directory to pack (pack-world only, default "world")
  --output PATH   Archive destination (pack-world) or config path (new, default tidy.toml)
  --format FMT    Archive format: tar.gz (default), tgz, tar, zip
  -h, --help      Show this help
`

// Run dispatches `tidy setup ...`. Returns process exit code.
func Run(args []string) int {
	var (
		workdir string
		root    string
		force   bool
		noColor bool
		help    bool
		world   string
		output  string
		format  string
	)

	// Extract subcommand first so flags may appear before or after it.
	sub := ""
	var flags []string
	for _, a := range args {
		if sub == "" && !strings.HasPrefix(a, "-") && (a == "init" || a == "status" || a == "check" || a == "pack-world" || a == "new") {
			sub = a
			continue
		}
		flags = append(flags, a)
	}

	fs := flag.NewFlagSet("tidy setup", flag.ContinueOnError)
	fs.StringVar(&workdir, "workdir", ".", "Working directory for Minecraft server root")
	fs.StringVar(&root, "root", "", "Alias for --workdir")
	fs.BoolVar(&force, "force", false, "Overwrite existing .gitignore/.gitattributes")
	fs.BoolVar(&noColor, "no-color", false, "Disable colored output")
	fs.StringVar(&world, "world", "world", "World directory to pack")
	fs.StringVar(&output, "output", "", "Archive destination")
	fs.StringVar(&format, "format", "", "Archive format: tar.gz, tgz, tar, zip")
	fs.BoolVar(&help, "help", false, "Show help")
	// -h shorthand
	fs.BoolVar(&help, "h", false, "Show help (shorthand)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "%s\n", Usage)
		return 2
	}
	if help {
		fmt.Fprintf(os.Stderr, "%s", Usage)
		return 0
	}
	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Unexpected argument: %s", strings.Join(extra, " "))))
		fmt.Fprintf(os.Stderr, "%s", Usage)
		return 2
	}

	if noColor {
		ui.SetEnabled(false)
	}

	dir := workdir
	if strings.TrimSpace(root) != "" {
		dir = root
	}
	serverRoot := FindServerRoot(dir)

	switch sub {
	case "", "status":
		if force && sub == "" {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Yellow("[!] --force only applies to init; ignoring."))
		}
		return CmdStatus(serverRoot)
	case "init":
		return CmdInit(serverRoot, force)
	case "check":
		if force {
			fmt.Fprintf(os.Stderr, "%s\n", ui.Yellow("[!] --force only applies to init; ignoring."))
		}
		return CmdCheck(serverRoot)
	case "pack-world":
		return CmdPackWorld(serverRoot, PackOptions{World: world, Output: output, Format: format})
	case "new":
		return CmdNew(serverRoot, output, force, os.Stdin, os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "%s", Usage)
		return 2
	}
}
