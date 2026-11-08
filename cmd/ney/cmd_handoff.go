package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var (
	handoffFull    bool
	handoffSession string
)

var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "Print the handoff brief for the current repo's recent agent sessions",
	Args:  cobra.NoArgs,
	RunE:  runHandoff,
}

func init() {
	handoffCmd.Flags().BoolVar(&handoffFull, "full", false, "print the full brief instead of the short inject form")
	handoffCmd.Flags().StringVar(&handoffSession, "session", "", "make this session id the primary one")
	rootCmd.AddCommand(handoffCmd)
}

func runHandoff(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cwd, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	brief, root, err := buildBrief(cmd.Context(), cfg, handoffRequest{
		Cwd:       cwd,
		SessionID: handoffSession,
		Now:       time.Now(),
	}, handoffFull)
	if err != nil {
		return err
	}
	if brief.Empty() {
		fmt.Fprintf(cmd.ErrOrStderr(), "no recent sessions for %s\n", filepath.Base(root))
		return nil
	}
	if handoffFull {
		fmt.Fprint(cmd.OutOrStdout(), brief.RenderFull())
	} else {
		fmt.Fprint(cmd.OutOrStdout(), brief.RenderInject())
	}
	return nil
}
