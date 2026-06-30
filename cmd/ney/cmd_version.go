package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		if flagJSON {
			PrintJSON(map[string]string{"version": Version})
			return
		}
		fmt.Printf("ney %s\n", Version)
	},
}
