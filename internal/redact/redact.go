// Package redact masks secrets in text before it enters a handoff brief.
// It prefers false positives over leaks.
package redact

import "regexp"

const marker = "[redacted]"

var (
	pemBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)

	// skKey needs a left boundary (RE2 has no lookbehind), so the preceding
	// character is captured and re-emitted.
	skKey = regexp.MustCompile(`(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{10,}`)

	tokenShapes = []*regexp.Regexp{
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{8,}`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)\b(?:Basic|Token)\s+[A-Za-z0-9+/=._-]{8,}`),
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	}

	keyValue = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:secret|password|passwd|token|api[_-]?key|private[_-]?key|credential|auth)[A-Za-z0-9_.-]*["']?\s*[:=]\s*["']?)[^\s",']+`)
)

// String returns s with secrets replaced by "[redacted]".
func String(s string) string {
	s = pemBlock.ReplaceAllString(s, marker)
	s = skKey.ReplaceAllString(s, "${1}"+marker)
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, marker)
	}
	return keyValue.ReplaceAllString(s, "${1}"+marker)
}
