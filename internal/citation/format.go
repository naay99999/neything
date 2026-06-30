package citation

import "fmt"

func FormatLocation(docType string, start, end int) string {
	if start <= 0 && end <= 0 {
		return ""
	}
	label := "lines"
	switch docType {
	case "pdf":
		label = "pages"
	case "docx":
		label = "paragraphs"
	}
	if start > 0 && end > 0 && start != end {
		return fmt.Sprintf("%s %d-%d", label, start, end)
	}
	pos := start
	if pos <= 0 {
		pos = end
	}
	return fmt.Sprintf("%s %d", label, pos)
}

func FormatSource(docPath, docType string, start, end int) string {
	loc := FormatLocation(docType, start, end)
	if loc == "" {
		return docPath
	}
	return fmt.Sprintf("%s (%s)", docPath, loc)
}
