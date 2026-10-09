package host

import (
	"context"
	"fmt"
	"net/url"
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
// awaiting their review through the native GitHub Search API.
func ListGitHubInboxItems(ctx context.Context, opts GitHubInboxOptions) ([]GitHubInboxItem, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	repo, err := resolveGitHubInboxRepo(ctx, opts.Repo)
	if err != nil {
		return nil, err
	}
	var out []GitHubInboxItem
	if opts.IncludeIssues {
		issues, err := listGitHubAssignedIssues(ctx, repo, opts.Assignee, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, issues...)
	}
	if opts.IncludePRs {
		prs, err := listGitHubReviewRequests(ctx, repo, opts.ReviewRequested, limit)
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
	q := githubIssueSearchQuery(repo, "is:issue", "is:open", "assignee:"+assignee)
	var raw githubIssueSearchResponse
	code, resp, err := githubAPIJSON(ctx, "GET", "search/issues?q="+url.QueryEscape(q)+"&per_page="+fmt.Sprintf("%d", limit), nil, &raw)
	if err != nil {
		return nil, fmt.Errorf("github inbox issues: %w", err)
	}
	if code >= 300 {
		return nil, fmt.Errorf("github inbox issues: %s", githubAPIError(resp))
	}
	items := make([]GitHubInboxItem, 0, len(raw.Items))
	for _, r := range raw.Items {
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
	q := githubIssueSearchQuery(repo, "is:pr", "is:open", "review-requested:"+reviewer)
	var raw githubIssueSearchResponse
	code, resp, err := githubAPIJSON(ctx, "GET", "search/issues?q="+url.QueryEscape(q)+"&per_page="+fmt.Sprintf("%d", limit), nil, &raw)
	if err != nil {
		return nil, fmt.Errorf("github inbox prs: %w", err)
	}
	if code >= 300 {
		return nil, fmt.Errorf("github inbox prs: %s", githubAPIError(resp))
	}
	items := make([]GitHubInboxItem, 0, len(raw.Items))
	for _, r := range raw.Items {
		num, title, url := githubNumberTitleURL(r)
		items = append(items, GitHubInboxItem{
			Kind:   "pr",
			Number: num,
			Title:  title,
			Author: loginFromMap(r["user"]),
			URL:    url,
		})
	}
	return items, nil
}

func resolveGitHubInboxRepo(ctx context.Context, repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo != "" {
		return repo, nil
	}
	stdout, stderr, code, err := cliExec(ctx, "", "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("github inbox: repo is required and git remote lookup failed: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("github inbox: repo is required and git remote lookup failed: %s", strings.TrimSpace(stderr))
	}
	repo = githubRepoFromRemote(stdout)
	if repo == "" {
		return "", fmt.Errorf("github inbox: repo is required and origin is not a GitHub repository")
	}
	return repo, nil
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
	htmlURL, _ := raw["html_url"].(string)
	apiURL, _ := raw["url"].(string)
	if strings.TrimSpace(htmlURL) != "" {
		return num, title, htmlURL
	}
	return num, title, apiURL
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
