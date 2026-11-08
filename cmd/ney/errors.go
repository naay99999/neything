package main

import (
	"fmt"
	"os"
)

// printCLIError renders an error on stderr instead of cobra's raw error +
// usage dump.
func printCLIError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %v\n", Red("✗"), err)
}
