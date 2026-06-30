package loader

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type DOCXLoader struct{}

func (d *DOCXLoader) Supports(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".docx"
}

func (d *DOCXLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx %s: %w", path, err)
	}
	defer zr.Close()

	var docXML io.ReadCloser
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docXML, err = f.Open()
			if err != nil {
				return nil, fmt.Errorf("open word/document.xml: %w", err)
			}
			break
		}
	}
	if docXML == nil {
		return nil, fmt.Errorf("word/document.xml not found in %s", path)
	}
	defer docXML.Close()

	paragraphs, err := parseDocXML(docXML)
	if err != nil {
		return nil, fmt.Errorf("parse docx xml: %w", err)
	}

	var sb strings.Builder
	var posMap []PositionEntry
	for i, para := range paragraphs {
		if strings.TrimSpace(para) == "" {
			continue
		}
		posMap = append(posMap, PositionEntry{ByteOffset: sb.Len(), Logical: i})
		sb.WriteString(para)
		sb.WriteString("\n\n")
	}

	doc := Document{
		Path:        path,
		Type:        "docx",
		Content:     sb.String(),
		Hash:        hash,
		Metadata:    map[string]string{},
		PositionMap: posMap,
	}
	return []Document{doc}, nil
}

func parseDocXML(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var paragraphs []string
	var currentPara strings.Builder
	inPara := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// try to continue on non-fatal XML errors
			continue
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				inPara = true
				currentPara.Reset()
			case "br":
				if inPara {
					currentPara.WriteString(" ")
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" && inPara {
				paragraphs = append(paragraphs, currentPara.String())
				inPara = false
			}
		case xml.CharData:
			if inPara {
				currentPara.WriteString(string(t))
			}
		}
	}
	return paragraphs, nil
}
