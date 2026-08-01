package chunk

import (
	"strings"
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

// TestCharacterChunkerNoDegenerateTail pins the fix for the tail bug: once a
// window reached the end of the document, `next := end - OverlapChars` stopped
// advancing and the "never go backwards" guard stepped one rune at a time,
// emitting ~OverlapChars shrinking suffix chunks per document. A 2000-rune doc
// used to produce 152 chunks (the last 150 being 150, 149, ... 1 chars long)
// where 2 are correct.
func TestCharacterChunkerNoDegenerateTail(t *testing.T) {
	content := strings.Repeat("word ", 400) // 2000 runes
	doc := loader.Document{Path: "big.md", Type: "md", Content: content}

	chunks := (&CharacterChunker{TargetChars: 1200, OverlapChars: 150}).Chunk(doc)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks for a 2000-rune doc at 1200/150, got %d", len(chunks))
	}
	// Every chunk must carry real content — the old bug's giveaway was a run
	// of 1-3 character chunks at the end.
	for i, c := range chunks {
		if len(c.Content) < 100 {
			t.Errorf("chunk %d is only %d bytes — degenerate tail is back: %q", i, len(c.Content), c.Content)
		}
	}
}

// TestCharacterChunkerCoversWholeDocument guards the EOF break: appending the
// final window and *then* stopping is what makes coverage complete. Breaking
// before the append would silently drop the tail of every document, which a
// count-based assertion alone would not catch.
func TestCharacterChunkerCoversWholeDocument(t *testing.T) {
	cases := []struct {
		name    string
		content string
		target  int
		overlap int
	}{
		{"exact multiple", strings.Repeat("word ", 400), 1200, 150},
		{"ragged tail", strings.Repeat("alpha beta ", 373) + "END", 1000, 200},
		{"shorter than target", "just a short note", 1200, 150},
		{"exactly one target", strings.Repeat("a", 1200), 1200, 150},
		{"no overlap", strings.Repeat("word ", 400), 300, 0},
		{"multibyte", strings.Repeat("สวัสดี ครับ ", 300), 500, 80},
		{"one huge token", strings.Repeat("x", 5000), 1200, 150},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An identity position map (byte offset -> itself) makes the
			// chunks' StartPos/EndPos equal their byte offsets, so coverage
			// can be checked exactly. Searching for each chunk's text instead
			// would be ambiguous: windows overlap, and the fixtures repeat.
			doc := loader.Document{
				Path:        "d.md",
				Type:        "md",
				Content:     tc.content,
				PositionMap: identityPositions(len(tc.content)),
			}
			chunks := (&CharacterChunker{TargetChars: tc.target, OverlapChars: tc.overlap}).Chunk(doc)
			if len(chunks) == 0 {
				t.Fatal("expected at least one chunk")
			}

			if chunks[0].StartPos != 0 {
				t.Fatalf("first chunk starts at byte %d, want 0", chunks[0].StartPos)
			}
			last := chunks[len(chunks)-1]
			if last.EndPos != len(tc.content) {
				t.Fatalf("last chunk ends at byte %d of %d — the document tail was dropped",
					last.EndPos, len(tc.content))
			}

			for i, c := range chunks {
				if got := tc.content[c.StartPos:c.EndPos]; got != c.Content {
					t.Fatalf("chunk %d content does not match its reported [%d,%d) range", i, c.StartPos, c.EndPos)
				}
				if i == 0 {
					continue
				}
				prev := chunks[i-1]
				if c.StartPos > prev.EndPos {
					t.Fatalf("chunk %d starts at %d, leaving a gap after the previous chunk's end %d",
						i, c.StartPos, prev.EndPos)
				}
				if c.StartPos <= prev.StartPos {
					t.Fatalf("chunk %d starts at %d, not past the previous chunk's start %d — no forward progress",
						i, c.StartPos, prev.StartPos)
				}
			}
		})
	}
}

// identityPositions builds a position map whose logical position equals the
// byte offset, for every offset in [0, n].
func identityPositions(n int) []loader.PositionEntry {
	out := make([]loader.PositionEntry, 0, n+1)
	for i := 0; i <= n; i++ {
		out = append(out, loader.PositionEntry{ByteOffset: i, Logical: i})
	}
	return out
}

// TestCharacterChunkerOverlapAtLeastTargetTerminates: a misconfigured overlap
// would otherwise reintroduce one-rune stepping. config.Validate rejects it,
// but the chunker is constructed directly in tests and by embedded callers.
func TestCharacterChunkerOverlapAtLeastTargetTerminates(t *testing.T) {
	doc := loader.Document{Path: "d.md", Type: "md", Content: strings.Repeat("word ", 400)}
	chunks := (&CharacterChunker{TargetChars: 200, OverlapChars: 500}).Chunk(doc)
	if len(chunks) > 30 {
		t.Fatalf("overlap >= target should be clamped, got %d chunks for a 2000-rune doc", len(chunks))
	}
}
