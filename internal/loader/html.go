package loader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

type HTMLLoader struct{}

func (h *HTMLLoader) Supports(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".html" || ext == ".htm"
}

func (h *HTMLLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("parse html %s: %w", path, err)
	}

	title := extractHTMLTitle(root)
	text, posMap := htmlToText(root)

	doc := Document{
		Path:        path,
		Type:        "html",
		Content:     text,
		Hash:        hash,
		Metadata:    map[string]string{},
		PositionMap: posMap,
	}
	if title != "" {
		doc.Metadata["title"] = title
	}
	return []Document{doc}, nil
}

func extractHTMLTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(textContent(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := extractHTMLTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func htmlToText(n *html.Node) (string, []PositionEntry) {
	var sb strings.Builder
	var posMap []PositionEntry
	block := 0

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript":
				return
			case "p", "div", "section", "article", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "br":
				if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") {
					posMap = append(posMap, PositionEntry{ByteOffset: sb.Len(), Logical: block})
					sb.WriteString("\n")
					block++
				}
			}
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text == "" {
				return
			}
			if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "\n") && !strings.HasSuffix(sb.String(), " ") {
				sb.WriteString(" ")
			}
			if sb.Len() == 0 {
				posMap = append(posMap, PositionEntry{ByteOffset: 0, Logical: block})
			}
			sb.WriteString(text)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String()), posMap
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
