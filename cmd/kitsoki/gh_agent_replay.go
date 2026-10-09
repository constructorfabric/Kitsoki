package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent/githubapp"
	"kitsoki/internal/host"
)

// newGHAgentReplayCmd builds `kitsoki gh-agent replay`: reconstruct a GitHub
// `issues` (or `pull_request`) webhook for an existing issue/PR and re-deliver it
// — correctly HMAC-signed — to a running agent's /gh-agent/webhook endpoint.
//
// This is the supported way to re-fire the agent for an issue: after deploying
// new agent code, when a delivery was missed, or to regenerate an artifact (e.g.
// the bug-report deck) without waiting for a fresh GitHub event. By default it
// fetches the issue's real title/body/labels via `gh` so the replayed payload
// matches what GitHub would send; flags override.
func newGHAgentReplayCmd() *cobra.Command {
	var (
		url     string
		secret  string
		repo    string
		issue   int
		action  string
		event   string
		title   string
		body    string
		labels  []string
		dryRun  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Re-deliver a signed issues/PR webhook for an existing issue to a running agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			ctx := cmd.Context()

			if strings.TrimSpace(repo) == "" || issue <= 0 {
				return fmt.Errorf("gh-agent replay: --repo and --issue are required")
			}
			if strings.TrimSpace(secret) == "" {
				secret = os.Getenv(githubapp.EnvWebhookSecret)
			}
			if strings.TrimSpace(secret) == "" {
				return fmt.Errorf("gh-agent replay: --secret or $%s is required to sign the delivery", githubapp.EnvWebhookSecret)
			}
			if strings.TrimSpace(event) == "" {
				event = "issues"
			}

			// Fill missing fields from the live issue so the replay is faithful.
			if strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "" || labels == nil {
				if t, b, ls, err := fetchIssueForReplay(ctx, repo, issue); err == nil {
					if strings.TrimSpace(title) == "" {
						title = t
					}
					if strings.TrimSpace(body) == "" {
						body = b
					}
					if labels == nil {
						labels = ls
					}
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "gh-agent replay: could not fetch issue %s#%d (%v); replaying with provided fields only\n", repo, issue, err)
				}
			}

			payload := buildIssuesWebhookPayload(repo, issue, action, title, body, labels)
			raw, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("marshal payload: %w", err)
			}

			if dryRun || strings.TrimSpace(url) == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "X-GitHub-Event: %s\nX-Hub-Signature-256: sha256=%s\n\n%s\n",
					event, signWebhook(secret, raw), string(raw))
				if strings.TrimSpace(url) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "gh-agent replay: no --url given; printed the signed payload (dry run)")
				}
				return nil
			}

			status, respBody, err := postWebhook(ctx, url, event, signWebhook(secret, raw), raw, timeout)
			if err != nil {
				return fmt.Errorf("deliver webhook: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d %s\n", status, strings.TrimSpace(respBody))
			if status < 200 || status >= 300 {
				return fmt.Errorf("agent returned %d", status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "agent webhook endpoint, e.g. https://kitsoki-test.slothattax.me/gh-agent/webhook (omit for a signed-payload dry run)")
	cmd.Flags().StringVar(&secret, "secret", "", "webhook secret (defaults to $"+githubapp.EnvWebhookSecret+")")
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo")
	cmd.Flags().IntVar(&issue, "issue", 0, "issue (or PR) number")
	cmd.Flags().StringVar(&action, "action", "opened", "webhook action: opened|reopened|labeled|edited|…")
	cmd.Flags().StringVar(&event, "event", "issues", "X-GitHub-Event type")
	cmd.Flags().StringVar(&title, "title", "", "override issue title (default: fetched via host.gh.ticket)")
	cmd.Flags().StringVar(&body, "body", "", "override issue body (default: fetched via host.gh.ticket)")
	cmd.Flags().StringSliceVar(&labels, "label", nil, "override issue labels (repeatable; default: fetched via host.gh.ticket)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the signed payload instead of delivering")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "HTTP timeout")
	return cmd
}

// buildIssuesWebhookPayload assembles a minimal-but-faithful GitHub issues
// webhook payload (the subset the agent parses).
func buildIssuesWebhookPayload(repo string, number int, action, title, body string, labels []string) map[string]any {
	labelObjs := make([]map[string]any, 0, len(labels))
	for _, l := range labels {
		labelObjs = append(labelObjs, map[string]any{"name": l})
	}
	return map[string]any{
		"action":     action,
		"repository": map[string]any{"full_name": repo},
		"issue": map[string]any{
			"number":   number,
			"title":    title,
			"html_url": fmt.Sprintf("https://github.com/%s/issues/%d", repo, number),
			"body":     body,
			"labels":   labelObjs,
		},
	}
}

// signWebhook returns the hex HMAC-SHA256 of body under secret (the value GitHub
// puts after "sha256=" in X-Hub-Signature-256).
func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// postWebhook delivers the signed payload, returning the status + response body.
func postWebhook(ctx context.Context, url, event, sig string, body []byte, timeout time.Duration) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-Hub-Signature-256", "sha256="+sig)
	req.Header.Set("User-Agent", "kitsoki-gh-agent-replay")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, string(b), nil
}

// fetchIssueForReplay reads an issue's title/body/labels via host.gh.ticket so
// the replay matches the real issue.
func fetchIssueForReplay(ctx context.Context, repo string, number int) (title, body string, labels []string, err error) {
	res, err := host.GitHubTicketHandler(ctx, map[string]any{
		"op":   "get",
		"repo": repo,
		"id":   strconv.Itoa(number),
	})
	if err != nil {
		return "", "", nil, err
	}
	if strings.TrimSpace(res.Error) != "" {
		return "", "", nil, fmt.Errorf("%s", res.Error)
	}
	title, _ = res.Data["title"].(string)
	body, _ = res.Data["body"].(string)
	if raw, ok := res.Data["labels"].([]string); ok {
		labels = raw
	} else if rawAny, ok := res.Data["labels"].([]any); ok {
		for _, l := range rawAny {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			}
		}
	}
	if labels == nil {
		labels = []string{}
	}
	return title, body, labels, nil
}
