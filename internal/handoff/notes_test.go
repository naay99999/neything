package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
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

func TestListNotesAcrossRepos(t *testing.T) {
	ney := t.TempDir()
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	repoA := filepath.Join(t.TempDir(), "alpha")
	repoB := filepath.Join(t.TempDir(), "beta")
	if _, err := WriteNote(ney, repoA, "claude-code", "\n# Handoff → codex: fix the webhook test\n## Next steps\n", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteNote(ney, repoB, "codex", "token sk-ant-api03-AAAABBBBCCCCDDDDEEEE leaked\nmore", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	refs, err := ListNotes(ney)
	if err != nil || len(refs) != 2 {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	b, a := refs[0], refs[1]
	if b.Root != repoB || b.Agent != "codex" || !b.At.Equal(t0.Add(time.Hour)) || b.Key != RepoKey(repoB) {
		t.Errorf("newest first, with its repo: %+v", b)
	}
	if strings.Contains(b.Title, "sk-ant-api03") || !strings.Contains(b.Title, "[redacted]") {
		t.Errorf("title not redacted: %q", b.Title)
	}
	if a.Title != "Handoff → codex: fix the webhook test" || a.ID != "2026-11-01T10-00-00Z-claude-code" {
		t.Errorf("title/id: %+v", a)
	}
	notes, _ := ReadNotes(ney, repoA)
	if len(notes) != 1 || notes[0].ID != a.ID {
		t.Errorf("ReadNotes id = %+v", notes)
	}
}

func TestListNotesWithoutRootFile(t *testing.T) {
	ney := t.TempDir()
	repo := filepath.Join(t.TempDir(), "old")
	if _, err := WriteNote(ney, repo, "codex", "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(NotesDir(ney, repo), rootFile)); err != nil {
		t.Fatal(err)
	}
	refs, _ := ListNotes(ney)
	if len(refs) != 1 || refs[0].Root != "" || refs[0].Key != RepoKey(repo) {
		t.Fatalf("refs = %+v", refs)
	}
	if refs, err := ListNotes(filepath.Join(ney, "missing")); err != nil || len(refs) != 0 {
		t.Errorf("missing dir: %+v %v", refs, err)
	}
}

func TestBriefPinNoteShowsOlderNote(t *testing.T) {
	s := baseSession()
	old := Note{ID: "x", Agent: "codex", At: s.Start.Add(-48 * time.Hour), Body: "PINNED sk-ant-api03-AAAABBBBCCCCDDDDEEEE"}
	b := Build("repo", []session.Session{s}, []Note{old}, neycontext.GitState{}, false, testNow)
	if b.Note != nil {
		t.Fatal("older note shown without pinning")
	}
	b.PinNote(old)
	out := b.RenderFull()
	if !strings.Contains(out, "PINNED") || strings.Contains(out, "sk-ant-api03") {
		t.Errorf("pinned note missing or unredacted:\n%s", out)
	}
}
