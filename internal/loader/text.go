package loader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TextLoader is the trivial .txt loader: the whole file becomes one section,
// no frontmatter/heading/wikilink parsing (that's markdown-specific).
type TextLoader struct{}

func (t *TextLoader) Supports(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".txt"
}

func (t *TextLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	content := string(data)

	doc := Document{
		Path:        path,
		Type:        "txt",
		Content:     content,
		Hash:        hash,
		PositionMap: buildLinePositionMap(content),
	}
	return []Document{doc}, nil
}
