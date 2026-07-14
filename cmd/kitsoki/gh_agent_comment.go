package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/host"
)

// newGHAgentCommentCmd builds `kitsoki gh-agent comment`: post a comment on an
// issue AS THE BOT (the GitHub App installation), by minting the App token via
// the same --github-app path the agent uses, then issuing host.gh.ticket
// op=comment under it. Use it to post bot-authored messages without the full
// dispatch/serve loop — e.g. the bug-report deck link when the deck was rendered
// off-box (the agent VM is too small to render) and only the comment must come
// from the app account.
func newGHAgentCommentCmd() *cobra.Command {
	var (
		repo           string
		issue          int
		body           string
		bodyFile       string
		deckURL        string
		useGitHubApp   bool
		appID          int64
		installationID int64
		appKeyFile     string
	)
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Post a comment on an issue as the bot (GitHub App installation)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			ctx := cmd.Context()

			if strings.TrimSpace(repo) == "" || issue <= 0 {
				return fmt.Errorf("gh-agent comment: --repo and --issue are required")
			}
			text, err := resolveCommentBody(body, bodyFile, deckURL)
			if err != nil {
				return err
			}

			// Mint the App installation token so the comment is authored by the bot.
			ctx, restore, err := setupGitHubAppAuth(ctx, useGitHubApp, appID, installationID, appKeyFile)
			if err != nil {
				return err
			}
			defer restore()

			res, err := host.GitHubTicketHandler(ctx, map[string]any{
				"op":   "comment",
				"repo": repo,
				"id":   strconv.Itoa(issue),
				"body": text,
			})
			if err != nil {
				return fmt.Errorf("post comment: %w", err)
			}
			if strings.TrimSpace(res.Error) != "" {
				return fmt.Errorf("post comment: %s", res.Error)
			}
			if url, _ := res.Data["url"].(string); strings.TrimSpace(url) != "" {
				fmt.Fprintln(cmd.OutOrStdout(), url)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "comment posted")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo")
	cmd.Flags().IntVar(&issue, "issue", 0, "issue number")
	cmd.Flags().StringVar(&body, "body", "", "comment body (markdown)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the comment body from this file ('-' for stdin)")
	cmd.Flags().StringVar(&deckURL, "deck-url", "", "shortcut: post the standard no-LLM bug-report deck comment linking this hosted deck URL")
	cmd.Flags().BoolVar(&useGitHubApp, "github-app", false, "authenticate as the GitHub App installation (required for a bot-authored comment)")
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides KITSOKI_GH_APP_ID)")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides KITSOKI_GH_APP_INSTALLATION_ID)")
	cmd.Flags().StringVar(&appKeyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides KITSOKI_GH_APP_PRIVATE_KEY_FILE)")
	return cmd
}

// resolveCommentBody picks the comment text: --deck-url renders the standard
// no-LLM deck comment; otherwise --body, then --body-file ('-' = stdin).
func resolveCommentBody(body, bodyFile, deckURL string) (string, error) {
	if u := strings.TrimSpace(deckURL); u != "" {
		return ghagent.DeckComment(u), nil
	}
	if strings.TrimSpace(body) != "" {
		return body, nil
	}
	if f := strings.TrimSpace(bodyFile); f != "" {
		if f == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			return string(b), nil
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("read --body-file: %w", err)
		}
		return string(b), nil
	}
	return "", fmt.Errorf("gh-agent comment: one of --body, --body-file, or --deck-url is required")
}
