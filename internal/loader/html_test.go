package loader

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLLoader(t *testing.T) {
	path := filepath.Join("testdata", "sample.html")
	l := &HTMLLoader{}
	if !l.Supports(path) {
		t.Fatal("expected supports html")
	}
	docs, err := l.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	doc := docs[0]
	if doc.Type != "html" {
		t.Fatalf("expected type html, got %s", doc.Type)
	}
	if doc.Metadata["title"] != "Billing Guide" {
		t.Fatalf("unexpected title: %q", doc.Metadata["title"])
	}
	if !strings.Contains(doc.Content, "Stripe handles subscriptions") {
		t.Fatalf("missing expected text: %q", doc.Content)
	}
	if strings.Contains(doc.Content, "ignore me") {
		t.Fatal("script content should be stripped")
	}
}

func TestJSONLoader(t *testing.T) {
	path := filepath.Join("testdata", "sample.json")
	l := &JSONLoader{}
	if !l.Supports(path) {
		t.Fatal("expected supports json")
	}
	docs, err := l.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	doc := docs[0]
	if doc.Type != "json" {
		t.Fatalf("expected type json, got %s", doc.Type)
	}
	if !strings.Contains(doc.Content, "title: API Reference") {
		t.Fatalf("missing flattened title: %q", doc.Content)
	}
	if !strings.Contains(doc.Content, "endpoints[0].path: /search") {
		t.Fatalf("missing nested path: %q", doc.Content)
	}
}
