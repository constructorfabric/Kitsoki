package studio

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
)

// gh_tools.go — a GitHub read/comment surface for Studio.
//
// issue.create files an issue and inbox.sync_github pulls assigned issues INTO a
// handle's inbox, but the everyday GitHub reads and comments an agent needs
// while developing still need a first-class MCP path. Issue listing and
// comments and PR view use native host providers. No LLM.
//
// gh.issues / gh.pr_view are pure reads (available read-only); gh.comment posts,
// so a read-only server omits it.

// registerGHTools wires the gh.* tools.
func (srv *Server) registerGHTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "gh.issues",
		Description: "List GitHub issues via the native host.gh.ticket provider. {dir? (a repo checkout to infer the repo from via git remote; default the server cwd), repo? (owner/name override), state? (open|closed|all; default open), assignee? (e.g. @me), search? (GitHub search terms), limit? (default 30)} → {ok, issues[{id,title,status,priority,assignee,url,type,source}]}. Read-only.",
	}, srv.handleGHIssues)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "gh.pr_view",
		Description: "View a pull request via native GitHub API. {number (required), dir?, repo?, include_diff? (also fetch the unified diff)} → {ok, pr:{number, title, state, url, body, files[{path, additions, deletions}]}, diff?}. Use it to read a PR's body and the exact files/diff it changed (e.g. a filed bug's own regression test). Read-only.",
	}, srv.handleGHPRView)

	if srv.readOnly {
		return
	}

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "gh.comment",
		Description: "Comment on a GitHub issue or PR via native host.gh.ticket / host.git providers. {number (required), body (required), on? (issue|pr; default issue), dir?, repo?} → {ok, url}. Mutating (posts to GitHub).",
	}, srv.handleGHComment)
}

// ── gh.issues ─────────────────────────────────────────────────────────────────

// GHIssuesArgs is the input to gh.issues.
type GHIssuesArgs struct {
	Dir      string `json:"dir,omitempty"`
	Repo     string `json:"repo,omitempty"`
	State    string `json:"state,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Search   string `json:"search,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// GHIssuesOK is the gh.issues result. Issues uses the provider-neutral ticket
// summary shape returned by host.gh.ticket.search.
type GHIssuesOK struct {
	OK     bool `json:"ok"`
	Issues any  `json:"issues"`
}

func (srv *Server) handleGHIssues(ctx context.Context, req *mcpsdk.CallToolRequest, args GHIssuesArgs) (*mcpsdk.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 30
	}

	repo, rerr := resolveGitHubRepoForStudio(ctx, "gh.issues", args.Dir, args.Repo)
	if rerr != nil {
		return rerr, nil, nil
	}

	state := strings.TrimSpace(args.State)
	if state == "" {
		state = "open"
	}
	var queryParts []string
	switch state {
	case "open", "closed":
		queryParts = append(queryParts, "is:"+state)
	case "all":
	default:
		return buildToolError(ErrBadRequest, "gh.issues: state must be \"open\", \"closed\", or \"all\""), nil, nil
	}
	if assignee := strings.TrimSpace(args.Assignee); assignee != "" {
		queryParts = append(queryParts, "assignee:"+assignee)
	}
	if search := strings.TrimSpace(args.Search); search != "" {
		queryParts = append(queryParts, search)
	}

	res, err := host.GitHubTicketHandler(ctx, map[string]any{
		"op":    "search",
		"repo":  repo,
		"query": strings.Join(queryParts, " "),
		"limit": limit,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("gh.issues: %v", err)), nil, nil
	}
	if res.Error != "" {
		return buildToolError(ErrBadRequest, "gh.issues: "+res.Error), nil, nil
	}
	return nil, GHIssuesOK{OK: true, Issues: res.Data["tickets"]}, nil
}

func resolveGitHubRepoForStudio(ctx context.Context, tool, dir, repoArg string) (string, *mcpsdk.CallToolResult) {
	if repo := strings.TrimSpace(repoArg); repo != "" {
		return repo, nil
	}
	out, exit, err := gitRun(ctx, dir, "remote", "get-url", "origin")
	if rerr := gitErr(tool, out, exit, err); rerr != nil {
		return "", rerr
	}
	if repo := githubRepoFromRemoteURL(out); repo != "" {
		return repo, nil
	}
	return "", buildToolError(ErrBadRequest, tool+": repo is required and origin is not a GitHub remote")
}

func githubRepoFromRemoteURL(remoteURL string) string {
	s := strings.TrimSpace(remoteURL)
	s = strings.TrimSuffix(s, ".git")
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		return strings.Trim(strings.TrimPrefix(s, "git@github.com:"), "/")
	case strings.Contains(s, "github.com/"):
		after := s[strings.Index(s, "github.com/")+len("github.com/"):]
		if u, err := url.Parse(s); err == nil && u.Host == "github.com" {
			after = strings.TrimPrefix(u.Path, "/")
		}
		return strings.Trim(after, "/")
	default:
		return ""
	}
}

// ── gh.pr_view ────────────────────────────────────────────────────────────────

// GHPRViewArgs is the input to gh.pr_view.
type GHPRViewArgs struct {
	Number      int    `json:"number"`
	Dir         string `json:"dir,omitempty"`
	Repo        string `json:"repo,omitempty"`
	IncludeDiff bool   `json:"include_diff,omitempty"`
}

// GHPRViewOK is the gh.pr_view result.
type GHPRViewOK struct {
	OK   bool   `json:"ok"`
	PR   any    `json:"pr"`
	Diff string `json:"diff,omitempty"`
}

func (srv *Server) handleGHPRView(ctx context.Context, req *mcpsdk.CallToolRequest, args GHPRViewArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.Number <= 0 {
		return buildToolError(ErrBadRequest, "gh.pr_view: number is required"), nil, nil
	}
	repo, rerr := resolveGitHubRepoForStudio(ctx, "gh.pr_view", args.Dir, args.Repo)
	if rerr != nil {
		return rerr, nil, nil
	}
	view, err := host.GitHubPRView(ctx, host.GitHubPRViewOptions{
		Repo:        repo,
		Number:      args.Number,
		IncludeDiff: args.IncludeDiff,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("gh.pr_view: %v", err)), nil, nil
	}
	return nil, GHPRViewOK{OK: true, PR: view.PR, Diff: view.Diff}, nil
}

// ── gh.comment ────────────────────────────────────────────────────────────────

// GHCommentArgs is the input to gh.comment.
type GHCommentArgs struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	On     string `json:"on,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Repo   string `json:"repo,omitempty"`
}

// GHCommentOK is the gh.comment result; URL is the posted comment's URL.
type GHCommentOK struct {
	OK  bool   `json:"ok"`
	URL string `json:"url"`
}

func (srv *Server) handleGHComment(ctx context.Context, req *mcpsdk.CallToolRequest, args GHCommentArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.Number <= 0 {
		return buildToolError(ErrBadRequest, "gh.comment: number is required"), nil, nil
	}
	if args.Body == "" {
		return buildToolError(ErrBadRequest, "gh.comment: body is required"), nil, nil
	}
	kind := args.On
	if kind == "" {
		kind = "issue"
	}
	if kind != "issue" && kind != "pr" {
		return buildToolError(ErrBadRequest, "gh.comment: on must be \"issue\" or \"pr\""), nil, nil
	}

	repo, rerr := resolveGitHubRepoForStudio(ctx, "gh.comment", args.Dir, args.Repo)
	if rerr != nil {
		return rerr, nil, nil
	}
	var res host.Result
	var err error
	if kind == "issue" {
		res, err = host.GitHubTicketHandler(ctx, map[string]any{
			"op":   "comment",
			"repo": repo,
			"id":   strconv.Itoa(args.Number),
			"body": args.Body,
		})
	} else {
		res, err = host.GitVCSHandler(ctx, map[string]any{
			"op":    "pr_comment",
			"repo":  repo,
			"pr_id": strconv.Itoa(args.Number),
			"body":  args.Body,
		})
	}
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("gh.comment: %v", err)), nil, nil
	}
	if res.Error != "" {
		return buildToolError(ErrBadRequest, "gh.comment: "+res.Error), nil, nil
	}
	url, _ := res.Data["url"].(string)
	if url == "" {
		url, _ = res.Data["comment_id"].(string)
	}
	return nil, GHCommentOK{OK: true, URL: strings.TrimSpace(url)}, nil
}
