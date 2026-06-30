package chunk

import (
	"strings"

	"github.com/naay99999/neything/internal/loader"
)

type PageChunker struct {
	TargetChars  int
	OverlapChars int
}

func (p *PageChunker) Chunk(doc loader.Document) []Chunk {
	if doc.Content == "" {
		return nil
	}

	pages := splitPages(doc)
	if len(pages) == 0 {
		cc := &CharacterChunker{TargetChars: p.TargetChars, OverlapChars: p.OverlapChars}
		return cc.Chunk(doc)
	}

	cc := &CharacterChunker{TargetChars: p.TargetChars, OverlapChars: p.OverlapChars}
	var all []Chunk
	idx := 0

	for _, pg := range pages {
		body := strings.TrimSpace(pg.content)
		if body == "" {
			continue
		}
		subDoc := loader.Document{
			Content:     body,
			PositionMap: pg.posMap,
		}
		if len([]rune(body)) <= p.TargetChars {
			all = append(all, Chunk{
				Index:    idx,
				Content:  body,
				StartPos: pg.pageNum,
				EndPos:   pg.pageNum,
			})
			idx++
			continue
		}
		sub := cc.Chunk(subDoc)
		for _, c := range sub {
			c.Index = idx
			c.StartPos = pg.pageNum
			c.EndPos = pg.pageNum
			all = append(all, c)
			idx++
		}
	}
	return all
}

type pageSegment struct {
	content string
	pageNum int
	posMap  []loader.PositionEntry
}

func splitPages(doc loader.Document) []pageSegment {
	if len(doc.PositionMap) == 0 {
		return nil
	}

	var pages []pageSegment
	for i, entry := range doc.PositionMap {
		end := len(doc.Content)
		if i+1 < len(doc.PositionMap) {
			end = doc.PositionMap[i+1].ByteOffset
		}
		if entry.ByteOffset >= len(doc.Content) {
			continue
		}
		if end > len(doc.Content) {
			end = len(doc.Content)
		}
		content := doc.Content[entry.ByteOffset:end]
		pages = append(pages, pageSegment{
			content: content,
			pageNum: entry.Logical,
			posMap: []loader.PositionEntry{
				{ByteOffset: 0, Logical: entry.Logical},
			},
		})
	}
	return pages
}
