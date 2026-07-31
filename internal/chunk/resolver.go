package chunk

import (
	"fmt"

	"github.com/naay99999/neything/internal/loader"
)

type Resolver struct {
	Default ChunkStrategy
	ByType  map[string]ChunkStrategy
}

func (r *Resolver) For(doc loader.Document) ChunkStrategy {
	if r == nil {
		return &CharacterChunker{TargetChars: 1200, OverlapChars: 150}
	}
	if r.ByType != nil {
		if c, ok := r.ByType[doc.Type]; ok {
			return c
		}
	}
	if r.Default != nil {
		return r.Default
	}
	return &CharacterChunker{TargetChars: 1200, OverlapChars: 150}
}

func NewResolver(strategy string, targetChars, overlapChars, targetTokens, overlapTokens int, byFormat map[string]string) (*Resolver, error) {
	if strategy != "auto" {
		c, err := NewChunker(strategy, targetChars, overlapChars, targetTokens, overlapTokens)
		if err != nil {
			return nil, err
		}
		return &Resolver{Default: c}, nil
	}

	defaults := defaultByFormat()
	for k, v := range byFormat {
		if v != "" {
			defaults[k] = v
		}
	}

	byType := make(map[string]ChunkStrategy, len(defaults))
	for docType, strat := range defaults {
		c, err := NewChunker(strat, targetChars, overlapChars, targetTokens, overlapTokens)
		if err != nil {
			return nil, fmt.Errorf("by_format.%s: %w", docType, err)
		}
		byType[docType] = c
	}

	fallback, err := NewChunker("markdown", targetChars, overlapChars, targetTokens, overlapTokens)
	if err != nil {
		return nil, err
	}
	return &Resolver{Default: fallback, ByType: byType}, nil
}

func defaultByFormat() map[string]string {
	return map[string]string{
		"md":       "markdown",
		"obsidian": "markdown",
		"notion":   "markdown",
		"txt":      "paragraph",
		"git":      "paragraph",
	}
}
