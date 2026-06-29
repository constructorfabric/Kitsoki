package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/githubapp"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

// newGHAgentCmd builds `kitsoki gh-agent`, whose single `poll` subcommand runs
// ONE poll cycle of the @kitsoki mention -> dispatch -> run -> ack loop:
// ListGitHubInboxItems (through the cliExec seam) -> FilterMentions -> for each
// mention, Dispatcher.Dispatch. Single-shot; the serve daemon is deferred.
func newGHAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gh-agent",
		Short: "Drive the @kitsoki GitHub mention -> dispatch -> run -> ack loop",
	}
	cmd.AddCommand(newGHAgentPollCmd())
	cmd.AddCommand(newGHAgentServeCmd())
	return cmd
}

func newGHAgentPollCmd() *cobra.Command {
	var (
		repo           string
		mentionFile    string
		dbPath         string
		trigger        string
		worker         string
		publicBaseURL  string
		useGitHubApp   bool
		appID          int64
		installationID int64
		appKeyFile     string
	)
	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Run one poll cycle: list mentions, dispatch the mapped story, ack",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// Opt-in GitHub App auth: when --github-app is set (or the
			// KITSOKI_GH_APP_* env is fully present), mint an installation
			// token and export it as GH_TOKEN so every `gh` subprocess spawned
			// under the dispatch (host.cliExec) authenticates as the App. When
			// not configured, today's offline/mention-file path is unchanged.
			restoreGHToken, err := setupGitHubAppAuth(ctx, useGitHubApp, appID, installationID, appKeyFile)
			if err != nil {
				return err
			}
			defer restoreGHToken()

			items, err := pollInboxItems(ctx, repo, mentionFile)
			if err != nil {
				return err
			}
			mentions := ghagent.FilterMentions(items, repo, trigger)

			if dbPath == "" {
				dbPath = ":memory:"
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				return fmt.Errorf("gh-agent: open db %q: %w", dbPath, err)
			}
			defer db.Close()

			store, err := jobs.NewGHJobStore(db)
			if err != nil {
				return err
			}

			d := &ghagent.Dispatcher{
				Jobs:          store,
				Routes:        ghagent.DefaultLabelStoryMap(),
				Comments:      &ghagent.CommentStore{Exec: host.GitHubTicketHandler, Repo: repo},
				WorkerID:      worker,
				PublicBaseURL: publicBaseURL,
				SpawnFn:       ghagent.RunStorySession,
			}

			for _, m := range mentions {
				job, err := d.Dispatch(ctx, m, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "gh-agent: dispatch %s: %v\n", m.OriginRef, err)
					continue
				}
				fmt.Printf("%s -> %s (state=%s)\n", job.OriginRef, job.Story, job.State)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo slug to poll")
	cmd.Flags().StringVar(&mentionFile, "mention", "", "JSON file with []host.GitHubInboxItem (bypasses the live gh list)")
	cmd.Flags().StringVar(&dbPath, "db", "", "sqlite path for the gh_jobs store (default in-memory)")
	cmd.Flags().StringVar(&trigger, "trigger", ghagent.DefaultMentionTrigger, "mention trigger literal")
	cmd.Flags().StringVar(&worker, "worker", "gh-agent-1", "worker id holding the claim")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "public URL base used in ack run links, e.g. https://kitsoki-test.slothattax.me")
	cmd.Flags().BoolVar(&useGitHubApp, "github-app", false, "authenticate as a GitHub App installation (mints GH_TOKEN); off keeps the offline path")
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides KITSOKI_GH_APP_ID)")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides KITSOKI_GH_APP_INSTALLATION_ID)")
	cmd.Flags().StringVar(&appKeyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides KITSOKI_GH_APP_PRIVATE_KEY_FILE)")
	return cmd
}

// setupGitHubAppAuth optionally mints a GitHub App installation token and
// exports it as GH_TOKEN for the gh subprocesses spawned during dispatch. It
// returns a restore func (always non-nil) that resets GH_TOKEN.
//
// Auth engages when --github-app is set, any --gh-app-* flag is provided, or
// the KITSOKI_GH_APP_* env config is fully present. Flags override env. When
// nothing is configured it is a no-op so the existing offline poll path and
// its tests stay unchanged.
func setupGitHubAppAuth(ctx context.Context, force bool, appID, installationID int64, keyFile string) (func(), error) {
	noop := func() {}

	cfg, err := githubapp.LoadConfigFromEnv()
	if err != nil {
		return noop, err
	}
	flagsGiven := appID != 0 || installationID != 0 || keyFile != ""
	if cfg == nil && !force && !flagsGiven {
		return noop, nil // offline path unchanged
	}
	if cfg == nil {
		cfg = &githubapp.Config{}
	}
	if appID != 0 {
		cfg.AppID = appID
	}
	if installationID != 0 {
		cfg.InstallationID = installationID
	}
	if keyFile != "" {
		cfg.PrivateKeyPath = keyFile
	}
	if err := cfg.Validate(); err != nil {
		return noop, fmt.Errorf("gh-agent: --github-app requires app id, installation id, and key file: %w", err)
	}

	src, err := githubapp.NewAppTokenSource(cfg, nil)
	if err != nil {
		return noop, err
	}
	token, _, err := src.InstallationToken(ctx)
	if err != nil {
		return noop, fmt.Errorf("gh-agent: mint installation token: %w", err)
	}

	prev, had := os.LookupEnv("GH_TOKEN")
	if err := os.Setenv("GH_TOKEN", token); err != nil {
		return noop, fmt.Errorf("gh-agent: set GH_TOKEN: %w", err)
	}
	return func() {
		if had {
			_ = os.Setenv("GH_TOKEN", prev)
		} else {
			_ = os.Unsetenv("GH_TOKEN")
		}
	}, nil
}

// pollInboxItems reads the inbox: from a JSON fixture when --mention is set,
// otherwise via ListGitHubInboxItems (which shells gh through the cliExec seam).
func pollInboxItems(ctx context.Context, repo, mentionFile string) ([]host.GitHubInboxItem, error) {
	if mentionFile != "" {
		raw, err := os.ReadFile(mentionFile)
		if err != nil {
			return nil, fmt.Errorf("gh-agent: read mention file: %w", err)
		}
		var items []host.GitHubInboxItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("gh-agent: parse mention file: %w", err)
		}
		return items, nil
	}
	return host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo:          repo,
		IncludeIssues: true,
		IncludePRs:    true,
	})
}
