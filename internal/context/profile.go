package neycontext

import (
	"os"
	"path/filepath"
	"strings"
)

// profileTemplate is written the first time LoadProfile is called against
// a path that doesn't exist yet. Sections match what the setup wizard (and
// update_profile) address by name.
const profileTemplate = `# Profile

## Name & role
(Who are you, and what's your role?)

## Current focus
(What are you working on right now?)

## Working style
(Preferences, conventions, tools you like — anything AI should know.)
`

// LoadProfile reads the profile at path. If the file doesn't exist, it
// writes profileTemplate there (mode 0600) and returns it with created
// set to true. Any other read/write error is returned; per the "Layer 1
// must never fail" principle, callers should treat a non-nil err as "no
// profile available" rather than fail the whole request.
func LoadProfile(path string) (content string, created bool, err error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), false, nil
	}
	if !os.IsNotExist(err) {
		return "", false, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, err
	}
	if err := writeFileAtomic(path, []byte(profileTemplate), 0o600); err != nil {
		return "", false, err
	}
	return profileTemplate, true, nil
}

// profileSection is one "## Header" block of a profile.md, plus the
// unheaded preamble that precedes the first heading (e.g. a top-level
// "# Profile" title).
type profileSection struct {
	header string
	body   string
}

// parseSections splits content on lines starting with "## ". Everything
// before the first such heading is returned as preamble.
func parseSections(content string) (preamble string, sections []profileSection) {
	lines := strings.Split(content, "\n")
	var pre []string
	var cur []string
	curIdx := -1

	flush := func() {
		if curIdx >= 0 {
			sections[curIdx].body = strings.Trim(strings.Join(cur, "\n"), "\n")
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			sections = append(sections, profileSection{header: strings.TrimSpace(strings.TrimPrefix(line, "## "))})
			curIdx = len(sections) - 1
			cur = nil
			continue
		}
		if curIdx == -1 {
			pre = append(pre, line)
		} else {
			cur = append(cur, line)
		}
	}
	flush()

	preamble = strings.TrimRight(strings.Join(pre, "\n"), "\n")
	return preamble, sections
}

// renderSections is the inverse of parseSections.
func renderSections(preamble string, sections []profileSection) string {
	var b strings.Builder
	if preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}
	for i, s := range sections {
		b.WriteString("## ")
		b.WriteString(s.header)
		b.WriteString("\n")
		if s.body != "" {
			b.WriteString(s.body)
			b.WriteString("\n")
		}
		if i != len(sections)-1 {
			b.WriteString("\n")
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return ""
	}
	return out + "\n"
}

// UpdateProfile edits one named section of the profile at path: replacing
// its body (appendMode false) or appending content to its existing body
// (appendMode true). If no section with that name exists, a new "##
// <section>" block is appended at EOF. The file is created if missing.
// The write is atomic (temp file + rename).
func UpdateProfile(path, section, content string, appendMode bool) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	existing := ""
	if err == nil {
		existing = string(data)
	}

	preamble, sections := parseSections(existing)
	content = strings.TrimSpace(content)

	found := false
	for i := range sections {
		if strings.EqualFold(sections[i].header, section) {
			found = true
			if appendMode && sections[i].body != "" {
				sections[i].body = sections[i].body + "\n\n" + content
			} else {
				sections[i].body = content
			}
			break
		}
	}
	if !found {
		sections = append(sections, profileSection{header: section, body: content})
	}

	out := renderSections(preamble, sections)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(out), 0o600)
}
