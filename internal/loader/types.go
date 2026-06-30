package loader

import "context"

type Document struct {
	Path        string
	Type        string // md | pdf | docx | html | json | obsidian | notion | confluence | git
	Content     string
	Hash        string
	Metadata    map[string]string
	PositionMap []PositionEntry // byte offset → logical position (line/page/para)
}

type PositionEntry struct {
	ByteOffset int
	Logical    int
}

type Loader interface {
	Load(ctx context.Context, path string) ([]Document, error)
	Supports(path string) bool
}
