package neycontext

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Memory is one entry to be written under the ney memory directory.
type Memory struct {
	Title   string
	Content string
	Project string
	Tags    []string
	Date    time.Time
}

var (
	multiDash  = regexp.MustCompile(`-+`)
	multiSpace = regexp.MustCompile(` +`)
)

// yamlSpecials are the characters that carry structural meaning to a YAML
// parser at the start of, or anywhere inside, a plain scalar — including the
// flow-sequence punctuation `[`, `]` and `,` that would otherwise let a tag
// break out of the `tags: [a, b]` sequence. A value containing any of them
// gets double-quoted rather than dropped, so the user's meaning survives.
const yamlSpecials = ":#\"'[]{},&*!|>%@`\\"

// sanitizeScalar makes s safe to emit as a single-line YAML value.
//
// Project names and tags reach us through the MCP `remember` tool, i.e. from
// the LLM, i.e. potentially from attacker-planted text the LLM was reading
// (indirect prompt injection). A value carrying a line break could close the
// frontmatter block early and inject arbitrary keys — or a whole second
// document — into a file ney indexes and re-serves to every connected client
// on every later session, so this collapses every character that a parser
// could read as a line break (CR/LF, NUL and the rest of C0/C1, DEL, NEL,
// U+2028/U+2029) to a plain space. After this the value is guaranteed to be
// one line, which is what makes the quoting in yamlScalar unescapable.
func sanitizeScalar(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToValidUTF8(s, "") {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f,
			r == '\u2028', r == '\u2029', r == '\ufeff':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(multiSpace.ReplaceAllString(b.String(), " "))
}

// yamlScalar renders an already-sanitized (single-line) value as a YAML
// scalar: plain when it is unambiguous, double-quoted with `\` and `"`
// escaped when it contains anything structural. Quoting beats deleting for
// fidelity, and is safe precisely because sanitizeScalar removed every line
// break first — there is no way to terminate the quoted form early.
func yamlScalar(s string) string {
	if s != "" && !strings.ContainsAny(s, yamlSpecials) &&
		!strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "?") {
		return s
	}
	return `"` + yamlEscaper.Replace(s) + `"`
}

var yamlEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// slugify turns title into a filesystem-safe, lowercase slug: anything
// outside [a-z0-9-] becomes '-', runs of '-' collapse to one, leading and
// trailing '-' are trimmed, and the result is capped at 60 bytes. An empty
// result (e.g. a title with no ASCII alphanumerics, such as non-Latin
// script) falls back to "memory".
//
// This is an allow-list, not a deny-list, which is what makes it safe against
// a hostile title from `remember`: '/', '\', '.' and NUL all fall into the
// default arm and become '-', so the slug can never carry a path separator,
// a traversal segment, or a string terminator. The 60-byte cut can't split a
// rune either, because by that point every byte is one of [a-z0-9-].
func slugify(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	slug := multiDash.ReplaceAllString(b.String(), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	if slug == "" {
		slug = "memory"
	}
	return slug
}

// uniqueFilename returns base+".md" if that name doesn't already exist in
// dir, otherwise base+"-2.md", base+"-3.md", ... — the first that's free.
func uniqueFilename(dir, base string) (string, error) {
	name := base + ".md"
	for i := 2; ; i++ {
		_, err := os.Stat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d.md", base, i)
	}
}

// WriteMemory writes m as a new markdown file under dir (created with
// 0700 if missing), named "YYYY-MM-DD-<slug>.md" with a slug derived from
// m.Title (see slugify); a filename collision gets a "-2", "-3", ...
// suffix. The write is atomic (temp file + rename). It returns the full
// path written.
func WriteMemory(dir string, m Memory) (path string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	dateStr := date.Format("2006-01-02")

	base := dateStr + "-" + slugify(m.Title)
	filename, err := uniqueFilename(dir, base)
	if err != nil {
		return "", err
	}
	fullPath := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "date: %s\n", dateStr)
	// Project/Tags are caller-supplied (ultimately LLM-supplied) text — they
	// must go through sanitizeScalar+yamlScalar or they can break out of the
	// frontmatter block; see the note on sanitizeScalar. A value that
	// sanitizes down to nothing is dropped rather than emitted empty.
	if project := sanitizeScalar(m.Project); project != "" {
		fmt.Fprintf(&b, "project: %s\n", yamlScalar(project))
	}
	tags := make([]string, 0, len(m.Tags))
	for _, tag := range m.Tags {
		if tag = sanitizeScalar(tag); tag != "" {
			tags = append(tags, yamlScalar(tag))
		}
	}
	if len(tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(tags, ", "))
	}
	b.WriteString("---\n")
	// The body is written verbatim (bar the trim). A stray "---" in it is
	// harmless: every markdown frontmatter parser in this repo
	// (loader.parseFrontmatter, used by ObsidianLoader — the loader that
	// actually claims these files, since they start with "---") only honours
	// a block anchored at byte 0 and terminates it at the FIRST "\n---",
	// which is always the delimiter written on the line above. Obsidian
	// itself behaves the same way. Escaping body dashes would corrupt code
	// fences and thematic breaks for no security gain.
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n")

	if err := writeFileAtomic(fullPath, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return fullPath, nil
}
