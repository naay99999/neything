package chat

import (
	"fmt"
	"strings"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/citation"
)

func buildPrompt(question string, ctxChunks []chunk.Chunk) string {
	var sb strings.Builder
	sb.WriteString("You are a helpful assistant. Answer based only on the provided context.\n\n")
	sb.WriteString("Context:\n")
	for _, c := range ctxChunks {
		source := citation.FormatSource(c.DocPath, c.DocType, c.StartPos, c.EndPos)
		if source == "" {
			source = fmt.Sprintf("chunk %s", c.ID)
		}
		sb.WriteString(fmt.Sprintf("[SOURCE: %s]\n", source))
		sb.WriteString(c.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Question: ")
	sb.WriteString(question)
	sb.WriteString("\n\nAnswer the question using the context above. At the end, include a 'Sources:' section listing the file paths and positions you used.")
	return sb.String()
}
