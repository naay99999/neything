// Package scan implements tier-0 "live scan" search (design §6): a
// stateless, index-free filesystem search used when a folder hasn't been
// (fully) indexed yet. It never touches SQLite or a VectorStore — just
// filename token matching plus a bounded content grep for small plain-text
// files — so it works the instant `ney mcp` starts, before Phase A finishes.
package scan

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Default caps (Options.withDefaults fills these in for zero-valued fields).
const (
	DefaultMaxFiles = 10000
	DefaultTimeout  = 2 * time.Second
	DefaultMaxHits  = 20

	// maxContentBytes bounds which plain-text files get grepped — matches
	// the design's "content grep ... ≤ 2 MB" cap.
	maxContentBytes = 2 * 1024 * 1024

	// maxSnippetLines bounds how many matching lines are captured per file.
	maxSnippetLines = 3

	// maxLineBytes bounds a single line read by the content-grep scanner —
	// long "lines" (e.g. minified JSON) are truncated rather than blowing up
	// memory or aborting the scan for the rest of the file.
	maxLineBytes = 1024 * 1024
)

// contentGrepExts are the plain-text formats content grep is allowed to
// read. Deliberately does NOT include .pdf/.docx (design §6: "ไม่แตะ
// PDF/DOCX — parse สดแพงเกิน, filename match ครอบเคสนั้น") and is a
// superset unrelated to internal/index's supportedExts — a live scan must
// still report filename hits on any extension (e.g. order-1233.xlsx), it
// just can't grep their contents.
var contentGrepExts = map[string]bool{
	".md":   true,
	".txt":  true,
	".html": true,
	".htm":  true,
	".json": true,
	".xml":  true,
	".csv":  true,
	".tsv":  true,
	".log":  true,
}

// Hit is one filename and/or content match found by Scan.
type Hit struct {
	Path    string
	Score   float64
	Snippet string
	// MatchedIn is "filename", "content", or "both".
	MatchedIn string
}

// Options bounds the cost of a Scan call. Zero values fall back to the
// package's Default* constants.
type Options struct {
	MaxFiles int
	Timeout  time.Duration
	MaxHits  int
}

func (o Options) withDefaults() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxHits <= 0 {
		o.MaxHits = DefaultMaxHits
	}
	return o
}

// Scan walks root looking for files whose name or (for small plain-text
// files) content contains any token of query. It is stateless — no DB, no
// vector store — and bounded by opts (file count, wall-clock time, and
// result count); truncated reports whether any of those caps was hit, so a
// caller can tell "no matches" apart from "stopped early, there may be
// more".
//
// Directory walking uses the same dot-dir/dotfile skip rule as
// internal/index/pipeline.go's Index, but — unlike the indexer — never
// restricts filename matching to a fixed set of supported extensions: a hit
// on order-1233.xlsx is still useful information even though ney can't
// parse xlsx.
func Scan(ctx context.Context, root, query string, opts Options) (hits []Hit, truncated bool, err error) {
	opts = opts.withDefaults()
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, false, nil
	}

	scanCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	var results []Hit
	filesWalked := 0

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable file/dir: skip it, same as the indexer's walk.
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		select {
		case <-scanCtx.Done():
			return scanCtx.Err()
		default:
		}

		filesWalked++
		if filesWalked > opts.MaxFiles {
			return filepath.SkipAll
		}

		if hit, ok := scoreFile(scanCtx, path, tokens); ok {
			results = append(results, hit)
		}
		return nil
	})

	if filesWalked > opts.MaxFiles {
		truncated = true
	}

	if walkErr != nil {
		if ctx.Err() != nil {
			// The caller's own context was cancelled (not just our internal
			// scan timeout) — propagate promptly so the caller isn't left
			// waiting, per the design's ctx-cancel requirement.
			return results, true, ctx.Err()
		}
		// Either our own timeout tripped, or WalkDir stopped via SkipAll —
		// both are a normal, non-error "stopped early" outcome.
		truncated = true
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > opts.MaxHits {
		results = results[:opts.MaxHits]
		truncated = true
	}
	return results, truncated, nil
}

// scoreFile scores one file against tokens: filename substring matching
// always runs; content grep only runs for small plain-text files (see
// isContentGrepCandidate). The score is the fraction of query tokens
// matched by either signal (filename ∪ content) — so a file matched by both
// channels scores at least as high as either alone, which is what the
// design means by "content match adds to score".
func scoreFile(ctx context.Context, path string, tokens []string) (Hit, bool) {
	base := strings.ToLower(filepath.Base(path))
	fMatched := matchedTokenSet(base, tokens)

	var cMatched map[string]bool
	var snippet string
	if isContentGrepCandidate(path) {
		cMatched, snippet = grepFile(ctx, path, tokens)
	}

	if len(fMatched) == 0 && len(cMatched) == 0 {
		return Hit{}, false
	}

	union := make(map[string]bool, len(fMatched)+len(cMatched))
	for t := range fMatched {
		union[t] = true
	}
	for t := range cMatched {
		union[t] = true
	}

	matchedIn := "filename"
	switch {
	case len(fMatched) > 0 && len(cMatched) > 0:
		matchedIn = "both"
	case len(cMatched) > 0:
		matchedIn = "content"
	}

	return Hit{
		Path:      path,
		Score:     float64(len(union)) / float64(len(tokens)),
		Snippet:   snippet,
		MatchedIn: matchedIn,
	}, true
}

// isContentGrepCandidate reports whether path is small enough and a plain
// enough format for content grep: a recognized text extension, ≤ 2 MB.
func isContentGrepCandidate(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if !contentGrepExts[ext] {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() > maxContentBytes {
		return false
	}
	return true
}

// grepFile scans path line by line for any token (case-insensitive),
// returning the set of tokens matched anywhere in the file and up to
// maxSnippetLines matching lines joined as a snippet. Best-effort: an
// unreadable file, or one whose scan is interrupted by ctx, simply returns
// whatever was found before the problem.
func grepFile(ctx context.Context, path string, tokens []string) (map[string]bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ""
	}
	defer f.Close()

	matched := make(map[string]bool)
	var snippetLines []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := scanner.Text()
		lower := strings.ToLower(line)
		lineMatched := false
		for _, tok := range tokens {
			if strings.Contains(lower, tok) {
				matched[tok] = true
				lineMatched = true
			}
		}
		if lineMatched && len(snippetLines) < maxSnippetLines {
			snippetLines = append(snippetLines, strings.TrimSpace(line))
		}
	}
	return matched, strings.Join(snippetLines, " / ")
}

// matchedTokenSet returns the subset of tokens that appear as a
// case-insensitive substring of haystackLower (already lower-cased by the
// caller).
func matchedTokenSet(haystackLower string, tokens []string) map[string]bool {
	matched := make(map[string]bool)
	for _, tok := range tokens {
		if strings.Contains(haystackLower, tok) {
			matched[tok] = true
		}
	}
	return matched
}

// tokenize splits query into lowercase tokens on any run of non-letter,
// non-digit characters, drops tokens shorter than 2 chars (and therefore
// any punctuation-only fragment, which becomes empty and is dropped too),
// and de-duplicates while preserving first-seen order.
func tokenize(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	seen := make(map[string]bool, len(fields))
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		tokens = append(tokens, f)
	}
	return tokens
}
