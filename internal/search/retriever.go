package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/naay99999/neything/internal/store"
)

type EnrichedResult struct {
	ChunkID   int64
	DocPath   string
	DocType   string
	Content   string
	StartPos  int
	EndPos    int
	Score     float32
	Workspace string
}

type RetrieveOptions struct {
	TopK int
	// FetchK is the overfetch budget before workspace/path filtering.
	// 0 means max(TopK*3, 10).
	FetchK     int
	Workspace  string
	PathPrefix string
}

// SearchMeta reports what actually happened during a Search call. Search is
// keyword-only (SQLite FTS5), so the only interesting case is a degraded one.
type SearchMeta struct {
	KeywordUsed bool `json:"keyword_used"`
	// Degraded is set when the FTS query itself failed. Search still returns
	// (empty results, nil error) in that case, so an MCP caller's live-scan
	// supplement can still answer.
	Degraded string `json:"degraded,omitempty"`
}

// chunkStore is the subset of *store.DB the retriever needs: FTS lookup,
// chunk content, and document metadata. Declared as an interface (rather
// than depending on the concrete *store.DB) so tests can substitute a
// call-counting stub — see TestRetriever_HydratesOnceForOverlappingIDs in
// retriever_test.go, which pins down the fix-1 restructuring below (hydrate
// the fused/deduped ID set exactly once, instead of once per search leg).
// *store.DB satisfies this interface with its existing method set, so
// callers elsewhere in the codebase that build a Retriever with a
// *store.DB value need no changes.
type chunkStore interface {
	SearchFTS(query string, limit int) ([]store.FTSResult, error)
	GetChunksByIDs(ids []int64) ([]*store.Chunk, error)
	GetDocumentsByChunkIDs(chunkIDs []int64) (map[int64]*store.DocWithWorkspace, error)
}

type Retriever struct {
	DB chunkStore
}

func (r *Retriever) Search(ctx context.Context, query string, opts RetrieveOptions) ([]EnrichedResult, SearchMeta, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 8
	}
	fetchK := opts.FetchK
	if fetchK <= 0 {
		fetchK = topK * 3
	}
	if fetchK < 10 {
		fetchK = 10
	}
	// Workspace/path filters are applied after hydration; overfetch so
	// filtered-out hits don't starve the final result set.
	if opts.Workspace != "" || opts.PathPrefix != "" {
		fetchK *= 4
	}

	var meta SearchMeta

	// keywordSearch returns raw (chunk ID, score) pairs only — content and
	// document metadata are NOT fetched here. Hydration is deferred (see
	// below) so a chunk ID truncated away by fetchK is never pulled from
	// SQLite at all.
	var fused []EnrichedResult
	kw, err := r.keywordSearch(ctx, query, fetchK)
	if err != nil {
		// FTS errors never fail the request — record and move on.
		meta.Degraded = fmt.Sprintf("keyword search failed: %v", err)
	} else if len(kw) > 0 {
		fused = kw
		meta.KeywordUsed = true
	}

	// Truncate to the candidate budget before hydrating: fetchK is inflated
	// above when workspace/path filters are active, to absorb filter
	// attrition.
	if len(fused) > fetchK {
		fused = fused[:fetchK]
	}

	// Hydrate content + document metadata ONCE for the deduped, ranked ID
	// set — two SQLite round-trips total for the whole search.
	results, err := r.hydrate(fused, opts.Workspace, opts.PathPrefix)
	if err != nil {
		return nil, meta, fmt.Errorf("hydrate results: %w", err)
	}

	if len(results) > topK {
		results = results[:topK]
	}
	return results, meta, nil
}

func (r *Retriever) keywordSearch(_ context.Context, query string, fetchK int) ([]EnrichedResult, error) {
	ftsResults, err := r.DB.SearchFTS(query, fetchK)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	if len(ftsResults) == 0 {
		return nil, nil
	}

	out := make([]EnrichedResult, len(ftsResults))
	for i, fr := range ftsResults {
		out[i] = EnrichedResult{ChunkID: fr.ChunkID, Score: fr.Score}
	}
	return out, nil
}

// hydrate fetches chunk content and document metadata for scored — in
// exactly two SQLite round-trips no matter how many search legs
// contributed IDs or how much they overlapped — then applies
// workspace/path filtering. The input order (typically post-fusion rank)
// is preserved in the output, minus anything filtered out or missing.
func (r *Retriever) hydrate(scored []EnrichedResult, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	if len(scored) == 0 {
		return nil, nil
	}

	chunkIDs := make([]int64, len(scored))
	for i, s := range scored {
		chunkIDs[i] = s.ChunkID
	}

	docMap, err := r.DB.GetDocumentsByChunkIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch docs: %w", err)
	}

	chunks, err := r.DB.GetChunksByIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: %w", err)
	}

	chunkByID := make(map[int64]*store.Chunk, len(chunks))
	for _, c := range chunks {
		chunkByID[c.ID] = c
	}

	var results []EnrichedResult
	for _, s := range scored {
		c, ok := chunkByID[s.ChunkID]
		if !ok {
			continue
		}
		dw, ok := docMap[s.ChunkID]
		if !ok {
			continue
		}
		if workspaceName != "" && dw.WorkspaceName != workspaceName {
			continue
		}
		if pathPrefix != "" && !hasPathPrefix(dw.Path, pathPrefix) {
			continue
		}
		results = append(results, EnrichedResult{
			ChunkID:   c.ID,
			DocPath:   dw.Path,
			DocType:   dw.Type,
			Content:   c.Content,
			StartPos:  c.StartPos,
			EndPos:    c.EndPos,
			Score:     s.Score,
			Workspace: dw.WorkspaceName,
		})
	}

	return results, nil
}

// hasPathPrefix reports whether path lies at or under prefix, respecting path
// component boundaries — so scoping a search to /home/u/proj does not also
// return /home/u/project-private. A prefix that already ends in a separator
// is a plain prefix test, since it cannot straddle a component.
func hasPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	if strings.HasSuffix(prefix, string(filepath.Separator)) {
		return true
	}
	return path[len(prefix)] == filepath.Separator
}
