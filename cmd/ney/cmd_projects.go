package main

import (
	"fmt"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List your projects and their ney cards",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		now := time.Now()
		items := collectItems(cmd.Context(), cfg, neycontext.ScanRepos(cmd.Context(), cfg.Context.DevRoots), now)
		fmt.Fprint(cmd.OutOrStdout(), renderProjectList(profileHeadline(), items, now))
		return nil
	},
}

var projectsShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Print one project's ney card",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text, err := renderProject(cmd.Context(), args[0], time.Now())
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), text)
		return nil
	},
}

func init() {
	projectsCmd.AddCommand(projectsShowCmd)
	rootCmd.AddCommand(projectsCmd)
}
