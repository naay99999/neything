// Package discover finds where a user's documents actually live. It deep-walks
// the home directory (plus iCloud Drive) counting indexable files per folder,
// then surfaces the folders where documents concentrate — the candidates the
// setup wizard offers for indexing. Sensitive and junk directories are never
// entered: everything pathfilter denies (dot-dirs, secret names) plus
// well-known noise like Library, node_modules, and app bundles.
package discover

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/pathfilter"
)

// Candidate is one folder worth offering for indexing.
type Candidate struct {
	Path     string
	DocCount int
	ByExt    map[string]int
}

// Options bounds a Discover run. Zero values get sensible defaults.
type Options struct {
	// Roots to walk. Default: $HOME plus iCloud Drive (when present).
	Roots []string
	// Filter applies the shared exclusion rules. nil = built-ins only.
	Filter *pathfilter.Filter
	// MaxCandidates caps the returned list (default 10).
	MaxCandidates int
}

// junkDirs are directories that never contain the user's own documents but
// are expensive to walk (or are application internals). Checked by basename,
// in addition to the pathfilter rules.
var junkDirs = map[string]bool{
	"Library":      true, // iCloud Drive is walked as its own root instead
	"node_modules": true,
	"vendor":       true,
	"venv":         true,
	".venv":        true,
	"Applications": true,
	"Music":        true,
	"Movies":       true,
	"Pictures":     true,
}

// bundleSuffixes are macOS package-directory suffixes that look like folders
// but are application/media internals.
var bundleSuffixes = []string{".app", ".photoslibrary", ".musiclibrary", ".tv-library", ".framework"}

// concentration is the child-share threshold above which a parent defers to
// its child as the candidate ("913 docs in ~/workspace" where 900 are in
// ~/workspace/projectA → offer projectA).
const concentration = 0.8

// secondaryShare is the share of a walk root's total documents a sibling
// subtree needs to also be surfaced as its own candidate.
const secondaryShare = 0.2

// progressEvery controls how often the progress callback fires.
const progressEvery = 500

// DefaultRoots returns $HOME plus the iCloud Drive documents root when it
// exists. iCloud Drive lives under ~/Library (which the walk skips), so it
// is handled as an explicit second root.
func DefaultRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	roots := []string{home}
	icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if fi, err := os.Stat(icloud); err == nil && fi.IsDir() {
		roots = append(roots, icloud)
	}
	return roots
}

// Discover walks opts.Roots and returns the top folders by indexable-document
// count, applying the concentration heuristic per root. progress (optional)
// receives the number of directories visited so far, called every few hundred
// directories. Unreadable entries are skipped silently; ctx cancellation
// aborts with ctx.Err().
func Discover(ctx context.Context, opts Options, progress func(dirs int)) ([]Candidate, error) {
	roots := opts.Roots
	if len(roots) == 0 {
		roots = DefaultRoots()
	}
	maxC := opts.MaxCandidates
	if maxC <= 0 {
		maxC = 10
	}

	var out []Candidate
	dirsSeen := 0

	for _, root := range roots {
		root = filepath.Clean(root)
		counts := make(map[string]int)
		exts := make(map[string]map[string]int)

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && excludedDir(d.Name(), opts.Filter) {
					return filepath.SkipDir
				}
				dirsSeen++
				if progress != nil && dirsSeen%progressEvery == 0 {
					progress(dirsSeen)
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			}
			if opts.Filter.ExcludedFile(d.Name()) || !index.IsSupportedExt(path) {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			// Credit the file to every ancestor up to (and including) root.
			for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
				counts[dir]++
				m := exts[dir]
				if m == nil {
					m = make(map[string]int)
					exts[dir] = m
				}
				m[ext]++
				if dir == root {
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		total := counts[root]
		if total == 0 {
			continue
		}
		picked := map[string]bool{}
		pick(root, root, counts, total, picked)
		for dir := range picked {
			out = append(out, Candidate{Path: dir, DocCount: counts[dir], ByExt: exts[dir]})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].DocCount != out[j].DocCount {
			return out[i].DocCount > out[j].DocCount
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > maxC {
		out = out[:maxC]
	}
	if progress != nil {
		progress(dirsSeen)
	}
	return out, nil
}

// pick implements the concentration heuristic for one directory: descend
// while a single child dominates, emit otherwise, and also surface secondary
// clusters that hold a meaningful share of the walk root's total. The walk
// root itself is never emitted — indexing all of $HOME wholesale is exactly
// what discovery exists to avoid.
func pick(dir, root string, counts map[string]int, rootTotal int, picked map[string]bool) {
	children := childDirs(dir, counts)

	// Dominant child → recurse into it instead of emitting dir.
	for _, c := range children {
		if float64(counts[c]) >= concentration*float64(counts[dir]) {
			pick(c, root, counts, rootTotal, picked)
			// Other children may still hold a meaningful share of the root.
			for _, o := range children {
				if o != c && float64(counts[o]) >= secondaryShare*float64(rootTotal) {
					pick(o, root, counts, rootTotal, picked)
				}
			}
			return
		}
	}

	if dir != root {
		picked[dir] = true
		return
	}
	// dir == root with no dominant child: emit each substantial child rather
	// than the root itself.
	for _, c := range children {
		if float64(counts[c]) >= secondaryShare*float64(rootTotal) {
			pick(c, root, counts, rootTotal, picked)
		}
	}
}

// childDirs returns the immediate subdirectories of dir that have any
// counted documents, derived from the counts map (no extra filesystem I/O).
func childDirs(dir string, counts map[string]int) []string {
	var out []string
	prefix := dir + string(filepath.Separator)
	for d := range counts {
		if !strings.HasPrefix(d, prefix) {
			continue
		}
		rest := d[len(prefix):]
		if strings.Contains(rest, string(filepath.Separator)) {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func excludedDir(name string, flt *pathfilter.Filter) bool {
	if flt.ExcludedDir(name) || junkDirs[name] {
		return true
	}
	lower := strings.ToLower(name)
	for _, suf := range bundleSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}
