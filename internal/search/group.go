package search

import "sort"

type FileGroup struct {
	DocPath   string           `json:"path"`
	DocType   string           `json:"type"`
	Workspace string           `json:"workspace,omitempty"`
	BestScore float32          `json:"best_score"`
	Chunks    []EnrichedResult `json:"chunks"`
	// Source is "index" for groups built from GroupByFile (FTS/semantic
	// results) or "live-scan" for tier-0 filesystem-scan hits appended by a
	// caller (see cmd/ney/cmd_search.go, cmd/ney/mcp_tools.go) when the
	// index isn't ready yet.
	Source string `json:"source,omitempty"`
}

type GroupedResults struct {
	Files []FileGroup `json:"files"`
	Meta  *SearchMeta `json:"meta,omitempty"`
}

func GroupByFile(results []EnrichedResult) []FileGroup {
	if len(results) == 0 {
		return nil
	}

	byPath := make(map[string]*FileGroup)
	order := make([]string, 0)

	for _, r := range results {
		g, ok := byPath[r.DocPath]
		if !ok {
			g = &FileGroup{
				DocPath:   r.DocPath,
				DocType:   r.DocType,
				Workspace: r.Workspace,
				Source:    "index",
			}
			byPath[r.DocPath] = g
			order = append(order, r.DocPath)
		}
		g.Chunks = append(g.Chunks, r)
		if r.Score > g.BestScore {
			g.BestScore = r.Score
		}
	}

	groups := make([]FileGroup, 0, len(order))
	for _, path := range order {
		g := byPath[path]
		sort.Slice(g.Chunks, func(i, j int) bool {
			return g.Chunks[i].Score > g.Chunks[j].Score
		})
		groups = append(groups, *g)
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].BestScore > groups[j].BestScore
	})
	return groups
}
