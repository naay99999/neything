package loader

import "context"

type Document struct {
	Path        string
	Type        string // md | txt | obsidian | notion
	Content     string
	Hash        string
	Metadata    map[string]string
	PositionMap []PositionEntry // byte offset → logical position (line/page/para)
}

type PositionEntry struct {
	ByteOffset int
	Logical    int
}

// Loader parses one file into one or more logical Documents. data is the
// file's already-read contents and hash its already-computed content hash
// (both computed once by the indexer's caller — Index/IndexPath — so Load
// implementations must not re-read or re-hash the file themselves).
type Loader interface {
	Load(ctx context.Context, path string, data []byte, hash string) ([]Document, error)
	Supports(path string) bool
}
