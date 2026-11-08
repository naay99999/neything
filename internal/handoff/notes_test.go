package handoff

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteReadNotes(t *testing.T) {
	neyDir := t.TempDir()
	repo := "/Users/me/work/my repo"
	t1 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	p1, err := WriteNote(neyDir, repo, "claude-code", "first body\nline two", t1)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := WriteNote(neyDir, repo, "codex", "second body", t2)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{p1, p2} {
		if filepath.Dir(p) != NotesDir(neyDir, repo) {
			t.Errorf("note %s not under %s", p, NotesDir(neyDir, repo))
		}
	}
	if strings.ContainsAny(RepoKey(repo), "/ ") {
		t.Errorf("RepoKey not filesystem safe: %q", RepoKey(repo))
	}

	notes, err := ReadNotes(neyDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes", len(notes))
	}
	if notes[0].Body != "second body" || notes[0].Agent != "codex" || !notes[0].At.Equal(t2) {
		t.Errorf("newest wrong: %+v", notes[0])
	}
	if notes[1].Body != "first body\nline two" || notes[1].Agent != "claude-code" {
		t.Errorf("oldest wrong: %+v", notes[1])
	}
}

func TestWriteNoteSameSecondDoesNotOverwrite(t *testing.T) {
	neyDir := t.TempDir()
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	for _, body := range []string{"a", "b"} {
		if _, err := WriteNote(neyDir, "/r", "x", body, now); err != nil {
			t.Fatal(err)
		}
	}
	notes, _ := ReadNotes(neyDir, "/r")
	if len(notes) != 2 || notes[0].Body != "b" {
		t.Fatalf("got %+v", notes)
	}
}

func TestReadNotesMissingDir(t *testing.T) {
	notes, err := ReadNotes(t.TempDir(), "/nope")
	if notes != nil || err != nil {
		t.Fatalf("got %v, %v", notes, err)
	}
}

func TestWriteNoteCapsLongAgentName(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteNote(dir, "/r", strings.Repeat("x", 300), "body", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(filepath.Base(path)); n > 100 {
		t.Errorf("file name too long: %d", n)
	}
}
