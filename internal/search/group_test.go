package search

import "testing"

func TestGroupByFile(t *testing.T) {
	results := []EnrichedResult{
		{DocPath: "b.md", DocType: "md", Score: 0.7, Content: "b1"},
		{DocPath: "a.md", DocType: "md", Score: 0.9, Content: "a1"},
		{DocPath: "a.md", DocType: "md", Score: 0.8, Content: "a2"},
	}

	groups := GroupByFile(results)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].DocPath != "a.md" {
		t.Fatalf("expected first group a.md, got %s", groups[0].DocPath)
	}
	if groups[0].BestScore != 0.9 {
		t.Fatalf("expected best score 0.9, got %f", groups[0].BestScore)
	}
	if len(groups[0].Chunks) != 2 {
		t.Fatalf("expected 2 chunks in a.md group, got %d", len(groups[0].Chunks))
	}
	if groups[0].Chunks[0].Score != 0.9 {
		t.Fatalf("expected chunks sorted by score desc")
	}
}
