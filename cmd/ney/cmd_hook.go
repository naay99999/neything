package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
)

var hookAgent string

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Entry points for agent harness hooks",
}

var hookSessionStartCmd = &cobra.Command{
	Use:           "session-start",
	Short:         "SessionStart hook: inject a handoff brief into a new agent session",
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runHookSessionStart,
}

func init() {
	hookSessionStartCmd.Flags().StringVar(&hookAgent, "agent", "claude-code", "calling agent (claude-code|codex); accepted for logging, unknown values are not an error")
	hookCmd.AddCommand(hookSessionStartCmd)
	rootCmd.AddCommand(hookCmd)
}

const hookStdinTimeout = time.Second

// readStdinTimeout reads up to 1 MiB from r but gives up after d, so a harness
// that never closes stdin cannot hang session startup.
func readStdinTimeout(r io.Reader, d time.Duration) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(r, 1<<20))
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.b, res.err
	case <-time.After(d):
		return nil, fmt.Errorf("timed out after %s", d)
	}
}

// hookInput is the subset of the harness's SessionStart payload ney uses.
type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Source    string `json:"source"`
}

type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// runHookSessionStart must never break session startup: every failure is one
// stderr line, exit 0, empty stdout.
func runHookSessionStart(cmd *cobra.Command, _ []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "ney hook: recovered: %v\n", r)
			err = nil
		}
	}()
	if msg := hookSessionStart(cmd); msg != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "ney hook (%s): %s\n", hookAgent, msg)
	}
	return nil
}

// hookSessionStart does the work and returns a diagnostic ("" when none).
func hookSessionStart(cmd *cobra.Command) string {
	raw, err := readStdinTimeout(cmd.InOrStdin(), hookStdinTimeout)
	if err != nil {
		return "read stdin: " + err.Error()
	}
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "parse stdin: " + err.Error()
	}
	// Resuming, compacting or forking (Codex copies the conversation) continues
	// a session that already has context.
	if in.Source == "resume" || in.Source == "compact" || in.Source == "fork" {
		return ""
	}
	cfg, err := config.Load()
	if err != nil {
		return "config: " + err.Error()
	}
	if !cfg.Handoff.Auto {
		return ""
	}
	cwd := in.Cwd
	if cwd == "" {
		if cwd, err = filepath.Abs("."); err != nil {
			return "cwd: " + err.Error()
		}
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, handoffGitTimeout)
	defer cancel()
	if root, _ := neycontext.ResolveRepo(ctx, cwd); hookRepoDisabled(cfg.Handoff.DisabledRepos, root, cwd) {
		return ""
	}

	brief, _, err := buildBrief(ctx, cfg, handoffRequest{
		Cwd:              cwd,
		ExcludeSessionID: in.SessionID,
		Now:              time.Now(),
	}, false)
	if err != nil {
		return "build brief: " + err.Error()
	}
	if brief.Empty() {
		return ""
	}
	out, err := json.Marshal(hookOutput{HookSpecificOutput: hookSpecific{
		HookEventName:     "SessionStart",
		AdditionalContext: brief.RenderInject(),
	}})
	if err != nil {
		return "encode: " + err.Error()
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return ""
}

// hookRepoDisabled reports whether the repo root, or the cwd itself, is a
// disabled root or lies inside one.
func hookRepoDisabled(disabled []string, root, cwd string) bool {
	for _, d := range disabled {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if session.Within(root, d) || session.Within(cwd, d) {
			return true
		}
	}
	return false
}
