package chunk

import (
	"strings"

	"github.com/naay99999/neything/internal/loader"
)

type MarkdownHeadingChunker struct {
	TargetChars  int
	OverlapChars int
}

func (m *MarkdownHeadingChunker) Chunk(doc loader.Document) []Chunk {
	lines := strings.Split(doc.Content, "\n")

	type section struct {
		heading  string
		content  string
		byteStart int
	}

	var sections []section
	var cur section
	bytePos := 0
	cur.byteStart = 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if cur.content != "" || cur.heading != "" {
				sections = append(sections, cur)
			}
			cur = section{heading: trimmed, byteStart: bytePos}
		} else {
			if cur.content != "" {
				cur.content += "\n"
			}
			cur.content += line
		}
		bytePos += len(line) + 1
	}
	if cur.content != "" || cur.heading != "" {
		sections = append(sections, cur)
	}

	cc := &CharacterChunker{TargetChars: m.TargetChars, OverlapChars: m.OverlapChars}
	var allChunks []Chunk
	globalIdx := 0

	for _, sec := range sections {
		body := sec.content
		if sec.heading != "" {
			body = sec.heading + "\n" + body
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}

		subDoc := loader.Document{
			Content:     body,
			PositionMap: shiftPositionMap(doc.PositionMap, sec.byteStart),
		}

		if len([]rune(body)) <= m.TargetChars {
			allChunks = append(allChunks, Chunk{
				Index:    globalIdx,
				Content:  body,
				StartPos: lookupPosition(doc.PositionMap, sec.byteStart),
				EndPos:   lookupPosition(doc.PositionMap, sec.byteStart+len(body)),
			})
			globalIdx++
		} else {
			sub := cc.Chunk(subDoc)
			for _, c := range sub {
				c.Index = globalIdx
				allChunks = append(allChunks, c)
				globalIdx++
			}
		}
	}
	return allChunks
}

func shiftPositionMap(pm []loader.PositionEntry, offset int) []loader.PositionEntry {
	out := make([]loader.PositionEntry, 0, len(pm))
	for _, e := range pm {
		if e.ByteOffset >= offset {
			out = append(out, loader.PositionEntry{
				ByteOffset: e.ByteOffset - offset,
				Logical:    e.Logical,
			})
		}
	}
	return out
}
