package loader

import (
	"context"
	"strings"
	"testing"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) LookPath(name string) (string, error) {
	if name == "git" {
		return "/usr/bin/git", nil
	}
	return "", context.Canceled
}

func (m *mockRunner) Run(name string, args ...string) (string, error) {
	key := name + " " + stringsJoin(args, " ")
	if out, ok := m.responses[key]; ok {
		return out, nil
	}
	return "", nil
}

func stringsJoin(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}

func TestGitHistoryLoader(t *testing.T) {
	runner := &mockRunner{responses: map[string]string{
		"git -C /repo rev-parse --git-dir": ".git\n",
		"git -C /repo log -n 2 --pretty=format:%H%x09%h%x09%an%x09%ad%x09%s --date=short":
			"abc123full\tabc123\tAlice\t2024-01-01\tInitial commit\n" +
				"def456full\tdef456\tBob\t2024-01-02\tAdd billing\n",
		"git -C /repo show --name-status --pretty=format: abc123full": "A\tREADME.md\n",
		"git -C /repo show --name-status --pretty=format: def456full": "M\tbilling.md\n",
	}}
	l := &GitHistoryLoader{RecentCommits: 2, runner: runner}
	docs, err := l.LoadRepo(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	if docs[0].Type != "git" {
		t.Fatalf("expected git type, got %s", docs[0].Type)
	}
	if docs[0].Hash != "abc123full" {
		t.Fatalf("expected full hash, got %s", docs[0].Hash)
	}
	if !strings.Contains(docs[0].Content, "Initial commit") {
		t.Fatalf("missing subject: %q", docs[0].Content)
	}
}
