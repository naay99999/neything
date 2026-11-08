package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func PrintJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json error: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

func CheckMark(ok bool) string {
	if ok {
		return Green("✓")
	}
	return Red("✗")
}

// displayPath shortens an absolute path for human output: paths under the
// home directory are shown with a ~ prefix. JSON output keeps full paths.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}
