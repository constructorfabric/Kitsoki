package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitHubInboxItem is a GitHub object that should surface as operator work.
type GitHubInboxItem struct {
	Kind   string // issue | pr
	Number string
	Title  string
	Author string
	URL    string
}

// GitHubInboxOptions controls GitHub issue/PR inbox discovery.
type GitHubInboxOptions struct {
	Repo            string
	IncludeIssues   bool
	IncludePRs      bool
	Assignee        string
	ReviewRequested string
	Limit           int
}

// ListGitHubInboxItems returns GitHub issues assigned to the operator and PRs
// awaiting their review. It shells through gh using the shared cliExec seam so
// callers can test it without network access or a real gh binary.
func ListGitHubInboxItems(ctx context.Context, opts GitHubInboxOptions) ([]GitHubInboxItem, error) {
	if !ghAvailable(ctx) {
		return nil, fmt.Errorf("gh CLI not available — install github.com/cli/cli and run `gh auth login`")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	var out []GitHubInboxItem
	if opts.IncludeIssues {
		issues, err := listGitHubAssignedIssues(ctx, opts.Repo, opts.Assignee, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, issues...)
	}
	if opts.IncludePRs {
		prs, err := listGitHubReviewRequests(ctx, opts.Repo, opts.ReviewRequested, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, prs...)
	}
	return out, nil
}

func listGitHubAssignedIssues(ctx context.Context, repo, assignee string, limit int) ([]GitHubInboxItem, error) {
	if strings.TrimSpace(assignee) == "" {
		assignee = "@me"
	}
	args := []string{"issue", "list"}
	if repo = strings.TrimSpace(repo); repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args,
		"--state", "open",
		"--assignee", assignee,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,assignees,url",
	)
	stdout, stderr, code, err := cliExec(ctx, "", "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("github inbox issues: exec: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("github inbox issues: %s", strings.TrimSpace(stderr))
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("github inbox issues: parse JSON: %w", err)
	}
	items := make([]GitHubInboxItem, 0, len(raw))
	for _, r := range raw {
		num, title, url := githubNumberTitleURL(r)
		items = append(items, GitHubInboxItem{
			Kind:   "issue",
			Number: num,
			Title:  title,
			Author: firstLogin(r["assignees"]),
			URL:    url,
		})
	}
	return items, nil
}

func listGitHubReviewRequests(ctx context.Context, repo, reviewer string, limit int) ([]GitHubInboxItem, error) {
	if strings.TrimSpace(reviewer) == "" {
		reviewer = "@me"
	}
	args := []string{"pr", "list"}
	if repo = strings.TrimSpace(repo); repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args,
		"--state", "open",
		"--search", "review-requested:"+reviewer,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,url",
	)
	stdout, stderr, code, err := cliExec(ctx, "", "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("github inbox prs: exec: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("github inbox prs: %s", strings.TrimSpace(stderr))
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("github inbox prs: parse JSON: %w", err)
	}
	items := make([]GitHubInboxItem, 0, len(raw))
	for _, r := range raw {
		num, title, url := githubNumberTitleURL(r)
		items = append(items, GitHubInboxItem{
			Kind:   "pr",
			Number: num,
			Title:  title,
			Author: loginFromMap(r["author"]),
			URL:    url,
		})
	}
	return items, nil
}

func githubNumberTitleURL(raw map[string]any) (string, string, string) {
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
	url, _ := raw["url"].(string)
	return num, title, url
}

func firstLogin(v any) string {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	return loginFromMap(list[0])
}

func loginFromMap(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	login, _ := m["login"].(string)
	return login
}
