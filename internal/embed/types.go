package embed

import (
	"context"
	"io"
	"strings"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
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
