package host

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// GitHubPRViewOptions controls a native GitHub pull-request read.
type GitHubPRViewOptions struct {
	Repo        string
	Number      int
	IncludeDiff bool
}

// GitHubPRViewData is the PR payload exposed to Studio's gh.pr_view tool.
type GitHubPRViewData struct {
	Number int                `json:"number"`
	Title  string             `json:"title"`
	State  string             `json:"state"`
	URL    string             `json:"url"`
	Body   string             `json:"body"`
	Files  []GitHubPRViewFile `json:"files"`
}

// GitHubPRViewFile is one changed file in a pull request.
type GitHubPRViewFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// GitHubPRViewResult is returned by GitHubPRView.
type GitHubPRViewResult struct {
	PR   GitHubPRViewData `json:"pr"`
	Diff string           `json:"diff,omitempty"`
}

// GitHubPRView reads a pull request, its changed file list, and optionally its
// unified diff via the native GitHub REST API.
func GitHubPRView(ctx context.Context, opts GitHubPRViewOptions) (GitHubPRViewResult, error) {
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view: repo is required")
	}
	if opts.Number <= 0 {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view: number is required")
	}
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	path := fmt.Sprintf("repos/%s/pulls/%d", repo, opts.Number)
	code, resp, err := githubAPIJSON(ctx, http.MethodGet, path, nil, &raw)
	if err != nil {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view: %w", err)
	}
	if code >= 300 {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view: %s", githubAPIError(resp))
	}
	var rawFiles []struct {
		Filename  string `json:"filename"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	code, resp, err = githubAPIJSON(ctx, http.MethodGet, path+"/files?per_page=100", nil, &rawFiles)
	if err != nil {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view files: %w", err)
	}
	if code >= 300 {
		return GitHubPRViewResult{}, fmt.Errorf("github pr view files: %s", githubAPIError(resp))
	}
	files := make([]GitHubPRViewFile, 0, len(rawFiles))
	for _, f := range rawFiles {
		files = append(files, GitHubPRViewFile{
			Path:      f.Filename,
			Additions: f.Additions,
			Deletions: f.Deletions,
		})
	}
	out := GitHubPRViewResult{PR: GitHubPRViewData{
		Number: raw.Number,
		Title:  raw.Title,
		State:  raw.State,
		URL:    raw.HTMLURL,
		Body:   raw.Body,
		Files:  files,
	}}
	if opts.IncludeDiff {
		code, resp, err = githubAPIRequestAccept(ctx, http.MethodGet, path, "application/vnd.github.v3.diff", "", nil, nil)
		if err != nil {
			return GitHubPRViewResult{}, fmt.Errorf("github pr view diff: %w", err)
		}
		if code >= 300 {
			return GitHubPRViewResult{}, fmt.Errorf("github pr view diff: %s", githubAPIError(resp))
		}
		out.Diff = resp
	}
	return out, nil
}
