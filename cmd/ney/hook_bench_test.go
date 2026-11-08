package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/session/claudecode"
)

// bigTranscript builds an n-line Claude Code transcript with a realistic mix
// of prompts, Bash/Edit tool uses and tool results.
func bigTranscript(repo string, n int) string {
	var b strings.Builder
	base := time.Now().Add(-2 * time.Hour)
	common := func(i int) string {
		ts := base.Add(time.Duration(i) * time.Second).UTC().Format("2006-01-02T15:04:05.000Z")
		return fmt.Sprintf(`"cwd":%q,"gitBranch":"main","sessionId":"big","timestamp":%q,"isSidechain":false,"uuid":"u%d"`, repo, ts, i)
	}
	for i := 0; i < n; i++ {
		switch i % 5 {
		case 0:
			fmt.Fprintf(&b, `{"type":"user","message":{"role":"user","content":"Please continue with step %d of the refactor"},%s}`+"\n", i, common(i))
		case 1:
			fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Working on step %d now."}]},%s}`+"\n", i, common(i))
		case 2:
			fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t%d","name":"Edit","input":{"file_path":%q,"old_string":"a","new_string":"b"}}]},%s}`+"\n", i, filepath.Join(repo, fmt.Sprintf("f%d.go", i%40)), common(i))
		case 3:
			fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"go test ./pkg%d/..."}}]},%s}`+"\n", i, i%7, common(i))
		default:
			fmt.Fprintf(&b, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","content":"%s"}]},%s}`+"\n", i-1, strings.Repeat("output line ", 20), common(i))
		}
	}
	return b.String()
}

// hookBenchFixture builds a fake HOME with a repo holding one 4,000-line
// session and two older small ones.
func hookBenchFixture(tb testing.TB) (repo string) {
	tb.Helper()
	home, err := filepath.EvalSymlinks(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	tb.Setenv("HOME", home)
	repo = filepath.Join(home, "work", "bigrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		tb.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "projects", claudecode.Slug(repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	now := time.Now()
	put := func(id, body string, mtime time.Time) {
		p := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			tb.Fatal(err)
		}
	}
	put("big", bigTranscript(repo, 4000), now)
	put("old1", bigTranscript(repo, 200), now.Add(-24*time.Hour))
	put("old2", bigTranscript(repo, 200), now.Add(-48*time.Hour))
	return repo
}

func TestHookUnder300ms(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	repo := hookBenchFixture(t)
	stdin := hookStdin(t, repo, "startup", "new-session")
	start := time.Now()
	out, _, err := execHook(t, stdin)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "additionalContext") {
		t.Fatalf("hook produced no brief:\n%s", out)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("hook took %v, want < 300ms", elapsed)
	}
}

func BenchmarkHook(b *testing.B) {
	repo := hookBenchFixture(b)
	stdin := hookStdin(b, repo, "startup", "new-session")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, _, err := execHook(b, stdin)
		if err != nil || out == "" {
			b.Fatalf("err=%v out=%q", err, out)
		}
	}
}
