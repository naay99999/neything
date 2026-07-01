package chat

import (
	"context"
	"io"
	"strings"

	"github.com/naay99999/neything/internal/chunk"
)

type ChatModel interface {
	Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error)
	ModelID() string
}

// errBodySnippet returns ": <body>" trimmed to a single short line, so HTTP
// error responses (e.g. LM Studio's "model not found") reach the user.
func errBodySnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 400))
	s := strings.Join(strings.Fields(string(b)), " ")
	if s == "" {
		return ""
	}
	return ": " + s
}
