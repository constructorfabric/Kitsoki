// Package host — host.gh.ticket — GitHub Issues-backed ticket provider.
//
// Implements the `ticket` host_interface (see docs/architecture/hosts.md) against the
// GitHub `gh` CLI.  Mirrors the localfiles_ticket.go surface so a parent
// story (kitsoki-dev, cyber-repo's devstory flavour) can rebind
// `iface.ticket → host.gh.ticket` without touching room YAML.
//
// Why a separate handler?  GitHub Issues is the obvious "next provider after
// local files" surface for the dogfood loop; we ship a
// `gh`-CLI shell-out provider in kitsoki rather than reusing the file-backed
// one when the operator wants live GitHub Issues.
//
// The companion `gh pr ...` family already lives in `internal/host/git_vcs.go`
// — that file's `host.git` handler dispatches PR ops (open_pr / pr_status /
// pr_comment) through `gh pr` when the gh CLI is available.  We deliberately
// do NOT duplicate the vcs surface here: a story binding GitHub picks
// `host.gh.ticket` for tickets and keeps `host.git` (which already routes to
// `gh pr` under the hood) for vcs.
//
// All exec calls go through the same `cliExec` seam declared in
// `cli_exec.go` so tests can substitute a deterministic runner without
// shelling out to the real `gh` binary.  When `gh` is not installed (or not
// authenticated), every op returns a clean Result.Error rather than crashing,
// so authors can route the YAML `on_error:` arc.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitHubTicketHandler implements host.gh.ticket (prefix-fallback for all 5
// ticket ops).  The runtime registry's prefix-fallback means a single
// registration of `host.gh.ticket` satisfies every `host.gh.ticket.<op>`
// dispatch site — see internal/host/host.go::Get.
//
// Required args:
//   - op (string): one of create, search, get, comment, transition, list_mine.
//
// Optional args (all ops):
//   - repo (string): the `owner/repo` slug for the `--repo` flag.  When
//     omitted, `gh` falls back to the current directory's git remote.
//
// Per-op input/output follows the ticket iface contract.  See doc comments on each
// dispatch helper below.
func GitHubTicketHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.gh.ticket: op argument is required"}, nil
	}
	if !ghAvailable(ctx) {
		return Result{Error: "host.gh.ticket: gh CLI not available — install github.com/cli/cli and run `gh auth login`"}, nil
	}
	switch op {
	case "create":
		return ghTicketCreate(ctx, args)
	case "search":
		return ghTicketSearch(ctx, args)
	case "get":
		return ghTicketGet(ctx, args)
	case "comment":
		return ghTicketComment(ctx, args)
	case "transition":
		return ghTicketTransition(ctx, args)
	case "list_mine":
		return ghTicketListMine(ctx, args)
	default:
		return Result{Error: fmt.Sprintf("host.gh.ticket: unknown op %q", op)}, nil
	}
}

// repoFlag returns `["--repo", v]` when args["repo"] is a non-empty string,
// or an empty slice otherwise.  Letting the caller decide is friendlier than
// hard-coding a default: in CI dogfood mode the operator runs `kitsoki run`
// from the repo directory and `gh` picks the remote up; in autonomous mode
// the world is seeded with the slug explicitly.
func repoFlag(args map[string]any) []string {
	if r, _ := args["repo"].(string); strings.TrimSpace(r) != "" {
		return []string{"--repo", r}
	}
	return nil
}

// ─── Op dispatchers ─────────────────────────────────────────────────────────

// ghTicketSearch implements ticket.search via `gh issue list --search`.
//
// Input  args: query (string), limit (int, optional), repo (string, optional).
// Output Data: tickets ([]{id,title,status,priority,assignee,url}).
func ghTicketSearch(ctx context.Context, args map[string]any) (Result, error) {
	query, _ := args["query"].(string)
	limit := optInt(args, "limit", 30)
	ghArgs := append([]string{"issue", "list"}, repoFlag(args)...)
	ghArgs = append(ghArgs,
		"--state", "all",
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,state,labels,assignees,url",
	)
	if q := strings.TrimSpace(query); q != "" {
		ghArgs = append(ghArgs, "--search", q)
	}
	stdout, stderr, code, err := cliExec(ctx, "", "gh", ghArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.search: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("ticket.search: %s", strings.TrimSpace(stderr))}, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return Result{Error: fmt.Sprintf("ticket.search: parse JSON: %v", err)}, nil
	}
	tickets := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		tickets = append(tickets, ghIssueSummary(r))
	}
	return Result{Data: map[string]any{"tickets": tickets}}, nil
}

// ghTicketGet implements ticket.get via `gh issue view --json`.
//
// Input args:  id (string — accepts either "owner/repo#N" or a bare "N"),
//
//	repo (string, optional fallback when id lacks a slug).
//
// Output Data: id, title, body, status, priority, assignee, url, comments.
func ghTicketGet(ctx context.Context, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.get: id argument is required"}, nil
	}
	repo, num := splitIssueID(id)
	if repo == "" {
		if r, _ := args["repo"].(string); r != "" {
			repo = r
		}
	}
	ghArgs := []string{"issue", "view", num}
	if repo != "" {
		ghArgs = append(ghArgs, "--repo", repo)
	}
	ghArgs = append(ghArgs, "--json",
		"number,title,body,state,labels,assignees,url,comments")
	stdout, stderr, code, err := cliExec(ctx, "", "gh", ghArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.get: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("ticket.get: %s", strings.TrimSpace(stderr))}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return Result{Error: fmt.Sprintf("ticket.get: parse JSON: %v", err)}, nil
	}
	data := ghIssueSummary(raw)
	if body, ok := raw["body"].(string); ok {
		data["body"] = body
		// Recover the ```kitsoki body-metadata block create() wrote (trace_ref,
		// kitsoki_rev, filed_by, legacy_id) so callers see the round-tripped
		// fields GitHub has no native home for — see github_create.go.
		if meta := ghParseMetadata(body); meta != nil {
			data["kitsoki_meta"] = meta
		}
	}
	if comments, ok := raw["comments"].([]any); ok {
		data["comments"] = comments
	} else {
		data["comments"] = []any{}
	}
	return Result{Data: data}, nil
}

// ghTicketComment implements ticket.comment via `gh issue comment --body`.
//
// Input  args: id (string), body (string), repo (string, optional).
// Output Data: ok (bool), comment_id (string — the comment URL from gh's
//
//	stdout when present, else the issue url).
func ghTicketComment(ctx context.Context, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	body, _ := args["body"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.comment: id argument is required"}, nil
	}
	if strings.TrimSpace(body) == "" {
		return Result{Error: "ticket.comment: body argument is required"}, nil
	}
	repo, num := splitIssueID(id)
	if repo == "" {
		if r, _ := args["repo"].(string); r != "" {
			repo = r
		}
	}
	ghArgs := []string{"issue", "comment", num}
	if repo != "" {
		ghArgs = append(ghArgs, "--repo", repo)
	}
	ghArgs = append(ghArgs, "--body", body)
	stdout, stderr, code, err := cliExec(ctx, "", "gh", ghArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.comment: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("ticket.comment: %s", strings.TrimSpace(stderr))}, nil
	}
	commentURL := lastNonEmptyLine(stdout)
	return Result{Data: map[string]any{
		"ok":         true,
		"comment_id": commentURL,
	}}, nil
}

// ghTicketTransition implements ticket.transition via `gh issue close` /
// `gh issue reopen`.  GitHub Issues has only two states (open / closed), so
// any `to:` value not in the closed-set re-opens.
//
// Input  args: id (string), to (string — "closed" | "resolved" | "open" | ...),
//
//	repo (string, optional).
//
// Output Data: ok (bool).
func ghTicketTransition(ctx context.Context, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	to, _ := args["to"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.transition: id argument is required"}, nil
	}
	if strings.TrimSpace(to) == "" {
		return Result{Error: "ticket.transition: to argument is required"}, nil
	}
	repo, num := splitIssueID(id)
	if repo == "" {
		if r, _ := args["repo"].(string); r != "" {
			repo = r
		}
	}
	// Map a wide set of "closed" synonyms to gh's `close`.  Anything else
	// triggers `reopen`.  This is intentionally permissive — the same
	// vocabulary the file-backed provider accepts (`resolved`, `closed`,
	// `done`, `wontfix`) maps cleanly.
	sub := "reopen"
	switch strings.ToLower(strings.TrimSpace(to)) {
	case "closed", "close", "resolved", "done", "wontfix", "fixed":
		sub = "close"
	}
	ghArgs := []string{"issue", sub, num}
	if repo != "" {
		ghArgs = append(ghArgs, "--repo", repo)
	}
	_, stderr, code, err := cliExec(ctx, "", "gh", ghArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.transition: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("ticket.transition: %s", strings.TrimSpace(stderr))}, nil
	}
	return Result{Data: map[string]any{"ok": true}}, nil
}

// ghTicketListMine implements ticket.list_mine via `gh issue list --assignee`.
//
// Input  args: filter (string — GitHub login of the assignee; defaults to
//
//	"@me"), repo (string, optional).
//
// Output Data: tickets ([]).
func ghTicketListMine(ctx context.Context, args map[string]any) (Result, error) {
	filter, _ := args["filter"].(string)
	filter = strings.TrimSpace(filter)
	if filter == "" {
		filter = "@me"
	}
	ghArgs := append([]string{"issue", "list"}, repoFlag(args)...)
	ghArgs = append(ghArgs,
		"--state", "open",
		"--assignee", filter,
		"--limit", "100",
		"--json", "number,title,state,labels,assignees,url",
	)
	stdout, stderr, code, err := cliExec(ctx, "", "gh", ghArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.list_mine: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("ticket.list_mine: %s", strings.TrimSpace(stderr))}, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return Result{Error: fmt.Sprintf("ticket.list_mine: parse JSON: %v", err)}, nil
	}
	tickets := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		tickets = append(tickets, ghIssueSummary(r))
	}
	return Result{Data: map[string]any{"tickets": tickets}}, nil
}

// ─── Field projections ─────────────────────────────────────────────────────

// ghIssueSummary projects a `gh issue list --json` row into the
// provider-neutral ticket summary the contract pins: id / title /
// status / priority / assignee / url.  GitHub does not have a native
// priority field; we leave priority empty (callers that need it can read it
// off labels via per-team convention — out of scope for v1).
func ghIssueSummary(raw map[string]any) map[string]any {
	num := ""
	switch v := raw["number"].(type) {
	case float64:
		num = fmt.Sprintf("%.0f", v)
	case int:
		num = fmt.Sprintf("%d", v)
	case string:
		num = v
	}
	title, _ := raw["title"].(string)
	state, _ := raw["state"].(string)
	url, _ := raw["url"].(string)
	assignee := ""
	if list, ok := raw["assignees"].([]any); ok && len(list) > 0 {
		if first, ok := list[0].(map[string]any); ok {
			if login, ok := first["login"].(string); ok {
				assignee = login
			}
		}
	}
	return map[string]any{
		"id":       num,
		"title":    title,
		"status":   strings.ToLower(state),
		"priority": "", // GitHub has no native priority field
		"assignee": assignee,
		"url":      url,
	}
}

// splitIssueID parses an issue ref.  Accepts "owner/repo#42" → ("owner/repo",
// "42"); a bare "42" → ("", "42"); a #-prefixed "#42" → ("", "42").
// Anything that doesn't fit either pattern returns ("", id) so gh's own
// resolution can take a swing at it.
func splitIssueID(id string) (string, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	if hash := strings.LastIndex(id, "#"); hash >= 0 {
		return strings.TrimSuffix(id[:hash], "/"), strings.TrimPrefix(id[hash+1:], "#")
	}
	return "", strings.TrimPrefix(id, "#")
}
