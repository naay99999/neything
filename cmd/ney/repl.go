package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/chzyer/readline"
	"github.com/mattn/go-isatty"
	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func runREPL() error {
	rootCmd.InitDefaultHelpCmd()

	if err := os.MkdirAll(config.NeyDir(), 0o700); err != nil {
		return err
	}
	historyPath := filepath.Join(config.NeyDir(), "history")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:            Cyan("ney> "),
		HistoryFile:       historyPath,
		AutoComplete:      buildCompleter(),
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		return err
	}
	defer rl.Close()

	state := printBanner()
	if state.chunkCount == 0 && colorEnabled && isatty.IsTerminal(os.Stdin.Fd()) {
		runOnboarding(state)
	}

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			continue
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if handled, exit := handleMetaCommand(line); handled {
			if exit {
				return nil
			}
			continue
		}

		if err := dispatchLine(line); err != nil {
			printCLIError(err)
			continue
		}
	}
}

func handleMetaCommand(line string) (handled bool, exit bool) {
	switch strings.ToLower(strings.TrimPrefix(line, "/")) {
	case ":quit", ":exit", ":q", "exit", "quit", "q", "leave", "bye":
		return true, true
	case ":help", "?":
		printREPLHelp()
		return true, false
	case ":clear":
		fmt.Print("\033[H\033[2J")
		return true, false
	default:
		return false, false
	}
}

func dispatchLine(line string) error {
	line = strings.TrimPrefix(line, "/")
	firstWord := strings.Fields(line)[0]
	known := knownCommandNames()

	var tokens []string
	if known[firstWord] {
		remainder := strings.TrimSpace(line[len(firstWord):])
		switch firstWord {
		case "ask", "search":
			remainder = unquote(remainder)
			if remainder == "" {
				return fmt.Errorf("usage: %s <question>", firstWord)
			}
			tokens = []string{firstWord, remainder}
		case "config":
			if remainder == "" {
				tokens = guidedConfigTokens()
			} else {
				tokens = append([]string{firstWord}, tokenizeRest(remainder)...)
			}
		case "reset":
			if remainder == "" {
				tokens = guidedResetTokens()
			} else {
				tokens = append([]string{firstWord}, tokenizeRest(remainder)...)
			}
		case "index":
			if remainder == "" {
				tokens = guidedIndexTokens()
			} else {
				tokens = append([]string{firstWord}, tokenizeRest(remainder)...)
			}
		default:
			tokens = append([]string{firstWord}, tokenizeRest(remainder)...)
		}
	} else {
		// A lone unknown word is almost always a typo or an exit attempt,
		// not a question — don't burn an LLM round-trip on it.
		if len(strings.Fields(line)) == 1 {
			if suggestion := nearestCommand(firstWord, known); suggestion != "" {
				fmt.Printf("Unknown command %q — did you mean %q?\n", firstWord, suggestion)
			} else {
				fmt.Printf("Unknown command %q.\n", firstWord)
			}
			fmt.Println(Dim("Type :help for commands, or write a full question to ask the LLM."))
			return nil
		}
		if suggestion := nearestCommand(firstWord, known); len(firstWord) > 2 && suggestion != "" && editDistance(strings.ToLower(firstWord), suggestion) <= 1 {
			fmt.Printf("%q is not a command — did you mean %q? Press ↑ to edit the line.\n", firstWord, suggestion)
			return nil
		}
		fmt.Println(Dim("→ sending to the LLM as a question…"))
		tokens = []string{"ask", line}
	}

	if tokens == nil {
		return nil
	}

	resetAllFlags(rootCmd)
	rootCmd.SetArgs(tokens)
	return rootCmd.Execute()
}

// stdinPrompt is shared by every prompt so buffered read-ahead (piped input,
// fast typists) isn't discarded between calls the way per-call Scanners do.
var stdinPrompt = bufio.NewReader(os.Stdin)

// promptLine prints question and reads one line from stdin. Safe to call
// interleaved with readline.Readline — mirrors the existing [y/N] confirm
// pattern in cmd_reset.go, which already works correctly between prompts.
func promptLine(question string) string {
	fmt.Print(question)
	line, _ := stdinPrompt.ReadString('\n')
	return strings.TrimSpace(line)
}

func guidedConfigTokens() []string {
	ans := strings.ToLower(promptLine(Cyan("Show or edit config? [s/e] (default: s) ")))
	if strings.HasPrefix(ans, "e") {
		return []string{"config", "edit"}
	}
	return []string{"config", "show"}
}

func guidedResetTokens() []string {
	ans := strings.ToLower(promptLine(Cyan("Reset (a)ll data or just one (w)orkspace? [a/w] (default: a) ")))
	if strings.HasPrefix(ans, "w") {
		name := promptLine(Cyan("Workspace name: "))
		if name == "" {
			fmt.Println(Yellow("No workspace given, aborting."))
			return nil
		}
		return []string{"reset", "--workspace", name}
	}
	return []string{"reset"}
}

func guidedIndexTokens() []string {
	path := promptIndexPath()
	if path == "" {
		fmt.Println(Yellow("No path given, aborting."))
		return nil
	}
	return []string{"index", path}
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

// nearestCommand returns the known command within edit distance 2 of word,
// preferring exact prefixes, or "" if nothing is close.
func nearestCommand(word string, known map[string]bool) string {
	word = strings.ToLower(word)
	best, bestDist := "", 3
	for name := range known {
		if strings.HasPrefix(name, word) {
			return name
		}
		if d := editDistance(word, name); d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func knownCommandNames() map[string]bool {
	m := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		m[c.Name()] = true
	}
	return m
}

func buildCompleter() *readline.PrefixCompleter {
	items := []readline.PrefixCompleterInterface{
		readline.PcItem("?"),
		readline.PcItem(":help"),
		readline.PcItem(":clear"),
		readline.PcItem(":quit"),
		readline.PcItem(":exit"),
	}
	for _, c := range rootCmd.Commands() {
		items = append(items, readline.PcItem(c.Name()), readline.PcItem("/"+c.Name()))
	}
	return readline.NewPrefixCompleter(items...)
}

func resetAllFlags(cmd *cobra.Command) {
	resetFlagSet(cmd.Flags())
	resetFlagSet(cmd.PersistentFlags())
	for _, sub := range cmd.Commands() {
		resetAllFlags(sub)
	}
}

func resetFlagSet(fs *pflag.FlagSet) {
	fs.VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// tokenizeRest splits s on whitespace, treating "..." and '...' as single tokens.
func tokenizeRest(s string) []string {
	var tokens []string
	var cur strings.Builder
	var inQuote rune

	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}

	for _, r := range s {
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			inQuote = r
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// unquote strips one layer of matching surrounding quotes, if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func printREPLHelp() {
	fmt.Println(Bold("Commands") + Dim("  (prefix with / if you like, e.g. /config)"))
	for _, c := range rootCmd.Commands() {
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		fmt.Printf("  %s %s\n", Cyan(fmt.Sprintf("%-9s", c.Name())), Dim(c.Short))
	}
	fmt.Println()
	fmt.Println(Bold("Shortcuts"))
	fmt.Println("  " + Cyan(fmt.Sprintf("%-9s", "?")) + Dim("show this help"))
	fmt.Println("  " + Cyan(fmt.Sprintf("%-9s", ":clear")) + Dim("clear the screen"))
	fmt.Println("  " + Cyan(fmt.Sprintf("%-9s", "exit")) + Dim("leave (also: quit, q)"))
	fmt.Println()
	fmt.Println(Dim("Anything else is asked to the LLM over your indexed notes."))
	fmt.Println(Dim("Bare \"config\", \"reset\", or \"index\" will ask what you want."))
	fmt.Println(Dim("Example: what is the deploy process for the api service?"))
}
