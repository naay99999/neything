package loader

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotionLoader(t *testing.T) {
	path := filepath.Join("testdata", "notion.md")
	l := &NotionLoader{}
	if !l.Supports(path) {
		t.Fatal("expected supports notion md")
	}
	docs, err := l.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	doc := docs[0]
	if doc.Type != "notion" {
		t.Fatalf("expected notion type, got %s", doc.Type)
	}
	if strings.Contains(doc.Content, "| Property |") {
		t.Fatal("property table should be stripped")
	}
	if !strings.Contains(doc.Content, "Notion export content") {
		t.Fatalf("missing body: %q", doc.Content)
	}
}

func TestObsidianLoader(t *testing.T) {
	path := filepath.Join("testdata", "obsidian.md")
	l := &ObsidianLoader{}
	if !l.Supports(path) {
		t.Fatal("expected supports obsidian md")
	}
	docs, err := l.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	doc := docs[0]
	if doc.Type != "obsidian" {
		t.Fatalf("expected obsidian type, got %s", doc.Type)
	}
	if doc.Metadata["title"] != "Daily Notes" {
		t.Fatalf("unexpected title: %q", doc.Metadata["title"])
	}
	if !strings.Contains(doc.Metadata["wikilinks"], "Project Alpha") {
		t.Fatalf("missing wikilinks: %q", doc.Metadata["wikilinks"])
	}
}

func TestMarkdownLoaderPlainFallback(t *testing.T) {
	path := filepath.Join("testdata", "plain.md")
	if (&NotionLoader{}).Supports(path) || (&ObsidianLoader{}).Supports(path) {
		t.Fatal("specialized loaders should not claim plain md")
	}
	docs, err := (&MarkdownLoader{}).Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Type != "md" {
		t.Fatalf("expected md type, got %s", docs[0].Type)
	}
}

func TestConfluenceLoader(t *testing.T) {
	path := filepath.Join("testdata", "confluence.xml")
	l := &ConfluenceLoader{}
	if !l.Supports(path) {
		t.Fatal("expected supports confluence xml")
	}
	docs, err := l.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	doc := docs[0]
	if doc.Type != "confluence" {
		t.Fatalf("expected confluence type, got %s", doc.Type)
	}
	if !strings.Contains(doc.Content, "Restart the service") {
		t.Fatalf("missing content: %q", doc.Content)
	}
}

func TestRegistryDispatchOrder(t *testing.T) {
	reg := NewRegistry(
		&NotionLoader{},
		&ObsidianLoader{},
		&MarkdownLoader{},
	)
	cases := map[string]string{
		"testdata/notion.md":   "notion",
		"testdata/obsidian.md": "obsidian",
		"testdata/plain.md":    "md",
	}
	for path, wantType := range cases {
		ld, ok := reg.Dispatch(path)
		if !ok {
			t.Fatalf("no loader for %s", path)
		}
		docs, err := ld.Load(context.Background(), path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		if docs[0].Type != wantType {
			t.Fatalf("%s: expected type %s, got %s via %T", path, wantType, docs[0].Type, ld)
		}
	}
}
