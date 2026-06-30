package chunk

import (
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

func TestCharacterChunkerPositionTracking(t *testing.T) {
	content := "line one\nline two\nline three"
	doc := loader.Document{
		Path:    "test.md",
		Type:    "md",
		Content: content,
		PositionMap: []loader.PositionEntry{
			{ByteOffset: 0, Logical: 1},
			{ByteOffset: 9, Logical: 2},
			{ByteOffset: 18, Logical: 3},
		},
	}

	chunker := &CharacterChunker{TargetChars: 20, OverlapChars: 0}
	chunks := chunker.Chunk(doc)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	if chunks[0].StartPos < 1 {
		t.Fatalf("expected positive start line, got %d", chunks[0].StartPos)
	}
}
