package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/githubapp"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

var newGitHubAppTokenSource = func(cfg *githubapp.Config, client githubapp.Doer) (githubapp.TokenSource, error) {
	return githubapp.NewAppTokenSource(cfg, client)
}

// newGHAgentCmd builds `kitsoki gh-agent`, whose single `poll` subcommand runs
// ONE poll cycle of the @kitsoki mention -> dispatch -> run -> ack loop:
// ListGitHubInboxItems (native GitHub Search API) -> FilterMentions -> for each
// mention, Dispatcher.Dispatch. Single-shot; the serve daemon is deferred.
func newGHAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gh-agent",
		Short: "Drive the @kitsoki GitHub mention -> dispatch -> run -> ack loop",
	}
	cmd.AddCommand(newGHAgentPollCmd())
	cmd.AddCommand(newGHAgentServeCmd())
	cmd.AddCommand(newGHAgentDeckCmd())
	cmd.AddCommand(newGHAgentReplayCmd())
	cmd.AddCommand(newGHAgentCommentCmd())
	cmd.AddCommand(newGHAgentSetupCmd())
	cmd.AddCommand(newGHAgentLoginCmd())
	cmd.AddCommand(newGHAgentTokenCmd())
	cmd.AddCommand(newGHAgentEnqueueCmd())
	cmd.AddCommand(newGHAgentDrainCmd())
	return cmd
}

func newGHAgentEnqueueCmd() *cobra.Command {
	var (
		dbPath     string
		repo       string
		issue      int
		story      string
		objectKind string
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "enqueue",
		Short: "Queue a GitHub issue/PR for the gh-agent worker to drain",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(dbPath) == "" {
				return fmt.Errorf("gh-agent enqueue: --db is required")
			}
			if strings.TrimSpace(repo) == "" {
				return fmt.Errorf("gh-agent enqueue: --repo is required")
			}
			if issue <= 0 {
				return fmt.Errorf("gh-agent enqueue: --issue must be positive")
			}
			objectKind = strings.TrimSpace(objectKind)
			if objectKind == "" {
				objectKind = "issue"
			}
			if objectKind != "issue" && objectKind != "pr" {
				return fmt.Errorf("gh-agent enqueue: --kind must be issue or pr")
			}
			if strings.TrimSpace(story) == "" {
				story = "stories/bugfix"
			}

			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				return fmt.Errorf("gh-agent enqueue: open db %q: %w", dbPath, err)
			}
			defer db.Close()
			store, err := jobs.NewGHJobStore(db)
			if err != nil {
				return err
			}

			number := strconv.Itoa(issue)
			kindPath := objectKind
			if objectKind == "issue" {
				kindPath = "issue"
			}
			job, created, err := store.Enqueue(ctx, jobs.GHMention{
				OriginRef:    "github:" + repo + "/" + kindPath + "/" + number,
				Repo:         repo,
				ObjectKind:   objectKind,
				ObjectNumber: number,
			}, story)
			if err != nil {
				return err
			}
			result := map[string]any{
				"status":        "queued",
				"created":       created,
				"job_id":        job.JobID,
				"origin_ref":    job.OriginRef,
				"repo":          job.Repo,
				"object_kind":   job.ObjectKind,
				"object_number": job.ObjectNumber,
				"story":         job.Story,
				"state":         job.State,
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				return enc.Encode(result)
			}
			action := "attached"
			if created {
				action = "queued"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s -> %s (%s)\n", action, job.OriginRef, job.Story, job.JobID)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "sqlite path for the gh_jobs store")
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo")
	cmd.Flags().IntVar(&issue, "issue", 0, "issue or PR number")
	cmd.Flags().StringVar(&objectKind, "kind", "issue", "GitHub object kind: issue or pr")
	cmd.Flags().StringVar(&story, "story", "stories/bugfix", "story path or gh-agent sentinel to run")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print a JSON result")
	return cmd
}

func newGHAgentDrainCmd() *cobra.Command {
	var (
		dbPath         string
		repo           string
		trigger        string
		worker         string
		publicBaseURL  string
		projectRoot    string
		incidentRepo   string
		assetDir       string
		commentMode    string
		useGitHubApp   bool
		appID          int64
		installationID int64
		appKeyFile     string
		jsonOut        bool
	)
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "Drain queued gh-agent jobs from the durable job DB",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(dbPath) == "" {
				return fmt.Errorf("gh-agent drain: --db is required")
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				return fmt.Errorf("gh-agent drain: open db %q: %w", dbPath, err)
			}
			defer db.Close()
			store, err := jobs.NewGHJobStore(db)
			if err != nil {
				return err
			}
			store.DataDir = ghAgentDrainAssetDir(dbPath, assetDir)
			opts := ghAgentServeOptions{
				Repo:           repo,
				PublicBaseURL:  publicBaseURL,
				Trigger:        trigger,
				Worker:         worker,
				IncidentRepo:   incidentRepo,
				ProjectRoot:    projectRoot,
				CommentMode:    commentMode,
				UseGitHubApp:   useGitHubApp,
				AppID:          appID,
				InstallationID: installationID,
				AppKeyFile:     appKeyFile,
			}
			var drained []*jobs.GHJob
			err = withGHAgentAuth(ctx, opts, func(authedCtx context.Context) error {
				var drainErr error
				drained, drainErr = drainQueuedGHAgentJobs(authedCtx, store, opts)
				return drainErr
			})
			if err != nil {
				return err
			}
			result := ghAgentDrainResult(ctx, store, drained, publicBaseURL)
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				return enc.Encode(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "drained %d queued gh-agent job(s): done %d, failed %d, active %d\n",
				result["drained_count"], result["done_count"], result["failed_count"], result["active_count"])
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "sqlite path for durable gh_jobs state")
	cmd.Flags().StringVar(&repo, "repo", "", "fallback owner/repo for comments and incidents")
	cmd.Flags().StringVar(&trigger, "trigger", ghagent.DefaultMentionTrigger, "mention trigger literal")
	cmd.Flags().StringVar(&worker, "worker", "gh-agent-1", "worker id holding the claim")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "public URL base used in ack run links")
	cmd.Flags().StringVar(&projectRoot, "project-root", os.Getenv("KITSOKI_GH_AGENT_PROJECT_ROOT"), "local checkout root for --repo; when onboarded, issue routes use its .kitsoki app")
	cmd.Flags().StringVar(&incidentRepo, "incident-repo", "", "owner/repo for gh-agent incidents; defaults to --repo")
	cmd.Flags().StringVar(&assetDir, "asset-dir", "", "root directory for on-disk asset blobs; defaults to <db-dir>/assets")
	cmd.Flags().StringVar(&commentMode, "comment-mode", "github", "comment mode for drained jobs: github or none")
	cmd.Flags().BoolVar(&useGitHubApp, "github-app", false, "authenticate as a GitHub App installation (mints GH_TOKEN)")
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides KITSOKI_GH_APP_ID)")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides KITSOKI_GH_APP_INSTALLATION_ID)")
	cmd.Flags().StringVar(&appKeyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides KITSOKI_GH_APP_PRIVATE_KEY_FILE)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print a JSON result")
	return cmd
}

func ghAgentDrainAssetDir(dbPath, assetDir string) string {
	if dir := strings.TrimSpace(assetDir); dir != "" {
		return dir
	}
	if dir := filepath.Dir(strings.TrimSpace(dbPath)); dir != "" && dir != "." {
		return filepath.Join(dir, "assets")
	}
	return "assets"
}

func ghAgentDrainResult(ctx context.Context, store *jobs.GHJobStore, drained []*jobs.GHJob, publicBaseURL string) map[string]any {
	jobsOut := make([]map[string]any, 0, len(drained))
	done, failed, active := 0, 0, 0
	for _, job := range drained {
		if job == nil {
			continue
		}
		switch job.State {
		case jobs.GHDone:
			done++
		case jobs.GHFailed:
			failed++
		default:
			active++
		}
		jobOut := map[string]any{
			"job_id":        job.JobID,
			"origin_ref":    job.OriginRef,
			"repo":          job.Repo,
			"object_kind":   job.ObjectKind,
			"object_number": job.ObjectNumber,
			"story":         job.Story,
			"state":         job.State,
			"run_url":       job.RunURL,
			"incident_url":  job.IncidentURL,
			"err_msg":       job.ErrMsg,
		}
		if branch := strings.TrimSpace(job.Metadata["integration_branch"]); branch != "" {
			jobOut["integration_branch"] = branch
		}
		if commit := strings.TrimSpace(job.Metadata["commit_sha"]); commit != "" {
			jobOut["commit_sha"] = commit
		}
		if commitURL := strings.TrimSpace(job.Metadata["commit_url"]); commitURL != "" {
			jobOut["commit_url"] = commitURL
		}
		assets, err := ghAgentDrainAssets(ctx, store, job.JobID, publicBaseURL)
		if err != nil {
			jobOut["asset_error"] = err.Error()
		} else {
			jobOut["assets"] = assets
		}
		jobsOut = append(jobsOut, jobOut)
	}
	status := "drained"
	if failed > 0 {
		status = "failed"
	} else if active > 0 {
		status = "active"
	}
	return map[string]any{
		"status":        status,
		"drained_count": len(jobsOut),
		"done_count":    done,
		"failed_count":  failed,
		"active_count":  active,
		"jobs":          jobsOut,
	}
}

func ghAgentDrainAssets(ctx context.Context, store *jobs.GHJobStore, jobID, publicBaseURL string) ([]map[string]any, error) {
	if store == nil || strings.TrimSpace(jobID) == "" {
		return nil, nil
	}
	assets, err := store.ListAssets(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		row := map[string]any{
			"name":       asset.Name,
			"mime_type":  asset.MimeType,
			"size_bytes": asset.SizeBytes,
			"created_at": asset.CreatedAt.Format(time.RFC3339),
		}
		assetPath := "/api/run/" + url.PathEscape(jobID) + "/assets/" + url.PathEscape(asset.Name)
		row["path"] = assetPath
		if base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"); base != "" {
			row["url"] = base + assetPath
		}
		out = append(out, row)
	}
	return out, nil
}

func newGHAgentPollCmd() *cobra.Command {
	var (
		repo           string
		mentionFile    string
		dbPath         string
		trigger        string
		worker         string
		publicBaseURL  string
		projectRoot    string
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
			// token and attach it to host GitHub operations so dispatch
			// authenticates as the App. When not configured, today's
			// offline/mention-file path is unchanged.
			ctx, restoreGHToken, err := setupGitHubAppAuth(ctx, useGitHubApp, appID, installationID, appKeyFile)
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
				ProjectRoutes: ghagent.ProjectRouteResolver{Root: projectRoot},
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
	cmd.Flags().StringVar(&mentionFile, "mention", "", "JSON file with []host.GitHubInboxItem (bypasses live GitHub inbox search)")
	cmd.Flags().StringVar(&dbPath, "db", "", "sqlite path for the gh_jobs store (default in-memory)")
	cmd.Flags().StringVar(&trigger, "trigger", ghagent.DefaultMentionTrigger, "mention trigger literal")
	cmd.Flags().StringVar(&worker, "worker", "gh-agent-1", "worker id holding the claim")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "public URL base used in ack run links, e.g. https://kitsoki-test.slothattax.me")
	cmd.Flags().StringVar(&projectRoot, "project-root", os.Getenv("KITSOKI_GH_AGENT_PROJECT_ROOT"), "local checkout root for --repo; when onboarded, issue routes use its .kitsoki app")
	cmd.Flags().BoolVar(&useGitHubApp, "github-app", false, "authenticate as a GitHub App installation (mints GH_TOKEN); off keeps the offline path")
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides KITSOKI_GH_APP_ID)")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides KITSOKI_GH_APP_INSTALLATION_ID)")
	cmd.Flags().StringVar(&appKeyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides KITSOKI_GH_APP_PRIVATE_KEY_FILE)")
	return cmd
}

// setupGitHubAppAuth optionally mints a GitHub App installation token and
// exports it as GH_TOKEN / GITHUB_TOKEN for host GitHub API calls during
// dispatch. It returns a restore func (always non-nil) that resets GH_TOKEN.
//
// Auth engages when --github-app is set, any --gh-app-* flag is provided, or
// the KITSOKI_GH_APP_* env config is fully present. Flags override env. When
// nothing is configured it is a no-op so the existing offline poll path and
// its tests stay unchanged.
func setupGitHubAppAuth(ctx context.Context, force bool, appID, installationID int64, keyFile string) (context.Context, func(), error) {
	noop := func() {}

	cfg, err := githubapp.LoadConfigFromEnv()
	if err != nil {
		return ctx, noop, err
	}
	flagsGiven := appID != 0 || installationID != 0 || keyFile != ""
	if cfg == nil && !force && !flagsGiven {
		return ctx, noop, nil // offline path unchanged
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
		return ctx, noop, fmt.Errorf("gh-agent: --github-app requires app id, installation id, and key file: %w", err)
	}

	src, err := newGitHubAppTokenSource(cfg, nil)
	if err != nil {
		return ctx, noop, err
	}
	token, _, err := src.InstallationToken(ctx)
	if err != nil {
		return ctx, noop, fmt.Errorf("gh-agent: mint installation token: %w", err)
	}

	prev, had := os.LookupEnv("GH_TOKEN")
	if err := os.Setenv("GH_TOKEN", token); err != nil {
		return ctx, noop, fmt.Errorf("gh-agent: set GH_TOKEN: %w", err)
	}
	authedCtx := host.WithCLIExecEnv(ctx, map[string]string{
		"GH_TOKEN":     token,
		"GITHUB_TOKEN": token,
	})
	return authedCtx, func() {
		if had {
			_ = os.Setenv("GH_TOKEN", prev)
		} else {
			_ = os.Unsetenv("GH_TOKEN")
		}
	}, nil
}

// pollInboxItems reads the inbox: from a JSON fixture when --mention is set,
// otherwise via ListGitHubInboxItems through the native GitHub Search API.
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
