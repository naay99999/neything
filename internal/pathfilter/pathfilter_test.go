package pathfilter

import (
	"path/filepath"
	"testing"
)

func TestExcludedFileDeniesSecrets(t *testing.T) {
	denied := []string{
		".env", ".hidden.md", ".anything",
		"prod.env", "staging.env",
		"id_rsa", "id_rsa.pub", "id_ed25519", "id_ed25519.pub", "id_ecdsa",
		"server.key", "cert.pem", "keystore.jks", "vault.kdbx", "backup.gpg",
		"secrets.yaml", "my-secret-notes.md", "credentials.json",
		"MyPasswords.md", "PASSWD.txt", "ApiKey.json", "api_key.md", "api-keys.txt",
	}
	var f *Filter // nil receiver: built-in rules only
	for _, name := range denied {
		if !f.ExcludedFile(name) {
			t.Errorf("ExcludedFile(%q) = false, want true", name)
		}
	}
}

// TestExcludedFileDeniesHighValueSecrets covers the patterns added for L-1:
// credential/host-trust files that are routinely copied OUT of a dot-dir (so
// the dotfile rule alone never sees them).
func TestExcludedFileDeniesHighValueSecrets(t *testing.T) {
	denied := []string{
		"known_hosts", "known_hosts.old", "authorized_keys", "authorized_keys2",
		"work.netrc", "prod.kubeconfig", "AuthKey_ABC123.p8", "office.ovpn",
	}
	var f *Filter
	for _, name := range denied {
		if !f.ExcludedFile(name) {
			t.Errorf("ExcludedFile(%q) = false, want true", name)
		}
	}
}

func TestExcludedFileAllowsNormalFiles(t *testing.T) {
	allowed := []string{
		"notes.md", "tokenizer.md", "keyboard.md", "README.md",
		"invoice-1233.pdf", "report.rtf", "data.json", "envelope.md",
		"monkey.md", // contains "key" but doesn't match *.key or *apikey*
		// The L-1 additions must not swallow innocent docs: they are anchored
		// (prefix or extension), never substring matches.
		"config.md", "configuration.md", "hosts.md", "unknown-hosts-plan.md",
		"netrc-migration.md", "kubeconfig-notes.md", "keys-of-the-kingdom.md",
	}
	var f *Filter
	for _, name := range allowed {
		if f.ExcludedFile(name) {
			t.Errorf("ExcludedFile(%q) = true, want false", name)
		}
	}
}

func TestExcludedDir(t *testing.T) {
	var f *Filter
	for _, name := range []string{".git", ".ssh", "secrets", "credentials"} {
		if !f.ExcludedDir(name) {
			t.Errorf("ExcludedDir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"docs", "notes", "src"} {
		if f.ExcludedDir(name) {
			t.Errorf("ExcludedDir(%q) = true, want false", name)
		}
	}
}

func TestUserExtraPatterns(t *testing.T) {
	f, err := New([]string{"*.bak", "drafts-*"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.ExcludedFile("old.bak") {
		t.Error("user pattern *.bak should deny old.bak")
	}
	if !f.ExcludedDir("drafts-2026") {
		t.Error("user pattern drafts-* should deny drafts-2026 dir")
	}
	if f.ExcludedFile("notes.md") {
		t.Error("user patterns should not affect normal files")
	}
	// built-ins still apply on a user-configured filter
	if !f.ExcludedFile("id_rsa") {
		t.Error("built-in deny should still apply with user extras")
	}
}

func TestNewRejectsBadGlob(t *testing.T) {
	if _, err := New([]string{"[unclosed"}); err == nil {
		t.Fatal("expected error for malformed glob")
	}
}

func TestExcludedPath(t *testing.T) {
	var f *Filter
	root := filepath.Join("/tmp", "corpus")
	cases := []struct {
		rel  string
		want bool
	}{
		{"notes.md", false},
		{"sub/notes.md", false},
		{".env", true},
		{"sub/.env", true},
		{".ssh/id_rsa", true},      // denied dir component
		{"sub/passwords.md", true}, // denied file component
		{"secrets/plan.md", true},  // denied intermediate dir
		{"docs/deep/tokenizer.md", false},
	}
	for _, c := range cases {
		p := filepath.Join(root, c.rel)
		if got := f.ExcludedPath(root, p); got != c.want {
			t.Errorf("ExcludedPath(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
	// The root itself and paths not under root are never denied here —
	// containment is the caller's check.
	if f.ExcludedPath(root, root) {
		t.Error("root itself should not be denied")
	}
	if f.ExcludedPath(root, "/somewhere/else/.env") {
		t.Error("paths outside root are not this function's call")
	}
}
