package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent/githubapp"
)

var errNoGitHubAppProfile = errors.New("no GitHub App profile found")

func newGHAgentTokenCmd() *cobra.Command {
	var (
		appID          int64
		installationID int64
		keyFile        string
		envFile        string
		outPath        string
		fromEnv        bool
		printToken     bool
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint or record a GitHub token for local kitsoki GitHub actions",
		Long: `Mint a short-lived GitHub App installation token and write a local env file
for kitsoki commands that need GH_TOKEN/GITHUB_TOKEN. The App path is preferred:
permissions are limited by the App installation to the kitsoki floor
(metadata read; issues, pull requests, and contents write; checks read) and to
repositories selected during App installation.

If an operator cannot use a GitHub App yet, --from-env records an explicitly
provided GH_TOKEN or GITHUB_TOKEN instead. For that fallback, use a fine-grained
PAT scoped only to the target repositories with the same repository
permissions.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if outPath == "" {
				outPath = defaultGitHubTokenEnvPath()
			}

			tokenSource := "GitHub App installation"
			var (
				token  string
				expiry time.Time
				err    error
			)
			if fromEnv {
				token, tokenSource, err = operatorProvidedGitHubToken()
				if err != nil {
					return err
				}
			} else {
				token, expiry, tokenSource, err = mintLocalGitHubAppToken(ctx, appID, installationID, keyFile, envFile)
				if err != nil {
					if errors.Is(err, errNoGitHubAppProfile) {
						envToken, envSource, envErr := operatorProvidedGitHubToken()
						if envErr != nil {
							return err
						}
						token = envToken
						tokenSource = envSource
						fmt.Fprintf(out, "warning: no complete GitHub App profile was available; recording operator-provided %s instead.\n", envSource)
					} else {
						return err
					}
				}
			}

			if printToken {
				fmt.Fprint(out, shellTokenEnv(token))
				return nil
			}
			if err := writeGitHubTokenEnvFile(outPath, token, tokenSource, expiry); err != nil {
				return err
			}
			fmt.Fprintf(out, "wrote GitHub auth env: %s\n", outPath)
			fmt.Fprintf(out, "token source: %s\n", tokenSource)
			if !expiry.IsZero() {
				fmt.Fprintf(out, "expires: %s\n", expiry.Format(time.RFC3339))
			}
			fmt.Fprintln(out, "permissions: repository metadata read; issues, pull requests, and contents write; checks read; limited to selected repositories")
			fmt.Fprintln(out, "user does: create/install the App or provide the PAT, choose repositories, and approve any GitHub consent screen")
			fmt.Fprintln(out, "kitsoki does: mint or record the token, write the local 0600 env file, and use it only for requested GitHub issue/PR/push operations")
			fmt.Fprintf(out, "next: source %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides "+githubapp.EnvAppID+")")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides "+githubapp.EnvInstallationID+")")
	cmd.Flags().StringVar(&keyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides "+githubapp.EnvPrivateKeyFile+")")
	cmd.Flags().StringVar(&envFile, "env-file", "", "profile env file (default $"+githubapp.EnvAppEnvFile+", else the store's default profile)")
	cmd.Flags().StringVar(&outPath, "out", "", "env file to write (default <credential store>/github.env)")
	cmd.Flags().BoolVar(&fromEnv, "from-env", false, "record an operator-provided GH_TOKEN/GITHUB_TOKEN instead of minting an App token")
	cmd.Flags().BoolVar(&printToken, "print", false, "print shell exports to stdout instead of writing a file; this exposes the token")
	return cmd
}

func mintLocalGitHubAppToken(ctx context.Context, appID, installationID int64, keyFile, envFile string) (string, time.Time, string, error) {
	resolved, err := githubapp.ResolveAppConfig(appID, installationID, keyFile, envFile)
	if err != nil {
		return "", time.Time{}, "", err
	}
	if resolved == nil || resolved.Config == nil {
		return "", time.Time{}, "", fmt.Errorf("gh-agent token: %w; run `kitsoki gh-agent setup app`, pass --env-file, or set GH_TOKEN/GITHUB_TOKEN and rerun with --from-env", errNoGitHubAppProfile)
	}
	src, err := newGitHubAppTokenSource(resolved.Config, nil)
	if err != nil {
		return "", time.Time{}, "", err
	}
	token, expiry, err := src.InstallationToken(ctx)
	if err != nil {
		return "", time.Time{}, "", err
	}
	return token, expiry, "GitHub App installation (" + resolved.Source + ")", nil
}

func operatorProvidedGitHubToken() (token, source string, err error) {
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(key)); tok != "" {
			return tok, "operator-provided " + key, nil
		}
	}
	return "", "", fmt.Errorf("gh-agent token: --from-env requires GH_TOKEN or GITHUB_TOKEN; use a fine-grained PAT scoped to the selected repos with Issues/Pull requests/Contents write, Checks read, and Metadata read")
}

func defaultGitHubTokenEnvPath() string {
	return filepath.Join(githubapp.CredentialsDir(), "github.env")
}

func writeGitHubTokenEnvFile(path, token, source string, expiry time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("gh-agent token: create env dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "# GitHub auth for local kitsoki commands.")
	fmt.Fprintf(&b, "# Written by kitsoki gh-agent token from %s.\n", source)
	if !expiry.IsZero() {
		fmt.Fprintf(&b, "# Expires: %s\n", expiry.Format(time.RFC3339))
	}
	fmt.Fprint(&b, shellTokenEnv(token))
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("gh-agent token: write env file: %w", err)
	}
	return nil
}

func shellTokenEnv(token string) string {
	quoted := shellSingleQuote(token)
	return "export GH_TOKEN=" + quoted + "\nexport GITHUB_TOKEN=\"$GH_TOKEN\"\n"
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
