// Package discover finds git repositories under a user's dev roots — the
// candidates the setup wizard offers to index as workspaces. It is a thin
// wrapper over internal/context's ScanRepos: repo discovery (walk, .git
// detection, git metadata) lives there and is shared with get_context /
// list_projects; this package adds the wizard-facing doc count and applies
// display bounds (sort, cap).
package discover

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/pathfilter"
)

// Candidate is one git repository worth offering the user for indexing.
type Candidate struct {
	Path       string
	Name       string
	LastCommit time.Time
	DocCount   int // indexable files found inside the repo (display only)
}

// Options bounds a Discover run. Zero values get sensible defaults.
type Options struct {
	// Roots to scan for git repositories, e.g. cfg.Context.DevRoots. No
	// default is applied here — an empty Roots means "nothing to scan",
	// and it's the wizard's job to ask the user for a root when this
	// config value is unset.
	Roots []string
	// Filter applies the shared exclusion rules when counting indexable
	// files inside each repo. nil = built-ins only.
	Filter *pathfilter.Filter
	// MaxCandidates caps the returned list (default 15).
	MaxCandidates int
}

// defaultMaxCandidates is used when Options.MaxCandidates is unset.
const defaultMaxCandidates = 15

// Discover scans opts.Roots for git repositories (via neycontext.ScanRepos)
// and returns one Candidate per repo, sorted by last-commit time descending
// and capped at MaxCandidates. DocCount is the number of indexable files
// (per index.IsSupportedExt) found inside the repo, skipping node_modules,
// vendor, dot-directories, and anything pathfilter denies.
//
// progress (optional) receives the number of repos processed so far, called
// once per repo as doc counts are computed (the repo scan itself is fast;
// walking each repo for its doc count is the part that can take a moment on
// large trees). ctx cancellation aborts early, returning ctx.Err().
func Discover(ctx context.Context, opts Options, progress func(scanned int)) ([]Candidate, error) {
	maxC := opts.MaxCandidates
	if maxC <= 0 {
		maxC = defaultMaxCandidates
	}

	projects := neycontext.ScanRepos(ctx, opts.Roots)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(projects))
	for i, p := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, Candidate{
			Path:       p.Path,
			Name:       p.Name,
			LastCommit: p.LastCommit,
			DocCount:   countDocs(ctx, p.Path, opts.Filter),
		})
		if progress != nil {
			progress(i + 1)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastCommit.After(out[j].LastCommit)
	})
	if len(out) > maxC {
		out = out[:maxC]
	}
	return out, nil
}

// countDocs walks root and counts files index.IsSupportedExt considers
// indexable, skipping anything pathfilter denies plus node_modules/vendor
// (dependency trees that are never the user's own documents). Unreadable
// entries are skipped silently; ctx cancellation stops the walk early.
func countDocs(ctx context.Context, root string, filter *pathfilter.Filter) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (filter.ExcludedDir(name) || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filter.ExcludedFile(d.Name()) || !index.IsSupportedExt(path) {
			return nil
		}
		count++
		return nil
	})
	return count
}
