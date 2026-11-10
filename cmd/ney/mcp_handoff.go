package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/handoff"
	"github.com/naay99999/neything/internal/projects"
)

const (
	maxHandoffNoteRunes = 8000
	defaultListSessions = 10
	maxListSessions     = 50
	defaultListHandoffs = 15
	maxListHandoffs     = 50
)

func registerHandoffTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_handoff",
		Description: "Get the session handoff brief for a repo: what recent agent sessions (Claude Code, etc.) were doing, " +
			"what changed in git since, and any explicit handoff notes. Call at the start of work to pick up where " +
			"a previous session left off. Pass your current working directory as `cwd` (required; absolute path). " +
			"`full` defaults to true; set false for the compact brief. `session_id` selects a specific session. " +
			"`note_id` (from list_handoffs) shows that handoff note even if it is older than the latest session.",
	}, getHandoffHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_handoffs",
		Description: "List saved handoff notes across all the user's repos, newest first, as JSON: n, project, cwd, " +
			"title, agent, written, note_id, current (the note belongs to the repo at `cwd`). `project` keeps only " +
			"that project's notes. To open one, call get_handoff with its `cwd` and `note_id`. Use it when the user " +
			"wants to resume or pick up a handoff.",
	}, listHandoffsHandler())

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
	NoteID    string `json:"note_id,omitempty" jsonschema:"show this handoff note (an id from list_handoffs)"`
}

func getHandoffHandler(deps mcpDeps) mcp.ToolHandlerFor[getHandoffInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getHandoffInput) (*mcp.CallToolResult, any, error) {
		if r := requireAbsCwd(in.Cwd); r != nil {
			return r, nil, nil
		}
		full := in.Full == nil || *in.Full
		brief, root, err := buildBrief(ctx, deps.cfg, handoffRequest{Cwd: in.Cwd, SessionID: in.SessionID, NoteID: in.NoteID}, full)
		if err != nil {
			var nf errSessionNotFound
			var nn errNoteNotFound
			if errors.As(err, &nf) || errors.As(err, &nn) {
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

// --- list_handoffs ---------------------------------------------------------------

type listHandoffsInput struct {
	Project string `json:"project,omitempty" jsonschema:"only this project's notes (a name from list_projects)"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"absolute path of your current working directory, to mark its notes current"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max notes (default 15, max 50)"`
}

type handoffListItem struct {
	N       int    `json:"n"`
	Project string `json:"project"`
	Cwd     string `json:"cwd"`
	Title   string `json:"title"`
	Agent   string `json:"agent"`
	Written string `json:"written"`
	NoteID  string `json:"note_id"`
	Current bool   `json:"current"`
}

func listHandoffsHandler() mcp.ToolHandlerFor[listHandoffsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listHandoffsInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = defaultListHandoffs
		}
		if limit > maxListHandoffs {
			limit = maxListHandoffs
		}
		refs, err := handoff.ListNotes(config.NeyDir())
		if err != nil {
			return handoffToolError("list handoffs: %v", err), nil, nil
		}
		if len(refs) == 0 {
			return handoffText("no handoff notes yet"), nil, nil
		}
		// Notes written before ney recorded their repo root are matched to
		// known repos by directory key.
		reg, _ := projects.LoadRegistry(projectsDir())
		byKey := map[string]projects.Entry{}
		for _, e := range reg.Repos {
			byKey[handoff.RepoKey(e.Path)] = e
		}
		currentKey := ""
		if in.Cwd != "" && filepath.IsAbs(in.Cwd) {
			gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
			root, _ := neycontext.ResolveRepo(gitCtx, in.Cwd)
			cancel()
			currentKey = handoff.RepoKey(root)
		}
		now := time.Now()
		seen := map[string]bool{}
		var names []string
		var out []handoffListItem
		for _, r := range refs {
			root, name := r.Root, ""
			if e, ok := byKey[r.Key]; ok {
				name = e.Name
				if root == "" {
					root = e.Path
				}
			}
			if name == "" && root != "" {
				name = filepath.Base(root)
			}
			if name == "" {
				name = r.Key
			}
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			if in.Project != "" && !strings.EqualFold(name, in.Project) {
				continue
			}
			if len(out) == limit {
				continue
			}
			out = append(out, handoffListItem{
				N: len(out) + 1, Project: name, Cwd: root, Title: r.Title, Agent: r.Agent,
				Written: neycontext.RelativeTime(r.At, now), NoteID: r.ID, Current: r.Key == currentKey,
			})
		}
		if len(out) == 0 {
			sort.Strings(names)
			msg := fmt.Sprintf("no handoff notes for %q. Projects with notes: %s", in.Project, strings.Join(names, ", "))
			if near := projects.Closest(names, in.Project, 3); len(near) > 0 {
				msg += ". Did you mean: " + strings.Join(near, ", ") + "?"
			}
			return handoffText(msg), nil, nil
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return handoffToolError("list handoffs: %v", err), nil, nil
		}
		return handoffText(string(b)), nil, nil
	}
}
