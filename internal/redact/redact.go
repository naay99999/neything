// Package redact masks secrets in text before it enters a handoff brief.
// It prefers false positives over leaks.
package redact

import (
	"regexp"
	"strings"
)

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
		regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{10,}`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`),
	}

	// authValue masks "Basic|Token|Bearer <value>" only for a case-sensitive
	// keyword and a value that looks like a credential (a digit or one of +/=),
	// so prose such as "Basic configuration" survives.
	authValue = regexp.MustCompile(`\b(Basic|Token|Bearer)(\s+)([A-Za-z0-9+/=._~-]{8,})`)

	pemOpen = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*`)

	urlUserinfo = regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^/\s:@]+:)[^@\s/]+@`)

	cliFlag = regexp.MustCompile(`(?i)(--(?:token|password|passwd|secret|api-key|apikey)[= ])\S+`)

	keyValue = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:secret|password|passwd|token|api[_-]?key|private[_-]?key|credential|auth)[A-Za-z0-9_.-]*["']?\s*[:=]\s*["']?)[^\s",']+`)
)

// String returns s with secrets replaced by "[redacted]".
func String(s string) string {
	s = pemBlock.ReplaceAllString(s, marker)
	s = pemOpen.ReplaceAllString(s, marker) // unterminated block: everything after BEGIN
	s = skKey.ReplaceAllString(s, "${1}"+marker)
	s = urlUserinfo.ReplaceAllString(s, "${1}"+marker+"@")
	s = cliFlag.ReplaceAllString(s, "${1}"+marker)
	s = authValue.ReplaceAllStringFunc(s, func(m string) string {
		sub := authValue.FindStringSubmatch(m)
		if strings.ContainsAny(sub[3], "0123456789+/=") {
			return sub[1] + sub[2] + marker
		}
		return m
	})
	for _, re := range tokenShapes {
		s = re.ReplaceAllString(s, marker)
	}
	return keyValue.ReplaceAllString(s, "${1}"+marker)
}
