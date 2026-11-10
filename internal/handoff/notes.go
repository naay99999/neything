package handoff

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/redact"
)

// Note is a handoff note an agent wrote deliberately via write_handoff.
type Note struct {
	ID    string // file name without .md: "<UTC stamp>-<agent>"
	Agent string
	At    time.Time
	Body  string
}

const (
	noteStampLayout = "2006-01-02T15-04-05Z"
	maxNoteFileRead = 64 << 10
	// rootFile in a notes directory records the repo root it belongs to;
	// RepoKey is lossy, so the root cannot be recovered from the name.
	rootFile     = ".root"
	noteTitleMax = 120
)

// RepoKey turns a repo root into a filesystem-safe directory name. It follows
// the same rule as the Claude Code project slug: every rune outside
// [A-Za-z0-9] becomes '-'.
func RepoKey(repoRoot string) string {
	var sb strings.Builder
	for _, r := range repoRoot {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

// NotesDir is where notes for repoRoot live.
func NotesDir(neyDir, repoRoot string) string {
	return filepath.Join(neyDir, "handoff", RepoKey(repoRoot))
}

// WriteNote stores body as a new note file and returns its path. Two notes in
// the same second never overwrite each other: the later one is stamped one
// second ahead.
func WriteNote(neyDir, repoRoot, agent, body string, now time.Time) (string, error) {
	dir := NotesDir(neyDir, repoRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, rootFile)); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(filepath.Join(dir, rootFile), []byte(repoRoot), 0o600) // best effort: only labels the list
	}
	agent = trimBytes(RepoKey(agent), 32)
	if agent == "" {
		agent = "agent"
	}
	t := now.UTC().Truncate(time.Second)
	for i := 0; i < 1000; i++ {
		path := filepath.Join(dir, t.Format(noteStampLayout)+"-"+agent+".md")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			t = t.Add(time.Second)
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.WriteString(body); err != nil {
			f.Close()
			return "", err
		}
		return path, f.Close()
	}
	return "", fmt.Errorf("handoff: no free note name in %s", dir)
}

// ReadNotes returns the repo's notes, newest first. A missing directory is not
// an error. Files that don't follow the naming scheme are skipped.
func ReadNotes(neyDir, repoRoot string) ([]Note, error) {
	return readNotesDir(NotesDir(neyDir, repoRoot))
}

func readNotesDir(dir string) ([]Note, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var notes []Note
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".md") || len(name) <= len(noteStampLayout)+4 {
			continue
		}
		at, err := time.Parse(noteStampLayout, name[:len(noteStampLayout)])
		if err != nil || name[len(noteStampLayout)] != '-' {
			continue
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxNoteFileRead))
		f.Close()
		if err != nil {
			continue
		}
		notes = append(notes, Note{
			ID:    strings.TrimSuffix(name, ".md"),
			Agent: strings.TrimSuffix(name[len(noteStampLayout)+1:], ".md"),
			At:    at,
			Body:  string(data),
		})
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	return notes, nil
}

// NoteRef describes one stored note for a list across repos.
type NoteRef struct {
	ID    string
	Agent string
	At    time.Time
	Title string // first non-empty line, redacted and flattened, ≤120 runes
	Root  string // repo root recorded when the note was written, or ""
	Key   string // the notes directory name, RepoKey(root)
}

// ListNotes returns every stored note across repos, newest first. Titles are
// redacted here: note bodies are stored as the agent wrote them. A missing
// handoff directory is not an error; unreadable repo directories are skipped.
func ListNotes(neyDir string) ([]NoteRef, error) {
	base := filepath.Join(neyDir, "handoff")
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []NoteRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		notes, err := readNotesDir(dir)
		if err != nil {
			continue
		}
		root := ""
		if b, err := os.ReadFile(filepath.Join(dir, rootFile)); err == nil && RepoKey(string(b)) == e.Name() {
			root = string(b)
		}
		for _, n := range notes {
			refs = append(refs, NoteRef{
				ID: n.ID, Agent: n.Agent, At: n.At, Root: root, Key: e.Name(),
				Title: noteTitle(n.Body),
			})
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].At.After(refs[j].At) })
	return refs, nil
}

func noteTitle(body string) string {
	for _, line := range strings.Split(trimBytes(body, 2*maxFieldBytes), "\n") {
		if t := inline(redact.String(line)); t != "" {
			return clipRunes(t, noteTitleMax)
		}
	}
	return ""
}
