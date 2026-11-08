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
)

// Note is a handoff note an agent wrote deliberately via write_handoff.
type Note struct {
	Agent string
	At    time.Time
	Body  string
}

const (
	noteStampLayout = "2006-01-02T15-04-05Z"
	maxNoteFileRead = 64 << 10
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
	dir := NotesDir(neyDir, repoRoot)
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
			Agent: strings.TrimSuffix(name[len(noteStampLayout)+1:], ".md"),
			At:    at,
			Body:  string(data),
		})
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	return notes, nil
}
