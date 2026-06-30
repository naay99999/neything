package search

import "testing"

func TestReciprocalRankFusion_PrefersBothLists(t *testing.T) {
	semantic := []EnrichedResult{
		{ChunkID: 1, Score: 0.9},
		{ChunkID: 2, Score: 0.8},
	}
	keyword := []EnrichedResult{
		{ChunkID: 2, Score: 0.7},
		{ChunkID: 3, Score: 0.6},
	}

	out := ReciprocalRankFusion(semantic, keyword, 60)
	if len(out) != 3 {
		t.Fatalf("expected 3 merged results, got %d", len(out))
	}
	if out[0].ChunkID != 2 {
		t.Fatalf("expected chunk 2 on top (in both lists), got %d", out[0].ChunkID)
	}
}

func TestReciprocalRankFusion_SemanticOnly(t *testing.T) {
	semantic := []EnrichedResult{{ChunkID: 1, Score: 0.5}}
	out := ReciprocalRankFusion(semantic, nil, 60)
	if len(out) != 1 || out[0].ChunkID != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
}
