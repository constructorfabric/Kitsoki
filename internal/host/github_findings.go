// github_findings.go — file product-journey QA `issue` findings as GitHub
// issues through the SAME artifact-preserving orchestration the web Report-bug
// and TUI /bug paths use (GitHubFileBug + release-asset evidence upload).
//
// A product-journey run bundle (tools/product-journey) records findings in
// <run-dir>/findings.json with attached evidence in <run-dir>/evidence.json and
// a driver journal of what was actually attempted. This orchestration walks the
// credible `issue` findings (observed, not seeded demo data), assembles an
// expected/actual/reproduction body from the finding + scenario contract +
// journal, checks for strongly-similar open issues, and either files one issue
// per new finding or comments on the related existing issue. Issue URLs are
// written back into findings.json (item.github_issue) so re-runs are
// idempotent, and a findings.filing block records that filing was requested so
// the runner's review/validate gates can require "issues filed for all credible
// findings".
//
// The GitHub write path uses the native ticket provider and GitHub REST helpers,
// so headless autonomous runs only need GH_TOKEN/GITHUB_TOKEN. Tests inject a
// fake HTTP API and never touch real GitHub.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/runstatus/harscrub"
)

// FindingsFilingInput is the input to GitHubFileFindings.
type FindingsFilingInput struct {
	RunDir   string // product-journey run bundle directory (findings.json et al)
	RepoRoot string // repo root used to resolve relative evidence refs ("" = cwd)
	Repo     string // owner/repo ticket target

	// DryRun renders every issue that WOULD be filed (title/body/evidence) and
	// makes no gh calls and no bundle writes.
	DryRun bool

	KitsokiRev string // short HEAD sha recorded in the kitsoki metadata block
	FiledBy    string // filed_by recorded in the kitsoki metadata block

	// PrivacyChecker optionally runs the provider-backed privacy gate before a
	// finding is searched/filed. Nil keeps deterministic scrubbing only.
	PrivacyChecker bugprivacy.Checker
	// PrivacyFollowUpRoot controls where depersonalized follow-up bugs are
	// written. Empty defaults to <run-dir>/privacy-followups. DryRun never writes
	// follow-ups and never calls PrivacyChecker.
	PrivacyFollowUpRoot string
	// ScrubOptions overrides the deterministic scrubber defaults. Empty fields
	// are filled with the standard HOME + built-in secret patterns.
	ScrubOptions harscrub.ScrubOptions

	now func() time.Time // test seam; nil = time.Now
}

// FindingFilingOutcome is the per-finding result row.
type FindingFilingOutcome struct {
	FindingID   string             `json:"finding_id"`
	Title       string             `json:"title"`
	Scenario    string             `json:"scenario,omitempty"`
	Status      string             `json:"status"` // filed | skipped | failed | dry-run | excluded
	IssueURL    string             `json:"issue_url,omitempty"`
	IssueNumber string             `json:"issue_number,omitempty"`
	CommentURL  string             `json:"comment_url,omitempty"`
	Error       string             `json:"error,omitempty"`
	Reason      string             `json:"reason,omitempty"`
	Evidence    []string           `json:"evidence,omitempty"`
	Body        string             `json:"body,omitempty"` // rendered body (dry-run only)
	Privacy     *bugprivacy.Result `json:"privacy,omitempty"`
}

// FindingsFilingResult is what GitHubFileFindings returns. Per-finding failures
// are recorded in Outcomes/Failed (the run completes); only bundle-level
// problems (unreadable bundle, missing target repo, etc.) return an error.
type FindingsFilingResult struct {
	Status   string                 `json:"status"` // findings_filed | findings_dry_run
	Repo     string                 `json:"ticket_repo"`
	RunDir   string                 `json:"run_dir"`
	DryRun   bool                   `json:"dry_run"`
	Filed    int                    `json:"filed"`
	Related  int                    `json:"related"`
	Skipped  int                    `json:"skipped"`
	Failed   int                    `json:"failed"`
	Excluded int                    `json:"excluded"`
	Outcomes []FindingFilingOutcome `json:"outcomes"`
}

// GitHubFileFindings files one GitHub issue per credible unfiled `issue`
// finding in the run bundle. See the file comment for the full contract.
func GitHubFileFindings(ctx context.Context, in FindingsFilingInput) (FindingsFilingResult, error) {
	res := FindingsFilingResult{Repo: in.Repo, RunDir: in.RunDir, DryRun: in.DryRun}
	res.Status = "findings_filed"
	if in.DryRun {
		res.Status = "findings_dry_run"
	}
	if strings.TrimSpace(in.Repo) == "" {
		return res, fmt.Errorf("file findings: ticket repo is required (owner/repo)")
	}
	runDir := strings.TrimSpace(in.RunDir)
	if runDir == "" {
		return res, fmt.Errorf("file findings: run dir is required")
	}
	repoRoot := in.RepoRoot
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	nowFn := in.now
	if nowFn == nil {
		nowFn = time.Now
	}

	findingsPath := filepath.Join(runDir, "findings.json")
	findings, err := readJSONMap(findingsPath)
	if err != nil {
		return res, fmt.Errorf("file findings: %w", err)
	}
	runJSON, err := readJSONMap(filepath.Join(runDir, "run.json"))
	if err != nil {
		return res, fmt.Errorf("file findings: %w", err)
	}
	// Optional context sources — a partially-built bundle still files.
	driverPlan, _ := readJSONMap(filepath.Join(runDir, "driver-plan.json"))
	journal, _ := readJSONMap(filepath.Join(runDir, "driver-journal.json"))
	evidence, _ := readJSONMap(filepath.Join(runDir, "evidence.json"))

	items, _ := findings["items"].([]any)
	changed := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !credibleIssueFinding(item) {
			res.Excluded++
			res.Outcomes = append(res.Outcomes, FindingFilingOutcome{
				FindingID: str(item["id"]),
				Title:     str(item["title"]),
				Scenario:  str(item["scenario"]),
				Status:    "excluded",
				Reason:    findingExclusionReason(item),
			})
			continue
		}
		out := FindingFilingOutcome{
			FindingID: str(item["id"]),
			Title:     str(item["title"]),
			Scenario:  str(item["scenario"]),
		}
		if url := filedIssueURL(item); url != "" {
			out.Status = "skipped"
			out.IssueURL = url
			res.Skipped++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}

		body, files, refs := assembleFindingIssue(runDir, repoRoot, runJSON, driverPlan, journal, evidence, item)
		title := findingIssueTitle(item)
		for _, f := range files {
			out.Evidence = append(out.Evidence, f.Path)
		}
		out.Evidence = append(out.Evidence, refs...)
		traceRef := fmt.Sprintf("product-journey://%s/%s", str(runJSON["run_id"]), str(item["id"]))
		safeTitle, safeBody, privacy, blocked, perr := checkFindingPrivacy(ctx, in, runDir, title, body, traceRef, out.Evidence)
		if perr != nil {
			out.Title = "privacy-check-error finding"
			out.Status = "failed"
			out.Error = perr.Error()
			out.Privacy = &privacy
			res.Failed++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		title = safeTitle
		body = safeBody
		out.Privacy = &privacy
		if blocked {
			out.Title = "privacy-blocked finding"
			out.Status = "failed"
			out.Error = privacy.Message
			if privacy.FollowUpPath != "" {
				out.Error += "; depersonalized follow-up filed at " + privacy.FollowUpPath
			}
			res.Failed++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		out.Title = title

		if in.DryRun {
			out.Status = "dry-run"
			out.Body = body
			res.Outcomes = append(res.Outcomes, out)
			continue
		}

		related, err := findRelatedFindingIssue(ctx, in.Repo, title, traceRef)
		if err != nil {
			out.Status = "failed"
			out.Error = err.Error()
			res.Failed++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		if related.URL != "" {
			commentBody := relatedFindingComment(runJSON, item, body, out.Evidence)
			comment, err := commentRelatedFinding(ctx, in.Repo, related.ID, commentBody)
			if err != nil {
				out.Status = "failed"
				out.Error = err.Error()
				res.Failed++
				res.Outcomes = append(res.Outcomes, out)
				continue
			}
			out.Status = "related"
			out.IssueURL = related.URL
			out.IssueNumber = related.ID
			out.CommentURL = comment
			item["github_issue"] = map[string]any{
				"url":         related.URL,
				"number":      related.ID,
				"repo":        in.Repo,
				"relation":    "related",
				"related_at":  nowFn().UTC().Format(time.RFC3339),
				"comment_url": comment,
			}
			changed = true
			res.Related++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}

		filed, err := GitHubFileBug(ctx, GitHubBugFiling{
			Repo:            in.Repo,
			Title:           title,
			Body:            body,
			Severity:        str(item["severity"]),
			Component:       "product-journey",
			Target:          "kitsoki",
			TraceRef:        traceRef,
			KitsokiRev:      in.KitsokiRev,
			FiledBy:         in.FiledBy,
			Evidence:        files,
			UploadArtifacts: true,
		})
		if err != nil {
			out.Status = "failed"
			out.Error = err.Error()
			res.Failed++
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
		out.Status = "filed"
		out.IssueURL = filed.URL
		out.IssueNumber = filed.Number
		issue := map[string]any{
			"url":      filed.URL,
			"number":   filed.Number,
			"repo":     in.Repo,
			"filed_at": nowFn().UTC().Format(time.RFC3339),
		}
		if assets := findingEvidenceAssetRecords(filed.Assets); len(assets) > 0 {
			issue["evidence_assets"] = assets
		}
		item["github_issue"] = issue
		changed = true
		res.Filed++
		res.Outcomes = append(res.Outcomes, out)
	}

	// Record that filing was requested (and its latest tallies) so the runner's
	// review/validate gates can require every credible finding to be filed.
	if !in.DryRun {
		findings["filing"] = map[string]any{
			"requested":   true,
			"ticket_repo": in.Repo,
			"updated_at":  nowFn().UTC().Format(time.RFC3339),
			"filed":       res.Filed,
			"related":     res.Related,
			"skipped":     res.Skipped,
			"failed":      res.Failed,
			"excluded":    res.Excluded,
		}
		if err := writeJSONMap(findingsPath, findings); err != nil {
			return res, fmt.Errorf("file findings: record results: %w", err)
		}
		_ = changed // findings.json is always rewritten to stamp the filing block
	}
	return res, nil
}

func checkFindingPrivacy(ctx context.Context, in FindingsFilingInput, runDir, title, body, traceRef string, evidence []string) (string, string, bugprivacy.Result, bool, error) {
	report := bugprivacy.Report{
		Surface:       "product-journey",
		Target:        "kitsoki",
		Title:         title,
		Body:          body,
		Component:     "product-journey",
		TraceRef:      traceRef,
		ArtifactNames: append([]string(nil), evidence...),
	}
	opts := findingPrivacyScrubOptions(in.ScrubOptions)
	if in.DryRun {
		safe, findings := bugprivacy.Sanitize(report, opts)
		privacy := bugprivacy.Result{
			Status:   "dry_run",
			Message:  "privacy dry-run; deterministic substitutions previewed",
			Findings: findings,
		}
		if len(findings) == 0 {
			privacy.Message = "privacy dry-run; no deterministic substitutions"
		}
		return safe.Title, safe.Body, privacy, false, nil
	}
	followUpRoot := strings.TrimSpace(in.PrivacyFollowUpRoot)
	if followUpRoot == "" {
		followUpRoot = filepath.Join(runDir, "privacy-followups")
	}
	safe, privacy, err := bugprivacy.Check(ctx, in.PrivacyChecker, report, opts, followUpRoot, in.FiledBy)
	return safe.Title, safe.Body, privacy, privacy.Blocked(), err
}

func findingPrivacyScrubOptions(opts harscrub.ScrubOptions) harscrub.ScrubOptions {
	if opts.Home == "" {
		opts.Home = os.Getenv("HOME")
	}
	if opts.SecretPatterns == nil {
		opts.SecretPatterns = harscrub.DefaultSecretPatterns()
	}
	return opts
}

func findingEvidenceAssetRecords(assets map[string]string) []map[string]any {
	if len(assets) == 0 {
		return nil
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		url := strings.TrimSpace(assets[name])
		if url == "" {
			continue
		}
		out = append(out, map[string]any{
			"name": name,
			"url":  url,
		})
	}
	return out
}

type relatedFindingIssue struct {
	ID    string
	URL   string
	Title string
}

func findRelatedFindingIssue(ctx context.Context, repo, title, traceRef string) (relatedFindingIssue, error) {
	titleTickets, err := searchRelatedFindingTickets(ctx, repo, strings.TrimSpace("is:open in:title "+title))
	if err != nil {
		return relatedFindingIssue{}, err
	}
	for _, ticket := range titleTickets {
		if strongRelatedTitle(title, str(ticket["title"])) {
			return relatedIssueFromTicket(ticket), nil
		}
	}
	if strings.TrimSpace(traceRef) != "" {
		traceTickets, err := searchRelatedFindingTickets(ctx, repo, strings.TrimSpace("is:open in:body "+traceRef))
		if err != nil {
			return relatedFindingIssue{}, err
		}
		for _, ticket := range traceTickets {
			matches, err := ticketMatchesTraceRef(ctx, repo, ticket, traceRef)
			if err != nil {
				return relatedFindingIssue{}, err
			}
			if matches {
				return relatedIssueFromTicket(ticket), nil
			}
		}
	}
	return relatedFindingIssue{}, nil
}

func searchRelatedFindingTickets(ctx context.Context, repo, query string) ([]map[string]any, error) {
	res, err := GitHubTicketHandler(ctx, map[string]any{
		"op":    "search",
		"repo":  repo,
		"query": query,
		"limit": 10,
	})
	if err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, fmt.Errorf("ticket.search: %s", res.Error)
	}
	return ticketsFromResult(res), nil
}

func relatedIssueFromTicket(ticket map[string]any) relatedFindingIssue {
	return relatedFindingIssue{
		ID:    str(ticket["id"]),
		URL:   str(ticket["url"]),
		Title: str(ticket["title"]),
	}
}

func ticketMatchesTraceRef(ctx context.Context, repo string, ticket map[string]any, traceRef string) (bool, error) {
	if issueHasTraceRef(ticket, traceRef) {
		return true, nil
	}
	id := str(ticket["id"])
	if id == "" {
		return false, nil
	}
	res, err := GitHubTicketHandler(ctx, map[string]any{
		"op":   "get",
		"repo": repo,
		"id":   id,
	})
	if err != nil {
		return false, err
	}
	if res.Error != "" {
		return false, fmt.Errorf("ticket.get: %s", res.Error)
	}
	return issueHasTraceRef(res.Data, traceRef), nil
}

func issueHasTraceRef(issue map[string]any, traceRef string) bool {
	traceRef = strings.TrimSpace(traceRef)
	if traceRef == "" {
		return false
	}
	if meta, ok := issue["kitsoki_meta"].(map[string]any); ok && strings.TrimSpace(str(meta["trace_ref"])) == traceRef {
		return true
	}
	body := str(issue["body"])
	if meta := GHParseMetadata(body); meta != nil && strings.TrimSpace(str(meta["trace_ref"])) == traceRef {
		return true
	}
	for _, raw := range anySlice(issue["comments"]) {
		comment, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		body := str(comment["body"])
		if strings.Contains(body, traceRef) {
			return true
		}
		if meta := GHParseMetadata(body); meta != nil && strings.TrimSpace(str(meta["trace_ref"])) == traceRef {
			return true
		}
	}
	return false
}

func commentRelatedFinding(ctx context.Context, repo, issueID, body string) (string, error) {
	res, err := GitHubTicketHandler(ctx, map[string]any{
		"op":   "comment",
		"repo": repo,
		"id":   issueID,
		"body": body,
	})
	if err != nil {
		return "", err
	}
	if res.Error != "" {
		return "", fmt.Errorf("ticket.comment: %s", res.Error)
	}
	return str(res.Data["url"]), nil
}

func relatedFindingComment(runJSON, item map[string]any, issueBody string, evidence []string) string {
	runID := str(runJSON["run_id"])
	var sb strings.Builder
	sb.WriteString("<!-- kitsoki-related-product-journey-finding -->\n")
	sb.WriteString("A product-journey QA run found a similar issue, so Kitsoki attached the new evidence here instead of filing a duplicate.\n\n")
	fmt.Fprintf(&sb, "- Run: `%s`\n", runID)
	fmt.Fprintf(&sb, "- Finding: `%s`\n", str(item["id"]))
	fmt.Fprintf(&sb, "- Trace ref: `product-journey://%s/%s`\n", runID, str(item["id"]))
	if scenario := str(item["scenario"]); scenario != "" {
		fmt.Fprintf(&sb, "- Scenario: `%s`\n", scenario)
	}
	if len(evidence) > 0 {
		sb.WriteString("- Evidence refs:\n")
		for _, ref := range evidence {
			fmt.Fprintf(&sb, "  - `%s`\n", ref)
		}
	}
	sb.WriteString("\n## Related observation\n\n")
	sb.WriteString(issueBody)
	return sb.String()
}

func ticketsFromResult(res Result) []map[string]any {
	if rows, ok := res.Data["tickets"].([]map[string]any); ok {
		return rows
	}
	rawRows, _ := res.Data["tickets"].([]any)
	out := make([]map[string]any, 0, len(rawRows))
	for _, raw := range rawRows {
		if row, ok := raw.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func anySlice(value any) []any {
	if rows, ok := value.([]any); ok {
		return rows
	}
	if rows, ok := value.([]map[string]any); ok {
		out := make([]any, 0, len(rows))
		for _, row := range rows {
			out = append(out, row)
		}
		return out
	}
	return nil
}

func strongRelatedTitle(left, right string) bool {
	for _, leftNorm := range normalizedFindingTitleCandidates(left) {
		for _, rightNorm := range normalizedFindingTitleCandidates(right) {
			if leftNorm == "" || rightNorm == "" {
				continue
			}
			if leftNorm == rightNorm ||
				strings.HasSuffix(leftNorm, rightNorm) ||
				strings.HasSuffix(rightNorm, leftNorm) ||
				findingTitleSimilarity(leftNorm, rightNorm) >= 0.82 {
				return true
			}
		}
	}
	return false
}

func normalizedFindingTitleCandidates(value string) []string {
	candidates := []string{normalizeFindingTitle(value)}
	if _, after, found := strings.Cut(value, ":"); found {
		candidates = append(candidates, normalizeFindingTitle(after))
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

func findingTitleSimilarity(left, right string) float64 {
	if len([]rune(left)) < 12 || len([]rune(right)) < 12 {
		return 0
	}
	leftGrams := findingTitleBigrams(left)
	rightGrams := findingTitleBigrams(right)
	if len(leftGrams) == 0 || len(rightGrams) == 0 {
		return 0
	}
	overlap := 0
	for gram, leftCount := range leftGrams {
		if rightCount := rightGrams[gram]; rightCount > 0 {
			if leftCount < rightCount {
				overlap += leftCount
			} else {
				overlap += rightCount
			}
		}
	}
	total := 0
	for _, count := range leftGrams {
		total += count
	}
	for _, count := range rightGrams {
		total += count
	}
	if total == 0 {
		return 0
	}
	return float64(2*overlap) / float64(total)
}

func findingTitleBigrams(value string) map[string]int {
	runes := []rune(value)
	if len(runes) < 2 {
		return nil
	}
	grams := make(map[string]int, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		grams[string(runes[i:i+2])]++
	}
	return grams
}

func normalizeFindingTitle(value string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// credibleIssueFinding reports whether a finding should be filed: an observed
// `issue`, not seeded demo data, and not a blocker. Blocked scenarios are
// recorded as issue findings in the bundle, but they describe capture gaps in
// the QA run (missing cassette, unavailable surface), not product defects, so
// they must never be filed upstream. Legacy findings predate the origin field
// and count as observed.
func credibleIssueFinding(item map[string]any) bool {
	if str(item["kind"]) != "issue" {
		return false
	}
	if str(item["status"]) == "blocked" {
		return false
	}
	origin := str(item["origin"])
	return origin == "" || origin != "seeded"
}

func findingExclusionReason(item map[string]any) string {
	if str(item["kind"]) != "issue" {
		return "non_issue"
	}
	if str(item["status"]) == "blocked" {
		return "blocked"
	}
	if strings.TrimSpace(str(item["origin"])) == "seeded" {
		return "seeded"
	}
	return "ineligible"
}

// filedIssueURL returns the already-filed issue URL recorded on the finding
// ("" when the finding has not been filed).
func filedIssueURL(item map[string]any) string {
	gi, _ := item["github_issue"].(map[string]any)
	return strings.TrimSpace(str(gi["url"]))
}

// findingIssueTitle renders the issue title: surface prefix + scenario + the
// finding's own title.
func findingIssueTitle(item map[string]any) string {
	title := strings.TrimSpace(str(item["title"]))
	scenario := strings.TrimSpace(str(item["scenario"]))
	if scenario == "" {
		return "product-journey: " + title
	}
	return fmt.Sprintf("product-journey %s: %s", scenario, title)
}

// assembleFindingIssue builds the issue body (expected / actual / reproduction /
// run-bundle context) plus the locally-resolvable evidence files to upload and
// the non-local evidence refs listed in the body.
func assembleFindingIssue(runDir, repoRoot string, runJSON, driverPlan, journal, evidence map[string]any, item map[string]any) (string, []EvidenceFile, []string) {
	scenario := str(item["scenario"])
	var sb strings.Builder

	sb.WriteString(strings.TrimSpace(str(item["summary"])))

	// Expected: the scenario's success criteria from the driver plan (the
	// contract the persona was driving against), with a generic fallback.
	sb.WriteString("\n\n## Expected\n\n")
	criteria := scenarioSuccessCriteria(driverPlan, scenario)
	if len(criteria) == 0 {
		if scenario != "" {
			fmt.Fprintf(&sb, "- The %s journey completes without the problem described below.\n", scenario)
		} else {
			sb.WriteString("- The journey completes without the problem described below.\n")
		}
	} else {
		for _, c := range criteria {
			fmt.Fprintf(&sb, "- %s\n", c)
		}
	}

	// Actual: what the QA driver observed.
	sb.WriteString("\n## Actual\n\n")
	fmt.Fprintf(&sb, "%s\n", strings.TrimSpace(str(item["summary"])))
	if status := str(item["status"]); status == "blocked" {
		sb.WriteString("\nThe scenario was attempted but blocked before evidence capture could complete.\n")
	}
	fmt.Fprintf(&sb, "\nSeverity: %s\n", orDefault(str(item["severity"]), "medium"))

	// Reproduction: run context + the scenario task + the driver journal of
	// what was actually attempted.
	sb.WriteString("\n## Reproduction\n\n")
	project, persona := runProjectPersona(runJSON)
	step := 1
	fmt.Fprintf(&sb, "%d. Product-journey QA run `%s` (project `%s`, persona `%s`, seed `%s`).\n",
		step, str(runJSON["run_id"]), project, persona, str(runJSON["seed"]))
	step++
	if task := scenarioTaskPrompt(runJSON, driverPlan, scenario); task != "" {
		fmt.Fprintf(&sb, "%d. Scenario `%s`: %s\n", step, scenario, task)
		step++
	}
	for _, ev := range journalEventsForScenario(journal, scenario) {
		line := str(ev["summary"])
		if tools := strList(ev["mcp_tools"]); len(tools) > 0 {
			line = fmt.Sprintf("%s (tools: %s)", line, strings.Join(tools, ", "))
		}
		fmt.Fprintf(&sb, "%d. %s\n", step, line)
		step++
	}

	// Run-bundle pointer so a reviewer can open the full journal/deck.
	sb.WriteString("\n## Run bundle\n\n")
	fmt.Fprintf(&sb, "- Run dir: `%s`\n", displayPath(runDir, repoRoot))
	fmt.Fprintf(&sb, "- Finding: `%s` (kind issue, status %s)\n", str(item["id"]), orDefault(str(item["status"]), "open"))

	files, refs := collectFindingEvidence(runDir, repoRoot, evidence, item)
	if len(refs) > 0 {
		sb.WriteString("\n## Additional evidence references\n\n")
		sb.WriteString("_These references are not local files and could not be uploaded; resolve them via the run bundle._\n\n")
		for _, r := range refs {
			fmt.Fprintf(&sb, "- `%s`\n", r)
		}
	}
	return sb.String(), files, refs
}

// collectFindingEvidence gathers the finding's own evidence_path plus every
// captured/validated evidence artifact attached to the same scenario. Refs that
// resolve to a local file become uploadable EvidenceFiles; the rest are
// returned as body-only references.
func collectFindingEvidence(runDir, repoRoot string, evidence map[string]any, item map[string]any) ([]EvidenceFile, []string) {
	scenario := str(item["scenario"])
	type ref struct{ label, path string }
	var ordered []ref
	seen := map[string]bool{}
	add := func(label, path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		ordered = append(ordered, ref{label, path})
	}
	add("finding evidence", str(item["evidence_path"]))
	items, _ := evidence["items"].([]any)
	for _, raw := range items {
		ev, ok := raw.(map[string]any)
		if !ok || str(ev["scenario"]) != scenario {
			continue
		}
		status := str(ev["status"])
		if status != "captured" && status != "validated" {
			continue
		}
		add(str(ev["kind"]), str(ev["path"]))
	}

	var files []EvidenceFile
	var bodyRefs []string
	usedNames := map[string]bool{}
	for _, r := range ordered {
		src := resolveEvidencePath(runDir, repoRoot, r.path)
		if src == "" {
			bodyRefs = append(bodyRefs, r.path)
			continue
		}
		name := filepath.Base(src)
		// Keep release-asset names unique within the filing.
		for usedNames[name] {
			name = "x-" + name
		}
		usedNames[name] = true
		files = append(files, EvidenceFile{
			Name:       name,
			Path:       r.path,
			SourcePath: src,
			Image:      isImagePath(src),
			Label:      r.label,
		})
	}
	return files, bodyRefs
}

// resolveEvidencePath maps an evidence ref (possibly a cassette:// URI or a
// bundle-relative path) to a readable local file, mirroring the runner's
// artifact_ref_exists resolution roots: run dir, run dir/cassettes, repo root.
// Returns "" when the ref does not resolve to a local file.
func resolveEvidencePath(runDir, repoRoot, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, "://") || strings.HasPrefix(ref, "retained:") ||
		strings.HasPrefix(ref, "image:") || strings.HasPrefix(ref, "trace:") ||
		strings.HasPrefix(ref, "mcp:") {
		// cassette:// URIs are local recordings addressed by trailing path.
		if strings.HasPrefix(ref, "cassette://") {
			parts := strings.SplitN(strings.TrimPrefix(ref, "cassette://"), "/", 2)
			if len(parts) == 2 {
				return resolveEvidencePath(runDir, repoRoot, parts[1])
			}
		}
		return ""
	}
	if filepath.IsAbs(ref) {
		if regularFileExists(ref) {
			return ref
		}
		return ""
	}
	for _, base := range []string{runDir, filepath.Join(runDir, "cassettes"), repoRoot} {
		p := filepath.Join(base, ref)
		if regularFileExists(p) {
			return p
		}
	}
	return ""
}

// scenarioSuccessCriteria pulls the driver-plan success criteria for a scenario.
func scenarioSuccessCriteria(driverPlan map[string]any, scenario string) []string {
	rows, _ := driverPlan["scenarios"].([]any)
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || str(row["scenario"]) != scenario {
			continue
		}
		return strList(row["success_criteria"])
	}
	return nil
}

// scenarioTaskPrompt prefers the driver-plan task prompt, then run.json's.
func scenarioTaskPrompt(runJSON, driverPlan map[string]any, scenario string) string {
	rows, _ := driverPlan["scenarios"].([]any)
	for _, raw := range rows {
		if row, ok := raw.(map[string]any); ok && str(row["scenario"]) == scenario {
			if t := strings.TrimSpace(str(row["task_prompt"])); t != "" {
				return t
			}
		}
	}
	scens, _ := runJSON["scenarios"].([]any)
	for _, raw := range scens {
		if row, ok := raw.(map[string]any); ok && str(row["id"]) == scenario {
			if t := strings.TrimSpace(str(row["task_prompt"])); t != "" {
				return t
			}
			return strings.TrimSpace(str(row["task"]))
		}
	}
	return ""
}

// journalEventsForScenario returns the driver-journal events for a scenario in
// recorded order.
func journalEventsForScenario(journal map[string]any, scenario string) []map[string]any {
	items, _ := journal["items"].([]any)
	var out []map[string]any
	for _, raw := range items {
		if ev, ok := raw.(map[string]any); ok && str(ev["scenario"]) == scenario {
			out = append(out, ev)
		}
	}
	return out
}

// runProjectPersona extracts the project and persona ids from run.json.
func runProjectPersona(runJSON map[string]any) (string, string) {
	proj, _ := runJSON["project"].(map[string]any)
	pers, _ := runJSON["persona"].(map[string]any)
	return str(proj["id"]), str(pers["id"])
}

// displayPath renders a path relative to the repo root when possible (stable
// across machines in issue bodies).
func displayPath(p, repoRoot string) string {
	if repoRoot != "" {
		if rel, err := filepath.Rel(repoRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

// --- tiny JSON/map helpers (bundle files are runner-owned free-form JSON) ---

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return m, nil
}

// writeJSONMap matches the runner's write_json format (2-space indent, sorted
// keys — Go sorts map keys natively — trailing newline) so mixed Go/Python
// writers do not churn the bundle diff.
func writeJSONMap(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strList(v any) []string {
	raw, _ := v.([]any)
	var out []string
	for _, r := range raw {
		if s, ok := r.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// isImagePath reports whether the evidence file renders inline in an issue
// body (screenshot/image extensions).
func isImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// regularFileExists reports whether p is an existing regular file (evidence
// refs must never resolve to directories).
func regularFileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
