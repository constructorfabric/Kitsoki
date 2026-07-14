package starlark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

var githubProbeAPI = struct {
	sync.Mutex
	base   string
	client *http.Client
}{
	base:   "https://api.github.com",
	client: http.DefaultClient,
}

// SetGitHubProbeAPIForTest redirects native GitHub probes to a fake API server.
func SetGitHubProbeAPIForTest(base string, client *http.Client) func() {
	githubProbeAPI.Lock()
	oldBase := githubProbeAPI.base
	oldClient := githubProbeAPI.client
	if client == nil {
		client = http.DefaultClient
	}
	githubProbeAPI.base = strings.TrimRight(base, "/")
	githubProbeAPI.client = client
	githubProbeAPI.Unlock()
	return func() {
		githubProbeAPI.Lock()
		githubProbeAPI.base = oldBase
		githubProbeAPI.client = oldClient
		githubProbeAPI.Unlock()
	}
}

func probeGitHubIssueList(ctx context.Context, args []string) (ProbeResult, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: expected one owner/repo argument", "gh.issue.list")
	}
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return ProbeResult{Exit: 1, Out: "GH_TOKEN or GITHUB_TOKEN is required for native GitHub issue probes"}, nil
	}

	githubProbeAPI.Lock()
	base := githubProbeAPI.base
	client := githubProbeAPI.client
	githubProbeAPI.Unlock()
	if client == nil {
		client = http.DefaultClient
	}

	q := "repo:" + strings.TrimSpace(args[0]) + " is:issue"
	endpoint := strings.TrimRight(base, "/") + "/search/issues?q=" + url.QueryEscape(q) + "&per_page=200"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: %w", "gh.issue.list", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: %w", "gh.issue.list", readErr)
	}
	if resp.StatusCode >= 300 {
		return ProbeResult{Exit: 1, Out: strings.TrimSpace(string(body))}, nil
	}

	var raw struct {
		Items []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			State  string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ProbeResult{}, fmt.Errorf("starlark probe %q: parse GitHub response: %w", "gh.issue.list", err)
	}
	rows := make([]map[string]any, 0, len(raw.Items))
	for _, item := range raw.Items {
		rows = append(rows, map[string]any{
			"number": item.Number,
			"title":  item.Title,
			"state":  item.State,
		})
	}
	out, err := json.Marshal(rows)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Exit: 0, Out: string(out)}, nil
}
