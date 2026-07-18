package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetAllFlags restores every flag on cmd and its subcommands to its default,
// so tests that drive rootCmd.Execute repeatedly don't leak flag state.
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
