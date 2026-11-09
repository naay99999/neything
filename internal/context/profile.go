package neycontext

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	if err := WriteFileAtomic(path, []byte(profileTemplate), 0o600); err != nil {
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
//
// section is emitted as a "## <section>" heading line, so it goes through
// sanitizeScalar first — the same treatment remember gives its caller-supplied
// frontmatter values, and for the same reason. This name reaches us from the
// MCP update_profile tool, i.e. from the LLM, i.e. potentially from
// attacker-planted text the LLM was reading. A newline in it would close the
// heading and forge additional "## " sections in profile.md, which ney
// re-serves to every connected client (list_projects shows it).
func UpdateProfile(path, section, content string, appendMode bool) error {
	section = sanitizeScalar(section)
	if section == "" {
		return fmt.Errorf("section name is empty after sanitization")
	}

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
	return WriteFileAtomic(path, []byte(out), 0o600)
}

// sanitizeScalar makes s safe to emit as a single-line YAML value.
//
// Section names reach us through the MCP `update_profile` tool, i.e. from
// the LLM, i.e. potentially from attacker-planted text the LLM was reading
// (indirect prompt injection). A value carrying a line break could close the
// frontmatter block early and inject arbitrary keys — or a whole second
// document — into a file ney re-serves to every connected client
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

var multiSpace = regexp.MustCompile(` +`)
