package chunk

import (
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

func TestPageChunker_SplitsByPage(t *testing.T) {
	doc := loader.Document{
		Type:    "pdf",
		Content: "page one text\npage two text that is longer than target",
		PositionMap: []loader.PositionEntry{
			{ByteOffset: 0, Logical: 1},
			{ByteOffset: 14, Logical: 2},
		},
	}
	c := &PageChunker{TargetChars: 20, OverlapChars: 0}
	chunks := c.Chunk(doc)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].StartPos != 1 || chunks[0].EndPos != 1 {
		t.Fatalf("expected page 1 positions, got %d-%d", chunks[0].StartPos, chunks[0].EndPos)
	}
}

func TestResolverAutoUsesByFormat(t *testing.T) {
	r, err := NewResolver("auto", 1200, 150, 300, 50, map[string]string{"pdf": "page"})
	if err != nil {
		t.Fatal(err)
	}
	pdfChunker := r.For(loader.Document{Type: "pdf"})
	page, ok := pdfChunker.(*PageChunker)
	if !ok {
		t.Fatalf("expected PageChunker for pdf, got %T", pdfChunker)
	}
	if page.TargetChars != 1200 {
		t.Fatalf("expected target 1200, got %d", page.TargetChars)
	}

	mdChunker := r.For(loader.Document{Type: "md"})
	if _, ok := mdChunker.(*MarkdownHeadingChunker); !ok {
		t.Fatalf("expected MarkdownHeadingChunker for md, got %T", mdChunker)
	}
}

func TestResolverFixedStrategy(t *testing.T) {
	r, err := NewResolver("paragraph", 800, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := r.For(loader.Document{Type: "pdf"})
	if _, ok := c.(*ParagraphChunker); !ok {
		t.Fatalf("expected ParagraphChunker for all types, got %T", c)
	}
}
