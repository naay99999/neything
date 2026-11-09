package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/skills"
	"github.com/spf13/cobra"
)

var (
	skillsQuiet         bool
	skillsInstalledOnly bool
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage the agent skills ney ships (ney-handoff)",
}

var skillsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Install or update ney's skills in ~/.agents/skills and ~/.claude/skills",
	Long: "Install or update ney's skills. A skill directory ney did not install, or one whose files you " +
		"edited, is never touched.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		results, err := syncSkills(home, skillsInstalledOnly)
		if !skillsQuiet {
			for _, r := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", r.State, displayPath(r.Dir))
			}
		}
		return err
	},
}

func init() {
	skillsSyncCmd.Flags().BoolVar(&skillsQuiet, "quiet", false, "print nothing unless there is an error")
	skillsSyncCmd.Flags().BoolVar(&skillsInstalledOnly, "installed-only", false, "only update skills that are already installed")
	skillsCmd.AddCommand(skillsSyncCmd)
	rootCmd.AddCommand(skillsCmd)
}

// syncSkills syncs every skill target under home. It keeps going after an
// error so one unwritable target does not block the other.
func syncSkills(home string, installedOnly bool) ([]skills.Result, error) {
	var results []skills.Result
	var firstErr error
	for _, dir := range skills.Targets(home) {
		r, err := skills.Sync(dir, Version, installedOnly)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", displayPath(dir), err)
			}
			continue
		}
		results = append(results, r)
	}
	return results, firstErr
}
