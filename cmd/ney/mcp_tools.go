package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
)

// memoryDir is where the `remember` tool writes memory files (one md file per
// memory, see internal/context's WriteMemory).
func memoryDir() string {
	return filepath.Join(config.NeyDir(), "memory")
}

// mcpDeps bundles everything the tool handlers need. Later tasks add fields.
type mcpDeps struct {
	cfg *config.Config
	// scanCache memoizes get_context's ScanRepos call for a short TTL, so
	// back-to-back calls reuse one scan instead of re-forking git per repo.
	// nil is valid (falls back to an uncached scan every call) — tests that
	// don't care about this optimization can leave it unset.
	scanCache *neycontext.ScanCache
}

// newMCPServer builds the MCP server and registers its tools (get_context,
// remember, update_profile). Factored out of runMCP so tests can construct
// one against an in-memory transport (mcp.NewInMemoryTransports) without
// going through stdio or the CLI's signal handling — see mcp_test.go.
func newMCPServer(deps mcpDeps) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ney", Version: Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_context",
		Description: "Call this FIRST, at the start of a session: a small markdown blob with who the user is " +
			"(their profile) and which projects they've worked on recently (git activity across dev_roots). " +
			"Cheap, never fails.",
	}, getContextHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "remember",
		Description: "Save a fact or decision to memory (~/.ney/memory), so it's still known in future sessions " +
			"and across every connected AI client. Server derives the filename (date + sanitized title slug) — " +
			"no path argument.",
	}, rememberHandler())

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_profile",
		Description: "Propose an edit to one section of the user's profile (~/.ney/profile.md), which get_context " +
			"reads every session. Replaces the named section's content, or appends to it if append=true; creates " +
			"the section if it doesn't exist yet.",
	}, updateProfileHandler())

	registerHandoffTools(server, deps)

	return server
}

// --- get_context -----------------------------------------------------------------

type getContextInput struct{}

type getContextOutput struct {
	Context string `json:"context"`
}

// getContextHandler renders the Layer-1 bootstrap blob: profile + active
// projects + the write tools. Per the "Layer 1 must never fail" principle,
// every failure path here degrades — an unreadable profile is noted in the
// rendered text instead of returning an MCP error.
func getContextHandler(deps mcpDeps) mcp.ToolHandlerFor[getContextInput, getContextOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getContextInput) (*mcp.CallToolResult, getContextOutput, error) {
		var notes []string

		var projects []neycontext.Project
		if deps.scanCache != nil {
			projects = deps.scanCache.Get(ctx, deps.cfg.Context.DevRoots)
		} else {
			projects = neycontext.ScanRepos(ctx, deps.cfg.Context.DevRoots)
		}

		profilePath := filepath.Join(config.NeyDir(), "profile.md")
		profile, created, perr := neycontext.LoadProfile(profilePath)
		if perr != nil {
			notes = append(notes, fmt.Sprintf("note: could not load profile.md (%v) — proceeding with an empty profile.", perr))
			profile = ""
		} else if created {
			notes = append(notes, fmt.Sprintf("note: no profile existed yet — created a starter template at %s; consider filling it in via update_profile.", profilePath))
		}

		activeDays := deps.cfg.Context.ActiveDays
		if activeDays <= 0 {
			activeDays = 14
		}
		text := neycontext.Render(profile, projects, activeDays, 10, time.Now())
		for _, n := range notes {
			text += "\n" + n + "\n"
		}

		out := getContextOutput{Context: text}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

// --- remember ----------------------------------------------------------------------

type rememberInput struct {
	Title   string   `json:"title" jsonschema:"short title for this memory (used to derive the filename)"`
	Content string   `json:"content" jsonschema:"the fact or decision to remember"`
	Project string   `json:"project,omitempty" jsonschema:"optional project name this memory relates to"`
	Tags    []string `json:"tags,omitempty" jsonschema:"optional tags"`
}

type rememberOutput struct {
	Path string `json:"path"`
}

// rememberHandler writes a new memory file: only a plain md file under
// memoryDir().
func rememberHandler() mcp.ToolHandlerFor[rememberInput, rememberOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in rememberInput) (*mcp.CallToolResult, rememberOutput, error) {
		if strings.TrimSpace(in.Title) == "" {
			return nil, rememberOutput{}, fmt.Errorf("title is required")
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, rememberOutput{}, fmt.Errorf("content is required")
		}
		path, err := neycontext.WriteMemory(memoryDir(), neycontext.Memory{
			Title:   in.Title,
			Content: in.Content,
			Project: in.Project,
			Tags:    in.Tags,
			Date:    time.Now(),
		})
		if err != nil {
			return nil, rememberOutput{}, err
		}
		out := rememberOutput{Path: path}
		text := fmt.Sprintf("Saved memory to %s.", path)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
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
// remember, it only ever touches that one plain md file.
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
