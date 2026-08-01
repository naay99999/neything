package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
)

func TestParseSelection(t *testing.T) {
	cases := []struct {
		in        string
		n         int
		wantIdx   []int
		wantPaths []string
	}{
		{"1,3", 5, []int{0, 2}, nil},
		{"1 3", 5, []int{0, 2}, nil},
		{"a", 3, []int{0, 1, 2}, nil},
		{"all", 2, []int{0, 1}, nil},
		{"2,~/notes", 3, []int{1}, []string{"~/notes"}},
		{"", 3, nil, nil},
		{"9", 3, nil, nil},          // out of range dropped
		{"1,1,1", 3, []int{0}, nil}, // deduped
	}
	for _, c := range cases {
		idx, paths := parseSelection(c.in, c.n)
		if fmt.Sprint(idx) != fmt.Sprint(c.wantIdx) || fmt.Sprint(paths) != fmt.Sprint(c.wantPaths) {
			t.Errorf("parseSelection(%q,%d) = %v,%v want %v,%v", c.in, c.n, idx, paths, c.wantIdx, c.wantPaths)
		}
	}
}

func TestWorkspaceNameFor(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := workspaceNameFor(db, "/Users/x/projects/docs"); got != "docs" {
		t.Fatalf("fresh name should be basename, got %q", got)
	}
	if _, err := db.UpsertWorkspace("docs", "/Users/x/projects/docs"); err != nil {
		t.Fatal(err)
	}
	// Same root -> same name (idempotent re-index).
	if got := workspaceNameFor(db, "/Users/x/projects/docs"); got != "docs" {
		t.Fatalf("same root should reuse the name, got %q", got)
	}
	// Different root with colliding basename -> disambiguated.
	if got := workspaceNameFor(db, "/Users/x/other/docs"); got != "docs-other" {
		t.Fatalf("collision should append parent name, got %q", got)
	}
}

func TestRelativeAge(t *testing.T) {
	if got := relativeAge(time.Time{}); got != "unknown" {
		t.Errorf("zero time: got %q, want unknown", got)
	}
	now := time.Now()
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{2 * 24 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := relativeAge(now.Add(-c.ago)); got != c.want {
			t.Errorf("relativeAge(-%v) = %q, want %q", c.ago, got, c.want)
		}
	}
}

func TestDisplayPaths(t *testing.T) {
	got := displayPaths([]string{"/a", "/b"})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %v", got)
	}
}

// TestWizardPersistsTypedDevRoot covers the step-1 "prompt for a dev root
// when unset" flow: whatever the user typed must survive into config.yaml as
// context.dev_roots, or the wizard would ask again on every run and
// get_context/list_projects would never see repos under it.
func TestWizardPersistsTypedDevRoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	devRoot := filepath.Join(dir, "code")
	if err := config.SetDevRoots([]string{devRoot}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != devRoot {
		t.Fatalf("expected dev_roots=[%s], got %v", devRoot, cfg.Context.DevRoots)
	}
}

// TestWizardWithoutDevRootLeavesConfigUntouched covers the common case: no
// custom dev root was typed (context.dev_roots was already set, or the wizard
// fell back silently). The wizard must not touch config.yaml at all — writing
// an empty context.dev_roots would make Load's IsSet check true and
// permanently suppress the ~/workspace default.
func TestWizardWithoutDevRootLeavesConfigUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(); err != nil { // materialize the default config
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".ney", "config.yaml")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := config.SetDevRoots(nil); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config.yaml was rewritten when no dev root was typed:\n%s", after)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "workspace")
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != want {
		t.Fatalf("expected default dev_roots=[%s], got %v", want, cfg.Context.DevRoots)
	}
}

// TestRunSetupWizardRefusesNonInteractive pins B6: an EOF/piped stdin used to
// answer "y" to every "register ney with this client?" prompt, writing into
// Claude Desktop / Codex configs with no human present.
func TestRunSetupWizardRefusesNonInteractive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	orig := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = orig })

	err := runSetupWizard(context.Background())
	if err == nil {
		t.Fatal("expected runSetupWizard to refuse a non-TTY stdin")
	}
	if !strings.Contains(err.Error(), "interactive") {
		t.Errorf("error should explain why: %v", err)
	}

	// Nothing may have been written to any AI client config.
	for _, p := range []string{
		filepath.Join(dir, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		filepath.Join(dir, ".codex", "config.toml"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("wizard wrote %s despite refusing to run", p)
		}
	}
}
