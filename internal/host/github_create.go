// Package host — host.gh.ticket create op + the kitsoki ⇄ GitHub conventions.
//
// This file adds the one op the localfiles_ticket provider has implicitly (a
// writer drops an issues/bugs/<id>.md file) but host.gh.ticket lacked: filing a
// new issue.  It also pins the two conventions every downstream filing path
// (CLI bug create, the web Report-bug modal, the design-pipeline publish, the
// migration tool) reuses so they never drift:
//
//   - a fixed label vocabulary mapping the local bug-format axes
//     (severity / component / target / status) onto GitHub labels, and
//   - a fenced ```kitsoki body-metadata block carrying the fields GitHub has no
//     native home for (trace_ref / kitsoki_rev / filed_by / legacy_id), written
//     on create and parsed back out on get.
//
// Both live here — data, not scattered Go literals — so a single edit changes
// what every filing path emits.  See docs/proposals/gh-issue-create.md.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kitsoki/internal/reportmeta"
)

// ghTicketCreate implements ticket.create via the native GitHub REST API.
//
// Input args:
//   - title (string, required)
//   - body (string) — the prose body; the kitsoki metadata block is appended.
//   - repo (string, required) — owner/repo slug.
//   - labels ([]string | []any | comma-string, optional) — explicit labels,
//     merged with the mapped axes below.
//   - severity / component / target / status (string, optional) — mapped to
//     labels via ghTicketLabels.
//   - trace_ref / kitsoki_rev / filed_by / legacy_id (string, optional) —
//     written into the ```kitsoki body-metadata block.
//   - assignee (string, optional).
//
// Output Data: ok (bool), id (string — issue number), number (string), url
// (string), and warning (string) when labels were dropped for lack of triage
// permission (the issue is still filed, unlabelled).
func ghTicketCreate(ctx context.Context, args map[string]any) (Result, error) {
	title := strings.TrimSpace(ghStr(args["title"]))
	if title == "" {
		return Result{Error: "ticket.create: title argument is required"}, nil
	}
	body := ghAppendMetadata(ghStr(args["body"]), args)
	labels := ghTicketLabels(args)
	repo := strings.TrimSpace(ghStr(args["repo"]))
	repo = resolveTicketRepo(ctx, repo, args)
	if repo == "" {
		return Result{Error: "ticket.create: repo argument is required for native GitHub issue filing"}, nil
	}
	assignee := strings.TrimSpace(ghStr(args["assignee"]))

	build := func(withLabels bool) map[string]any {
		payload := map[string]any{"title": title, "body": body}
		if withLabels && len(labels) > 0 {
			payload["labels"] = labels
		}
		if assignee != "" {
			payload["assignees"] = []string{assignee}
		}
		return payload
	}

	var warning string
	var created struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	code, resp, err := githubAPIJSON(ctx, http.MethodPost, "repos/"+repo+"/issues", build(len(labels) > 0), &created)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.create: %v", err)}, nil
	}
	// The GitHub API rejects a labelled create if any one label is missing OR
	// the caller lacks triage. Recover in two steps so the fixed kitsoki
	// vocabulary is applied whenever possible:
	//
	//   1. ensure the labels exist (best-effort), then retry WITH labels;
	//   2. only if that still fails (a real triage-permission wall) drop labels
	//      and warn — a fork contributor can still file the issue unlabelled.
	//
	// This preserves the legacy "retry without labels when label creation is
	// blocked" behavior while keeping issue filing native.
	if code >= 300 && len(labels) > 0 && ghLooksLikeLabelErr(resp) {
		ghEnsureLabels(ctx, repo, labels)
		code, resp, err = githubAPIJSON(ctx, http.MethodPost, "repos/"+repo+"/issues", build(true), &created)
		if err != nil {
			return Result{Error: fmt.Sprintf("ticket.create: %v", err)}, nil
		}
		if code >= 300 && ghLooksLikeLabelErr(resp) {
			warning = "labels not applied (insufficient triage permission on the repo); issue filed unlabelled"
			code, resp, err = githubAPIJSON(ctx, http.MethodPost, "repos/"+repo+"/issues", build(false), &created)
			if err != nil {
				return Result{Error: fmt.Sprintf("ticket.create: %v", err)}, nil
			}
		}
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.create: %s", githubAPIError(resp))}, nil
	}
	url := strings.TrimSpace(created.HTMLURL)
	if url == "" && created.Number > 0 {
		url = githubIssueURL(repo, created.Number)
	}
	num := fmt.Sprintf("%d", created.Number)
	data := map[string]any{
		"ok":     true,
		"id":     num,
		"number": num,
		"url":    url,
	}
	if warning != "" {
		data["warning"] = warning
	}
	githubTicketSourceData(data, repo)
	data["ref"] = "github:" + repo + "#" + num
	return Result{Data: data}, nil
}

func githubAPIError(resp string) string {
	var raw struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(resp), &raw); err == nil && strings.TrimSpace(raw.Message) != "" {
		return strings.TrimSpace(raw.Message)
	}
	return strings.TrimSpace(resp)
}

// ghTicketLabels maps the local bug-format axes onto the fixed GitHub label
// vocabulary, merged with any explicit `labels` arg (de-duplicated, order
// preserved).  This is the single source of truth #2/#3/#4 reuse:
//
//	severity P0..P3   → label "P0".."P3"
//	component <c>     → label "comp:<c>"
//	target <t>        → label "target:<t>"
//	status in_progress→ label "in_progress"   (open/closed handled by transition)
func ghTicketLabels(args map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	}
	for _, l := range ghStrSlice(args["labels"]) {
		add(l)
	}
	if sev := strings.TrimSpace(ghStr(args["severity"])); sev != "" {
		add(strings.ToUpper(sev)) // P0..P3
	}
	if comp := strings.TrimSpace(ghStr(args["component"])); comp != "" {
		add("comp:" + comp)
	}
	if tgt := strings.TrimSpace(ghStr(args["target"])); tgt != "" {
		add("target:" + tgt)
	}
	if st := strings.ToLower(strings.TrimSpace(ghStr(args["status"]))); st == "in_progress" {
		add("in_progress")
	}
	return out
}

// ghAppendMetadata appends the fenced ```kitsoki metadata block to an issue
// body, carrying only the non-empty kitsoki-specific fields.  GitHub has no
// custom-field surface, so this machine-readable block is how the data
// survives a round-trip (ghParseMetadata recovers it on get).
func ghAppendMetadata(body string, args map[string]any) string {
	type kv struct{ k, v string }
	fields := []kv{
		{"trace_ref", ghStr(args["trace_ref"])},
		{"kitsoki_rev", ghStr(args["kitsoki_rev"])},
		{"filed_by", ghStr(args["filed_by"])},
		{"legacy_id", ghStr(args["legacy_id"])},
	}
	for _, key := range reportmeta.RuntimeFieldKeys {
		fields = append(fields, kv{key, ghStr(args[key])})
	}
	var lines []string
	for _, f := range fields {
		if strings.TrimSpace(f.v) != "" {
			lines = append(lines, f.k+": "+f.v)
		}
	}
	if len(lines) == 0 {
		return body
	}
	block := "```kitsoki\n" + strings.Join(lines, "\n") + "\n```"
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block + "\n"
}

// GHParseMetadata is the exported entry point onto ghParseMetadata: it recovers
// the fenced ```kitsoki body-metadata block from a comment/issue body. Exported
// (rather than duplicated) so the GitHub-agent's ack substrate and its tests
// round-trip the same block ghAppendMetadata writes, against a single source of
// truth for the fence format.
func GHParseMetadata(body string) map[string]any { return ghParseMetadata(body) }

// ghParseMetadata recovers the ```kitsoki body-metadata block written by
// ghAppendMetadata, returning the key/value pairs as a map (nil when absent).
func ghParseMetadata(body string) map[string]any {
	const open = "```kitsoki"
	start := strings.Index(body, open)
	if start < 0 {
		return nil
	}
	rest := body[start+len(open):]
	// the block runs to the next ``` fence
	end := strings.Index(rest, "```")
	if end < 0 {
		return nil
	}
	out := map[string]any{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if colon := strings.Index(line, ":"); colon >= 0 {
			k := strings.TrimSpace(line[:colon])
			v := strings.TrimSpace(line[colon+1:])
			if k != "" {
				out[k] = v
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ghLooksLikeLabelErr is a heuristic over gh's stderr for a create that failed
// on labels — either a missing label or the "you may open the issue but not
// label it" 403 a fork contributor hits.
func ghLooksLikeLabelErr(stderr string) bool {
	s := strings.ToLower(stderr)
	if !strings.Contains(s, "label") && !strings.Contains(s, "labels") {
		return false
	}
	return strings.Contains(s, "not have permission") ||
		strings.Contains(s, "resource not accessible") ||
		strings.Contains(s, "could not add label") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "must have") ||
		strings.Contains(s, "403")
}

// ghEnsureLabels best-effort creates or updates each label on the repo with a
// colour derived from its prefix. Failures are ignored — the caller's
// degrade-to-unlabelled path covers a repo where the caller lacks triage.
func ghEnsureLabels(ctx context.Context, repo string, labels []string) {
	for _, l := range labels {
		payload := map[string]any{"name": l, "color": labelColor(l)}
		if l == "source-autonomous" {
			payload["description"] = "Filed by an autonomous agent"
		}
		code, _, _ := githubAPIJSON(ctx, http.MethodPost, "repos/"+repo+"/labels", payload, nil)
		if code == http.StatusUnprocessableEntity {
			_, _, _ = githubAPIJSON(ctx, http.MethodPatch, githubLabelPath(repo, l), payload, nil)
		}
	}
}

// labelColor picks a GitHub label colour for a kitsoki-vocabulary label.
func labelColor(label string) string {
	switch {
	case label == "P0":
		return "b60205"
	case label == "P1":
		return "d93f0b"
	case label == "P2":
		return "fbca04"
	case label == "P3":
		return "0e8a16"
	case strings.HasPrefix(label, "comp:"):
		return "d4c5f9"
	case strings.HasPrefix(label, "target:"):
		return "1d76db"
	case label == "in_progress":
		return "fef2c0"
	case label == "source-autonomous":
		return "bfd4f2"
	default:
		return "ededed"
	}
}

// issueNumberFromURL pulls the trailing issue number off a gh-printed issue
// URL ("https://github.com/owner/repo/issues/42" → "42").  Unlike splitIssueID
// (which keys on "#"), a created issue's URL is path-shaped.
func issueNumberFromURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return ""
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// ghStr is a nil-safe string coercion for args map values.
func ghStr(v any) string { s, _ := v.(string); return s }

// ghStrSlice coerces a labels-style arg (a []string, a []any of strings, or a
// comma-separated string) into a trimmed []string.
func ghStrSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		var out []string
		for _, p := range strings.Split(t, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}
