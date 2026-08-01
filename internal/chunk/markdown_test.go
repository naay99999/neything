package chunk

import (
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/loader"
)

// TestMarkdownChunkerNoDegenerateTail: `markdown` is the default strategy and
// delegates any section longer than TargetChars to CharacterChunker, so the
// tail bug reached real users through here rather than through `character`.
// A 7 KB note used to produce 157 chunks where 7 are correct.
func TestMarkdownChunkerNoDegenerateTail(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Note\n\n")
	for i := 0; i < 12; i++ {
		b.WriteString(strings.Repeat("billing stripe roadmap latency ", 15))
		b.WriteString("\n\n")
	}
	content := b.String()

	doc := loader.Document{Path: "note.md", Type: "md", Content: content}
	chunks := (&MarkdownHeadingChunker{TargetChars: 1200, OverlapChars: 150}).Chunk(doc)

	// ~5.6 KB of body at a ~1050-char step: a handful of chunks, nowhere near
	// the ~150 the tail bug added.
	if len(chunks) > 12 {
		t.Fatalf("expected roughly %d chunks for a %d-byte note, got %d — degenerate tail is back",
			len(content)/1050, len(content), len(chunks))
	}
	for i, c := range chunks {
		if len(c.Content) < 100 {
			t.Errorf("chunk %d is only %d bytes: %q", i, len(c.Content), c.Content)
		}
	}
}

// TestMarkdownChunkerKeepsShortSectionsWhole is the counterpart: a section
// under the target must stay a single chunk, headings included.
func TestMarkdownChunkerKeepsShortSectionsWhole(t *testing.T) {
	content := "# Title\n\nintro line\n\n## Alpha\n\nalpha body\n\n## Beta\n\nbeta body\n"
	doc := loader.Document{Path: "note.md", Type: "md", Content: content}
	chunks := (&MarkdownHeadingChunker{TargetChars: 1200, OverlapChars: 150}).Chunk(doc)

	if len(chunks) != 3 {
		t.Fatalf("expected one chunk per heading section, got %d", len(chunks))
	}
	for _, want := range []string{"# Title", "## Alpha", "## Beta"} {
		found := false
		for _, c := range chunks {
			if strings.HasPrefix(c.Content, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no chunk starts with %q", want)
		}
	}
}
