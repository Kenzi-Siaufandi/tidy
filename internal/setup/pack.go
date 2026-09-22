package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kenzi-Siaufandi/tidy/internal/ui"
)

// Pack strip lists: player/user data removed from the staged copy.
// Everything else (region, level.dat, poi, entities, datapacks) is kept.
var packStripDirs = []string{"playerdata", "stats", "advancements"}
var packStripFiles = []string{"session.lock", "uid.dat"}

// PackOptions configures a world-template pack run.
type PackOptions struct {
	Root   string // server root containing the world dir
	World  string // world directory name (default "world")
	Output string // archive destination ("" = <world>-template.<ext> in cwd)
	Format string // "tar.gz" (default), "tgz", "tar", "zip" ("" = infer)
}

// PackResult summarizes a completed pack run.
type PackResult struct {
	ArchivePath   string
	SHA256        string
	Size          int64
	Files         int
	Uncompressed  int64
	StrippedFiles int
	StrippedBytes int64
	SkippedLinks  int
	Format        string
}

// normalizePackFormat resolves the archive format from flag/output/default.
func normalizePackFormat(flagVal, output string) (format, ext string) {
	f := strings.ToLower(strings.TrimSpace(flagVal))
	if f == "" {
		lower := strings.ToLower(output)
		switch {
		case strings.HasSuffix(lower, ".zip"):
			return "zip", ".zip"
		case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
			return "tar.gz", ".tar.gz"
		case strings.HasSuffix(lower, ".tar"):
			return "tar", ".tar"
		default:
			return "tar.gz", ".tar.gz"
		}
	}
	switch f {
	case "zip":
		return "zip", ".zip"
	case "tar":
		return "tar", ".tar"
	case "tgz", "tar.gz", "tar-gz":
		return "tar.gz", ".tar.gz"
	default:
		return "tar.gz", ".tar.gz"
	}
}

// copyDir copies src tree into dst, preserving modes. Symlinks are skipped.
func copyDir(src, dst string) (files, skippedLinks int, err error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			skippedLinks++
			continue
		}
		if e.IsDir() {
			n, s, err := copyDir(s, d)
			files += n
			skippedLinks += s
			if err != nil {
				return files, skippedLinks, err
			}
			_ = os.Chmod(d, info.Mode().Perm()&0777)
			continue
		}
		if err := copyFile(s, d, info.Mode().Perm()&0777); err != nil {
			return files, skippedLinks, err
		}
		files++
	}
	return files, skippedLinks, nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// stripPlayerData removes player/user data from a staged world copy.
func stripPlayerData(staging string) (files int, bytes int64, err error) {
	stripDir := map[string]bool{}
	for _, d := range packStripDirs {
		stripDir[d] = true
	}
	stripFile := map[string]bool{}
	for _, f := range packStripFiles {
		stripFile[f] = true
	}
	err = filepath.WalkDir(staging, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == staging {
			return nil
		}
		base := strings.ToLower(d.Name())
		if d.IsDir() {
			if stripDir[base] {
				n, b := dirStats(path)
				if rmErr := os.RemoveAll(path); rmErr != nil {
					return rmErr
				}
				files += n
				bytes += b
				return filepath.SkipDir
			}
			return nil
		}
		if stripFile[base] {
			var size int64
			if info, statErr := os.Stat(path); statErr == nil {
				size = info.Size()
			}
			if rmErr := os.Remove(path); rmErr != nil {
				return rmErr
			}
			files++
			bytes += size
		}
		return nil
	})
	return files, bytes, err
}

func dirStats(dir string) (files int, bytes int64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

// collectStage lists staged files (relative, slash-separated) sorted.
func collectStage(staging string) ([]string, int64, error) {
	var rels []string
	var total int64
	err := filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == staging {
			return nil
		}
		if !d.IsDir() {
			rel, relErr := filepath.Rel(staging, path)
			if relErr != nil {
				return nil
			}
			rels = append(rels, filepath.ToSlash(rel))
			if info, statErr := d.Info(); statErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	sort.Strings(rels)
	return rels, total, err
}

func writeTarGz(staging, dest string, rels []string, gzipOn bool) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	var tw *tar.Writer
	var gw *gzip.Writer
	if gzipOn {
		gw = gzip.NewWriter(out)
		defer gw.Close()
		tw = tar.NewWriter(gw)
	} else {
		tw = tar.NewWriter(out)
	}
	defer tw.Close()

	seenDirs := map[string]bool{}
	for _, rel := range rels {
		dir := parentDir(rel)
		if dir != "" && !seenDirs[dir] {
			// Emit ancestor dir entries so empty dirs survive.
			parts := strings.Split(dir, "/")
			for i := range parts {
				p := strings.Join(parts[:i+1], "/")
				if seenDirs[p] {
					continue
				}
				seenDirs[p] = true
				if err := tw.WriteHeader(&tar.Header{Name: p + "/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
					return err
				}
			}
		}
		full := filepath.Join(staging, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: rel,
			Mode: int64(info.Mode().Perm() & 0777),
			Size: info.Size(),
		}); err != nil {
			return err
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func parentDir(rel string) string {
	if i := strings.LastIndex(rel, "/"); i > 0 {
		return rel[:i]
	}
	return ""
}

func writeZip(staging, dest string, rels []string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	seenDirs := map[string]bool{}
	for _, rel := range rels {
		if dir := parentDir(rel); dir != "" && !seenDirs[dir] {
			parts := strings.Split(dir, "/")
			for i := range parts {
				p := strings.Join(parts[:i+1], "/")
				if seenDirs[p] {
					continue
				}
				seenDirs[p] = true
				if _, err := zw.Create(p + "/"); err != nil {
					return err
				}
			}
		}
		full := filepath.Join(staging, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		hdr := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		hdr.SetMode(info.Mode().Perm() & 0777)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// PackWorld copies a world, strips player data, and archives the result.
// The live world directory is never modified.
func PackWorld(opts PackOptions) (*PackResult, error) {
	world := strings.TrimSpace(opts.World)
	if world == "" {
		world = "world"
	}
	worldDir := filepath.Join(opts.Root, world)
	if info, err := os.Stat(worldDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("world directory %q not found", worldDir)
	}

	format, ext := normalizePackFormat(opts.Format, opts.Output)
	output := strings.TrimSpace(opts.Output)
	if output == "" {
		cwd, _ := os.Getwd()
		output = filepath.Join(cwd, world+"-template"+ext)
	}
	absOut, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp("", "tidy-pack-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	stageWorld := filepath.Join(staging, "stage")

	_, skippedLinks, err := copyDir(worldDir, stageWorld)
	if err != nil {
		return nil, fmt.Errorf("failed to stage world copy: %w", err)
	}
	strippedFiles, strippedBytes, err := stripPlayerData(stageWorld)
	if err != nil {
		return nil, fmt.Errorf("failed to strip player data: %w", err)
	}

	rels, uncompressed, err := collectStage(stageWorld)
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("nothing left to pack after stripping (empty world?)")
	}

	if err := os.MkdirAll(filepath.Dir(absOut), 0755); err != nil {
		return nil, err
	}
	switch format {
	case "zip":
		err = writeZip(stageWorld, absOut, rels)
	case "tar":
		err = writeTarGz(stageWorld, absOut, rels, false)
	default:
		err = writeTarGz(stageWorld, absOut, rels, true)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to write archive: %w", err)
	}

	sum, size, err := sha256File(absOut)
	if err != nil {
		return nil, err
	}
	return &PackResult{
		ArchivePath:   absOut,
		SHA256:        sum,
		Size:          size,
		Files:         len(rels),
		Uncompressed:  uncompressed,
		StrippedFiles: strippedFiles,
		StrippedBytes: strippedBytes,
		SkippedLinks:  skippedLinks,
		Format:        format,
	}, nil
}

// CmdPackWorld runs the pack flow with user-facing output.
func CmdPackWorld(root string, opts PackOptions) int {
	opts.Root = root
	fmt.Printf("%s\n", ui.Yellow("[!] Stop the server before packing so region files are consistent."))
	fmt.Printf("%s %s\n", ui.Cyan("[*] Packing world:"), opts.World)

	res, err := PackWorld(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Red(fmt.Sprintf("[!] Pack failed: %v", err)))
		return 1
	}
	fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[+] Stripped %d player-data files (%s)", res.StrippedFiles, FormatSize(res.StrippedBytes))))
	if res.SkippedLinks > 0 {
		fmt.Printf("%s\n", ui.Yellow(fmt.Sprintf("[!] Skipped %d symlinks (tidy cannot extract them).", res.SkippedLinks)))
	}
	fmt.Printf("%s\n", ui.Green(fmt.Sprintf("[+] Wrote %s (%d files, %s, SHA256: %s...)",
		res.ArchivePath, res.Files, FormatSize(res.Size), res.SHA256[:12])))
	if res.Files > 10000 {
		fmt.Printf("%s\n", ui.Yellow("[!] Archive exceeds tidy's 10k-file extract limit; prune the world or split it."))
	}
	if res.Uncompressed > int64(2<<30) {
		fmt.Printf("%s\n", ui.Yellow("[!] Unpacked size exceeds tidy's 2 GiB extract limit."))
	}

	world := strings.TrimSpace(opts.World)
	if world == "" {
		world = "world"
	}
	fmt.Printf("\n%s (upload the archive, then paste):\n", ui.Bold("tidy.toml snippet"))
	fmt.Printf("[worlds.%s_template]\n", world)
	fmt.Printf("source = \"http\"\npath = %q\nurl = \"https://.../%s\"\nsha256 = %q\nextract = true\nonce = true\n",
		world, filepath.Base(res.ArchivePath), res.SHA256)
	return 0
}
