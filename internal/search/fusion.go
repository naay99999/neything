package search

import "sort"

const defaultRRFK = 60

// ReciprocalRankFusion merges semantic and keyword result lists by chunk ID.
func ReciprocalRankFusion(semantic, keyword []EnrichedResult, rrfK int) []EnrichedResult {
	if rrfK <= 0 {
		rrfK = defaultRRFK
	}
	if len(keyword) == 0 {
		return semantic
	}
	if len(semantic) == 0 {
		return keyword
	}

	type agg struct {
		result EnrichedResult
		score  float64
	}

	merged := make(map[int64]*agg)

	addList := func(list []EnrichedResult) {
		for rank, r := range list {
			rrfScore := 1.0 / float64(rrfK+rank+1)
			if existing, ok := merged[r.ChunkID]; ok {
				existing.score += rrfScore
				if r.Score > existing.result.Score {
					existing.result.Score = r.Score
				}
			} else {
				cp := r
				merged[r.ChunkID] = &agg{result: cp, score: rrfScore}
			}
		}
	}

	addList(semantic)
	addList(keyword)

	results := make([]EnrichedResult, 0, len(merged))
	for _, a := range merged {
		a.result.Score = float32(a.score)
		results = append(results, a.result)
	}

	sortByScoreDesc(results)
	return results
}

// sortByScoreDesc sorts by score descending, breaking ties by chunk ID
// ascending so results are deterministic regardless of map iteration order.
func sortByScoreDesc(results []EnrichedResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ChunkID < results[j].ChunkID
	})
}
