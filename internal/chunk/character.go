package chunk

import (
	"sort"
	"unicode"

	"github.com/naay/ney/internal/loader"
)

type CharacterChunker struct {
	TargetChars  int
	OverlapChars int
}

func (c *CharacterChunker) Chunk(doc loader.Document) []Chunk {
	runes := []rune(doc.Content)
	total := len(runes)
	if total == 0 {
		return nil
	}

	var chunks []Chunk
	idx := 0
	chunkIndex := 0

	for idx < total {
		end := idx + c.TargetChars
		if end > total {
			end = total
		} else {
			// walk back to a word boundary
			for end > idx+1 && !unicode.IsSpace(runes[end-1]) {
				end--
			}
			if end == idx+1 {
				// no word boundary found, hard cut
				end = idx + c.TargetChars
				if end > total {
					end = total
				}
			}
		}

		content := string(runes[idx:end])
		byteStart := runeOffsetToByteOffset(doc.Content, idx)
		byteEnd := runeOffsetToByteOffset(doc.Content, end)

		chunks = append(chunks, Chunk{
			DocumentID: 0,
			Index:      chunkIndex,
			Content:    content,
			StartPos:   lookupPosition(doc.PositionMap, byteStart),
			EndPos:     lookupPosition(doc.PositionMap, byteEnd),
		})
		chunkIndex++

		next := end - c.OverlapChars
		if next <= idx {
			next = idx + 1
		}
		idx = next
	}
	return chunks
}

func runeOffsetToByteOffset(s string, runeIdx int) int {
	for i := range s {
		if runeIdx == 0 {
			return i
		}
		runeIdx--
	}
	return len(s)
}

func lookupPosition(posMap []loader.PositionEntry, byteOffset int) int {
	if len(posMap) == 0 {
		return 0
	}
	// binary search: find largest entry where ByteOffset <= byteOffset
	i := sort.Search(len(posMap), func(i int) bool {
		return posMap[i].ByteOffset > byteOffset
	})
	if i == 0 {
		return posMap[0].Logical
	}
	return posMap[i-1].Logical
}
