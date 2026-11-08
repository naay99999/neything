package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Guided setup — set up profile, connect AI clients",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetupWizard(cmd.Context())
	},
}

// isInteractive is a var so tests can drive the wizard's TTY guard.
var isInteractive = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

// setupCompleted reports whether this machine has been through setup: the
// wizard bootstraps ~/.ney/profile.md, so its presence is the marker.
func setupCompleted() bool {
	_, err := os.Stat(filepath.Join(config.NeyDir(), "profile.md"))
	return err == nil
}

// runSetupWizard walks the user through the full first-run flow. Every step
// is idempotent and skippable; the profile is bootstrapped first, so an
// interrupted run that never got that far simply offers itself again.
func runSetupWizard(_ context.Context) error {
	// Every step of the wizard is a prompt, so there is no useful
	// non-interactive behaviour. Refusing outright also means an EOF stdin
	// can never accept a default that writes to an AI client's config.
	if !isInteractive() {
		return fmt.Errorf("`ney init` is interactive — run it from a terminal (stdin is not a TTY)")
	}

	fmt.Println(Bold("Ney setup — let your AI know who you are and what you are working on"))
	fmt.Println(Dim("Enter = accept the default, Ctrl+C = cancel (you can rerun `ney init` any time)"))
	fmt.Println()

	stepProfile()

	stepClients()

	fmt.Println()
	fmt.Println(Bold("✓ Setup complete"))
	fmt.Println(Dim("  Memory: tell your AI \"remember that ...\" and it saves a file under " + displayPath(memoryDir()) +
		". Edit your profile any time at " + displayPath(filepath.Join(config.NeyDir(), "profile.md")) + "."))
	return nil
}

// --- step 1: profile bootstrap ---------------------------------------------------

// stepProfile creates ~/.ney/profile.md from the embedded template (via
// neycontext.LoadProfile) and, on a fresh profile, asks 2-3 short questions
// to seed it. If a profile already exists, this step is a no-op note — the
// file is user-owned from then on (AI edits it via update_profile).
func stepProfile() {
	fmt.Println()
	fmt.Println(Bold("[1/2] Setting up your profile (~/.ney/profile.md)"))

	profilePath := filepath.Join(config.NeyDir(), "profile.md")
	if _, err := os.Stat(profilePath); err == nil {
		fmt.Println(Dim("      profile.md already exists — skipping (edit it directly, or let your AI update it via update_profile)"))
		return
	}

	if _, _, err := neycontext.LoadProfile(profilePath); err != nil {
		fmt.Println(Yellow("      Could not create profile.md: " + err.Error()))
		return
	}

	fmt.Println(Dim("      Short answers are fine (Enter skips a question) — edit later at " + displayPath(profilePath)))

	if ans := promptLine(Cyan("      Who are you, and what do you do? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Name & role", ans, false)
	}
	if ans := promptLine(Cyan("      What are you focused on right now? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Current focus", ans, false)
	}
	if ans := promptLine(Cyan("      Working style / anything your AI should know? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Working style", ans, false)
	}

	fmt.Println(Green("      ✓ Saved profile.md"))
}

// --- step 2: AI clients ---------------------------------------------------------

func stepClients() {
	fmt.Println()
	fmt.Println(Bold("[2/2] Connecting your AI clients"))

	neyBin := resolveNeyBinary()

	for _, c := range detectClients(neyBin) {
		if !c.Detected {
			fmt.Printf("      ✗ %s — not found (to add it yourself later:)\n", c.Name)
			fmt.Println(Dim(indentLines(c.Manual, "        ")))
			continue
		}
		ans := strings.ToLower(promptDefault(fmt.Sprintf("      ✓ Found %s — register ney with it? (y/n)", c.Name), "y"))
		if !strings.HasPrefix(ans, "y") {
			fmt.Println(Dim("        Skipped"))
			continue
		}
		if err := c.Register(); err != nil {
			fmt.Println(Yellow("        Registration failed: " + err.Error()))
			continue
		}
		fmt.Println(Green("        ✓ Registered (restart " + c.Name + " to apply)"))
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// --- prompt helpers -------------------------------------------------------------

// promptDefault asks question, falling back to def when the user just presses
// Enter. At EOF it returns "" rather than def: an exhausted stdin is not
// consent, and this function gates writes into AI-client config files.
func promptDefault(question, def string) string {
	label := question
	if def != "" {
		label += " [" + def + "]"
	}
	ans := promptLine(Cyan(label + ": "))
	if stdinEOF {
		return ""
	}
	if ans == "" {
		return def
	}
	return ans
}
