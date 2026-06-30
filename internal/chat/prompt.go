package chat

import (
	"fmt"
	"strings"

	"github.com/naay/ney/internal/chunk"
)

func buildPrompt(question string, ctxChunks []chunk.Chunk) string {
	var sb strings.Builder
	sb.WriteString("You are a helpful assistant. Answer based only on the provided context.\n\n")
	sb.WriteString("Context:\n")
	for _, c := range ctxChunks {
		sb.WriteString(fmt.Sprintf("[SOURCE: chunk %s (positions %d-%d)]\n", c.ID, c.StartPos, c.EndPos))
		sb.WriteString(c.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Question: ")
	sb.WriteString(question)
	sb.WriteString("\n\nAnswer the question using the context above. At the end, include a 'Sources:' section listing the chunk IDs and positions you used.")
	return sb.String()
}
