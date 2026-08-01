package main

import (
	"bufio"
	"strings"
	"testing"
)

// withStdin swaps the shared prompt reader for the duration of a test and
// clears the EOF latch on both sides, since both are package globals.
func withStdin(t *testing.T, input string) {
	t.Helper()
	origReader, origEOF := stdinPrompt, stdinEOF
	stdinPrompt = bufio.NewReader(strings.NewReader(input))
	stdinEOF = false
	t.Cleanup(func() { stdinPrompt, stdinEOF = origReader, origEOF })
}

// TestPromptDefaultReturnsEmptyOnEOF is the B6 unit guard: without the EOF
// latch, promptDefault(..., "y") answers "y" forever once stdin is exhausted,
// which is how `ney init < /dev/null` silently registered ney with every
// detected AI client.
func TestPromptDefaultReturnsEmptyOnEOF(t *testing.T) {
	withStdin(t, "")

	if got := promptDefault("register ney? (y/n)", "y"); got != "" {
		t.Errorf("promptDefault at EOF = %q, want \"\" — a default must never be auto-accepted", got)
	}
}

func TestPromptDefaultUsesDefaultOnEmptyLine(t *testing.T) {
	withStdin(t, "\n")

	// A real human pressing Enter still gets the default...
	if got := promptDefault("register ney? (y/n)", "y"); got != "y" {
		t.Errorf("promptDefault on empty line = %q, want %q", got, "y")
	}
	// ...but the next call, now at EOF, does not.
	if got := promptDefault("register ney? (y/n)", "y"); got != "" {
		t.Errorf("promptDefault after EOF = %q, want \"\"", got)
	}
}

func TestPromptLineLatchesEOF(t *testing.T) {
	withStdin(t, "hello\n")

	if got := promptLine("q: "); got != "hello" {
		t.Errorf("promptLine = %q, want %q", got, "hello")
	}
	if stdinEOF {
		t.Error("EOF latched too early")
	}
	if got := promptLine("q: "); got != "" {
		t.Errorf("promptLine at EOF = %q, want \"\"", got)
	}
	if !stdinEOF {
		t.Error("EOF should be latched after an exhausted read")
	}
}
