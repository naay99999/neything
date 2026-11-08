package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunSetupWizardRefusesNonInteractive pins B6: an EOF/piped stdin used to
// answer "y" to every "register ney with this client?" prompt, writing into
// Claude Desktop / Codex configs with no human present.
func TestRunSetupWizardRefusesNonInteractive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	orig := isInteractive
	isInteractive = func() bool { return false }
	t.Cleanup(func() { isInteractive = orig })

	err := runSetupWizard(context.Background())
	if err == nil {
		t.Fatal("expected runSetupWizard to refuse a non-TTY stdin")
	}
	if !strings.Contains(err.Error(), "interactive") {
		t.Errorf("error should explain why: %v", err)
	}

	// Nothing may have been written to any AI client config.
	for _, p := range []string{
		filepath.Join(dir, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		filepath.Join(dir, ".codex", "config.toml"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("wizard wrote %s despite refusing to run", p)
		}
	}
}

// TestSetupCompletedFollowsProfile: the wizard bootstraps ~/.ney/profile.md,
// so that file is the "set up" marker.
func TestSetupCompletedFollowsProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if setupCompleted() {
		t.Fatal("setupCompleted = true on a machine with no profile.md")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ney"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ney", "profile.md"), []byte("# me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !setupCompleted() {
		t.Fatal("setupCompleted = false although profile.md exists")
	}
}
