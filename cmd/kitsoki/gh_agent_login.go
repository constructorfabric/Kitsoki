package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var errGitHubCLINotFound = errors.New("GitHub CLI gh not found")

var runGitHubCLIAuthLogin = func(ctx context.Context, hostname string, stdin io.Reader, stdout, stderr io.Writer) error {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("%w; install GitHub CLI for browser/PIN login, or use `kitsoki gh-agent setup app --name <app-name> --local-only`, or set GH_TOKEN/GITHUB_TOKEN and run `kitsoki gh-agent token --from-env`", errGitHubCLINotFound)
	}
	args := []string{"auth", "login", "--web", "--git-protocol", "https"}
	if strings.TrimSpace(hostname) != "" {
		args = append(args, "--hostname", strings.TrimSpace(hostname))
	}
	cmd := exec.CommandContext(ctx, ghPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh auth login: %w", err)
	}
	return nil
}

var readGitHubCLIToken = func(ctx context.Context, hostname string) (string, error) {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("%w", errGitHubCLINotFound)
	}
	args := []string{"auth", "token"}
	if strings.TrimSpace(hostname) != "" {
		args = append(args, "--hostname", strings.TrimSpace(hostname))
	}
	cmd := exec.CommandContext(ctx, ghPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("gh auth token: %w: %s", err, msg)
		}
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("gh auth token returned an empty token")
	}
	return token, nil
}

func newGHAgentLoginCmd() *cobra.Command {
	var (
		hostname string
		outPath  string
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in with GitHub CLI's browser/PIN flow and write Kitsoki's local env file",
		Long: `Log in with the GitHub CLI's browser/device flow and write the local
GH_TOKEN/GITHUB_TOKEN env file used by Kitsoki's GitHub issue, PR, artifact,
and agent operations.

This is the fastest local setup path. It reuses GitHub CLI auth, which is easy
but usually broader than a repo-limited GitHub App installation token. For the
tightest permissions, use:

  kitsoki gh-agent setup app --name <app-name> --local-only
  kitsoki gh-agent setup attach --repo owner/name
  kitsoki gh-agent token`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			if outPath == "" {
				outPath = defaultGitHubTokenEnvPath()
			}

			token, err := readGitHubCLIToken(ctx, hostname)
			if force || err != nil {
				if errors.Is(err, errGitHubCLINotFound) {
					return fmt.Errorf("gh-agent login: GitHub CLI `gh` is not installed; install GitHub CLI for browser/PIN login, or use `kitsoki gh-agent setup app --name <app-name> --local-only`, or set GH_TOKEN/GITHUB_TOKEN and run `kitsoki gh-agent token --from-env`")
				}
				if err != nil {
					fmt.Fprintf(out, "No usable GitHub CLI token found; starting GitHub login.\n")
				} else {
					fmt.Fprintf(out, "Refreshing GitHub CLI login.\n")
				}
				fmt.Fprintln(out, "user does: approve GitHub CLI auth in the browser, or enter the one-time code shown by GitHub CLI")
				fmt.Fprintln(out, "kitsoki does: run `gh auth login --web`, read `gh auth token`, and write the local 0600 env file")
				if err := runGitHubCLIAuthLogin(ctx, hostname, cmd.InOrStdin(), out, errOut); err != nil {
					return err
				}
				token, err = readGitHubCLIToken(ctx, hostname)
				if err != nil {
					return err
				}
			}

			source := "GitHub CLI OAuth token (`gh auth token`)"
			if strings.TrimSpace(hostname) != "" && strings.TrimSpace(hostname) != "github.com" {
				source += " for " + strings.TrimSpace(hostname)
			}
			if err := writeGitHubTokenEnvFile(outPath, token, source, time.Time{}); err != nil {
				return err
			}
			fmt.Fprintf(out, "wrote GitHub auth env: %s\n", outPath)
			fmt.Fprintf(out, "token source: %s\n", source)
			fmt.Fprintln(out, "permissions: GitHub CLI OAuth scopes; inspect with `gh auth status`. Use the GitHub App setup path for repo-limited least privilege.")
			fmt.Fprintln(out, "user does: authorize GitHub CLI and choose the account")
			fmt.Fprintln(out, "kitsoki does: copy the CLI token into GH_TOKEN/GITHUB_TOKEN for only the GitHub operations you request")
			fmt.Fprintf(out, "next: source %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&hostname, "hostname", "github.com", "GitHub hostname to authenticate with")
	cmd.Flags().StringVar(&outPath, "out", "", "env file to write (default <credential store>/github.env)")
	cmd.Flags().BoolVar(&force, "force", false, "run GitHub CLI login even when a token is already available")
	return cmd
}
