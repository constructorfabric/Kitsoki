// Package host — host.gh.ticket — GitHub Issues-backed ticket provider.
//
// Implements the `ticket` host_interface (see docs/architecture/hosts.md)
// against GitHub Issues. Mirrors the localfiles_ticket.go surface so a parent
// story (kitsoki-dev, or a meta-repo/monorepo parent) can rebind
// `iface.ticket -> host.gh.ticket` without touching room YAML.
//
// Why a separate handler?  GitHub Issues is the obvious "next provider after
// local files" surface for the dogfood loop. Issue creation and bug evidence
// filing use the native GitHub REST API with GH_TOKEN/GITHUB_TOKEN so headless
// autonomous runs do not depend on a locally logged-in gh binary.
//
// The companion PR family lives in `internal/host/git_vcs.go`: stories bind
// `host.gh.ticket` for tickets and keep `host.git` for branch/PR operations
// backed by local git plus the native GitHub REST API.
//
// Native operations use an injectable HTTP client. Auth and transport failures
// return clean Result.Error values rather than crashing, so authors can route
// YAML `on_error:` arcs.
//
// TODO(ticket-provider): migrate this built-in GitHub provider onto the common
// ticketprovider/Starlark runner, or wrap it behind the same provider boundary,
// so GitHub and meta-repo/monorepo-defined providers share one auth,
// structured-error, CLI, and MCP reuse path.
package host

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// GitHubTicketHandler implements host.gh.ticket (prefix-fallback for all 5
// ticket ops).  The runtime registry's prefix-fallback means a single
// registration of `host.gh.ticket` satisfies every `host.gh.ticket.<op>`
// dispatch site — see internal/host/host.go::Get.
//
// Required args:
//   - op (string): one of create, search, get, comment, comment_edit,
//     comment_reactions, transition, list_mine.
//
// Optional args (all ops except create):
//   - repo (string): the `owner/repo` slug. Required for native operations
//     whose id/comment_id does not already include a GitHub repository.
//
// Required args (create):
//   - repo (string): the `owner/repo` slug. Native create does not infer a
//     remote because headless autonomous runs need explicit routing.
//
// Per-op input/output follows the ticket iface contract.  See doc comments on each
// dispatch helper below.
func GitHubTicketHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.gh.ticket: op argument is required"}, nil
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
	case "comment_edit":
		return ghTicketCommentEdit(ctx, args)
	case "comment_reactions":
		return ghTicketCommentReactions(ctx, args)
	case "transition":
		return ghTicketTransition(ctx, args)
	case "list_mine":
		return ghTicketListMine(ctx, args)
	default:
		return Result{Error: fmt.Sprintf("host.gh.ticket: unknown op %q", op)}, nil
	}
}

// ─── Op dispatchers ─────────────────────────────────────────────────────────

// ghTicketSearch implements ticket.search via the native GitHub Search API.
//
// Input  args: query (string), limit (int, optional), repo (string, required).
// Output Data: tickets ([]{id,title,status,priority,assignee,url}).
func ghTicketSearch(ctx context.Context, args map[string]any) (Result, error) {
	query, _ := args["query"].(string)
	limit := optInt(args, "limit", 30)
	repo := strings.TrimSpace(ghStr(args["repo"]))
	repo = resolveTicketRepo(ctx, repo, args)
	if repo == "" {
		return Result{Error: "ticket.search: repo argument is required for native GitHub issue search"}, nil
	}
	q := githubIssueSearchQuery(repo, "is:issue", strings.TrimSpace(query))
	var raw githubIssueSearchResponse
	code, resp, err := githubAPIJSON(ctx, "GET", githubSearchIssuesURL(q, limit), nil, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.search: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.search: %s", githubAPIError(resp))}, nil
	}
	ghSortItemsNewestFirst(raw.Items)
	tickets := make([]map[string]any, 0, len(raw.Items))
	for _, r := range raw.Items {
		ghNormalizeIssueURL(r)
		tickets = append(tickets, ghIssueSummary(r))
	}
	return Result{Data: map[string]any{"tickets": tickets}}, nil
}

// ghTicketGet implements ticket.get via the native GitHub REST API.
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
	repo = resolveTicketRepo(ctx, repo, args)
	if strings.TrimSpace(repo) == "" {
		return Result{Error: "ticket.get: repo argument is required for native GitHub issue lookup"}, nil
	}

	var raw map[string]any
	code, resp, err := githubAPIJSON(ctx, "GET", "repos/"+repo+"/issues/"+num, nil, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.get: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.get: %s", githubAPIError(resp))}, nil
	}
	ghNormalizeIssueURL(raw)
	data := ghIssueSummary(raw)
	if body, ok := raw["body"].(string); ok {
		data["body"] = body
		// Recover the ```kitsoki body-metadata block create() wrote (trace_ref,
		// kitsoki_rev, filed_by, legacy_id) so callers see the round-tripped
		// fields GitHub has no native home for — see github_create.go.
		if meta := ghParseMetadata(body); meta != nil {
			data["kitsoki_meta"] = meta
			// Lift legacy_id to a top-level field so the ticket view can show
			// the local-bug-file ↔ GitHub-issue identity without reaching into
			// the nested meta map. A bug filed as issues/bugs/<iso>.md and
			// re-filed as issue #N only exists in the loop as #N; surfacing the
			// legacy id makes that mapping visible instead of forcing an
			// operator to eyeball-match by title (P5).
			if lid, ok := meta["legacy_id"].(string); ok && strings.TrimSpace(lid) != "" {
				data["legacy_id"] = lid
			}
		}
	}
	var comments []any
	code, resp, err = githubAPIJSON(ctx, "GET", "repos/"+repo+"/issues/"+num+"/comments?per_page=100", nil, &comments)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.get comments: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.get comments: %s", githubAPIError(resp))}, nil
	}
	data["comments"] = comments
	return Result{Data: data}, nil
}

// ghTicketComment implements ticket.comment via the native GitHub REST API.
//
// Input  args: id (string), body (string), repo (string, optional).
// Output Data: ok (bool), comment_id/url (string — the comment web URL).
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
	repo = resolveTicketRepo(ctx, repo, args)
	if strings.TrimSpace(repo) == "" {
		return Result{Error: "ticket.comment: repo argument is required for native GitHub issue comments"}, nil
	}
	var raw map[string]any
	code, resp, err := githubAPIJSON(ctx, "POST", "repos/"+repo+"/issues/"+num+"/comments", map[string]any{"body": body}, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.comment: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.comment: %s", githubAPIError(resp))}, nil
	}
	commentURL, _ := raw["html_url"].(string)
	return Result{Data: map[string]any{
		"ok":         true,
		"comment_id": commentURL,
		"url":        commentURL,
	}}, nil
}

// ghTicketCommentEdit implements ticket.comment_edit via the GitHub REST issue
// comments endpoint.
//
// Input args: comment_id (string — accepts a raw id, an API URL, or a web URL
// with #issuecomment-N), body (string), repo (string, required unless
// comment_id is an API URL containing /repos/owner/repo/).
// Output Data: ok (bool), comment_id (string).
func ghTicketCommentEdit(ctx context.Context, args map[string]any) (Result, error) {
	commentID, _ := args["comment_id"].(string)
	body, _ := args["body"].(string)
	repo, id := splitIssueCommentID(commentID)
	if repo == "" {
		if r, _ := args["repo"].(string); r != "" {
			repo = r
		}
	}
	repo = resolveTicketRepo(ctx, repo, args)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.comment_edit: comment_id argument is required"}, nil
	}
	if strings.TrimSpace(repo) == "" {
		return Result{Error: "ticket.comment_edit: repo argument is required"}, nil
	}
	if strings.TrimSpace(body) == "" {
		return Result{Error: "ticket.comment_edit: body argument is required"}, nil
	}
	path := fmt.Sprintf("repos/%s/issues/comments/%s", repo, id)
	var raw map[string]any
	code, resp, err := githubAPIJSON(ctx, "PATCH", path, map[string]any{"body": body}, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.comment_edit: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.comment_edit: %s", githubAPIError(resp))}, nil
	}
	commentURL := commentID
	if url, _ := raw["html_url"].(string); strings.TrimSpace(url) != "" {
		commentURL = url
	}
	return Result{Data: map[string]any{
		"ok":         true,
		"comment_id": commentURL,
		"url":        commentURL,
	}}, nil
}

// ghTicketCommentReactions implements ticket.comment_reactions via the native
// GitHub REST reactions endpoint — reads the emoji reactions left on a comment
// (e.g. the ghagent ack comment) so a caller can detect a 👎 (thumbsdown)
// dissatisfaction signal (WS-C C4's gh-agent surface; see
// docs/testing/routing-tuning.md).
//
// Input  args: comment_id (string — same accepted forms as comment_edit: a
// raw id, an API URL, or a web URL with #issuecomment-N), repo (string,
// required unless comment_id is an API URL containing /repos/owner/repo/).
// Output Data: ok (bool), reactions ([]map[string]any — each carries at least
// "content", the reaction shortcode: "+1" | "-1" | "laugh" | … per the GitHub
// API), has_thumbsdown (bool), has_thumbsup (bool) — precomputed convenience
// flags so callers don't need to know the GitHub content vocabulary.
func ghTicketCommentReactions(ctx context.Context, args map[string]any) (Result, error) {
	commentID, _ := args["comment_id"].(string)
	repo, id := splitIssueCommentID(commentID)
	if repo == "" {
		if r, _ := args["repo"].(string); r != "" {
			repo = r
		}
	}
	repo = resolveTicketRepo(ctx, repo, args)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.comment_reactions: comment_id argument is required"}, nil
	}
	if strings.TrimSpace(repo) == "" {
		return Result{Error: "ticket.comment_reactions: repo argument is required"}, nil
	}
	path := fmt.Sprintf("repos/%s/issues/comments/%s/reactions", repo, id)
	var raw []map[string]any
	code, resp, err := githubAPIJSON(ctx, "GET", path, nil, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.comment_reactions: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.comment_reactions: %s", githubAPIError(resp))}, nil
	}
	hasThumbsdown, hasThumbsup := false, false
	for _, r := range raw {
		switch fmt.Sprint(r["content"]) {
		case "-1":
			hasThumbsdown = true
		case "+1":
			hasThumbsup = true
		}
	}
	reactions := make([]any, len(raw))
	for i, r := range raw {
		reactions[i] = r
	}
	return Result{Data: map[string]any{
		"ok":             true,
		"reactions":      reactions,
		"has_thumbsdown": hasThumbsdown,
		"has_thumbsup":   hasThumbsup,
	}}, nil
}

// ghTicketTransition implements ticket.transition via the native GitHub REST
// API. GitHub Issues has only two states (open / closed), so
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
	repo = resolveTicketRepo(ctx, repo, args)
	if strings.TrimSpace(repo) == "" {
		return Result{Error: "ticket.transition: repo argument is required for native GitHub issue transitions"}, nil
	}
	// Map a wide set of "closed" synonyms to GitHub's `closed`. Anything else
	// maps to `open`. This is intentionally permissive — the same
	// vocabulary the file-backed provider accepts (`resolved`, `closed`,
	// `done`, `wontfix`) maps cleanly.
	state := "open"
	switch strings.ToLower(strings.TrimSpace(to)) {
	case "closed", "close", "resolved", "done", "wontfix", "fixed":
		state = "closed"
	}
	var raw map[string]any
	code, resp, err := githubAPIJSON(ctx, "PATCH", "repos/"+repo+"/issues/"+num, map[string]any{"state": state}, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.transition: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.transition: %s", githubAPIError(resp))}, nil
	}
	return Result{Data: map[string]any{
		"ok":     true,
		"status": state,
	}}, nil
}

// ghTicketListMine implements ticket.list_mine via the native GitHub Search API.
//
// Input  args: filter (string — GitHub login of the assignee; defaults to
//
//	"@me"), repo (string, required).
//
// Output Data: tickets ([]).
func ghTicketListMine(ctx context.Context, args map[string]any) (Result, error) {
	filter, _ := args["filter"].(string)
	filter = strings.TrimSpace(filter)
	if filter == "" {
		filter = "@me"
	}
	repo := strings.TrimSpace(ghStr(args["repo"]))
	repo = resolveTicketRepo(ctx, repo, args)
	if repo == "" {
		return Result{Error: "ticket.list_mine: repo argument is required for native GitHub issue listing"}, nil
	}
	q := githubIssueSearchQuery(repo, "is:issue", "is:open", "assignee:"+filter)
	var raw githubIssueSearchResponse
	code, resp, err := githubAPIJSON(ctx, "GET", githubSearchIssuesURL(q, 100), nil, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.list_mine: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("ticket.list_mine: %s", githubAPIError(resp))}, nil
	}
	ghSortItemsNewestFirst(raw.Items)
	tickets := make([]map[string]any, 0, len(raw.Items))
	for _, r := range raw.Items {
		ghNormalizeIssueURL(r)
		tickets = append(tickets, ghIssueSummary(r))
	}
	return Result{Data: map[string]any{"tickets": tickets}}, nil
}

type githubIssueSearchResponse struct {
	Items []map[string]any `json:"items"`
}

func githubIssueSearchQuery(repo string, parts ...string) string {
	var out []string
	out = append(out, "repo:"+strings.TrimSpace(repo))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// githubSearchIssuesURL builds the GitHub Search API path for an issue query,
// asking GitHub for newest-first (sort=created, order=desc) as a page-level
// recency hint. GitHub's Search `sort=created` orders by the issue's
// `created_at`, which diverges from the issue NUMBER when issues are
// transferred/imported with a backdated timestamp — so the page comes back
// ~recent but not strictly highest-number-first. ghSortItemsNewestFirst is the
// real guarantee: it re-sorts the fetched page by issue number (the source of
// truth for "newest bug" in a repo) so the queue is strict id-DESC, matching the
// local-files provider's "id DESC (newest first)" ordering.
func githubSearchIssuesURL(q string, limit int) string {
	return "search/issues?q=" + url.QueryEscape(q) +
		"&sort=created&order=desc&per_page=" + fmt.Sprintf("%d", limit)
}

// ghSortItemsNewestFirst sorts GitHub issue rows by issue number, highest
// (newest) first. It is the load-bearing newest-first guarantee for
// host.gh.ticket listings; see githubSearchIssuesURL for why the server-side
// sort alone is insufficient.
func ghSortItemsNewestFirst(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		return ghRawNumber(items[i]) > ghRawNumber(items[j])
	})
}

// ghRawNumber reads a GitHub issue row's `number` as an int, tolerating the
// float64 / int / string shapes the API and fixtures emit. Returns 0 when the
// field is absent or unparseable, so a row without a number sorts last.
func ghRawNumber(m map[string]any) int {
	switch v := m["number"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func ghNormalizeIssueURL(raw map[string]any) {
	if _, ok := raw["url"]; ok {
		return
	}
	if html, _ := raw["html_url"].(string); strings.TrimSpace(html) != "" {
		raw["url"] = html
	}
}

// ─── Field projections ─────────────────────────────────────────────────────

// ghIssueSummary projects a GitHub issue JSON object into the
// provider-neutral ticket summary the contract pins: id / title /
// status / priority / assignee / url, plus the kitsoki-routing fields
// type (classified from labels — see ghClassifyType) and source
// ("github").  GitHub does not have a native priority field; we leave
// priority empty (callers that need it can read it off labels via
// per-team convention — out of scope for v1).
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
		// type is how dev-story's `drive` arc routes a picked ticket
		// (bug → bf, feature → impl, epic → cyp). GitHub has no native
		// ticket-type field, so we classify it from the issue's labels
		// (with a title-keyword fallback). Without this the field is "",
		// every type-guarded `drive` transition falls through to the
		// catch-all self-loop, and the headline drive button no-ops — the
		// mirror of the local-files provider's source-dir `Kind` tagging.
		"type": ghClassifyType(raw),
		// source marks this row as GitHub-issue-backed so the ticket view
		// can surface the local↔issue identity (see ghTicketGet, which
		// also lifts the legacy_id out of the ```kitsoki metadata block).
		"source": "github",
	}
}

// ghClassifyType derives a kitsoki ticket type (bug | feature | epic) from a
// `gh issue` JSON row.  GitHub Issues has no native type field, so we read it
// off the issue's labels first (a `bug` / `feature` / `epic` label, or the
// `kind:`-prefixed variants some repos use), falling back to a title-keyword
// sniff, and finally to "bug" — the historically-correct default for the
// dogfood loop, whose GitHub provider was wired to file bugs.
//
// Returning a concrete default (rather than "") is the load-bearing choice:
// dev-story's `drive` arc routes on `ticket_type == 'bug'|'feature'|'epic'`,
// and an empty type silently falls through to the no-op self-loop. A
// GitHub-sourced ticket must always classify to *some* pipeline.
// GHClassifyType is the exported entry point onto ghClassifyType: it maps a
// `gh issue ... --json labels,title` row to a bug|feature|epic class for the
// GitHub-agent router. Exported (rather than duplicated) so router.go reuses
// the single source of truth for label/title classification.
func GHClassifyType(raw map[string]any) string { return ghClassifyType(raw) }

func ghClassifyType(raw map[string]any) string {
	for _, name := range ghLabelNames(raw) {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "bug", "kind:bug", "type:bug":
			return "bug"
		case "feature", "enhancement", "kind:feature", "type:feature":
			return "feature"
		case "epic", "kind:epic", "type:epic":
			return "epic"
		}
	}
	// Title-keyword fallback for repos that don't label by type.
	title, _ := raw["title"].(string)
	switch t := strings.ToLower(title); {
	case strings.HasPrefix(t, "epic:") || strings.Contains(t, "[epic]"):
		return "epic"
	case strings.HasPrefix(t, "feature:") || strings.Contains(t, "[feature]"):
		return "feature"
	}
	return "bug"
}

// ghLabelNames pulls the label name strings off a `gh issue ... --json labels`
// row.  gh renders labels as `[{"name":"bug",...}]`; we tolerate a bare
// `["bug"]` string list too (some gh JSON shapes / fixtures).
func ghLabelNames(raw map[string]any) []string {
	list, ok := raw["labels"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, l := range list {
		switch v := l.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				out = append(out, name)
			}
		case string:
			out = append(out, v)
		}
	}
	return out
}

// splitIssueID parses an issue ref.  Accepts:
//   - "owner/repo#42" → ("owner/repo", "42")
//   - "https://github.com/owner/repo/issues/42" → ("owner/repo", "42")
//   - bare "42" or "#42" → ("", "42")
//
// Anything that doesn't fit either pattern returns ("", id) so gh's own
// resolution can take a swing at it.
func splitIssueID(id string) (string, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	if u, err := url.Parse(id); err == nil && strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "issues" && parts[3] != "" {
			return parts[0] + "/" + parts[1], parts[3]
		}
	}
	if hash := strings.LastIndex(id, "#"); hash >= 0 {
		return strings.TrimSuffix(id[:hash], "/"), strings.TrimPrefix(id[hash+1:], "#")
	}
	return "", strings.TrimPrefix(id, "#")
}

// splitIssueCommentID extracts the GitHub issue-comment id from the forms we
// store in gh_jobs.comment_id. It also recovers owner/repo from API URLs, where
// the path includes /repos/<owner>/<repo>/issues/comments/<id>.
func splitIssueCommentID(commentID string) (repo, id string) {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return "", ""
	}
	if marker := "#issuecomment-"; strings.Contains(commentID, marker) {
		parts := strings.Split(commentID, marker)
		id = strings.TrimSpace(parts[len(parts)-1])
		if u, err := url.Parse(commentID); err == nil && strings.EqualFold(u.Host, "github.com") {
			pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(pathParts) >= 2 {
				repo = pathParts[0] + "/" + pathParts[1]
			}
		}
		return repo, id
	}
	const apiPrefix = "/repos/"
	if i := strings.Index(commentID, apiPrefix); i >= 0 {
		rest := commentID[i+len(apiPrefix):]
		parts := strings.Split(rest, "/")
		if len(parts) >= 5 && parts[2] == "issues" && parts[3] == "comments" {
			return parts[0] + "/" + parts[1], parts[4]
		}
	}
	if slash := strings.LastIndex(commentID, "/"); slash >= 0 {
		return "", strings.TrimSpace(commentID[slash+1:])
	}
	return "", commentID
}

func resolveTicketRepo(ctx context.Context, repo string, args map[string]any) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		if dir := ticketRepoLookupDir(args); dir != "" {
			stdout, _, code, err := cliExec(ctx, dir, "git", "remote", "get-url", "origin")
			if err == nil && code == 0 {
				if resolved := githubRepoFromRemote(stdout); resolved != "" {
					return resolved
				}
			}
		}
		return ""
	}
	if strings.Contains(repo, "/") {
		return repo
	}
	dir := ticketRepoLookupDir(args)
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			dir = ""
		}
	}
	stdout, _, code, err := cliExec(ctx, dir, "git", "remote", "get-url", repo)
	if err == nil && code == 0 {
		if resolved := githubRepoFromRemote(stdout); resolved != "" {
			return resolved
		}
	}
	return repo
}

func ticketRepoLookupDir(args map[string]any) string {
	if v, _ := args["root"].(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, _ := args["workdir"].(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("KITSOKI_TICKETS_ROOT")); v != "" {
		return v
	}
	return ""
}
