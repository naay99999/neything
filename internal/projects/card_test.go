package projects

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const exampleCard = `---
name: neything
path: /Users/naay/workspace/naay/neything
remote: github.com/naay99999/neything
stack: Go, Cobra, MCP
status: active — v0.11.0 released; designing project memory
commit: dfa6faf2b7c0e1d2a3b4c5d6e7f8091a2b3c4d5e
updated: 2026-11-10T04:12:00Z
updated_by: claude-code
---
Automatic cross-agent session handoff CLI + MCP server.

## Read more
- README.md
- CLAUDE.md

## Facts
- No LLM calls, no DB, no network — by design
- Distributed via curl / PowerShell installer only
`

func TestParseMarshalRoundTrip(t *testing.T) {
	c, err := Parse([]byte(exampleCard))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "neything" || c.Stack != "Go, Cobra, MCP" || len(c.ReadMore) != 2 || len(c.Facts) != 2 ||
		c.Summary != "Automatic cross-agent session handoff CLI + MCP server." || c.Updated.IsZero() {
		t.Fatalf("parsed = %+v", c)
	}
	if got := string(c.Marshal()); got != exampleCard {
		t.Fatalf("round trip mismatch:\n--- got ---\n%s--- want ---\n%s", got, exampleCard)
	}
}

func TestParsePreservesUnknown(t *testing.T) {
	in := strings.Replace(exampleCard, "updated_by: claude-code\n", "updated_by: claude-code\nowner: me\n", 1) + "\n## Links\n- x\n"
	c, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(c.Marshal()); got != in {
		t.Fatalf("unknown keys/sections lost:\n--- got ---\n%s--- want ---\n%s", got, in)
	}
}

func TestParseRejectsNoFrontmatter(t *testing.T) {
	if _, err := Parse([]byte("hello")); err == nil {
		t.Fatal("want error for a file without frontmatter")
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "x.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestSaveBacksUpUnparseable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "x.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Card{Name: "x", Summary: "new"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != "garbage" {
		t.Errorf(".bak = %q, want the unparseable original", b)
	}
	c, err := Load(path)
	if err != nil || c.Summary != "new" {
		t.Fatalf("Load after Save = %+v, %v", c, err)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveNoBackupForValidCard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.md")
	if err := Save(path, Card{Name: "x", Summary: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Card{Name: "x", Summary: "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unexpected .bak for a valid card: %v", err)
	}
}
