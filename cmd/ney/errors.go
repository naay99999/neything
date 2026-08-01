package main

import (
	"fmt"
	"os"
	"strings"
)

// printCLIError renders an error with an actionable hint instead of cobra's
// raw error + usage dump.
func printCLIError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %v\n", Red("✗"), err)
	for _, hint := range friendlyHints(err) {
		fmt.Fprintf(os.Stderr, "  %s %s\n", Dim("→"), hint)
	}
}

// friendlyHints maps common failure text to next steps a user can act on.
func friendlyHints(err error) []string {
	msg := err.Error()
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}

	switch {
	case has("is writing — stop it first"):
		return []string{
			"Wait for the other process to finish, or stop it and rerun this command",
			"Check status with: ney status",
		}
	case has("status 404"):
		return []string{"The model was not found on the server — check the model name (ney models) or load it in your server"}
	}
	return nil
}
