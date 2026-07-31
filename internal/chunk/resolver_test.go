package chunk

import (
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

func TestResolverAutoUsesByFormat(t *testing.T) {
	r, err := NewResolver("auto", 1200, 150, 300, 50, map[string]string{"txt": "character"})
	if err != nil {
		t.Fatal(err)
	}
	txtChunker := r.For(loader.Document{Type: "txt"})
	cc, ok := txtChunker.(*CharacterChunker)
	if !ok {
		t.Fatalf("expected CharacterChunker for txt (by_format override), got %T", txtChunker)
	}
	if cc.TargetChars != 1200 {
		t.Fatalf("expected target 1200, got %d", cc.TargetChars)
	}

	mdChunker := r.For(loader.Document{Type: "md"})
	if _, ok := mdChunker.(*MarkdownHeadingChunker); !ok {
		t.Fatalf("expected MarkdownHeadingChunker for md, got %T", mdChunker)
	}
}

func TestResolverAutoDefaultsTxtToParagraph(t *testing.T) {
	r, err := NewResolver("auto", 1200, 150, 300, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	txtChunker := r.For(loader.Document{Type: "txt"})
	if _, ok := txtChunker.(*ParagraphChunker); !ok {
		t.Fatalf("expected ParagraphChunker for txt by default, got %T", txtChunker)
	}
}

func TestResolverFixedStrategy(t *testing.T) {
	r, err := NewResolver("paragraph", 800, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := r.For(loader.Document{Type: "txt"})
	if _, ok := c.(*ParagraphChunker); !ok {
		t.Fatalf("expected ParagraphChunker for all types, got %T", c)
	}
}
