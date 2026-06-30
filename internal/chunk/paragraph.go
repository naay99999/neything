package chunk

import (
	"strings"

	"github.com/naay/ney/internal/loader"
)

type ParagraphChunker struct {
	TargetChars int
}

func (p *ParagraphChunker) Chunk(doc loader.Document) []Chunk {
	paragraphs := strings.Split(doc.Content, "\n\n")
	var chunks []Chunk
	chunkIndex := 0
	var acc strings.Builder
	accStart := 0
	bytePos := 0

	flush := func() {
		content := strings.TrimSpace(acc.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Index:    chunkIndex,
			Content:  content,
			StartPos: lookupPosition(doc.PositionMap, accStart),
			EndPos:   lookupPosition(doc.PositionMap, bytePos),
		})
		chunkIndex++
		acc.Reset()
	}

	firstPara := true
	for _, para := range paragraphs {
		if strings.TrimSpace(para) == "" {
			bytePos += len(para) + 2
			continue
		}
		if acc.Len() > 0 && acc.Len()+len(para)+2 > p.TargetChars {
			flush()
			accStart = bytePos
			firstPara = true
		}
		if firstPara {
			accStart = bytePos
			firstPara = false
		} else {
			acc.WriteString("\n\n")
		}
		acc.WriteString(para)
		bytePos += len(para) + 2
	}
	flush()
	return chunks
}
