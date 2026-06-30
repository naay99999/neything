package loader

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxJSONLeaves  = 500
	maxJSONDepth   = 12
	maxJSONKeyLen  = 120
	maxJSONValLen  = 2000
)

type JSONLoader struct{}

func (j *JSONLoader) Supports(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".json"
}

func (j *JSONLoader) Load(_ context.Context, path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse json %s: %w", path, err)
	}

	var lines []string
	w := &jsonFlattener{lines: &lines}
	w.walk("", root, 0)

	content := strings.Join(lines, "\n")
	doc := Document{
		Path:     path,
		Type:     "json",
		Content:  content,
		Hash:     hash,
		Metadata: map[string]string{"leaf_count": fmt.Sprintf("%d", len(lines))},
	}
	return []Document{doc}, nil
}

type jsonFlattener struct {
	lines *[]string
	count int
}

func (f *jsonFlattener) walk(prefix string, v any, depth int) {
	if f.count >= maxJSONLeaves || depth > maxJSONDepth {
		return
	}
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			key := truncate(k, maxJSONKeyLen)
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			f.walk(path, child, depth+1)
		}
	case []any:
		for i, child := range val {
			path := fmt.Sprintf("%s[%d]", prefix, i)
			if prefix == "" {
				path = fmt.Sprintf("[%d]", i)
			}
			f.walk(path, child, depth+1)
		}
	default:
		text := truncate(fmt.Sprintf("%v", val), maxJSONValLen)
		if strings.TrimSpace(text) == "" {
			return
		}
		line := prefix + ": " + text
		if prefix == "" {
			line = text
		}
		*f.lines = append(*f.lines, line)
		f.count++
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
