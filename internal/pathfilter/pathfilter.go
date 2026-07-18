// Package pathfilter decides which files and directories ney is allowed to
// touch. One shared Filter is applied by every surface that exposes file
// content — the indexer walk, the tier-0 live scan, and the MCP
// read_document tool — so "excluded" means excluded everywhere: never
// chunked, never grepped, never served to an AI client.
//
// Two rules are always on and cannot be disabled:
//   - dotfiles and dot-directories (.env, .ssh/, .git/, ...)
//   - DefaultDeny, a curated list of secret-file basename patterns
//
// User-configured patterns (config index.exclude) only ever add to that.
package pathfilter

import (
	"path"
	"path/filepath"
	"strings"
)

// DefaultDeny is the built-in, always-on set of secret-file basename
// patterns, matched case-insensitively with path.Match semantics. Dotfiles
// are denied unconditionally and independently of this list.
//
// Deliberately absent: "*token*" (matches innocent docs like
// tokenizer-notes.md) and "*.crt" (public certificate material).
var DefaultDeny = []string{
	"*secret*", "*credential*", "*password*", "*passwd*",
	"*apikey*", "*api_key*", "*api-key*",
	"*.env", // prod.env etc.; ".env" itself is caught by the dotfile rule
	"*.key", "*.pem", "*.p12", "*.pfx", "*.jks", "*.keystore",
	"*.ppk", "*.kdbx", "*.gpg", "*.asc", "*.der",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
}

// Filter reports whether names/paths are excluded. The zero value is not
// meant to be constructed directly — use New — but a nil *Filter is valid
// and applies the built-in rules only, so callers that never wire config
// (tests, embedded use) still get secret protection by default.
type Filter struct {
	patterns []string // lowercased globs: DefaultDeny + user extras
}

// New compiles DefaultDeny plus user-configured extra patterns. A malformed
// glob returns path.ErrBadPattern (wrapped with the offending pattern) so
// config validation can reject it up front.
func New(extra []string) (*Filter, error) {
	patterns := make([]string, 0, len(DefaultDeny)+len(extra))
	for _, p := range append(append([]string{}, DefaultDeny...), extra...) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if _, err := path.Match(p, "probe"); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return &Filter{patterns: patterns}, nil
}

func (f *Filter) matches(name string) bool {
	name = strings.ToLower(name)
	patterns := DefaultDeny
	if f != nil {
		patterns = f.patterns
	}
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), name); ok {
			return true
		}
	}
	return false
}

// ExcludedFile reports whether a file with this basename is denied:
// leading dot, or any deny-pattern match. Nil-receiver safe (built-ins only).
func (f *Filter) ExcludedFile(name string) bool {
	return strings.HasPrefix(name, ".") || f.matches(name)
}

// ExcludedDir reports whether a directory with this basename is denied.
// Same rules as ExcludedFile.
func (f *Filter) ExcludedDir(name string) bool {
	return strings.HasPrefix(name, ".") || f.matches(name)
}

// ExcludedPath reports whether resolved — an absolute, cleaned path assumed
// to be under root — has ANY denied component: intermediate directories via
// ExcludedDir, the final element via ExcludedFile. This is what read_document
// uses, so root/.ssh/id_rsa is denied even though a directory walk (which
// prunes .ssh early) never reaches it. The root itself is not checked.
func (f *Filter) ExcludedPath(root, resolved string) bool {
	sep := string(filepath.Separator)
	if resolved == root || !strings.HasPrefix(resolved, root+sep) {
		// Not under root (or is root itself) — not this function's call to
		// allow; containment is the caller's check. Don't deny here.
		return false
	}
	rel := strings.TrimPrefix(resolved, root+sep)
	parts := strings.Split(rel, sep)
	for i, part := range parts {
		if i == len(parts)-1 {
			if f.ExcludedFile(part) {
				return true
			}
		} else if f.ExcludedDir(part) {
			return true
		}
	}
	return false
}
