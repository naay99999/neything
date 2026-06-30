package chunk

import "github.com/naay99999/neything/internal/loader"

type Chunk struct {
	ID         string
	DocumentID int64
	DocPath    string
	DocType    string
	Index      int
	Content    string
	StartPos   int
	EndPos     int
}

type ChunkStrategy interface {
	Chunk(doc loader.Document) []Chunk
}
