package loader

import (
	"context"
	"path/filepath"
	"strings"
)

type MarkdownLoader struct{}

func (m *MarkdownLoader) Supports(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func (m *MarkdownLoader) Load(_ context.Context, path string, data []byte, hash string) ([]Document, error) {
	content := string(data)

	posMap := buildLinePositionMap(content)
	headings := extractHeadings(content)

	doc := Document{
		Path:        path,
		Type:        "md",
		Content:     content,
		Hash:        hash,
		Metadata:    map[string]string{"headings": strings.Join(headings, ", ")},
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}

func buildLinePositionMap(content string) []PositionEntry {
	var entries []PositionEntry
	line := 1
	entries = append(entries, PositionEntry{ByteOffset: 0, Logical: line})
	for i, ch := range content {
		if ch == '\n' {
			line++
			if i+1 < len(content) {
				entries = append(entries, PositionEntry{ByteOffset: i + 1, Logical: line})
			}
		}
	}
	return entries
}

func extractHeadings(content string) []string {
	var headings []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if text != "" {
				headings = append(headings, text)
			}
		}
	}
	return headings
}
