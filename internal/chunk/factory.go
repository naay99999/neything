package chunk

import "fmt"

func NewChunker(strategy string, targetChars, overlapChars, targetTokens, overlapTokens int) (ChunkStrategy, error) {
	switch strategy {
	case "character":
		return &CharacterChunker{TargetChars: targetChars, OverlapChars: overlapChars}, nil
	case "paragraph":
		return &ParagraphChunker{TargetChars: targetChars}, nil
	case "markdown":
		return &MarkdownHeadingChunker{TargetChars: targetChars, OverlapChars: overlapChars}, nil
	case "sentence":
		return &SentenceChunker{TargetChars: targetChars}, nil
	case "tokenizer":
		if targetTokens <= 0 {
			targetTokens = 300
		}
		if overlapTokens <= 0 {
			overlapTokens = 50
		}
		return &TokenizerChunker{TargetTokens: targetTokens, OverlapTokens: overlapTokens}, nil
	default:
		return nil, fmt.Errorf("unknown chunk strategy %q (valid: character, paragraph, markdown, sentence, tokenizer)", strategy)
	}
}
