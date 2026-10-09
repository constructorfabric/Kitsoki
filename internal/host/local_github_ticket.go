package host

import (
	"context"
	"fmt"
	"strings"
)

// LocalGitHubTicketHandler implements a dogfood-oriented ticket provider that
// searches local artifact tickets and GitHub Issues as two independent queues.
//
// The read/list operations return local rows first, then GitHub rows, with
// per-provider counts so views can render separate sections while preserving a
// single pick index over the combined ticket list.
func LocalGitHubTicketHandler(ctx context.Context, args map[string]any) (Result, error) {
	op := strings.TrimSpace(ghStr(args["op"]))
	if op == "" {
		return Result{Error: "host.local_github.ticket: op argument is required"}, nil
	}
	switch op {
	case "search", "list_mine":
		return localGitHubTicketList(ctx, args, op)
	case "get":
		return localGitHubTicketGet(ctx, args)
	case "comment", "transition", "assign", "unassign":
		if localGitHubUseGitHub(args) {
			return GitHubTicketHandler(ctx, withOp(args, op))
		}
		return LocalFilesTicketHandler(ctx, withOp(args, op))
	case "create", "comment_edit", "comment_reactions":
		return GitHubTicketHandler(ctx, withOp(args, op))
	default:
		return Result{Error: fmt.Sprintf("host.local_github.ticket: unknown op %q", op)}, nil
	}
}

func localGitHubTicketList(ctx context.Context, args map[string]any, op string) (Result, error) {
	localArgs := withOp(args, op)
	localRes, err := LocalFilesTicketHandler(ctx, localArgs)
	if err != nil {
		return Result{}, err
	}

	var providerErrors []string
	localTickets := ticketRows(localRes.Data["tickets"])
	for _, warning := range ticketFederationWarnings(localRes.Data["provider_errors"]) {
		providerErrors = append(providerErrors, "local: "+warning)
	}
	if localRes.Error != "" {
		providerErrors = append(providerErrors, "local: "+localRes.Error)
		localTickets = nil
	}
	for _, row := range localTickets {
		if strings.TrimSpace(fmt.Sprint(row["source"])) == "" {
			row["source"] = "local"
		}
	}

	var githubTickets []map[string]any
	if strings.TrimSpace(ghStr(args["repo"])) != "" {
		ghRes, ghErr := GitHubTicketHandler(ctx, withOp(args, op))
		if ghErr != nil {
			return Result{}, ghErr
		}
		githubTickets = ticketRows(ghRes.Data["tickets"])
		for _, warning := range ticketFederationWarnings(ghRes.Data["provider_errors"]) {
			providerErrors = append(providerErrors, "github: "+warning)
		}
		if ghRes.Error != "" {
			providerErrors = append(providerErrors, "github: "+ghRes.Error)
			githubTickets = nil
		}
		for _, row := range githubTickets {
			row["source"] = "github"
		}
		localTickets = removeMigratedLocalTickets(localTickets, githubTickets)
	}

	tickets := make([]map[string]any, 0, len(localTickets)+len(githubTickets))
	tickets = append(tickets, localTickets...)
	tickets = append(tickets, githubTickets...)
	if len(tickets) == 0 && len(providerErrors) > 0 {
		return Result{Error: strings.Join(providerErrors, "; ")}, nil
	}
	return Result{Data: map[string]any{
		"tickets":              tickets,
		"local_count":          len(localTickets),
		"github_count":         len(githubTickets),
		"ticket_local_count":   len(localTickets),
		"ticket_github_count":  len(githubTickets),
		"provider_errors":      providerErrors,
		"ticket_source_counts": map[string]any{"local": len(localTickets), "github": len(githubTickets)},
	}}, nil
}

func removeMigratedLocalTickets(localTickets, githubTickets []map[string]any) []map[string]any {
	migrated := make(map[string]struct{})
	for _, row := range githubTickets {
		legacyID := strings.TrimSpace(fmt.Sprint(row["legacy_id"]))
		if legacyID != "" {
			migrated[legacyID] = struct{}{}
		}
	}
	if len(migrated) == 0 {
		return localTickets
	}
	out := localTickets[:0]
	for _, row := range localTickets {
		if _, found := migrated[strings.TrimSpace(fmt.Sprint(row["id"]))]; !found {
			out = append(out, row)
		}
	}
	return out
}

func localGitHubTicketGet(ctx context.Context, args map[string]any) (Result, error) {
	source := strings.ToLower(strings.TrimSpace(ghStr(args["source"])))
	switch source {
	case "github":
		return GitHubTicketHandler(ctx, withOp(args, "get"))
	case "local":
		return LocalFilesTicketHandler(ctx, withOp(args, "get"))
	}

	localRes, err := LocalFilesTicketHandler(ctx, withOp(args, "get"))
	if err != nil {
		return Result{}, err
	}
	if localRes.Error == "" {
		return localRes, nil
	}
	if !strings.Contains(strings.ToLower(localRes.Error), "not found") || !localGitHubUseGitHub(args) {
		return localRes, nil
	}
	return GitHubTicketHandler(ctx, withOp(args, "get"))
}

func localGitHubUseGitHub(args map[string]any) bool {
	if strings.EqualFold(strings.TrimSpace(ghStr(args["source"])), "github") {
		return true
	}
	if strings.TrimSpace(ghStr(args["repo"])) != "" {
		return true
	}
	thread := strings.TrimSpace(ghStr(args["thread"]))
	id := strings.TrimSpace(ghStr(args["id"]))
	return strings.Contains(thread, "github.com/") || strings.Contains(id, "github.com/")
}

func withOp(args map[string]any, op string) map[string]any {
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["op"] = op
	return out
}

func ticketRows(v any) []map[string]any {
	switch rows := v.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, copyStringMap(row))
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(rows))
		for _, v := range rows {
			if row, ok := v.(map[string]any); ok {
				out = append(out, copyStringMap(row))
			}
		}
		return out
	default:
		return nil
	}
}

func copyStringMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
