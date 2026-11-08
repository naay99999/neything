package handoff

import (
	"strings"
	"testing"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
)

var testNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func baseSession() session.Session {
	return session.Session{
		Agent:       "claude-code",
		ID:          "s1",
		Title:       "Add payments",
		Branch:      "feat/pay",
		Start:       testNow.Add(-3 * time.Hour),
		End:         testNow.Add(-2 * time.Hour),
		FirstPrompt: "implement stripe checkout",
		LastPrompt:  "now fix the failing webhook test",
		LastMessage: "I updated the handler but the webhook test still fails.",
	}
}

func TestBuildRedacts(t *testing.T) {
	s := baseSession()
	s.LastPrompt = "use key sk-ant-api03-AAAABBBBCCCCDDDDEEEE please"
	s.Commands = []session.CommandRun{{Command: "export DB_PASSWORD=hunter2hunter2", Failed: true}}
	s.Tail = []session.TailEntry{{Kind: "command", Head: "export DB_PASSWORD=hunter2hunter2", Body: "sk-ant-api03-AAAABBBBCCCCDDDDEEEE", At: testNow}}
	b := Build("repo", []session.Session{s}, nil, neycontext.GitState{}, false, testNow)
	for name, out := range map[string]string{"inject": b.RenderInject(), "full": b.RenderFull()} {
		for _, secret := range []string{"sk-ant-api03-AAAABBBBCCCCDDDDEEEE", "hunter2hunter2"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s leaks %q:\n%s", name, secret, out)
			}
		}
	}
}

func TestBuildNoteOrdering(t *testing.T) {
	s := baseSession()
	newer := Note{Agent: "codex", At: s.End.Add(time.Minute), Body: "NOTE-BODY remember the migration"}
	older := Note{Agent: "codex", At: s.End.Add(-time.Minute), Body: "OLD-NOTE"}

	b := Build("repo", []session.Session{s}, []Note{newer, older}, neycontext.GitState{}, false, testNow)
	if b.Note == nil {
		t.Fatal("newer note dropped")
	}
	out := b.RenderInject()
	ni, gi := strings.Index(out, "NOTE-BODY"), strings.Index(out, "Goal:")
	if ni < 0 || gi < 0 || ni > gi {
		t.Errorf("note must precede Goal:\n%s", out)
	}

	b = Build("repo", []session.Session{s}, []Note{older}, neycontext.GitState{}, false, testNow)
	if b.Note != nil {
		t.Errorf("older note kept: %+v", b.Note)
	}

	b = Build("repo", nil, []Note{older}, neycontext.GitState{}, false, testNow)
	if b.Note == nil || b.Empty() || b.RenderInject() == "" {
		t.Errorf("note alone should make a brief")
	}
}

func TestBuildLimitsOthersAndTrimsHugeFields(t *testing.T) {
	var ss []session.Session
	for i := 0; i < 5; i++ {
		ss = append(ss, baseSession())
	}
	ss[0].LastMessage = strings.Repeat("é", 4<<20)
	b := Build("repo", ss, nil, neycontext.GitState{}, false, testNow)
	if len(b.Others) != 2 {
		t.Errorf("others = %d", len(b.Others))
	}
	if len(b.Primary.LastMessage) > maxFieldBytes {
		t.Errorf("not pre-trimmed: %d", len(b.Primary.LastMessage))
	}
	if !strings.HasSuffix(b.Primary.LastMessage, "é") {
		t.Error("cut inside a rune")
	}
}

func TestEmpty(t *testing.T) {
	b := Build("repo", nil, nil, neycontext.GitState{}, true, testNow)
	if !b.Empty() || b.RenderInject() != "" || b.RenderFull() != "" {
		t.Error("empty brief must render nothing")
	}
}
