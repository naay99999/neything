package loader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

type PDFLoader struct{}

func (p *PDFLoader) Supports(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

func (p *PDFLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open pdf %s: %w", path, err)
	}
	defer f.Close()

	var sb strings.Builder
	var posMap []PositionEntry

	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		// normalize whitespace
		normalized := strings.Join(strings.Fields(text), " ")
		if normalized == "" {
			continue // skip image-only pages
		}
		posMap = append(posMap, PositionEntry{ByteOffset: sb.Len(), Logical: i})
		sb.WriteString(normalized)
		sb.WriteString("\n")
	}

	doc := Document{
		Path:        path,
		Type:        "pdf",
		Content:     sb.String(),
		Hash:        hash,
		Metadata:    map[string]string{},
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}
