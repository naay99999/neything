package chunk

import "github.com/naay99999/neything/internal/loader"

// charsPerToken is a rough approximation used when no provider tokenizer is available.
const charsPerToken = 4

type TokenizerChunker struct {
	TargetTokens  int
	OverlapTokens int
}

func (t *TokenizerChunker) Chunk(doc loader.Document) []Chunk {
	targetChars := t.TargetTokens * charsPerToken
	overlapChars := t.OverlapTokens * charsPerToken
	if targetChars <= 0 {
		targetChars = 300 * charsPerToken
	}
	return (&CharacterChunker{
		TargetChars:  targetChars,
		OverlapChars: overlapChars,
	}).Chunk(doc)
}
