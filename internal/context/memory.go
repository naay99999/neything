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

var multiDash = regexp.MustCompile(`-+`)

// slugify turns title into a filesystem-safe, lowercase slug: anything
// outside [a-z0-9-] becomes '-', runs of '-' collapse to one, leading and
// trailing '-' are trimmed, and the result is capped at 60 bytes. An empty
// result (e.g. a title with no ASCII alphanumerics, such as non-Latin
// script) falls back to "memory".
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
	if m.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", m.Project)
	}
	if len(m.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(m.Tags, ", "))
	}
	b.WriteString("---\n")
	b.WriteString(strings.TrimSpace(m.Content))
	b.WriteString("\n")

	if err := writeFileAtomic(fullPath, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return fullPath, nil
}
