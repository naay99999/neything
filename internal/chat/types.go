package chat

import (
	"context"

	"github.com/naay99999/neything/internal/chunk"
)

type ChatModel interface {
	Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error)
	ModelID() string
}
