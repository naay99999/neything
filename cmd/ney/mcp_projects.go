package main

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/projects"
)

func registerProjectTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_projects",
		Description: "The user's projects across their machine, one line each: name, path, what it is (from its ney card), " +
			"last activity. Call when the user mentions a project you don't know, or asks what they are working on.",
	}, listProjectsHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_project",
		Description: "Get the ney card for one of the user's projects: what it is, where it lives, stack, status, files to " +
			"read for detail, key facts, and live git state. Call before guessing about a project the user names. For " +
			"deeper detail, read the files it points to.",
	}, getProjectHandler())

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_project",
		Description: "Create or update the ney card for the project at cwd. Send only fields that changed. A card holds " +
			"durable things someone working in a DIFFERENT project would need: what it is (summary ≤300 chars), stack " +
			"(≤100), status (≤150), read_more (≤5 repo-relative paths), facts (≤8, ≤150 chars each: decisions, " +
			"constraints). Not session progress, not code detail.",
	}, updateProjectHandler())
}

type listProjectsInput struct{}

func listProjectsHandler(deps mcpDeps) mcp.ToolHandlerFor[listProjectsInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ listProjectsInput) (*mcp.CallToolResult, any, error) {
		var scan []neycontext.Project
		if deps.scanCache != nil {
			scan = deps.scanCache.Get(ctx, deps.cfg.Context.DevRoots)
		} else {
			scan = neycontext.ScanRepos(ctx, deps.cfg.Context.DevRoots)
		}
		now := time.Now()
		return handoffText(renderProjectList(profileHeadline(), collectItems(scan, now), now)), nil, nil
	}
}

type getProjectInput struct {
	Name string `json:"name" jsonschema:"the project's name as listed by list_projects, or its absolute path"`
}

func getProjectHandler() mcp.ToolHandlerFor[getProjectInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in getProjectInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Name) == "" {
			return handoffToolError("name is required"), nil, nil
		}
		gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
		defer cancel()
		text, err := renderProject(gitCtx, strings.TrimSpace(in.Name), time.Now())
		if err != nil {
			return handoffToolError("%v", err), nil, nil
		}
		return handoffText(text), nil, nil
	}
}

type updateProjectInput struct {
	Cwd         string    `json:"cwd" jsonschema:"absolute path of your current working directory"`
	Agent       string    `json:"agent" jsonschema:"your agent name, e.g. claude-code or codex"`
	Summary     *string   `json:"summary,omitempty" jsonschema:"what the project is and is for (max 300 characters)"`
	Stack       *string   `json:"stack,omitempty" jsonschema:"languages and main frameworks (max 100 characters)"`
	Status      *string   `json:"status,omitempty" jsonschema:"active / paused / done and what is happening (max 150 characters)"`
	ReadMore    *[]string `json:"read_more,omitempty" jsonschema:"repo-relative files to read for detail; replaces the list (max 5)"`
	FactsAdd    []string  `json:"facts_add,omitempty" jsonschema:"facts to add: decisions, constraints (max 8 in total, 150 characters each)"`
	FactsRemove []int     `json:"facts_remove,omitempty" jsonschema:"1-based numbers of facts to remove, as listed by get_project"`
}

func updateProjectHandler() mcp.ToolHandlerFor[updateProjectInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateProjectInput) (*mcp.CallToolResult, any, error) {
		if r := requireAbsCwd(in.Cwd); r != nil {
			return r, nil, nil
		}
		if strings.TrimSpace(in.Agent) == "" {
			return handoffToolError("agent is required"), nil, nil
		}
		gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
		defer cancel()
		text, err := updateProjectCard(gitCtx, in.Cwd, strings.TrimSpace(in.Agent), projects.Patch{
			Summary: in.Summary, Stack: in.Stack, Status: in.Status, ReadMore: in.ReadMore,
			FactsAdd: in.FactsAdd, FactsRemove: in.FactsRemove,
		}, time.Now())
		if err != nil {
			return handoffToolError("%v", err), nil, nil
		}
		return handoffText(text), nil, nil
	}
}
