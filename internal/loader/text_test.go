package loader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTextLoaderSupports(t *testing.T) {
	l := &TextLoader{}
	if !l.Supports("notes.txt") {
		t.Fatal("expected supports .txt")
	}
	if !l.Supports("NOTES.TXT") {
		t.Fatal("expected case-insensitive supports .txt")
	}
	if l.Supports("notes.md") {
		t.Fatal("did not expect supports .md")
	}
}

func TestTextLoaderLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	docs, err := (&TextLoader{}).Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 section, got %d", len(docs))
	}
	doc := docs[0]
	if doc.Type != "txt" {
		t.Fatalf("expected type txt, got %s", doc.Type)
	}
	if doc.Content != content {
		t.Fatalf("unexpected content: %q", doc.Content)
	}
	if doc.Hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if len(doc.PositionMap) != 3 {
		t.Fatalf("expected 3 position entries (one per line), got %d", len(doc.PositionMap))
	}
}
