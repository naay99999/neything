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

type PDFLoader struct {
	OCR *OCRRunner
}

func (p *PDFLoader) Supports(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

func (p *PDFLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	content, posMap, err := p.extractText(path)
	if err != nil {
		return nil, err
	}

	if p.OCR != nil && p.OCR.ShouldOCR(content) {
		ocrText, ocrPos, ocrErr := p.OCR.ExtractPDF(path)
		if ocrErr != nil {
			if strings.TrimSpace(content) == "" {
				return nil, fmt.Errorf("pdf ocr failed: %w", ocrErr)
			}
		} else if strings.TrimSpace(ocrText) != "" {
			content = ocrText
			posMap = ocrPos
		}
	}

	doc := Document{
		Path:        path,
		Type:        "pdf",
		Content:     content,
		Hash:        hash,
		Metadata:    map[string]string{},
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}

func (p *PDFLoader) extractText(path string) (string, []PositionEntry, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open pdf %s: %w", path, err)
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
		normalized := strings.Join(strings.Fields(text), " ")
		if normalized == "" {
			continue
		}
		posMap = append(posMap, PositionEntry{ByteOffset: sb.Len(), Logical: i})
		sb.WriteString(normalized)
		sb.WriteString("\n")
	}
	return sb.String(), posMap, nil
}
