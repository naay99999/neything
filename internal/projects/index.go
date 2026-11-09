package projects

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	neycontext "github.com/naay99999/neything/internal/context"
)

const (
	noCard         = "(no card yet)"
	firstSentenceN = 120
)

// Item is one row of the table of contents.
type Item struct {
	Name, Path, Summary string
	HasCard             bool
	Activity            time.Time
}

// FirstSentence returns s up to and including its first sentence end, capped
// at 120 runes ("…" marks a cut).
func FirstSentence(s string) string {
	s = strings.TrimSpace(s)
	cut := len(s)
	for _, sep := range []string{". ", "。"} {
		if i := strings.Index(s, sep); i >= 0 && i+len(strings.TrimRight(sep, " ")) < cut {
			cut = i + len(strings.TrimRight(sep, " "))
		}
	}
	s = s[:cut]
	if utf8.RuneCountInString(s) > firstSentenceN {
		s = string([]rune(s)[:firstSentenceN-1]) + "…"
	}
	return s
}

// tildePath shows a path under the home directory as "~/...".
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return filepath.Join("~", rel)
	}
	return p
}

func (it Item) blurb() string {
	if !it.HasCard || strings.TrimSpace(it.Summary) == "" {
		return noCard
	}
	return FirstSentence(it.Summary)
}

// TOCLine is one table-of-contents line:
// "<name> · <path> · <first sentence | (no card yet)> · <last activity>".
func TOCLine(it Item, now time.Time) string {
	return strings.Join([]string{it.Name, tildePath(it.Path), it.blurb(), neycontext.RelativeTime(it.Activity, now)}, " · ")
}

// WriteIndex writes dir/index.md, an Obsidian-friendly table of contents
// linking each card with [[wikilinks]], sorted by name.
func WriteIndex(dir string, items []Item) error {
	sorted := append([]Item(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	b.WriteString("# Projects\n")
	for _, it := range sorted {
		b.WriteString("- [[" + it.Name + "]] — " + it.blurb() + " · " + tildePath(it.Path) + "\n")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return neycontext.WriteFileAtomic(filepath.Join(dir, "index.md"), []byte(b.String()), 0o600)
}
