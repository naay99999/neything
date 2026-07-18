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

// promptLine prints question and reads one line from stdin.
func promptLine(question string) string {
	fmt.Print(question)
	line, _ := stdinPrompt.ReadString('\n')
	return strings.TrimSpace(line)
}

// promptIndexPath offers the current folder first, then falls back to a typed
// path. Returns "" when the user gives nothing.
func promptIndexPath() string {
	if cwd, err := os.Getwd(); err == nil {
		ans := strings.ToLower(promptLine(Cyan(fmt.Sprintf("Index this folder (%s)? [Y/n] ", displayPath(cwd)))))
		if ans == "" || strings.HasPrefix(ans, "y") {
			return cwd
		}
	}
	return promptLine(Cyan("Path to index: "))
}
