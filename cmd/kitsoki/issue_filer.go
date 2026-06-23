package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	studio "kitsoki/internal/mcp/studio"
)

// ghIssueFiler is the production studio.IssueFiler: it shells to the GitHub CLI
// (`gh`) to create the issue. gh is the authenticated path the operator already
// has, so the studio inherits that auth with no token plumbing of its own. The
// seam keeps the studio package free of any exec/network dependency — tests
// inject a fake instead (no `gh`, no network).
//
// Label handling mirrors host.gh.ticket's ghTicketCreate so the studio's
// self-improvement loop survives a repo where the caller lacks triage:
//
//   1. best-effort `gh label create --force` every requested label (idempotent
//      upsert) so source-autonomous (and any others) exist before the create;
//   2. file WITH labels;
//   3. if that fails AND it looks like a label error (a missing label, or the
//      403 a fork contributor hits — they may open an issue but not label it),
//      retry WITHOUT labels so the issue is still filed (unlabelled) rather than
//      lost. Only a non-label failure (auth, bad repo) is fatal.
//
// Without step 3 the loop was inoperable on constructorfabric/Kitsoki from a
// fork checkout: label-create 403s (ignored), then create hard-fails on the
// unknown label, so no gap could ever be filed.
func ghIssueFiler(ctx context.Context, req studio.IssueRequest) (studio.IssueResult, error) {
	for _, label := range req.Labels {
		args := []string{"label", "create", label, "--force"}
		if label == "source-autonomous" {
			args = append(args, "--color", "BFD4F2", "--description", "Filed by an autonomous agent")
		}
		if req.Repo != "" {
			args = append(args, "--repo", req.Repo)
		}
		_ = exec.CommandContext(ctx, "gh", args...).Run() // best-effort
	}

	build := func(withLabels bool) []string {
		a := []string{"issue", "create", "--title", req.Title, "--body", req.Body}
		if req.Repo != "" {
			a = append(a, "--repo", req.Repo)
		}
		if withLabels {
			for _, label := range req.Labels {
				a = append(a, "--label", label)
			}
		}
		return a
	}

	out, err := exec.CommandContext(ctx, "gh", build(len(req.Labels) > 0)...).Output()
	if err != nil {
		stderr := ghStderr(err)
		// A label-only failure degrades to an unlabelled create rather than
		// losing the issue entirely.
		if len(req.Labels) > 0 && looksLikeLabelErr(stderr) {
			out, err = exec.CommandContext(ctx, "gh", build(false)...).Output()
		}
		if err != nil {
			if s := ghStderr(err); s != "" {
				return studio.IssueResult{}, fmt.Errorf("gh issue create: %s", s)
			}
			return studio.IssueResult{}, fmt.Errorf("gh issue create: %w", err)
		}
	}

	// gh prints the new issue's URL on stdout, e.g.
	// https://github.com/owner/repo/issues/123
	url := strings.TrimSpace(string(out))
	return studio.IssueResult{URL: url, Number: issueNumberFromURL(url)}, nil
}

// ghStderr extracts gh's stderr from an exec error (it explains auth /
// unknown-label / repo failures), or "" when none is attached.
func ghStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return ""
}

// looksLikeLabelErr heuristically detects a `gh issue create` failure caused by
// labels — a missing label or the "may open but not label" 403 a fork
// contributor hits. Mirrors host.ghLooksLikeLabelErr (kept local so cmd/kitsoki
// owns no dependency on host internals).
func looksLikeLabelErr(stderr string) bool {
	s := strings.ToLower(stderr)
	if !strings.Contains(s, "label") {
		return false
	}
	return strings.Contains(s, "not have permission") ||
		strings.Contains(s, "resource not accessible") ||
		strings.Contains(s, "could not add label") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "must have") ||
		strings.Contains(s, "403")
}

// issueNumberFromURL extracts the trailing issue number from a gh-printed issue
// URL (".../issues/123"). Returns 0 when the URL doesn't end in a number.
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
