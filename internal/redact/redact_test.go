package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	cases := []struct{ in, secret string }{
		{"export OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl", "sk-proj-abc123def456ghi789jkl"},
		{"key sk-ant-api03-AAAABBBBCCCCDDDDEEEE", "sk-ant-api03-AAAABBBBCCCCDDDDEEEE"},
		{"git clone https://ghp_0123456789abcdefghijklmnopqrstuvwxyz@github.com/x", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
		{"github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "github_pat_11ABCDEFG0123456789_abcdefghijklmnop"},
		{"SLACK=xoxb-1234-5678-abcdefgh", "xoxb-1234-5678-abcdefgh"},
		{"aws AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc-123", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc-123"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----", "b3BlbnNzaC1rZXk"},
		{"DB_PASSWORD=hunter2hunter2", "hunter2hunter2"},
		{`{"client_secret": "s3cr3tvalue"}`, "s3cr3tvalue"},
		{"MY_TOKEN: abc.def.ghi", "abc.def.ghi"},
		{"export API_KEY='abc123secret'", "abc123secret"},
		{"{'password': 'hunter2x'}", "hunter2x"},
		{`{"password": "v4lue9"}`, "v4lue9"},
		{"Authorization: Basic dXNlcjpwYXNzd29yZA==", "dXNlcjpwYXNzd29yZA=="},
	}
	for _, c := range cases {
		got := String(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("secret leaked: in=%q got=%q", c.in, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Errorf("no marker: in=%q got=%q", c.in, got)
		}
	}
}

func TestStringLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{"go test ./... && bun run build", "token budget is 300", "src/task-runner-config.ts", "risk-assessment-notes.md"} {
		if got := String(s); got != s {
			t.Errorf("changed %q -> %q", s, got)
		}
	}
}
