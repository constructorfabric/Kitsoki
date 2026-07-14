package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"kitsoki/internal/host"
	"kitsoki/internal/mcp/graphsrv"
	studio "kitsoki/internal/mcp/studio"
)

// ghIssueFiler is the production studio.IssueFiler. The name is kept for the
// existing MCP wiring, but the implementation is native: it routes through
// host.gh.ticket.create, which files via the GitHub REST API using GH_TOKEN or
// GITHUB_TOKEN. Tests inject a fake IssueFiler at the studio seam.
func ghIssueFiler(ctx context.Context, req studio.IssueRequest) (studio.IssueResult, error) {
	args := map[string]any{
		"op":     "create",
		"repo":   req.Repo,
		"root":   req.Root,
		"title":  req.Title,
		"body":   req.Body,
		"labels": req.Labels,
	}
	res, err := host.GitHubTicketHandler(ctx, args)
	if err != nil {
		return studio.IssueResult{}, err
	}
	if res.Error != "" {
		return studio.IssueResult{}, fmt.Errorf("%s", res.Error)
	}
	url, _ := res.Data["url"].(string)
	return studio.IssueResult{URL: url, Number: issueNumberFromURL(url)}, nil
}

// ghGraphIssueFiler adapts ghIssueFiler — which already matches
// studio.IssueFiler's exact signature — to graphsrv.IssueFiler for the
// graph-mcp/studio-mounted feedback.report github sink (P6). This is a real
// (if trivial) adapter rather than a bare pass-through: Go's function-type
// assignability requires identical parameter/return types, and
// studio.IssueRequest/graphsrv.IssueRequest are distinct named types even
// though their fields match field-for-field, so `ghIssueFiler` cannot be
// passed directly where a graphsrv.IssueFiler is expected.
func ghGraphIssueFiler(ctx context.Context, req graphsrv.IssueRequest) (graphsrv.IssueResult, error) {
	res, err := ghIssueFiler(ctx, studio.IssueRequest{
		Repo:   req.Repo,
		Root:   req.Root,
		Title:  req.Title,
		Body:   req.Body,
		Labels: req.Labels,
	})
	return graphsrv.IssueResult{URL: res.URL, Number: res.Number}, err
}

// issueNumberFromURL extracts the trailing issue number from an issue URL
// (".../issues/123"). Returns 0 when the URL doesn't end in a number.
func issueNumberFromURL(url string) int {
	idx := strings.LastIndex(url, "/")
	if idx < 0 || idx == len(url)-1 {
		return 0
	}
	n, err := strconv.Atoi(url[idx+1:])
	if err != nil {
		return 0
	}
	return n
}
