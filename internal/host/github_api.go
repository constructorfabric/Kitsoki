package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

var githubAPI = githubAPIClient{
	baseURL: "https://api.github.com",
	client:  http.DefaultClient,
}

// GitHubAuthSetupHint is the shared user-facing recovery path for every native
// GitHub write surface (CLI, TUI/web Report bug, MCP tools, and story hosts).
const GitHubAuthSetupHint = "GitHub auth is not configured (missing GH_TOKEN/GITHUB_TOKEN, and no `gh` CLI login was found). " +
	"Run `gh auth login` — kitsoki reuses the standard GitHub CLI token automatically — or " +
	"`kitsoki gh-agent login` for the easy GitHub CLI browser/PIN flow; " +
	"or for repo-limited GitHub App auth run `kitsoki gh-agent setup app --name <app-name> --local-only`, " +
	"`kitsoki gh-agent setup attach --repo <owner/repo>`, `kitsoki gh-agent token`, " +
	"then `source ~/.config/kitsoki/github.env`; or set a fine-grained PAT in " +
	"GH_TOKEN/GITHUB_TOKEN and run `kitsoki gh-agent token --from-env`."

// GitHubAuthStatus reports whether kitsoki has any configured credential for
// GitHub write APIs. It intentionally does not validate repository permissions:
// callers use it as a fast, local preflight to catch the guaranteed-fail case
// before a bug filing path reaches GitHub.
type GitHubAuthStatus struct {
	Configured bool
	SetupHint  string
}

// GitHubWriteAuthStatus is the shared preflight for UI surfaces that depend on
// GitHub writes. It checks the same credential chain githubAPIRequest uses:
// execution env, ambient secrets, process env, ~/.kitsoki/secrets.yaml, then
// the GitHub CLI token fallback.
func GitHubWriteAuthStatus(ctx context.Context) GitHubAuthStatus {
	if strings.TrimSpace(githubToken(ctx)) != "" {
		return GitHubAuthStatus{Configured: true}
	}
	return GitHubAuthStatus{Configured: false, SetupHint: GitHubAuthSetupHint}
}

type githubAPIClient struct {
	baseURL string
	client  *http.Client
}

func SetGitHubAPIForTest(baseURL string, client *http.Client) func() {
	prev := githubAPI
	githubAPI = githubAPIClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
	return func() { githubAPI = prev }
}

func githubToken(ctx context.Context) string {
	if env := CLIExecEnvFromCtx(ctx); len(env) > 0 {
		if tok := strings.TrimSpace(env["GH_TOKEN"]); tok != "" {
			return tok
		}
		if tok := strings.TrimSpace(env["GITHUB_TOKEN"]); tok != "" {
			return tok
		}
	}
	if secrets := SecretsFromContext(ctx); len(secrets) > 0 {
		if tok := strings.TrimSpace(secrets["GH_TOKEN"]); tok != "" {
			return tok
		}
		if tok := strings.TrimSpace(secrets["GITHUB_TOKEN"]); tok != "" {
			return tok
		}
	}
	if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		return tok
	}
	if secrets := LoadSecrets(); len(secrets) > 0 {
		if tok := strings.TrimSpace(secrets["GH_TOKEN"]); tok != "" {
			return tok
		}
		if tok := strings.TrimSpace(secrets["GITHUB_TOKEN"]); tok != "" {
			return tok
		}
	}
	return ghCLIToken(ctx)
}

// ghCLIToken asks the standard GitHub CLI for its stored token (`gh auth
// token`), so any surface that already works via `gh` (issues, PRs, gists)
// also authorizes kitsoki's native GitHub writes without a separate
// GH_TOKEN/GITHUB_TOKEN setup step. Env and secrets sources always win —
// this is the fallback of last resort. A missing binary, a redirected HOME
// with no gh config, or a logged-out CLI all resolve to "" so the existing
// missing-auth guidance still fires. Swappable for tests.
var ghCLIToken = func(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SetGHCLITokenForTest replaces the gh CLI token fallback and returns a
// restore func, letting tests pin the fallback on or off regardless of the
// host machine's gh login state.
func SetGHCLITokenForTest(fn func(context.Context) string) func() {
	prev := ghCLIToken
	ghCLIToken = fn
	return func() { ghCLIToken = prev }
}

func githubAPIJSON(ctx context.Context, method, path string, body any, out any) (int, string, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		r = bytes.NewReader(b)
	}
	return githubAPIRequest(ctx, method, path, "application/json", r, out)
}

func githubAPIRequest(ctx context.Context, method, path, contentType string, body io.Reader, out any) (int, string, error) {
	return githubAPIRequestAccept(ctx, method, path, "application/vnd.github+json", contentType, body, out)
}

func githubAPIRequestAccept(ctx context.Context, method, path, accept, contentType string, body io.Reader, out any) (int, string, error) {
	token := githubToken(ctx)
	if token == "" && githubMethodRequiresToken(method) {
		return 0, "", fmt.Errorf("%s", GitHubAuthSetupHint)
	}
	base := strings.TrimRight(githubAPI.baseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	client := githubAPI.client
	if client == nil {
		client = http.DefaultClient
	}
	target := path
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = base + "/" + strings.TrimLeft(path, "/")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, "", err
	}
	if strings.TrimSpace(accept) == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, string(data), err
		}
	}
	return resp.StatusCode, string(data), nil
}

func githubMethodRequiresToken(method string) bool {
	return strings.ToUpper(strings.TrimSpace(method)) != http.MethodGet
}

func githubIssueURL(repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, number)
}

func githubLabelPath(repo, label string) string {
	return fmt.Sprintf("repos/%s/labels/%s", strings.Trim(repo, "/"), url.PathEscape(label))
}
