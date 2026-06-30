package chunk

import "fmt"

func NewChunker(strategy string, targetChars, overlapChars int) (ChunkStrategy, error) {
	switch strategy {
	case "character":
		return &CharacterChunker{TargetChars: targetChars, OverlapChars: overlapChars}, nil
	case "paragraph":
		return &ParagraphChunker{TargetChars: targetChars}, nil
	case "markdown":
		return &MarkdownHeadingChunker{TargetChars: targetChars, OverlapChars: overlapChars}, nil
	case "sentence":
		return &SentenceChunker{TargetChars: targetChars}, nil
	default:
		return nil, fmt.Errorf("unknown chunk strategy %q (valid: character, paragraph, markdown, sentence)", strategy)
	}
}
