package main

import (
	"strings"
	"testing"
)

// TestAskWithoutChatProviderIsFriendly exercises the full CLI path (matching
// what the REPL's ask dispatch also goes through, via rootCmd.Execute) with a
// fresh ~/.ney whose default config has no chat provider configured
// ("none"). It must fail fast with a friendly, actionable error instead of a
// nil-pointer panic on app.Chat.Complete.
func TestAskWithoutChatProviderIsFriendly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	resetAllFlags(rootCmd)
	defer resetAllFlags(rootCmd)

	rootCmd.SetArgs([]string{"ask", "what is the meaning of life"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no chat provider is configured")
	}
	if !strings.Contains(err.Error(), "no chat provider configured") {
		t.Fatalf("expected friendly 'no chat provider configured' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ney init") {
		t.Fatalf("expected error to point at `ney init`, got: %v", err)
	}
}
