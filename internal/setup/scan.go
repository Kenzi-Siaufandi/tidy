package setup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxConfigSizeBytes caps tracked config size (2 MB) to avoid tracking binaries.
const MaxConfigSizeBytes = 2 * 1024 * 1024

// ConfigExtensions are file extensions treated as trackable server configs.
var ConfigExtensions = map[string]bool{
	".yml":        true,
	".yaml":       true,
	".json":       true,
	".properties": true,
	".toml":       true,
	".conf":       true,
	".txt":        true,
}

// AlwaysIgnoreFilenames are never treated as configs even on extension match.
// Keys are lowercase; lookups must lowercase first.
var AlwaysIgnoreFilenames = map[string]bool{
	"usercache.json": true,
	"session.lock":   true,
	"uid.dat":        true,
	"about.txt":      true,
}

// IgnoredDirectories are never traversed for configs.
var IgnoredDirectories = map[string]bool{
	"world":                true,
	"world_nether":         true,
	"world_the_end":        true,
	"world_the_end_nether": true,
	".git":                 true,
	".paper":               true,
	".purpur":              true,
	"libraries":            true,
	"bundler":              true,
	"cache":                true,
	"logs":                 true,
	"crash-reports":        true,
	"sessions":             true,
	"session":              true,
	"tmp":                  true,
	"temp":                 true,
	"updater":              true,
	"schematics":           true,
}

// IgnoredCounts tallies files/dirs filtered out of the config scan.
type IgnoredCounts struct {
	WorldDirs    int
	Jars         int
	Databases    int
	Archives     int
	CachesTemp   int
	OtherIgnored int
}

// ScanResult groups discovered configs by location.
type ScanResult struct {
	RootConfigs   []string            // absolute paths
	PluginConfigs map[string][]string // plugin name -> absolute paths
	Ignored       IgnoredCounts
}

// FindServerRoot walks up from start looking for server.properties or plugins/.
// Falls back to the absolute start dir when no marker is found.
func FindServerRoot(start string) string {
	if strings.TrimSpace(start) == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, "server.properties")); err == nil {
			return cur
		}
		if info, err := os.Stat(filepath.Join(cur, "plugins")); err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		cur = parent
	}
}

// isDatabaseName reports runtime database filenames (incl. compound .h2.db).
func isDatabaseName(lower string) bool {
	return strings.HasSuffix(lower, ".db") ||
		strings.HasSuffix(lower, ".sqlite") ||
		strings.HasSuffix(lower, ".sqlite3") ||
		strings.HasSuffix(lower, ".h2.db") ||
		strings.HasSuffix(lower, ".mv.db")
}

// isArchiveName reports binary archive/world-export filenames.
func isArchiveName(lower string) bool {
	return strings.HasSuffix(lower, ".zip") ||
		strings.HasSuffix(lower, ".tar") ||
		strings.HasSuffix(lower, ".gz") ||
		strings.HasSuffix(lower, ".schem") ||
		strings.HasSuffix(lower, ".schematic") ||
		strings.HasSuffix(lower, ".litematic")
}

// ScanRepository walks root for trackable configs, pruning runtime directories.
// Git ignore checks are batched into a single `git check-ignore --stdin`
// invocation instead of one git process per file.
func ScanRepository(root string) (*ScanResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	res := &ScanResult{PluginConfigs: map[string][]string{}}
	pluginsDir := filepath.Join(absRoot, "plugins")

	if _, err := os.ReadDir(absRoot); err != nil {
		return res, err
	}

	type candidate struct {
		full string
		cur  string
		rel  string
	}
	var candidates []candidate
	var walk func(cur string) error
	walk = func(cur string) error {
		dirEntries, err := os.ReadDir(cur)
		if err != nil {
			return nil
		}
		for _, e := range dirEntries {
			name := e.Name()
			full := filepath.Join(cur, name)
			if e.IsDir() {
				if IgnoredDirectories[name] || strings.HasPrefix(name, ".") {
					if strings.Contains(name, "world") {
						res.Ignored.WorldDirs++
					} else if name == "tmp" || name == "temp" || name == "sessions" || name == "session" || name == "cache" || name == ".paper" {
						res.Ignored.CachesTemp++
					}
					continue
				}
				if err := walk(full); err != nil {
					return err
				}
				continue
			}

			lowerName := strings.ToLower(name)
			ext := strings.ToLower(filepath.Ext(lowerName))

			rel, err := filepath.Rel(absRoot, full)
			if err != nil {
				continue
			}
			relParts := strings.Split(rel, string(os.PathSeparator))
			relDirParts := relParts[:len(relParts)-1]

			switch {
			case ext == ".jar":
				res.Ignored.Jars++
				continue
			case isDatabaseName(lowerName):
				res.Ignored.Databases++
				continue
			case isArchiveName(lowerName):
				res.Ignored.Archives++
				continue
			case AlwaysIgnoreFilenames[lowerName] || containsPart(relDirParts, "tmp") || containsPart(relDirParts, "sessions"):
				res.Ignored.CachesTemp++
				continue
			}

			if !ConfigExtensions[ext] && lowerName != "server-icon.png" {
				continue
			}

			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			pruned := false
			for _, part := range relDirParts {
				if IgnoredDirectories[part] {
					pruned = true
					break
				}
			}
			if pruned {
				if containsWorld(relDirParts) {
					res.Ignored.WorldDirs++
				} else {
					res.Ignored.OtherIgnored++
				}
				continue
			}

			if info.Size() > MaxConfigSizeBytes {
				res.Ignored.OtherIgnored++
				continue
			}
			candidates = append(candidates, candidate{full: full, cur: cur, rel: filepath.ToSlash(rel)})
		}
		return nil
	}

	if err := walk(absRoot); err != nil {
		return res, err
	}

	// Single batched git ignore query for all candidates.
	ignored := map[string]bool{}
	if len(candidates) > 0 {
		rels := make([]string, 0, len(candidates))
		for _, c := range candidates {
			rels = append(rels, c.rel)
		}
		ignored = BatchIgnored(absRoot, rels)
	}

	for _, c := range candidates {
		if ignored[c.rel] {
			res.Ignored.OtherIgnored++
			continue
		}
		switch {
		case c.cur == absRoot || c.cur == filepath.Join(absRoot, "config"):
			res.RootConfigs = append(res.RootConfigs, c.full)
		case strings.HasPrefix(c.full, pluginsDir+string(os.PathSeparator)):
			relPlugins, err := filepath.Rel(pluginsDir, c.full)
			if err != nil {
				res.RootConfigs = append(res.RootConfigs, c.full)
				continue
			}
			parts := strings.Split(relPlugins, string(os.PathSeparator))
			if len(parts) == 0 || parts[0] == "" {
				res.RootConfigs = append(res.RootConfigs, c.full)
				continue
			}
			res.PluginConfigs[parts[0]] = append(res.PluginConfigs[parts[0]], c.full)
		default:
			res.RootConfigs = append(res.RootConfigs, c.full)
		}
	}

	sort.Strings(res.RootConfigs)
	for plugin := range res.PluginConfigs {
		sort.Strings(res.PluginConfigs[plugin])
	}
	return res, nil
}

func containsPart(parts []string, want string) bool {
	for _, p := range parts {
		if p == want {
			return true
		}
	}
	return false
}

func containsWorld(parts []string) bool {
	for _, p := range parts {
		if strings.Contains(p, "world") {
			return true
		}
	}
	return false
}

// FormatSize renders bytes as a human readable string.
func FormatSize(sizeBytes int64) string {
	switch {
	case sizeBytes < 1024:
		return itoa(sizeBytes) + " B"
	case sizeBytes < 1024*1024:
		return oneDecimal(float64(sizeBytes)/1024) + " KB"
	default:
		return oneDecimal(float64(sizeBytes)/(1024*1024)) + " MB"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [32]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func oneDecimal(f float64) string {
	rounded := float64(int(f*10+0.5)) / 10
	whole := int(rounded)
	frac := int(rounded*10) - whole*10
	if frac < 0 {
		frac = -frac
	}
	return itoa(int64(whole)) + "." + itoa(int64(frac))
}
