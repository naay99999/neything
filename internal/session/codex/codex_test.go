package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/session"
)

const fixtureName = "rollout-2026-11-01T10-00-00-0190aaaa-1111-2222-3333-444455556666.jsonl"

func fixtureRef() session.Ref {
	return session.Ref{
		Agent: "codex", ID: "0190aaaa-1111-2222-3333-444455556666",
		Path: filepath.Join("testdata", "sessions", "2026", "11", "01", fixtureName),
	}
}

func loadPath(t *testing.T, path string, full bool) session.Session {
	t.Helper()
	s, err := New("").Load(session.Ref{Agent: "codex", ID: "x", Path: path}, full)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return s
}

func TestCodexLoad(t *testing.T) {
	s, err := New("").Load(fixtureRef(), true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "codex" || s.ID != "0190aaaa-1111-2222-3333-444455556666" {
		t.Errorf("agent/id = %q/%q", s.Agent, s.ID)
	}
	if s.Title != "" {
		t.Errorf("Title = %q, want empty", s.Title)
	}
	if s.Branch != "main" || s.Cwd != "/repo" {
		t.Errorf("Branch/Cwd = %q/%q", s.Branch, s.Cwd)
	}
	if s.PromptCount != 1 {
		t.Errorf("PromptCount = %d, want 1 (injected context and event_msg duplicates excluded)", s.PromptCount)
	}
	if s.FirstPrompt != "Fix the payment flow so refunds work" || s.LastPrompt != s.FirstPrompt {
		t.Errorf("prompts = %q / %q", s.FirstPrompt, s.LastPrompt)
	}
	if s.LastMessage != "Tests still failing in pay_test.go" {
		t.Errorf("LastMessage = %q, want task_complete.last_agent_message", s.LastMessage)
	}
	wantCmds := []session.CommandRun{{Command: "go test ./...", Failed: true}}
	if !reflect.DeepEqual(s.Commands, wantCmds) {
		t.Errorf("Commands = %+v", s.Commands)
	}
	wantFiles := []session.FileChange{
		{Path: "a.go", Ops: []string{"edit"}, Count: 1},
		{Path: "b.go", Ops: []string{"write"}, Count: 1},
	}
	if !reflect.DeepEqual(s.Files, wantFiles) {
		t.Errorf("Files = %+v", s.Files)
	}
	wantStart := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 1, 10, 0, 32, 0, time.UTC)
	if !s.Start.Equal(wantStart) || !s.End.Equal(wantEnd) {
		t.Errorf("Start/End = %v / %v", s.Start, s.End)
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
	if p, _ := New("").Load(fixtureRef(), false); len(p.Tail) != 0 {
		t.Errorf("non-full load filled Tail: %d", len(p.Tail))
	}
}

func TestCodexLoadFallbacksAndTolerance(t *testing.T) {
	s := loadPath(t, filepath.Join("testdata", "nocomplete.jsonl"), true)
	if s.LastMessage != "final reply" {
		t.Errorf("LastMessage = %q, want last assistant text", s.LastMessage)
	}
	want := []session.CommandRun{{Command: "make build", Failed: true}, {Command: "git status --short", Failed: false}}
	if !reflect.DeepEqual(s.Commands, want) {
		t.Errorf("Commands = %+v", s.Commands)
	}
	if s.Branch != "" || s.PromptCount != 1 {
		t.Errorf("Branch/PromptCount = %q/%d", s.Branch, s.PromptCount)
	}
}

func TestCodexLoadNotASession(t *testing.T) {
	_, err := New("").Load(session.Ref{Path: filepath.Join("testdata", "injectedonly.jsonl")}, true)
	if !errors.Is(err, session.ErrNotASession) {
		t.Errorf("err = %v, want ErrNotASession", err)
	}
}

func TestCommandText(t *testing.T) {
	cases := []struct{ args, want string }{
		{`{"command":["bash","-lc","go test ./..."]}`, "go test ./..."},
		{`{"command":["zsh","-c","ls -la"]}`, "ls -la"},
		{`{"command":["sh","-lc","echo a && echo b"]}`, "echo a && echo b"},
		{`{"command":["git","status"]}`, "git status"},
		{`{"cmd":"make test"}`, "make test"},
		{`{"command":"make lint"}`, "make lint"},
		{`not json`, ""},
		{`{}`, ""},
	}
	for _, tc := range cases {
		if got := commandText(tc.args); got != tc.want {
			t.Errorf("commandText(%s) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestCodexFailed(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{`{"output":"x","metadata":{"exit_code":1}}`, true},
		{`{"output":"x","metadata":{"exit_code":0}}`, false},
		{"Exit code: 2\nWall time: 1s\nOutput:\nboom", true},
		{"Exit code: 0\nOutput:\nok", false},
		{"plain output, mentions Exit code: 3 inline", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := failed(tc.out); got != tc.want {
			t.Errorf("failed(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

func TestCodexLoadPartialLargeFile(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-11-01T10:00:00.000Z","type":"session_meta","payload":{"id":"big","cwd":"/repo","git":{"branch":"dev"}}}` + "\n")
	b.WriteString(`{"timestamp":"2026-11-01T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first ask"}]}}` + "\n")
	pad := strings.Repeat("p", 500)
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, `{"timestamp":"2026-11-01T10:05:00.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"%s %d"}]}}`+"\n", pad, i)
	}
	b.WriteString(`{"timestamp":"2026-11-01T11:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"latest ask"}]}}` + "\n")
	b.WriteString(`{"timestamp":"2026-11-01T11:00:05.000Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"all done"}}` + "\n")
	path := filepath.Join(t.TempDir(), "rollout-big.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if b.Len() <= tailWindow*2 {
		t.Fatalf("fixture too small: %d", b.Len())
	}
	s := loadPath(t, path, false)
	if s.FirstPrompt != "first ask" || s.LastPrompt != "latest ask" || s.LastMessage != "all done" {
		t.Errorf("prompts/last = %q / %q / %q", s.FirstPrompt, s.LastPrompt, s.LastMessage)
	}
	if s.Branch != "dev" || s.Cwd != "/repo" {
		t.Errorf("Branch/Cwd = %q/%q (head metadata must survive a partial load)", s.Branch, s.Cwd)
	}
	if !s.End.Equal(time.Date(2026, 11, 1, 11, 0, 5, 0, time.UTC)) {
		t.Errorf("End = %v", s.End)
	}
}

func TestCodexRecent(t *testing.T) {
	home := t.TempDir()
	data, err := os.ReadFile(fixtureRef().Path)
	if err != nil {
		t.Fatal(err)
	}
	put := func(day, name, cwd string, mod time.Time) string {
		dir := filepath.Join(home, "sessions", day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		body := strings.Replace(string(data), `"cwd":"/repo"`, `"cwd":"`+cwd+`"`, -1)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	const uuid = "0190bbbb-0000-0000-0000-00000000000"
	newer := put("2026/11/02", "rollout-2026-11-02T09-00-00-"+uuid+"2.jsonl", "/repo/sub", since.Add(48*time.Hour))
	older := put("2026/11/01", "rollout-2026-11-01T10-00-00-"+uuid+"1.jsonl", "/repo", since.Add(10*time.Hour))
	put("2026/11/02", "rollout-2026-11-02T10-00-00-"+uuid+"3.jsonl", "/other", since.Add(50*time.Hour))
	put("2026/11/02", "rollout-2026-11-02T11-00-00-"+uuid+"4.jsonl", "/repository", since.Add(50*time.Hour))
	put("2026/10/05", "rollout-2026-10-05T10-00-00-"+uuid+"5.jsonl", "/repo", since.Add(60*time.Hour)) // date dir before since
	put("2026/11/01", "rollout-2026-11-01T01-00-00-"+uuid+"6.jsonl", "/repo", since.Add(-time.Hour))   // mtime before since
	put("2026/11/02", "notes.txt", "/repo", since.Add(50*time.Hour))

	refs, err := New(home).Recent("/repo", since)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Path != newer || refs[1].Path != older {
		t.Errorf("order/paths = %q, %q", refs[0].Path, refs[1].Path)
	}
	if refs[0].Agent != "codex" || refs[0].ID != uuid+"2" || !refs[0].Modified.Equal(since.Add(48*time.Hour)) {
		t.Errorf("ref = %+v", refs[0])
	}

	if refs, err := New(filepath.Join(home, "missing")).Recent("/repo", since); err != nil || len(refs) != 0 {
		t.Errorf("missing home: %v %v", refs, err)
	}
}

func TestHomeHonorsCodexHome(t *testing.T) {
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if got, ok := Home(); !ok || got != dir {
		t.Errorf("Home() = %q, %v; want %q, true", got, ok, dir)
	}

	t.Setenv("CODEX_HOME", filepath.Join(dir, "missing"))
	if got, ok := Home(); ok {
		t.Errorf("Home() with missing CODEX_HOME = %q, true; want not ok", got)
	}

	t.Setenv("CODEX_HOME", "")
	if _, ok := Home(); ok {
		t.Error("Home() ok without ~/.codex")
	}
	if err := os.Mkdir(filepath.Join(fake, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Home(); !ok || got != filepath.Join(fake, ".codex") {
		t.Errorf("Home() = %q, %v", got, ok)
	}
}
