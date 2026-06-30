package chunk

import (
	"regexp"
	"strings"

	"github.com/naay/ney/internal/loader"
)

type SentenceChunker struct {
	TargetChars int
}

var sentenceSplit = regexp.MustCompile(`([.!?]["')»\]]*)\s+([A-Z"'«\[])`)

var abbreviations = []string{"Mr.", "Mrs.", "Ms.", "Dr.", "Prof.", "Sr.", "Jr.", "etc.", "vs.", "i.e.", "e.g.", "al.", "fig.", "no."}

const abbrevPlaceholder = "§"

func (s *SentenceChunker) Chunk(doc loader.Document) []Chunk {
	text := doc.Content

	// replace known abbreviations to prevent false splits
	masked := text
	for _, abbr := range abbreviations {
		masked = strings.ReplaceAll(masked, abbr, strings.ReplaceAll(abbr, ".", abbrevPlaceholder))
	}

	// find sentence boundaries
	locs := sentenceSplit.FindAllStringIndex(masked, -1)
	var boundaries []int
	boundaries = append(boundaries, 0)
	for _, loc := range locs {
		// loc[0] is start of match; the boundary is after the punctuation (loc[1] - len of next char captured)
		boundaries = append(boundaries, loc[1]-1) // start of next sentence
	}
	boundaries = append(boundaries, len(text))

	var sentences []string
	for i := 0; i < len(boundaries)-1; i++ {
		start := boundaries[i]
		end := boundaries[i+1]
		if start >= end {
			continue
		}
		raw := text[start:end]
		// restore abbreviation placeholders
		restored := strings.ReplaceAll(raw, abbrevPlaceholder, ".")
		sentences = append(sentences, strings.TrimSpace(restored))
	}

	var chunks []Chunk
	chunkIndex := 0
	var acc strings.Builder
	accByteStart := 0
	bytePos := 0

	flush := func() {
		content := strings.TrimSpace(acc.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Index:    chunkIndex,
			Content:  content,
			StartPos: lookupPosition(doc.PositionMap, accByteStart),
			EndPos:   lookupPosition(doc.PositionMap, bytePos),
		})
		chunkIndex++
		acc.Reset()
	}

	for _, sent := range sentences {
		if acc.Len() > 0 && acc.Len()+len(sent)+1 > s.TargetChars {
			flush()
			accByteStart = bytePos
		}
		if acc.Len() > 0 {
			acc.WriteString(" ")
		}
		acc.WriteString(sent)
		bytePos += len(sent) + 1
	}
	flush()
	return chunks
}
