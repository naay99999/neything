package config

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// SetDevRoots sets context.dev_roots in ~/.ney/config.yaml, preserving every
// other key, every comment, and key order.
//
// This is the ONLY writer of config.yaml in the codebase — config.yaml is
// user-owned. Never re-marshal the whole file from a Config value: that
// silently drops the user's comments and any key this version doesn't know
// about (which is exactly the bug this function replaced).
//
// An empty paths slice is a deliberate no-op that leaves the file untouched.
// Materializing `context.dev_roots: []` would make Load's
// v.IsSet("context.dev_roots") true and permanently suppress defaultDevRoots
// (~/workspace) for that install, silently stopping the repo scan behind
// get_context / list_projects.
func SetDevRoots(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	cfgPath := ConfigPath()
	if _, err := ensureConfigFile(); err != nil {
		return err
	}

	original, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	rendered, err := renderWithDevRoots(original, paths)
	if err != nil {
		return err
	}

	// Verify before committing. A writer that emits a duplicate top-level
	// key makes the NEXT Load fail with "mapping key already defined" — an
	// unrecoverable-looking break right after setup. Re-parsing the rendered
	// bytes (both as plain YAML and through viper, the decoder Load uses)
	// turns that into a returned error with the original file still intact.
	if err := verifyRendered(rendered, paths); err != nil {
		return fmt.Errorf("refusing to write config: %w", err)
	}

	if err := os.WriteFile(cfgPath+".bak", original, 0600); err != nil {
		return fmt.Errorf("back up config: %w", err)
	}
	return writeFileAtomic(cfgPath, rendered, 0600)
}

// renderWithDevRoots edits the context.dev_roots key in a YAML document,
// creating the context mapping or the dev_roots key when absent. It operates
// on a yaml.Node tree, which round-trips comments, key order, and every key
// this package does not model.
func renderWithDevRoots(original []byte, paths []string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	root, err := documentRoot(&doc)
	if err != nil {
		return nil, err
	}

	ctxNode := findOrCreateMapping(root, "context")
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, p := range paths {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p})
	}
	setMappingValue(ctxNode, "dev_roots", seq)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	return buf.Bytes(), nil
}

// documentRoot returns the top-level mapping node of a parsed document,
// creating one for an empty or comment-only file.
func documentRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 || len(doc.Content) == 0 {
		mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{mapping}
		return mapping, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config root is not a mapping")
	}
	return root, nil
}

// findOrCreateMapping returns the mapping node stored under key in m,
// appending an empty mapping when key is absent or holds a null.
func findOrCreateMapping(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		v := m.Content[i+1]
		// `context:` with nothing under it parses as a null scalar.
		if v.Kind != yaml.MappingNode {
			*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		return v
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
	return value
}

// setMappingValue replaces the value stored under key, or appends the pair.
// Replacing in place (rather than delete+append) preserves key order and the
// comments attached to the existing key.
func setMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			value.HeadComment = m.Content[i+1].HeadComment
			value.LineComment = m.Content[i+1].LineComment
			value.FootComment = m.Content[i+1].FootComment
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// verifyRendered re-parses the bytes SetDevRoots is about to write and
// confirms viper — the decoder Load uses — reads back exactly the dev roots
// we asked for.
func verifyRendered(rendered []byte, want []string) error {
	var probe map[string]any
	if err := yaml.Unmarshal(rendered, &probe); err != nil {
		return fmt.Errorf("rendered config does not parse: %w", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(rendered)); err != nil {
		return fmt.Errorf("rendered config does not load: %w", err)
	}
	if !v.IsSet("context.dev_roots") {
		return fmt.Errorf("rendered config has no context.dev_roots")
	}
	got := v.GetStringSlice("context.dev_roots")
	if len(got) != len(want) {
		return fmt.Errorf("rendered config has %d dev roots, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("rendered dev root %d = %q, want %q", i, got[i], want[i])
		}
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so a reader never observes a partial config.
// Duplicated from internal/context rather than shared: internal/config is
// imported by nearly every package and stays dependency-light on purpose.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
