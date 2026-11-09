package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
)

// mcpDeps bundles everything the tool handlers need. Later tasks add fields.
type mcpDeps struct {
	cfg *config.Config
	// scanCache memoizes list_projects' ScanRepos call for a short TTL, so
	// back-to-back calls reuse one scan instead of re-forking git per repo.
	// nil is valid (falls back to an uncached scan every call) — tests that
	// don't care about this optimization can leave it unset.
	scanCache *neycontext.ScanCache
}

// newMCPServer builds the MCP server and registers its tools (handoff,
// projects, update_profile). Factored out of runMCP so tests can construct
// one against an in-memory transport (mcp.NewInMemoryTransports) without
// going through stdio or the CLI's signal handling — see mcp_test.go.
func newMCPServer(deps mcpDeps) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ney", Version: Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_profile",
		Description: "Propose an edit to one section of the user's profile (~/.ney/profile.md), which list_projects " +
			"shows. Replaces the named section's content, or appends to it if append=true; creates " +
			"the section if it doesn't exist yet.",
	}, updateProfileHandler())

	registerHandoffTools(server, deps)
	registerProjectTools(server, deps)

	return server
}

// --- update_profile ------------------------------------------------------------------

type updateProfileInput struct {
	Section string `json:"section" jsonschema:"the profile section heading to edit (e.g. 'Current focus')"`
	Content string `json:"content" jsonschema:"the new section content"`
	Append  bool   `json:"append,omitempty" jsonschema:"append to the existing section body instead of replacing it (default: replace)"`
}

type updateProfileOutput struct {
	Path string `json:"path"`
}

// updateProfileHandler edits one named section of ~/.ney/profile.md. Like
// the card tools, it only ever touches that one plain md file.
func updateProfileHandler() mcp.ToolHandlerFor[updateProfileInput, updateProfileOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in updateProfileInput) (*mcp.CallToolResult, updateProfileOutput, error) {
		if strings.TrimSpace(in.Section) == "" {
			return nil, updateProfileOutput{}, fmt.Errorf("section is required")
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, updateProfileOutput{}, fmt.Errorf("content is required")
		}
		path := filepath.Join(config.NeyDir(), "profile.md")
		if err := neycontext.UpdateProfile(path, in.Section, in.Content, in.Append); err != nil {
			return nil, updateProfileOutput{}, err
		}
		out := updateProfileOutput{Path: path}
		text := fmt.Sprintf("Updated profile section %q at %s.", in.Section, path)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}
