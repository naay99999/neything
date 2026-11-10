package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naay99999/neything/internal/skills"
	"github.com/spf13/cobra"
)

var (
	skillsQuiet         bool
	skillsInstalledOnly bool
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage the agent skills ney ships (ney-handoff, ney-resume)",
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
	skillsSyncCmd.Flags().BoolVar(&skillsInstalledOnly, "installed-only", false, "only update skills where ney's skills are already installed")
	skillsCmd.AddCommand(skillsSyncCmd)
	rootCmd.AddCommand(skillsCmd)
}

// syncSkills syncs every skill target under home. It keeps going after an
// error so one unwritable target does not block the others. With
// installedOnly, a skills folder that already holds a skill ney installed
// counts as opted in, so skills added in a newer ney land there too.
func syncSkills(home string, installedOnly bool) ([]skills.Result, error) {
	optedIn := map[string]bool{}
	for _, dir := range skills.Targets(home) {
		switch skills.Status(dir).State {
		case skills.Absent, skills.Unmanaged:
		default:
			optedIn[filepath.Dir(dir)] = true
		}
	}
	var results []skills.Result
	var firstErr error
	for _, dir := range skills.Targets(home) {
		r, err := skills.Sync(dir, Version, installedOnly && !optedIn[filepath.Dir(dir)])
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
