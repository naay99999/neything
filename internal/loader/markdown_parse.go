package loader

import (
	"regexp"
	"strings"
)

var (
	wikilinkRe  = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
	inlineTagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][\w/-]*)`)
)

func isNotionExport(content string) bool {
	if !strings.Contains(content, "| Property |") && !strings.Contains(content, "| --- |") {
		return false
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return false
	}
	first := strings.TrimSpace(lines[0])
	return strings.HasPrefix(first, "# ")
}

func isObsidianNote(content string) bool {
	if strings.HasPrefix(content, "---") {
		return true
	}
	return wikilinkRe.MatchString(content)
}

func extractWikilinks(content string) []string {
	matches := wikilinkRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var links []string
	for _, m := range matches {
		link := strings.TrimSpace(m[1])
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		links = append(links, link)
	}
	return links
}

func extractInlineTags(content string) []string {
	matches := inlineTagRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var tags []string
	for _, m := range matches {
		tag := strings.TrimSpace(m[1])
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	return tags
}

type frontmatter struct {
	body     string
	metadata map[string]string
}

func parseFrontmatter(content string) frontmatter {
	if !strings.HasPrefix(content, "---") {
		return frontmatter{body: content, metadata: map[string]string{}}
	}
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return frontmatter{body: content, metadata: map[string]string{}}
	}
	end += 3
	fmBlock := content[3:end]
	body := strings.TrimPrefix(content[end+4:], "\n")

	meta := map[string]string{}
	for _, line := range strings.Split(fmBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return frontmatter{body: body, metadata: meta}
}

func stripNotionPropertyTable(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}

	title := lines[0]
	tableStart := -1
	for i := 1; i < len(lines)-1; i++ {
		if !strings.Contains(lines[i], "|") {
			continue
		}
		if strings.Contains(lines[i+1], "---") {
			tableStart = i
			break
		}
	}
	if tableStart < 0 {
		return content
	}

	tableEnd := tableStart + 2
	for tableEnd < len(lines) && strings.Contains(lines[tableEnd], "|") {
		tableEnd++
	}

	var bodyLines []string
	bodyLines = append(bodyLines, title)
	if tableEnd < len(lines) {
		bodyLines = append(bodyLines, lines[tableEnd:]...)
	}
	return strings.TrimSpace(strings.Join(bodyLines, "\n"))
}
