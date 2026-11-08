package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// stdinPrompt is shared by every prompt so buffered read-ahead (piped input,
// fast typists) isn't discarded between calls the way per-call Scanners do.
var stdinPrompt = bufio.NewReader(os.Stdin)

// stdinEOF latches once stdin is exhausted. Without it every later prompt
// reads "" instantly, and any prompt with a default would answer with that
// default — which is how a piped/EOF stdin used to silently answer "y" to
// "register ney with this AI client?" and write into Claude Desktop / Codex
// configs with no human present.
var stdinEOF bool

// promptLine prints question and reads one line from stdin. After EOF it
// returns "" without printing, so a caller looping over prompts stops
// echoing into a closed pipe.
func promptLine(question string) string {
	if stdinEOF {
		return ""
	}
	fmt.Print(question)
	line, err := stdinPrompt.ReadString('\n')
	if err != nil && line == "" {
		stdinEOF = true
		fmt.Println()
	}
	return strings.TrimSpace(line)
}
