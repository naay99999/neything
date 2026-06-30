package search

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

func sortByScoreDesc(results []EnrichedResult) {
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}
