package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or edit configuration",
	RunE:  runConfigShow,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print current config",
	RunE:  runConfigShow,
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		c := exec.Command(editor, config.ConfigPath())
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("cannot read config: %w", err)
	}
	if flagJSON {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		PrintJSON(cfg)
		return nil
	}
	fmt.Print(string(data))
	return nil
}

func init() {
	configCmd.AddCommand(configShowCmd, configEditCmd)
}
