package loader

import (
	"context"
	"strings"
)

const peekSize = 2048

type NotionLoader struct{}

func (n *NotionLoader) Supports(path string) bool {
	if !isMarkdownExt(path) {
		return false
	}
	peek, err := peekFile(path, peekSize)
	if err != nil {
		return false
	}
	return isNotionExport(peek)
}

func (n *NotionLoader) Load(_ context.Context, path string, data []byte, hash string) ([]Document, error) {
	content := string(data)
	body := stripNotionPropertyTable(content)
	posMap := buildLinePositionMap(body)

	doc := Document{
		Path:        path,
		Type:        "notion",
		Content:     body,
		Hash:        hash,
		Metadata:    map[string]string{"source": "notion_export"},
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}

type ObsidianLoader struct{}

func (o *ObsidianLoader) Supports(path string) bool {
	if !isMarkdownExt(path) {
		return false
	}
	peek, err := peekFile(path, peekSize)
	if err != nil {
		return false
	}
	if isNotionExport(peek) {
		return false
	}
	return isObsidianNote(peek)
}

func (o *ObsidianLoader) Load(_ context.Context, path string, data []byte, hash string) ([]Document, error) {
	content := string(data)

	fm := parseFrontmatter(content)
	body := fm.body
	posMap := buildLinePositionMap(body)
	headings := extractHeadings(body)
	wikilinks := extractWikilinks(body)
	tags := extractInlineTags(body)

	meta := map[string]string{}
	for k, v := range fm.metadata {
		meta[k] = v
	}
	if len(headings) > 0 {
		meta["headings"] = strings.Join(headings, ", ")
	}
	if len(wikilinks) > 0 {
		meta["wikilinks"] = strings.Join(wikilinks, ", ")
	}
	if len(tags) > 0 {
		meta["tags"] = strings.Join(tags, ", ")
	}
	if title := meta["title"]; title == "" && len(headings) > 0 {
		meta["title"] = headings[0]
	}

	doc := Document{
		Path:        path,
		Type:        "obsidian",
		Content:     body,
		Hash:        hash,
		Metadata:    meta,
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}
