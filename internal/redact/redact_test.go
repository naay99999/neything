package redact

import (
	"strings"
	"testing"
)

// fakeGoogleKey is a made-up key split in two so secret scanners do not flag the fixture.
const fakeGoogleKey = "AIza" + "SyA1234567890abcdefghijklmnopqrstuv"

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
		{"STRIPE sk_live_abcdefghij1234567890", "sk_live_abcdefghij1234567890"},
		{"rk_test_ABCDEFGHIJ0123456789", "rk_test_ABCDEFGHIJ0123456789"},
		{"key " + fakeGoogleKey, fakeGoogleKey},
		{"npm_abcdefghijklmnopqrstuvwxyz0123456789", "npm_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"postgres://admin:hunter22pw@db.example.com/app", "hunter22pw"},
		{"curl --token abcd1234efgh", "abcd1234efgh"},
		{"cli --password=hunter22pw", "hunter22pw"},
		{"cli --api-key zzzz9999", "zzzz9999"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAunterminated", "MIIEowIBAAKCAQEAunterminated"},
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
	for _, s := range []string{"go test ./... && bun run build", "token budget is 300", "src/task-runner-config.ts", "risk-assessment-notes.md", "Basic configuration is fine", "token management works", "cli --token-budget 3000"} {
		if got := String(s); got != s {
			t.Errorf("changed %q -> %q", s, got)
		}
	}
}

func TestURLUserinfoKeepsUser(t *testing.T) {
	got := String("postgres://u:pw@host/db")
	if got != "postgres://u:[redacted]@host/db" {
		t.Errorf("got %q", got)
	}
}
