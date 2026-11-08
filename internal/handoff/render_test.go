package handoff

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
)

var update = flag.Bool("update", false, "rewrite golden files")

func goldenBrief() Brief {
	s := baseSession()
	for i := 0; i < 12; i++ {
		fc := session.FileChange{Path: fmt.Sprintf("internal/pay/file%02d.go", i), Ops: []string{"edit"}, Count: 1 + i%3}
		if i == 1 {
			fc.Ops = []string{"edit", "write"}
		}
		s.Files = append(s.Files, fc)
	}
	s.Commands = []session.CommandRun{
		{Command: "go build ./...", Failed: false},
		{Command: "go test ./pay -run A", Failed: true},
		{Command: "go test ./pay -run B", Failed: true},
		{Command: "go vet ./...", Failed: false},
		{Command: "go test ./pay -run C", Failed: true},
		{Command: "go test ./pay -run D", Failed: true},
	}
	s.Tail = []session.TailEntry{
		{Kind: "user", Head: "fix the webhook", Body: "now fix the failing webhook test", At: testNow.Add(-130 * time.Minute)},
		{Kind: "command", Head: "go test ./pay", Body: "FAIL\n```\nnested fence\n```", At: testNow.Add(-125 * time.Minute)},
		{Kind: "assistant", Head: "reply", Body: "I updated the handler.", At: testNow.Add(-121 * time.Minute)},
	}
	o1, o2 := baseSession(), baseSession()
	o1.Agent, o1.Title, o1.End = "codex", "Refactor ledger", testNow.Add(-26*time.Hour)
	o2.Title, o2.End = "Docs pass", testNow.Add(-72*time.Hour)
	return Build("neything", []session.Session{s, o1, o2}, nil, neycontext.GitState{Branch: "feat/pay", Uncommitted: 3, CommitsSince: 1}, true, testNow)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s mismatch (rerun with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRenderInjectGolden(t *testing.T) {
	checkGolden(t, "inject.golden", goldenBrief().RenderInject())
}

func TestRenderFullGolden(t *testing.T) {
	checkGolden(t, "full.golden", goldenBrief().RenderFull())
}

func TestRenderInjectCap(t *testing.T) {
	s := baseSession()
	huge := strings.Repeat("word ", 4000)
	s.FirstPrompt, s.LastPrompt, s.LastMessage = huge, huge, huge
	for i := 0; i < 200; i++ {
		s.Files = append(s.Files, session.FileChange{Path: fmt.Sprintf("pkg/some/deep/dir/file%03d.go", i), Ops: []string{"edit"}, Count: 2})
	}
	o := baseSession()
	b := Build("repo", []session.Session{s, o, o}, nil, neycontext.GitState{Branch: "x", Uncommitted: 1}, true, testNow)
	out := b.RenderInject()
	if len(out) > 3072 {
		t.Errorf("len = %d", len(out))
	}
	if !strings.HasPrefix(out, "## ney — unfinished work in repo\n") || !strings.Contains(out, "Ended with:") {
		t.Errorf("lost heading or Ended with:\n%s", out)
	}
}

func TestRenderInjectCapMultibyte(t *testing.T) {
	s := baseSession()
	thai := strings.Repeat("ทดสอบ", 2000)
	s.FirstPrompt, s.LastPrompt, s.LastMessage = thai, thai, thai
	out := Build("repo", []session.Session{s}, nil, neycontext.GitState{}, false, testNow).RenderInject()
	if len(out) > 3072 {
		t.Errorf("len = %d", len(out))
	}
	if !strings.Contains(out, "Ended with:") {
		t.Error("lost Ended with:")
	}
}

func TestRenderFullCap(t *testing.T) {
	s := baseSession()
	for i := 0; i < 500; i++ {
		s.Tail = append(s.Tail, session.TailEntry{Kind: "assistant", Head: fmt.Sprintf("step-%03d", i), Body: strings.Repeat("x", 2048), At: testNow})
	}
	out := Build("repo", []session.Session{s}, nil, neycontext.GitState{}, false, testNow).RenderFull()
	if len(out) > 32768 {
		t.Errorf("len = %d", len(out))
	}
	if !strings.Contains(out, "step-499") {
		t.Error("newest tail entry missing")
	}
	if strings.Contains(out, "step-000") {
		t.Error("oldest tail entry present")
	}
}

func TestInlineEscaping(t *testing.T) {
	s := baseSession()
	s.Title = "x\n## Fake section"
	s.FirstPrompt = "## heading\n# another"
	s.Files = []session.FileChange{{Path: "a\n## Evil.go", Ops: []string{"edit"}, Count: 1}}
	s.Commands = []session.CommandRun{{Command: "echo `hi`\n## cmd", Failed: true}}
	b := Build("repo", []session.Session{s}, nil, neycontext.GitState{}, false, testNow)
	for name, out := range map[string]string{"inject": b.RenderInject(), "full": b.RenderFull()} {
		if !strings.Contains(out, "x ## Fake section") && !strings.Contains(out, "x Fake section") {
			t.Errorf("%s: title not flattened:\n%s", name, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "## Fake") || strings.HasPrefix(line, "## Evil") || strings.HasPrefix(line, "## cmd") || strings.HasPrefix(line, "# ") {
				t.Errorf("%s: forged structure %q", name, line)
			}
		}
	}
}

func TestInlineHelper(t *testing.T) {
	cases := map[string]string{
		"a\nb":        "a b",
		"  ## x  ":    "x",
		"a \t\r\n  b": "a b",
		"":            "",
	}
	for in, want := range cases {
		if got := inline(in); got != want {
			t.Errorf("inline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFenceLongerThanInnerBackticks(t *testing.T) {
	if f := fenceFor("a ````` b"); f != "``````" {
		t.Errorf("fence = %q", f)
	}
	if f := fenceFor("plain"); f != "```" {
		t.Errorf("fence = %q", f)
	}
}

func TestLatestAskOmittedWhenSameAsGoal(t *testing.T) {
	s := baseSession()
	s.LastPrompt = s.FirstPrompt
	b := Build("repo", []session.Session{s}, nil, neycontext.GitState{}, false, testNow)
	for name, out := range map[string]string{"inject": b.RenderInject(), "full": b.RenderFull()} {
		if strings.Contains(out, "Latest ask:") {
			t.Errorf("%s: Latest ask rendered although identical to goal:\n%s", name, out)
		}
		if !strings.Contains(out, "Goal: implement stripe checkout") {
			t.Errorf("%s: Goal missing:\n%s", name, out)
		}
	}
}

func TestTitleFallback(t *testing.T) {
	p := baseSession()
	p.Title, p.FirstPrompt = "", strings.Repeat("x", 80)
	o := baseSession()
	o.ID, o.Title, o.FirstPrompt = "s2", "", "refactor the\nrouter"
	b := Build("repo", []session.Session{p, o}, nil, neycontext.GitState{}, false, testNow)
	out := b.RenderFull()
	if !strings.Contains(out, `"`+strings.Repeat("x", 60)+`…"`) {
		t.Errorf("primary should fall back to FirstPrompt clipped to 60 runes:\n%s", out)
	}
	if !strings.Contains(out, `"refactor the router"`) {
		t.Errorf("other should fall back to FirstPrompt:\n%s", out)
	}
	p.Title = "Real title"
	b = Build("repo", []session.Session{p}, nil, neycontext.GitState{}, false, testNow)
	if o := b.RenderFull(); !strings.Contains(o, `"Real title"`) || strings.Contains(o, `"`+strings.Repeat("x", 60)) {
		t.Errorf("explicit Title must win:\n%s", o)
	}
}

func TestNowLineOnlyWhenGitOK(t *testing.T) {
	s := baseSession()
	b := Build("repo", []session.Session{s}, nil, neycontext.GitState{Uncommitted: 4, CommitsSince: 2}, false, testNow)
	if strings.Contains(b.RenderInject(), "Now:") {
		t.Error("Now: rendered without git")
	}
	b.GitOK = true
	if !strings.Contains(b.RenderInject(), "Now: 4 uncommitted files, 2 commits since that session") {
		t.Errorf("missing Now line:\n%s", b.RenderInject())
	}
}
