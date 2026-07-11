package main

import "testing"

func TestVectorParityState(t *testing.T) {
	cases := []struct {
		name         string
		vecCount     int
		chunkCount   int
		wantState    string
		wantCoverage float64
	}{
		{"equal counts", 10, 10, "ok", 1.0},
		{"both zero", 0, 0, "ok", 1.0},
		{"embedding in progress", 4, 10, "embedding", 0.4},
		{"embedding just started", 0, 10, "embedding", 0.0},
		{"orphan vectors", 12, 10, "orphan", 1.0},
		{"orphan with zero chunks", 5, 0, "orphan", 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, coverage := vectorParityState(c.vecCount, c.chunkCount)
			if state != c.wantState {
				t.Errorf("vectorParityState(%d, %d) state = %q, want %q", c.vecCount, c.chunkCount, state, c.wantState)
			}
			if coverage != c.wantCoverage {
				t.Errorf("vectorParityState(%d, %d) coverage = %v, want %v", c.vecCount, c.chunkCount, coverage, c.wantCoverage)
			}
		})
	}
}

func TestVectorParityMessage(t *testing.T) {
	cases := []struct {
		name       string
		vecCount   int
		chunkCount int
		want       string
	}{
		{"ok", 10, 10, "ok"},
		{"embedding in progress", 4, 10, "embedding in progress (4/10, 40%)"},
		{"orphan vectors", 12, 10, "orphan vectors (vectors=12, chunks=10)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := vectorParityMessage(c.vecCount, c.chunkCount)
			if got != c.want {
				t.Errorf("vectorParityMessage(%d, %d) = %q, want %q", c.vecCount, c.chunkCount, got, c.want)
			}
		})
	}
}
