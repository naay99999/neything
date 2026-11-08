package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run ney as an MCP server over stdio (get_context, remember, update_profile)",
	Long: "Run ney as an MCP server over stdio, for AI clients like Claude Code/Desktop/Cursor/Codex.\n" +
		"stdout is reserved for the MCP protocol — all diagnostics go to stderr.",
	RunE: runMCP,
}

// runMCP wires one long-lived server process and serves it over stdio.
//
// STDOUT HYGIENE: stdout is reserved for the MCP protocol end to end. This
// function and everything it calls (loadConfig, the tool handlers) only ever
// write diagnostics to os.Stderr — never fmt.Println/PrintJSON, which write
// to os.Stdout.
func runMCP(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := newMCPServer(mcpDeps{
		cfg: cfg,
		// scanCache lives on this long-running process only — back-to-back
		// get_context calls reuse one ScanRepos pass instead of forking git
		// 3x per repo each time. CLI one-shot paths call ScanRepos directly
		// and never see this cache, so staleness is never a concern there.
		scanCache: &neycontext.ScanCache{},
	})

	fmt.Fprintln(os.Stderr, "ney mcp: serving over stdio (Ctrl+C or close stdin to stop)")

	if runErr := server.Run(ctx, &mcp.StdioTransport{}); runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}
