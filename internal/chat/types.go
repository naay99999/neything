package chat

import (
	"context"

	"github.com/naay/ney/internal/chunk"
)

type ChatModel interface {
	Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error)
	ModelID() string
}
