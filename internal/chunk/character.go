package chunk

import (
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/naay99999/neything/internal/loader"
)

type CharacterChunker struct {
	TargetChars  int
	OverlapChars int
}

// Chunk slices doc.Content into overlapping windows of ~TargetChars
// characters, preferring to end each window just after a space.
//
// It walks byte offsets and decodes runes on the fly rather than
// materializing []rune(doc.Content) plus a byte-offset table. Those two cost
// 4 and 8 bytes respectively for every byte of the document — a 20 MB file
// (index.maxIndexFileSize, the largest this is ever handed) meant ~240 MB of
// scratch allocation to produce a few thousand substrings. Chunk contents are
// plain string slices, which share the input's backing array and allocate
// nothing.
//
// The loop ends the moment a window reaches EOF. That is load-bearing, not an
// optimization: `next := end - OverlapChars` stops advancing once end is
// pinned to the end of the document, so without the break the "never go
// backwards" guard below would step one rune at a time and emit ~OverlapChars
// extra chunks per document — each a shrinking suffix of the last real one.
// The final window always covers everything after the previous window's
// start, so stopping there loses nothing.
func (c *CharacterChunker) Chunk(doc loader.Document) []Chunk {
	s := doc.Content
	if s == "" {
		return nil
	}

	target := c.TargetChars
	if target <= 0 {
		target = 1200
	}
	overlap := c.OverlapChars
	if overlap < 0 {
		overlap = 0
	}
	// An overlap at or above the target would make every window start where
	// the previous one did, falling back to one-rune steps. Config validation
	// rejects this, but the chunker is also constructed directly in tests and
	// by callers that don't go through Validate.
	if overlap >= target {
		overlap = target / 2
	}

	var chunks []Chunk
	chunkIndex := 0
	start := 0

	for {
		end := advanceRunes(s, start, target)
		if end < len(s) {
			end = wordBoundaryBefore(s, start, end)
		}

		chunks = append(chunks, Chunk{
			DocumentID: 0,
			Index:      chunkIndex,
			Content:    s[start:end],
			StartPos:   lookupPosition(doc.PositionMap, start),
			EndPos:     lookupPosition(doc.PositionMap, end),
		})
		chunkIndex++

		if end >= len(s) {
			break
		}

		next := retreatRunes(s, start, end, overlap)
		if next <= start {
			next = start + runeSizeAt(s, start)
		}
		start = next
	}

	return chunks
}

// advanceRunes returns the byte offset n runes past from, clamped to len(s).
func advanceRunes(s string, from, n int) int {
	i := from
	for k := 0; k < n && i < len(s); k++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return i
}

// retreatRunes returns the byte offset n runes before from, never going below
// floor.
func retreatRunes(s string, floor, from, n int) int {
	i := from
	for k := 0; k < n && i > floor; k++ {
		_, size := utf8.DecodeLastRuneInString(s[floor:i])
		i -= size
	}
	return i
}

// wordBoundaryBefore walks end back to the first position whose preceding
// rune is a space, so a window ends between words rather than mid-token. It
// gives up (returning end unchanged, a hard cut) once it would reach the
// window's own first rune — a single unbroken token longer than the target
// has no boundary to find.
func wordBoundaryBefore(s string, start, end int) int {
	limit := start + runeSizeAt(s, start)
	e := end
	for e > limit {
		r, size := utf8.DecodeLastRuneInString(s[start:e])
		if unicode.IsSpace(r) {
			return e
		}
		e -= size
	}
	return end
}

// runeSizeAt returns the byte width of the rune starting at offset at.
func runeSizeAt(s string, at int) int {
	if at >= len(s) {
		return 1
	}
	_, size := utf8.DecodeRuneInString(s[at:])
	return size
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
