// Package projects holds ney's project memory: one small, agent-written card
// per repo under ~/.ney/projects, a registry of known repos, the table of
// contents rendered from them, and the rules for when a card is stale.
package projects

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	neycontext "github.com/naay99999/neything/internal/context"
)

// Card caps, in runes (counts for the list fields).
const (
	MaxSummary     = 300
	MaxStack       = 100
	MaxStatus      = 150
	MaxReadMore    = 5
	MaxReadMoreLen = 200
	MaxFacts       = 8
	MaxFactLen     = 150
)

const (
	sectionReadMore = "Read more"
	sectionFacts    = "Facts"
)

// Card is one project's memory. The file is the source of truth and the user
// may edit it by hand: unknown frontmatter keys and unknown "## " sections
// are kept, in order, across a Parse/Marshal round trip.
type Card struct {
	Name, Path, Remote, Stack, Status, Commit, UpdatedBy string
	Updated                                              time.Time
	Summary                                              string   // body text before the first "## " section
	ReadMore                                             []string // "## Read more" bullets
	Facts                                                []string // "## Facts" bullets

	extraFront []*yaml.Node // unknown frontmatter key/value node pairs
	extraBody  string       // unknown "## " sections, verbatim
}

// Parse reads a card file: YAML frontmatter between "---" lines, then a
// markdown body.
func Parse(data []byte) (Card, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Card{}, errors.New("card has no frontmatter")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	front, body := "", ""
	switch {
	case strings.HasPrefix(rest, "---\n"):
		body = rest[len("---\n"):]
	case end >= 0:
		front, body = rest[:end+1], rest[end+len("\n---\n"):]
	case strings.HasSuffix(rest, "\n---"):
		front = rest[:len(rest)-len("---")]
	default:
		return Card{}, errors.New("card frontmatter is not closed")
	}

	var c Card
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return Card{}, fmt.Errorf("card frontmatter: %w", err)
	}
	if len(doc.Content) > 0 {
		m := doc.Content[0]
		if m.Kind != yaml.MappingNode {
			return Card{}, errors.New("card frontmatter is not a mapping")
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i], m.Content[i+1]
			if !c.setKnown(k.Value, v) {
				c.extraFront = append(c.extraFront, k, v)
			}
		}
	}
	c.parseBody(body)
	return c, nil
}

func (c *Card) setKnown(key string, v *yaml.Node) bool {
	if v.Kind != yaml.ScalarNode {
		return false
	}
	switch key {
	case "name":
		c.Name = v.Value
	case "path":
		c.Path = v.Value
	case "remote":
		c.Remote = v.Value
	case "stack":
		c.Stack = v.Value
	case "status":
		c.Status = v.Value
	case "commit":
		c.Commit = v.Value
	case "updated_by":
		c.UpdatedBy = v.Value
	case "updated":
		t, err := time.Parse(time.RFC3339, v.Value)
		if err != nil {
			return false
		}
		c.Updated = t
	default:
		return false
	}
	return true
}

func (c *Card) parseBody(body string) {
	lines := strings.Split(body, "\n")
	var summary []string
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		summary = append(summary, lines[i])
	}
	c.Summary = strings.TrimSpace(strings.Join(summary, "\n"))
	var extra []string
	for i < len(lines) {
		title := strings.TrimSpace(strings.TrimPrefix(lines[i], "## "))
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "## ") {
			j++
		}
		block := lines[i:j]
		switch title {
		case sectionReadMore:
			c.ReadMore = bullets(block[1:])
		case sectionFacts:
			c.Facts = bullets(block[1:])
		default:
			extra = append(extra, strings.TrimRight(strings.Join(block, "\n"), "\n"))
		}
		i = j
	}
	if len(extra) > 0 {
		c.extraBody = strings.Join(extra, "\n\n") + "\n"
	}
}

func bullets(lines []string) []string {
	var out []string
	for _, l := range lines {
		if s, ok := strings.CutPrefix(strings.TrimSpace(l), "- "); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// Marshal renders the card file. Empty fields and sections are omitted.
func (c Card) Marshal() []byte {
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) {
		if v != "" {
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Value: v})
		}
	}
	add("name", c.Name)
	add("path", c.Path)
	add("remote", c.Remote)
	add("stack", c.Stack)
	add("status", c.Status)
	add("commit", c.Commit)
	if !c.Updated.IsZero() {
		add("updated", c.Updated.UTC().Format(time.RFC3339))
	}
	add("updated_by", c.UpdatedBy)
	m.Content = append(m.Content, c.extraFront...)

	var b bytes.Buffer
	b.WriteString("---\n")
	if len(m.Content) > 0 {
		var fb bytes.Buffer
		enc := yaml.NewEncoder(&fb)
		enc.SetIndent(2)
		_ = enc.Encode(m)
		_ = enc.Close()
		b.Write(fb.Bytes())
	}
	b.WriteString("---\n")
	if c.Summary != "" {
		b.WriteString(c.Summary + "\n")
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n## " + title + "\n")
		for _, it := range items {
			b.WriteString("- " + it + "\n")
		}
	}
	section(sectionReadMore, c.ReadMore)
	section(sectionFacts, c.Facts)
	if c.extraBody != "" {
		b.WriteString("\n" + c.extraBody)
	}
	return b.Bytes()
}

// Load reads and parses the card at path. A missing file yields an error
// wrapping fs.ErrNotExist.
func Load(path string) (Card, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Card{}, err
	}
	return Parse(data)
}

// Save writes c to path atomically (dir 0700, file 0600). An existing file
// that does not parse as a card — e.g. a broken hand edit — is copied to
// path+".bak" first, never silently overwritten.
func Save(path string, c Card) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if _, perr := Parse(old); perr != nil {
			if err := neycontext.WriteFileAtomic(path+".bak", old, 0o600); err != nil {
				return err
			}
		}
	}
	return neycontext.WriteFileAtomic(path, c.Marshal(), 0o600)
}
