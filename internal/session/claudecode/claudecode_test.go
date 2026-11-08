package claudecode

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/session"
)

func ref(name string) session.Ref {
	return session.Ref{Agent: "claude-code", ID: strings.TrimSuffix(name, ".jsonl"), Path: filepath.Join("testdata", name)}
}

func load(t *testing.T, name string, full bool) session.Session {
	t.Helper()
	s, err := New("").Load(ref(name), full)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return s
}

func TestLoadBasic(t *testing.T) {
	s := load(t, "basic.jsonl", true)
	if s.Agent != "claude-code" || s.ID != "basic" {
		t.Errorf("agent/id = %q/%q", s.Agent, s.ID)
	}
	if s.Title != "Fix payment flow" {
		t.Errorf("Title = %q", s.Title)
	}
	if s.Branch != "feat/pay" || s.Cwd != "/repo" {
		t.Errorf("Branch/Cwd = %q/%q", s.Branch, s.Cwd)
	}
	if s.PromptCount != 2 {
		t.Errorf("PromptCount = %d", s.PromptCount)
	}
	if s.FirstPrompt != "Fix the payment flow so refunds work" || s.LastPrompt != "Now run the tests please" {
		t.Errorf("prompts = %q / %q", s.FirstPrompt, s.LastPrompt)
	}
	if s.LastMessage != "Done: tests still failing in pay_test.go" {
		t.Errorf("LastMessage = %q", s.LastMessage)
	}
	wantFiles := []session.FileChange{
		{Path: "/repo/a.go", Ops: []string{"edit"}, Count: 2},
		{Path: "/repo/b.go", Ops: []string{"write"}, Count: 1},
	}
	if !reflect.DeepEqual(s.Files, wantFiles) {
		t.Errorf("Files = %+v", s.Files)
	}
	wantCmds := []session.CommandRun{{Command: "go test ./...", Failed: true}, {Command: "ls", Failed: false}}
	if !reflect.DeepEqual(s.Commands, wantCmds) {
		t.Errorf("Commands = %+v", s.Commands)
	}
	wantStart := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 1, 10, 1, 10, 0, time.UTC)
	if !s.Start.Equal(wantStart) || !s.End.Equal(wantEnd) {
		t.Errorf("Start/End = %v / %v", s.Start, s.End)
	}
	if len(s.Tail) == 0 {
		t.Fatal("full load should fill Tail")
	}
	kinds := map[string]bool{}
	for _, e := range s.Tail {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"user", "assistant", "command", "file", "failure"} {
		if !kinds[k] {
			t.Errorf("Tail missing kind %q", k)
		}
	}
}

func TestLoadSidechainAndMetaOnly(t *testing.T) {
	for _, name := range []string{"sidechain.jsonl", "metaonly.jsonl"} {
		if _, err := New("").Load(ref(name), true); !errors.Is(err, session.ErrNotASession) {
			t.Errorf("%s: err = %v, want ErrNotASession", name, err)
		}
	}
}

func TestLoadTruncated(t *testing.T) {
	s := load(t, "truncated.jsonl", true)
	if s.PromptCount != 2 {
		t.Errorf("PromptCount = %d", s.PromptCount)
	}
	if s.LastMessage != "Looking at the code." {
		t.Errorf("partial final line leaked into LastMessage: %q", s.LastMessage)
	}
}

func TestLoadLongLines(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := func(n int) string {
		return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"zz","content":"` +
			strings.Repeat("a", n) + `"}]},"cwd":"/repo","timestamp":"2026-11-01T10:01:05.000Z"}` + "\n"
	}
	p := filepath.Join(t.TempDir(), "long.jsonl")
	data := string(base) + line(2<<20) + line(17<<20)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{true, false} {
		s, err := New("").Load(session.Ref{Agent: "claude-code", ID: "long", Path: p}, full)
		if err != nil {
			t.Fatalf("full=%v: %v", full, err)
		}
		if !full {
			// The tail window lies inside the oversize line, so only the
			// head contributes; the session must still load.
			if s.FirstPrompt != "Fix the payment flow so refunds work" {
				t.Errorf("partial: FirstPrompt = %q", s.FirstPrompt)
			}
			continue
		}
		if s.PromptCount != 2 || s.Title != "Fix payment flow" {
			t.Errorf("full: PromptCount=%d Title=%q", s.PromptCount, s.Title)
		}
	}
}

func TestLoadPartialMatchesFull(t *testing.T) {
	full := load(t, "basic.jsonl", true)
	part := load(t, "basic.jsonl", false)
	if len(part.Tail) != 0 {
		t.Errorf("partial Tail = %d entries", len(part.Tail))
	}
	full.Tail = nil
	if !reflect.DeepEqual(full, part) {
		t.Errorf("partial != full\nfull: %+v\npart: %+v", full, part)
	}
}

func TestLoadPartialLargeFile(t *testing.T) {
	// A file bigger than the tail window: head supplies the first prompt,
	// the tail window supplies the end state.
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"first ask"},"cwd":"/repo","gitBranch":"main","timestamp":"2026-11-01T10:00:00.000Z"}` + "\n")
	filler := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("f", 1000) + `"}]},"cwd":"/repo","timestamp":"2026-11-01T10:00:01.000Z"}` + "\n"
	for b.Len() < 600<<10 {
		b.WriteString(filler)
	}
	b.WriteString(`{"type":"user","message":{"role":"user","content":"last ask"},"cwd":"/repo","gitBranch":"dev","timestamp":"2026-11-01T11:00:00.000Z"}` + "\n")
	b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"all done"}]},"cwd":"/repo","timestamp":"2026-11-01T11:00:05.000Z"}` + "\n")
	p := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New("").Load(session.Ref{Agent: "claude-code", ID: "big", Path: p}, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.FirstPrompt != "first ask" || s.LastPrompt != "last ask" || s.LastMessage != "all done" || s.Branch != "dev" {
		t.Errorf("got %+v", s)
	}
	if s.PromptCount != 2 {
		t.Errorf("PromptCount = %d, want 2 (no double count)", s.PromptCount)
	}
	if s.Start.Hour() != 10 || s.End.Hour() != 11 {
		t.Errorf("Start/End = %v / %v", s.Start, s.End)
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("/Users/x/my.app/ไทย repo"); got != "-Users-x-my-app-----repo" {
		t.Errorf("Slug = %q", got)
	}
}

func TestRecent(t *testing.T) {
	root := "/work/proj"
	pd := t.TempDir()
	now := time.Now()
	entry := func(cwd string) string {
		return `{"type":"mode","x":1}` + "\n" + `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hi"}}` + "\n"
	}
	write := func(dir, name, content string, age time.Duration) string {
		d := filepath.Join(pd, dir)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldest := write(Slug(root), "old.jsonl", entry(root), 3*time.Hour)
	_ = oldest
	newest := write(Slug(root), "new.jsonl", entry(root), time.Minute)
	sub := write(Slug(root+"/sub"), "sub.jsonl", entry(root+"/sub"), 10*time.Minute)
	write(Slug(root+"-other"), "other.jsonl", entry(root+"-other"), time.Minute)                          // prefix dir, cwd outside
	write(Slug(root), "elsewhere.jsonl", entry("/somewhere/else"), time.Minute)                           // cwd outside root
	write(Slug(root), "nocwd.jsonl", strings.Repeat(`{"type":"mode"}`+"\n", 70)+entry(root), time.Minute) // cwd past 64 lines
	write(filepath.Join(Slug(root), "sess", "subagents"), "agent.jsonl", entry(root), time.Minute)
	write("-unrelated", "u.jsonl", entry(root), time.Minute)
	write(Slug(root), "notes.txt", "x", time.Minute)

	refs, err := New(pd).Recent(root, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Path)
		if r.Agent != "claude-code" {
			t.Errorf("Agent = %q", r.Agent)
		}
	}
	want := []string{newest, sub}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Recent = %v\nwant %v", got, want)
	}
	if refs[0].ID != "new" {
		t.Errorf("ID = %q", refs[0].ID)
	}
}
