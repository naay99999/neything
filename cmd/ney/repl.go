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
	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const replBanner = `ney — local-first AI knowledge engine (interactive mode)
Type a command (ask, search, index, watch, status, config, doctor, models, reset, version, help)
or just type a question to ask it directly. Commands can be prefixed with / (e.g. /config).
Type a bare "config", "reset", or "index" with no arguments and ney will ask what you want.
:help   show this message    :clear  clear the screen    :quit / :exit  leave
`

func runREPL() error {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.SilenceUsage = true

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

	fmt.Print(replBanner)

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
			continue
		}
	}
}

func handleMetaCommand(line string) (handled bool, exit bool) {
	switch line {
	case ":quit", ":exit":
		return true, true
	case ":help":
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
		tokens = []string{"ask", line}
	}

	if tokens == nil {
		return nil
	}

	resetAllFlags(rootCmd)
	rootCmd.SetArgs(tokens)
	return rootCmd.Execute()
}

// promptLine prints question and reads one line from stdin. Safe to call
// interleaved with readline.Readline — mirrors the existing [y/N] confirm
// pattern in cmd_reset.go, which already works correctly between prompts.
func promptLine(question string) string {
	fmt.Print(question)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
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
	path := promptLine(Cyan("Path to index: "))
	if path == "" {
		fmt.Println(Yellow("No path given, aborting."))
		return nil
	}
	return []string{"index", path}
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
	var names []string
	for _, c := range rootCmd.Commands() {
		names = append(names, c.Name())
	}
	fmt.Printf("Commands: %s\n", strings.Join(names, ", "))
	fmt.Println("Prefix any command with / if you like (e.g. /config).")
	fmt.Println("Bare \"config\", \"reset\", or \"index\" (no arguments) will ask what you want.")
	fmt.Println("Meta-commands: :help  :clear  :quit  :exit")
	fmt.Println("Example: ask what is the deploy process for the api service")
}
