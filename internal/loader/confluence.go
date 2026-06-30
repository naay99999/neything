package loader

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ConfluenceLoader struct{}

func (c *ConfluenceLoader) Supports(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".xml" {
		return false
	}
	peek, err := peekFile(path, peekSize)
	if err != nil {
		return false
	}
	return strings.Contains(peek, "ac:rich-text-body") || strings.Contains(peek, "confluence")
}

func (c *ConfluenceLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	text, err := parseConfluenceXML(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("parse confluence xml %s: %w", path, err)
	}

	doc := Document{
		Path:        path,
		Type:        "confluence",
		Content:     text,
		Hash:        hash,
		Metadata:    map[string]string{},
		PositionMap: buildLinePositionMap(text),
	}
	return []Document{doc}, nil
}

func parseConfluenceXML(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var blocks []string
	var current strings.Builder
	inBody := false
	blockIdx := 0

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			blocks = append(blocks, text)
		}
		current.Reset()
		blockIdx++
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if name == "rich-text-body" {
				inBody = true
			}
			if inBody && (name == "p" || name == "h1" || name == "h2" || name == "h3" || name == "li") {
				flush()
			}
		case xml.EndElement:
			name := t.Name.Local
			if name == "rich-text-body" {
				flush()
				inBody = false
			}
			if inBody && (name == "p" || name == "h1" || name == "h2" || name == "h3" || name == "li") {
				flush()
			}
		case xml.CharData:
			if inBody {
				current.WriteString(string(t))
			}
		}
	}
	flush()
	return strings.Join(blocks, "\n\n"), nil
}
