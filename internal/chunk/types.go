package chunk

import "github.com/naay/ney/internal/loader"

type Chunk struct {
	ID         string
	DocumentID int64
	Index      int
	Content    string
	StartPos   int
	EndPos     int
}

type ChunkStrategy interface {
	Chunk(doc loader.Document) []Chunk
}
