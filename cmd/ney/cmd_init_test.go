package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/store"
)

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"192.168.1.150:1234":      "http://192.168.1.150:1234",
		"http://localhost:1234/":  "http://localhost:1234",
		"https://api.example.com": "https://api.example.com",
		"  http://host:1234/  ":   "http://host:1234",
		"":                        "",
	}
	for in, want := range cases {
		if got := normalizeEndpoint(in); got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeEmbedder(t *testing.T) {
	if !looksLikeEmbedder("text-embedding-nomic-embed-text-v1.5") {
		t.Error("nomic embed model should look like an embedder")
	}
	if looksLikeEmbedder("google/gemma-4-e4b") {
		t.Error("gemma should not look like an embedder")
	}
}

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
		{"9", 3, nil, nil},           // out of range dropped
		{"1,1,1", 3, []int{0}, nil},  // deduped
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
