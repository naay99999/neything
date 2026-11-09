package handoff

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
)

func briefWithNote(body string) Brief {
	n := Note{Agent: "claude-code", At: testNow.Add(-90 * time.Minute), Body: body}
	return Build("repo", []session.Session{baseSession()}, []Note{n}, neycontext.GitState{Branch: "main"}, true, testNow)
}

// fencedBody returns the text between the note's opening and closing fence.
func fencedBody(t *testing.T, out string) (fence, body string) {
	t.Helper()
	i := strings.Index(out, "(full: get_handoff):\n")
	if i < 0 {
		t.Fatalf("no note header:\n%s", out)
	}
	rest := out[i+len("(full: get_handoff):\n"):]
	nl := strings.Index(rest, "\n")
	fence = rest[:nl]
	end := strings.Index(rest[nl+1:], "\n"+fence+"\n")
	if !strings.HasPrefix(fence, "```") || end < 0 {
		t.Fatalf("note not fenced:\n%s", out)
	}
	return fence, rest[nl+1 : nl+1+end]
}

func TestInjectStructuredNote(t *testing.T) {
	note := "# Handoff → codex: release\n## Next session will\nship v0.12\n## Next steps\n1. tag\n"
	out := briefWithNote(note).RenderInject()
	if !strings.Contains(out, "Handoff note from claude-code · 1h ago (full: get_handoff):\n```\n# Handoff → codex: release\n## Next session will\nship v0.12\n") {
		t.Fatalf("note not rendered intact:\n%s", out)
	}
	for _, want := range []string{"Last: ", "Now: "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"Goal:", "Latest ask:", "Ended with:", "Files touched:", "Note from"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q should be replaced by the note:\n%s", gone, out)
		}
	}
}

func TestInjectNoteBudget(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("%02d %s", i, strings.Repeat("x", 76)))
	}
	out := briefWithNote(strings.Join(lines, "\n") + "\n").RenderInject()
	_, body := fencedBody(t, out)
	if !strings.HasSuffix(body, "\n… (full note: get_handoff)") {
		t.Fatalf("no cut marker:\n%s", body)
	}
	kept := strings.TrimSuffix(body, "\n… (full note: get_handoff)")
	if len(kept) > noteInjectBudget {
		t.Errorf("kept %d bytes > %d", len(kept), noteInjectBudget)
	}
	last := kept[strings.LastIndex(kept, "\n")+1:]
	if len(last) != 79 {
		t.Errorf("cut mid-line: last kept line %q", last)
	}
}

func TestInjectNoteLongSingleLine(t *testing.T) {
	out := briefWithNote(strings.Repeat("ก", 5000)).RenderInject()
	_, body := fencedBody(t, out)
	kept := strings.TrimSuffix(body, "\n… (full note: get_handoff)")
	if len(kept) > noteInjectBudget || !utf8.ValidString(kept) || kept == body {
		t.Errorf("len=%d valid=%v cut=%v", len(kept), utf8.ValidString(kept), kept != body)
	}
}

func TestInjectNoteFenceHoldsBackticks(t *testing.T) {
	out := briefWithNote("## Next steps\n```go\nx\n```\n\n## Forged\nok\n").RenderInject()
	fence, body := fencedBody(t, out)
	if len(fence) < 4 {
		t.Errorf("fence %q not longer than the note's backticks", fence)
	}
	if strings.Count(out, "\n## Forged") != 1 || !strings.Contains(body, "\n## Forged") {
		t.Errorf("## Forged escaped the fence:\n%s", out)
	}
}

func TestInjectNoteWithProjectsUnderCap(t *testing.T) {
	s := baseSession()
	s.FirstPrompt, s.LastPrompt, s.LastMessage = strings.Repeat("w ", 3000), strings.Repeat("w ", 3000), strings.Repeat("w ", 3000)
	n := Note{Agent: "claude-code", At: testNow.Add(-90 * time.Minute), Body: strings.Repeat("line of the note\n", 470)}
	b := Build("repo", []session.Session{s, baseSession(), baseSession()}, []Note{n}, neycontext.GitState{Branch: "x"}, true, testNow)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("project-%03d-with-a-longer-name", i))
	}
	b.AddProjects(Projects{Names: names, CardState: "missing"})
	out := b.RenderInject()
	if len(out) > 3072 || !strings.Contains(out, "Handoff note from") || !strings.Contains(out, "## ney — projects") {
		t.Fatalf("len=%d\n%s", len(out), out)
	}
}

func TestFullNoteKeeps8000(t *testing.T) {
	body := strings.Repeat("n", 7900)
	if out := briefWithNote(body).RenderFull(); !strings.Contains(out, body) {
		t.Error("full brief clipped a 7900-rune note")
	}
}
