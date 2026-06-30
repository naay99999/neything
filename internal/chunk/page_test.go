package chunk

import (
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

func TestPageChunker_OversizedPageSubSplits(t *testing.T) {
	longPage := strings.Repeat("word ", 500)
	doc := loader.Document{
		Type:    "pdf",
		Content: longPage,
		PositionMap: []loader.PositionEntry{
			{ByteOffset: 0, Logical: 3},
		},
	}
	c := &PageChunker{TargetChars: 100, OverlapChars: 10}
	chunks := c.Chunk(doc)
	if len(chunks) <= 1 {
		t.Fatalf("expected multiple sub-chunks for oversized page, got %d", len(chunks))
	}
	for _, ch := range chunks {
		if ch.StartPos != 3 || ch.EndPos != 3 {
			t.Fatalf("expected page 3 positions, got %d-%d", ch.StartPos, ch.EndPos)
		}
	}
}
