package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/handoff"
)

type listedHandoff struct {
	N       int    `json:"n"`
	Project string `json:"project"`
	Cwd     string `json:"cwd"`
	Title   string `json:"title"`
	Agent   string `json:"agent"`
	Written string `json:"written"`
	NoteID  string `json:"note_id"`
	Current bool   `json:"current"`
}

// twoRepoNotes writes an older note in pay (the fixture repo) and a newer
// one in a second repo, portfolio.
func twoRepoNotes(t *testing.T) (*mcpTestEnv, string, string) {
	t.Helper()
	env, pay := handoffEnv(t)
	portfolio := filepath.Join(filepath.Dir(pay), "portfolio")
	gitInit(t, portfolio)
	now := time.Now()
	if _, err := handoff.WriteNote(config.NeyDir(), pay, "claude-code", "# Handoff → codex: OLD-PAY refund webhook\nbody", now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := handoff.WriteNote(config.NeyDir(), portfolio, "codex", "# Handoff → next session: NEW-PORTFOLIO hero section", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return env, pay, portfolio
}

func listHandoffs(t *testing.T, env *mcpTestEnv, args map[string]any) []listedHandoff {
	t.Helper()
	var out []listedHandoff
	if err := json.Unmarshal([]byte(callText(t, env, "list_handoffs", args)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestListHandoffsAllProjects(t *testing.T) {
	env, pay, portfolio := twoRepoNotes(t)
	got := listHandoffs(t, env, map[string]any{"cwd": pay})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].N != 1 || got[0].Project != "portfolio" || got[0].Cwd != portfolio || got[0].Current ||
		got[0].Title != "Handoff → next session: NEW-PORTFOLIO hero section" || got[0].Agent != "codex" || got[0].Written != "1h ago" {
		t.Errorf("newest: %+v", got[0])
	}
	if got[1].N != 2 || got[1].Project != "payrepo" || got[1].Cwd != pay || !got[1].Current || got[1].NoteID == "" {
		t.Errorf("older, current repo: %+v", got[1])
	}
}

func TestListHandoffsByProject(t *testing.T) {
	env, _, _ := twoRepoNotes(t)
	got := listHandoffs(t, env, map[string]any{"project": "PayRepo"})
	if len(got) != 1 || got[0].Project != "payrepo" || !strings.Contains(got[0].Title, "OLD-PAY") {
		t.Fatalf("got %+v", got)
	}
	msg := callText(t, env, "list_handoffs", map[string]any{"project": "portfolo"})
	if !strings.Contains(msg, `no handoff notes for "portfolo"`) || !strings.Contains(msg, "portfolio") {
		t.Errorf("unknown project: %q", msg)
	}
}

func TestListHandoffsEmpty(t *testing.T) {
	env, _ := handoffEnv(t)
	if msg := callText(t, env, "list_handoffs", map[string]any{}); msg != "no handoff notes yet" {
		t.Errorf("got %q", msg)
	}
}

func TestGetHandoffPicksNote(t *testing.T) {
	env, pay, _ := twoRepoNotes(t)
	id := listHandoffs(t, env, map[string]any{"project": "payrepo"})[0].NoteID
	// The fixture session is newer than the note, so without note_id it is hidden.
	if out := callText(t, env, "get_handoff", map[string]any{"cwd": pay}); strings.Contains(out, "OLD-PAY") {
		t.Fatalf("stale note shown by default:\n%s", out)
	}
	out := callText(t, env, "get_handoff", map[string]any{"cwd": pay, "note_id": id})
	if !strings.Contains(out, "OLD-PAY refund webhook") {
		t.Errorf("picked note missing:\n%s", out)
	}
	if msg := callToolExpectError(t, env, "get_handoff", map[string]any{"cwd": pay, "note_id": "nope"}); !strings.Contains(msg, "note nope not found") {
		t.Errorf("unknown note: %q", msg)
	}
}
