package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/handoff"
)

const (
	maxHandoffNoteRunes = 8000
	defaultListSessions = 10
	maxListSessions     = 50
)

func registerHandoffTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_handoff",
		Description: "Get the session handoff brief for a repo: what recent agent sessions (Claude Code, etc.) were doing, " +
			"what changed in git since, and any explicit handoff notes. Call at the start of work to pick up where " +
			"a previous session left off. Pass your current working directory as `cwd` (required; absolute path). " +
			"`full` defaults to true; set false for the compact brief. `session_id` selects a specific session.",
	}, getHandoffHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_sessions",
		Description: "List recent agent sessions (across agents) for the repo containing `cwd`, newest first, as JSON: " +
			"agent, id, title, branch, start, end, prompts. Pass your current working directory as `cwd` " +
			"(required; absolute path). Use an id with get_handoff's session_id.",
	}, listSessionsHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "write_handoff",
		Description: "Leave a handoff note for the next session in this repo; it is shown first in the next brief. Use it " +
			"when stopping mid-task to record what is done, what is left, and decisions made. Pass your current " +
			"working directory as `cwd` (required; absolute path), your agent name, and the note (max 8000 characters). " +
			"If the user asked for a handoff: ask what the next session will do if they have not said, show the draft, " +
			"and save only after they approve.",
	}, writeHandoffHandler())
}

func handoffToolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

func handoffText(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func requireAbsCwd(cwd string) *mcp.CallToolResult {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return handoffToolError("cwd must be an absolute path (got %q)", cwd)
	}
	return nil
}

// --- get_handoff -----------------------------------------------------------------

type getHandoffInput struct {
	Cwd       string `json:"cwd" jsonschema:"absolute path of your current working directory"`
	SessionID string `json:"session_id,omitempty" jsonschema:"make this session primary instead of the newest"`
	Full      *bool  `json:"full,omitempty" jsonschema:"full brief (default true) or compact"`
}

func getHandoffHandler(deps mcpDeps) mcp.ToolHandlerFor[getHandoffInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getHandoffInput) (*mcp.CallToolResult, any, error) {
		if r := requireAbsCwd(in.Cwd); r != nil {
			return r, nil, nil
		}
		full := in.Full == nil || *in.Full
		brief, root, err := buildBrief(ctx, deps.cfg, handoffRequest{Cwd: in.Cwd, SessionID: in.SessionID}, full)
		if err != nil {
			var nf errSessionNotFound
			if errors.As(err, &nf) {
				return handoffToolError("%s", err.Error()), nil, nil
			}
			return handoffToolError("handoff: %v", err), nil, nil
		}
		if brief.Empty() {
			return handoffText("no recent sessions for " + filepath.Base(root)), nil, nil
		}
		if full {
			return handoffText(brief.RenderFull()), nil, nil
		}
		return handoffText(brief.RenderInject()), nil, nil
	}
}

// --- list_sessions ---------------------------------------------------------------

type listSessionsInput struct {
	Cwd   string `json:"cwd" jsonschema:"absolute path of your current working directory"`
	Limit int    `json:"limit,omitempty" jsonschema:"max sessions (default 10, max 50)"`
}

type sessionSummary struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Branch  string `json:"branch"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Prompts int    `json:"prompts"`
}

func listSessionsHandler(deps mcpDeps) mcp.ToolHandlerFor[listSessionsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listSessionsInput) (*mcp.CallToolResult, any, error) {
		if r := requireAbsCwd(in.Cwd); r != nil {
			return r, nil, nil
		}
		limit := in.Limit
		if limit <= 0 {
			limit = defaultListSessions
		}
		if limit > maxListSessions {
			limit = maxListSessions
		}
		sessions, _, err := listSessions(ctx, deps.cfg, in.Cwd, limit)
		if err != nil {
			return handoffToolError("list sessions: %v", err), nil, nil
		}
		out := make([]sessionSummary, 0, len(sessions))
		for _, s := range sessions {
			out = append(out, sessionSummary{
				Agent: s.Agent, ID: s.ID, Title: s.Title, Branch: s.Branch,
				Start: s.Start.Format("2006-01-02T15:04:05Z07:00"), End: s.End.Format("2006-01-02T15:04:05Z07:00"),
				Prompts: s.PromptCount,
			})
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return handoffToolError("list sessions: %v", err), nil, nil
		}
		return handoffText(string(b)), nil, nil
	}
}

// --- write_handoff ---------------------------------------------------------------

type writeHandoffInput struct {
	Cwd   string `json:"cwd" jsonschema:"absolute path of your current working directory"`
	Agent string `json:"agent" jsonschema:"your agent name, e.g. claude-code or codex"`
	Note  string `json:"note" jsonschema:"what is done, what is left, decisions made (max 8000 characters)"`
}

func writeHandoffHandler() mcp.ToolHandlerFor[writeHandoffInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in writeHandoffInput) (*mcp.CallToolResult, any, error) {
		if r := requireAbsCwd(in.Cwd); r != nil {
			return r, nil, nil
		}
		if in.Agent == "" || in.Note == "" {
			return handoffToolError("agent and note are required"), nil, nil
		}
		if n := utf8.RuneCountInString(in.Note); n > maxHandoffNoteRunes {
			return handoffToolError("note is %d characters; the limit is %d", n, maxHandoffNoteRunes), nil, nil
		}
		gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
		defer cancel()
		root, _ := neycontext.ResolveRepo(gitCtx, in.Cwd)
		path, err := handoff.WriteNote(config.NeyDir(), root, in.Agent, in.Note, time.Now())
		if err != nil {
			return handoffToolError("write handoff: %v", err), nil, nil
		}
		b, _ := json.Marshal(map[string]string{"path": path})
		return handoffText(string(b)), nil, nil
	}
}
