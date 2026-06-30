package chunk

import (
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

func TestTokenizerChunker_ApproximateSize(t *testing.T) {
	content := strings.Repeat("word ", 400) // ~2000 chars
	doc := loader.Document{
		Path:    "test.md",
		Type:    "md",
		Content: content,
	}

	c := &TokenizerChunker{TargetTokens: 300, OverlapTokens: 50}
	chunks := c.Chunk(doc)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}

	// 300 tokens * 4 chars = 1200 chars target
	for i, ch := range chunks {
		if len(ch.Content) > 1300 {
			t.Fatalf("chunk %d too large: %d chars", i, len(ch.Content))
		}
	}
}

func TestNewChunkerTokenizerStrategy(t *testing.T) {
	c, err := NewChunker("tokenizer", 1200, 150, 300, 50)
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := c.(*TokenizerChunker)
	if !ok {
		t.Fatalf("expected TokenizerChunker, got %T", c)
	}
	if tc.TargetTokens != 300 || tc.OverlapTokens != 50 {
		t.Fatalf("unexpected token settings: %+v", tc)
	}
}
