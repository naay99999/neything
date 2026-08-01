package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawConfig plants a config.yaml under a fake $HOME and returns its path.
func writeRawConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ney")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSetDevRootsPreservesUnknownKeysAndComments is the regression guard for
// the reported data-loss bug: rerunning `ney init` used to rewrite
// config.yaml from a template, silently discarding everything the user had
// configured.
func TestSetDevRootsPreservesUnknownKeysAndComments(t *testing.T) {
	path := writeRawConfig(t, `# my hand-written notes about this file
retrieval:
  top_k: 12

index:
  exclude: ["*.bak", "drafts-*"]

# a key this version of ney knows nothing about
chat:
  provider: something
  model: whatever

context:
  active_days: 30
`)

	if err := SetDevRoots([]string{"/tmp/code"}); err != nil {
		t.Fatalf("SetDevRoots: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load after SetDevRoots: %v", err)
	}
	if cfg.Retrieval.TopK != 12 {
		t.Errorf("top_k = %d, want 12 (user value must survive)", cfg.Retrieval.TopK)
	}
	if len(cfg.Index.Exclude) != 2 || cfg.Index.Exclude[0] != "*.bak" {
		t.Errorf("index.exclude = %v, want the user's two patterns", cfg.Index.Exclude)
	}
	if cfg.Context.ActiveDays != 30 {
		t.Errorf("active_days = %d, want 30", cfg.Context.ActiveDays)
	}
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != "/tmp/code" {
		t.Errorf("dev_roots = %v, want [/tmp/code]", cfg.Context.DevRoots)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "chat:") {
		t.Errorf("unknown `chat:` key was dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), "# my hand-written notes about this file") {
		t.Errorf("comment was dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), "# a key this version of ney knows nothing about") {
		t.Errorf("inline comment was dropped:\n%s", raw)
	}
}

// TestSetDevRootsEmptyIsNoOp pins the constraint that keeps defaultDevRoots
// alive: writing `context.dev_roots: []` would make Load's
// v.IsSet("context.dev_roots") true and permanently suppress ~/workspace.
func TestSetDevRootsEmptyIsNoOp(t *testing.T) {
	path := writeRawConfig(t, "retrieval:\n  top_k: 8\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := SetDevRoots(nil); err != nil {
		t.Fatalf("SetDevRoots(nil): %v", err)
	}
	if err := SetDevRoots([]string{}); err != nil {
		t.Fatalf("SetDevRoots(empty): %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("file changed on an empty call:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("a no-op call must not create a .bak")
	}
}

func TestSetDevRootsCreatesConfigWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SetDevRoots([]string{"/tmp/code"}); err != nil {
		t.Fatalf("SetDevRoots: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != "/tmp/code" {
		t.Errorf("dev_roots = %v, want [/tmp/code]", cfg.Context.DevRoots)
	}
	// The template's other defaults must still be there.
	if cfg.Retrieval.TopK != 8 {
		t.Errorf("top_k = %d, want the template default 8", cfg.Retrieval.TopK)
	}
}

// TestSetDevRootsIsIdempotent guards against appending a second `dev_roots`
// (or a second `context:`) — a duplicate key makes the NEXT Load fail.
func TestSetDevRootsIsIdempotent(t *testing.T) {
	path := writeRawConfig(t, "context:\n  active_days: 21\n")

	for i := 0; i < 3; i++ {
		if err := SetDevRoots([]string{"/tmp/a", "/tmp/b"}); err != nil {
			t.Fatalf("SetDevRoots #%d: %v", i, err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "dev_roots"); n != 1 {
		t.Errorf("found %d dev_roots keys, want 1:\n%s", n, raw)
	}
	if n := strings.Count(string(raw), "\ncontext:"); n > 1 {
		t.Errorf("found %d context: blocks, want 1:\n%s", n, raw)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Context.DevRoots) != 2 {
		t.Errorf("dev_roots = %v, want 2 entries", cfg.Context.DevRoots)
	}
	if cfg.Context.ActiveDays != 21 {
		t.Errorf("active_days = %d, want 21", cfg.Context.ActiveDays)
	}
}

// TestSetDevRootsLeavesFileIntactOnUnwritableShape: a config whose root is
// not a mapping can't be edited surgically. The original must survive.
func TestSetDevRootsLeavesFileIntactOnUnwritableShape(t *testing.T) {
	path := writeRawConfig(t, "- this is a sequence, not a mapping\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := SetDevRoots([]string{"/tmp/code"}); err == nil {
		t.Fatal("expected an error for a non-mapping config root")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("original file was modified despite the failure:\n%s", after)
	}
}

func TestSetDevRootsHandlesEmptyContextKey(t *testing.T) {
	writeRawConfig(t, "retrieval:\n  top_k: 8\n\ncontext:\n")

	if err := SetDevRoots([]string{"/tmp/code"}); err != nil {
		t.Fatalf("SetDevRoots: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != "/tmp/code" {
		t.Errorf("dev_roots = %v, want [/tmp/code]", cfg.Context.DevRoots)
	}
}
