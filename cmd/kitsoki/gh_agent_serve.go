package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/ghagent/githubapp"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

func newGHAgentServeCmd() *cobra.Command {
	var (
		repo              string
		dbPath            string
		addr              string
		publicBaseURL     string
		trigger           string
		worker            string
		pollInterval      time.Duration
		reconcileInterval time.Duration
		drainInterval     time.Duration
		stuckAfter        time.Duration
		maxAttempts       int
		incidentRepo      string
		projectRoot       string
		webhookSecret     string
		useGitHubApp      bool
		appID             int64
		installationID    int64
		appKeyFile        string
		assetDir          string
		decksDir          string
		evidenceDir       string
		slideyDir         string
		slideyBin         string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the hosted @kitsoki GitHub agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(dbPath) == "" {
				return fmt.Errorf("gh-agent serve: --db is required")
			}
			if strings.TrimSpace(publicBaseURL) == "" {
				return fmt.Errorf("gh-agent serve: --public-base-url is required")
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
			store.DataDir = assetDir
			if strings.TrimSpace(webhookSecret) == "" {
				webhookSecret = os.Getenv(githubapp.EnvWebhookSecret)
			}
			opts := ghAgentServeOptions{
				Repo:              repo,
				Addr:              addr,
				PublicBaseURL:     publicBaseURL,
				Trigger:           trigger,
				Worker:            worker,
				PollInterval:      pollInterval,
				WebhookSecret:     webhookSecret,
				ReconcileInterval: reconcileInterval,
				DrainInterval:     drainInterval,
				StuckAfter:        stuckAfter,
				MaxAttempts:       maxAttempts,
				IncidentRepo:      incidentRepo,
				ProjectRoot:       projectRoot,
				UseGitHubApp:      useGitHubApp,
				AppID:             appID,
				InstallationID:    installationID,
				AppKeyFile:        appKeyFile,
				DecksDir:          decksDir,
				EvidenceDir:       evidenceDir,
				SlideyDir:         slideyDir,
				SlideyBin:         slideyBin,
			}
			if err := withGHAgentAuth(ctx, opts, func(context.Context) error { return nil }); err != nil {
				return err
			}
			return runGHAgentServe(ctx, store, opts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo slug to poll/comment")
	cmd.Flags().StringVar(&dbPath, "db", "", "sqlite path for durable gh_jobs state")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "HTTP listen address")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "public URL base used in ack run links")
	cmd.Flags().StringVar(&trigger, "trigger", ghagent.DefaultMentionTrigger, "mention trigger literal")
	cmd.Flags().StringVar(&worker, "worker", "gh-agent-1", "worker id holding the claim")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 30*time.Second, "poll fallback interval; set 0 to disable polling")
	cmd.Flags().DurationVar(&reconcileInterval, "reconcile-interval", 1*time.Minute, "interval for stuck-job reconciliation; set 0 to disable")
	cmd.Flags().DurationVar(&drainInterval, "drain-interval", 30*time.Second, "interval for draining queued jobs (retries/re-mentions parked while the queue starved); runs independent of --poll-interval so a webhook-only deployment (--poll-interval=0) still drains; set 0 to disable")
	cmd.Flags().DurationVar(&stuckAfter, "stuck-after", 15*time.Minute, "active job age without updates before retry/escalation")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 2, "stuck-job retries before marking failed and filing an incident")
	cmd.Flags().StringVar(&incidentRepo, "incident-repo", "", "owner/repo for gh-agent incidents; defaults to --repo")
	cmd.Flags().StringVar(&projectRoot, "project-root", os.Getenv("KITSOKI_GH_AGENT_PROJECT_ROOT"), "local checkout root for --repo; when onboarded, issue routes use its .kitsoki app")
	cmd.Flags().StringVar(&webhookSecret, "webhook-secret", "", "GitHub webhook secret; defaults to KITSOKI_GH_WEBHOOK_SECRET")
	cmd.Flags().BoolVar(&useGitHubApp, "github-app", false, "authenticate as a GitHub App installation (mints GH_TOKEN)")
	cmd.Flags().Int64Var(&appID, "gh-app-id", 0, "GitHub App id (overrides KITSOKI_GH_APP_ID)")
	cmd.Flags().Int64Var(&installationID, "gh-app-installation-id", 0, "installation id (overrides KITSOKI_GH_APP_INSTALLATION_ID)")
	cmd.Flags().StringVar(&appKeyFile, "gh-app-key-file", "", "path to the App's RSA private key .pem (overrides KITSOKI_GH_APP_PRIVATE_KEY_FILE)")
	cmd.Flags().StringVar(&assetDir, "asset-dir", "/var/lib/kitsoki-gh-agent/assets", "root directory for on-disk asset blobs")
	cmd.Flags().StringVar(&decksDir, "decks-dir", "/var/lib/kitsoki-gh-agent/decks", "directory the agent hosts rendered bug-report decks from, served at /decks/<id>")
	cmd.Flags().StringVar(&evidenceDir, "evidence-dir", "/var/lib/kitsoki-gh-agent/evidence", "directory the agent holds per-issue bug evidence (rrweb.json/har.json) under <deck-id>/")
	cmd.Flags().StringVar(&slideyDir, "slidey-dir", "", "slidey project dir; enables the bug-report deck auto-trigger on issues.opened (or set --slidey-bin)")
	cmd.Flags().StringVar(&slideyBin, "slidey-bin", "", "slidey binary/entrypoint (alternative to --slidey-dir)")
	return cmd
}

type ghAgentServeOptions struct {
	Repo              string
	Addr              string
	PublicBaseURL     string
	Trigger           string
	Worker            string
	PollInterval      time.Duration
	WebhookSecret     string
	ReconcileInterval time.Duration
	DrainInterval     time.Duration
	StuckAfter        time.Duration
	MaxAttempts       int
	IncidentRepo      string
	ProjectRoot       string
	CommentMode       string
	IntegrationBranch string
	UseGitHubApp      bool
	AppID             int64
	InstallationID    int64
	AppKeyFile        string
	DecksDir          string
	EvidenceDir       string
	SlideyDir         string
	SlideyBin         string
}

func runGHAgentServe(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/api/ready", ghAgentReadyAPIHandler(store, opts))
	mux.HandleFunc("/api/runs", ghAgentRunsAPIHandler(store))
	mux.HandleFunc("/runs", ghAgentRunsHandler(store))
	mux.HandleFunc("/api/run/", ghAgentRunAPIHandler(store))
	mux.HandleFunc("/run/", ghAgentRunHandler(store))
	// Deck hosting + the bug-report deck auto-trigger share a DeckStore. The
	// trigger only engages when slidey + the evidence/decks dirs are configured;
	// otherwise the agent still hosts any decks produced out-of-band.
	deckTrigger := buildDeckTrigger(opts)
	if strings.TrimSpace(opts.DecksDir) != "" {
		if deckStore, err := ghagent.NewDeckStore(opts.DecksDir); err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: deck hosting disabled: %v\n", err)
		} else {
			mux.HandleFunc("/decks/", deckStore.Handler())
		}
	}
	mux.HandleFunc("/gh-agent/webhook", ghAgentWebhookHandler(store, opts, deckTrigger))

	srv := &http.Server{Addr: opts.Addr, Handler: mux}
	errc := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		errc <- err
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if opts.PollInterval > 0 {
		go runGHAgentPollLoop(ctx, store, opts)
	}
	if opts.ReconcileInterval > 0 && opts.StuckAfter > 0 {
		go runGHAgentReconcileLoop(ctx, store, opts)
	}
	// The drain loop runs on its own ticker, independent of PollInterval:
	// queued jobs (stuck-job retries the reconcile loop parks in GHQueued,
	// or re-mentions the dispatcher couldn't route) previously only drained
	// as a side effect of runGHAgentPollOnce, so a webhook-only deployment
	// (--poll-interval=0, relying solely on GitHub push webhooks) never
	// drained them at all — they sat in GHQueued until an operator manually
	// intervened or polling was turned on. See
	// docs/proposals/gh-agent-honest-issues.md task 3.
	if opts.DrainInterval > 0 {
		go runGHAgentDrainLoop(ctx, store, opts)
	}
	fmt.Fprintf(os.Stdout, "gh-agent: serving %s (public %s)\n", opts.Addr, opts.PublicBaseURL)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

func runGHAgentReconcileLoop(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) {
	ticker := time.NewTicker(opts.ReconcileInterval)
	defer ticker.Stop()
	for {
		if err := runGHAgentReconcileOnce(ctx, store, opts); err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: reconcile: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runGHAgentReconcileOnce(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) error {
	return withGHAgentAuth(ctx, opts, func(authedCtx context.Context) error {
		return runGHAgentReconcileOnceAuthed(authedCtx, store, opts)
	})
}

func runGHAgentReconcileOnceAuthed(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) error {
	stuck, err := store.ListStuck(ctx, time.Now().Add(-opts.StuckAfter), 50)
	if err != nil {
		return err
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for _, job := range stuck {
		nextAttempt, err := store.BumpAttempt(ctx, job.JobID)
		if err != nil {
			return err
		}
		if nextAttempt <= maxAttempts {
			if err := store.Advance(ctx, job.JobID, jobs.GHQueued, fmt.Sprintf("stuck job queued for retry after %s", opts.StuckAfter)); err != nil {
				return err
			}
			continue
		}
		errMsg := fmt.Sprintf("job stuck in %s for more than %s after %d attempt(s)", job.State, opts.StuckAfter, nextAttempt)
		if err := store.Advance(ctx, job.JobID, jobs.GHFailed, errMsg); err != nil {
			return err
		}
		if strings.TrimSpace(job.IncidentURL) == "" {
			if incidentURL, err := fileGHAgentIncident(ctx, opts, job, errMsg); err == nil && incidentURL != "" {
				_ = store.SetIncidentURL(ctx, job.JobID, incidentURL)
			} else if err != nil {
				_ = store.RecordEvent(ctx, job.JobID, "incident_failed", err.Error())
			}
		}
	}
	return nil
}

func runGHAgentPollLoop(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) {
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()
	for {
		if err := runGHAgentPollOnce(ctx, store, opts); err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: poll: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runGHAgentPollOnce(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) error {
	return withGHAgentAuth(ctx, opts, func(authedCtx context.Context) error {
		if _, err := drainQueuedGHAgentJobs(authedCtx, store, opts); err != nil {
			return err
		}
		items, err := pollInboxItems(authedCtx, opts.Repo, "")
		if err != nil {
			return err
		}
		for _, mention := range ghagent.FilterMentions(items, opts.Repo, opts.Trigger) {
			if _, err := ghAgentDispatchMention(authedCtx, store, opts, mention, nil); err != nil {
				fmt.Fprintf(os.Stderr, "gh-agent: dispatch %s: %v\n", mention.OriginRef, err)
			}
		}
		return nil
	})
}

// runGHAgentDrainLoop periodically redispatches jobs parked in GHQueued
// (reconcile-loop retries, or classify-then-retry re-mentions) on its own
// ticker, independent of runGHAgentPollLoop/PollInterval. Draining used to
// only happen as a side effect of runGHAgentPollOnce, so any deployment that
// relies purely on the GitHub webhook (--poll-interval=0, no scheduled poll)
// never drained the queue at all. Splitting it out means a webhook-only
// deployment still guarantees forward progress on a stuck/queued job within
// one DrainInterval, without paying for (or requiring) inbox polling.
func runGHAgentDrainLoop(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) {
	ticker := time.NewTicker(opts.DrainInterval)
	defer ticker.Stop()
	for {
		if err := runGHAgentDrainOnce(ctx, store, opts); err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: drain: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runGHAgentDrainOnce(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) error {
	return withGHAgentAuth(ctx, opts, func(authedCtx context.Context) error {
		_, err := drainQueuedGHAgentJobs(authedCtx, store, opts)
		return err
	})
}

func drainQueuedGHAgentJobs(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions) ([]*jobs.GHJob, error) {
	queued, err := store.ListQueued(ctx, 20)
	if err != nil {
		return nil, err
	}
	drained := make([]*jobs.GHJob, 0, len(queued))
	for _, job := range queued {
		mention := mentionFromQueuedJob(job, opts.Trigger)
		labels := labelsFromQueuedJob(job)
		drainedJob, err := ghAgentDispatchMention(ctx, store, opts, mention, labels)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: queued dispatch %s: %v\n", job.OriginRef, err)
		}
		if drainedJob == nil {
			drainedJob, _ = store.GetJob(ctx, job.JobID)
		}
		if drainedJob != nil {
			drained = append(drained, drainedJob)
		}
	}
	return drained, nil
}

func mentionFromQueuedJob(job *jobs.GHJob, trigger string) ghagent.Mention {
	if strings.TrimSpace(trigger) == "" {
		trigger = ghagent.DefaultMentionTrigger
	}
	title := trigger
	if job == nil {
		return ghagent.Mention{Trigger: trigger}
	}
	if job.Story == ghagent.StoryPRRebase {
		title += " rebase"
	}
	return ghagent.Mention{
		Item: host.GitHubInboxItem{
			Kind:   job.ObjectKind,
			Number: job.ObjectNumber,
			Title:  title,
			URL:    githubObjectURL(job),
		},
		Repo:      job.Repo,
		OriginRef: job.OriginRef,
		Trigger:   trigger,
	}
}

func labelsFromQueuedJob(job *jobs.GHJob) []string {
	if job == nil {
		return nil
	}
	switch job.Story {
	case "stories/bugfix":
		return []string{"bug"}
	case "stories/dev-story":
		return []string{"feature"}
	default:
		return nil
	}
}

func githubObjectURL(job *jobs.GHJob) string {
	if job == nil || strings.TrimSpace(job.Repo) == "" || strings.TrimSpace(job.ObjectNumber) == "" {
		return ""
	}
	switch job.ObjectKind {
	case "pr":
		return "https://github.com/" + job.Repo + "/pull/" + job.ObjectNumber
	case "issue":
		return "https://github.com/" + job.Repo + "/issues/" + job.ObjectNumber
	default:
		return ""
	}
}

// buildDeckTrigger assembles the bug-report deck auto-trigger from serve flags.
// It returns nil (feature off) unless slidey and the evidence/decks dirs are all
// configured, so existing deployments are unaffected until opted in.
func buildDeckTrigger(opts ghAgentServeOptions) *ghagent.DeckTrigger {
	if strings.TrimSpace(opts.SlideyDir) == "" && strings.TrimSpace(opts.SlideyBin) == "" {
		return nil
	}
	if strings.TrimSpace(opts.DecksDir) == "" || strings.TrimSpace(opts.EvidenceDir) == "" {
		return nil
	}
	deckStore, err := ghagent.NewDeckStore(opts.DecksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gh-agent: deck auto-trigger disabled: %v\n", err)
		return nil
	}
	evStore, err := ghagent.NewEvidenceStore(opts.EvidenceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gh-agent: deck auto-trigger disabled: %v\n", err)
		return nil
	}
	return &ghagent.DeckTrigger{
		Evidence:      evStore,
		Store:         deckStore,
		Renderer:      bugdeck.SlideyRenderer{Dir: opts.SlideyDir, Bin: opts.SlideyBin},
		Ticket:        host.GitHubTicketHandler,
		PublicBaseURL: opts.PublicBaseURL,
		FallbackRepo:  opts.Repo,
	}
}

func ghAgentWebhookHandler(store *jobs.GHJobStore, opts ghAgentServeOptions, deckTrigger *ghagent.DeckTrigger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if !githubapp.VerifyWebhookSignature(opts.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		mention, labels, ok, err := webhookMention(body, opts.Repo, opts.Trigger)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !ok {
			// Not an @kitsoki mention. A freshly-filed bug-report issue gets a
			// hosted no-LLM deck instead — fire-and-forget so the webhook returns
			// promptly (rendering takes seconds), gated on the agent already
			// holding the issue's evidence.
			if deckTrigger != nil && r.Header.Get("X-GitHub-Event") == "issues" {
				fireDeckTrigger(opts, deckTrigger, body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, "ignored\n")
			return
		}
		var job *jobs.GHJob
		err = withGHAgentAuth(r.Context(), opts, func(authedCtx context.Context) error {
			var dispatchErr error
			job, dispatchErr = dispatchGHAgentMention(authedCtx, store, opts, mention, labels)
			return dispatchErr
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"job_id":  job.JobID,
			"state":   job.State,
			"run_url": job.RunURL,
		})
	}
}

// fireDeckTrigger runs the bug-report deck pipeline for an issues webhook in the
// background: it mints the App token (for the deck-link comment) and calls the
// trigger, logging the outcome. It never blocks the webhook response — deck
// rendering takes seconds — and swallows the "not applicable" case (no on-agent
// evidence / wrong action) quietly.
func fireDeckTrigger(opts ghAgentServeOptions, deckTrigger *ghagent.DeckTrigger, body []byte) {
	go func() {
		err := withGHAgentAuth(context.Background(), opts, func(authedCtx context.Context) error {
			url, handled, herr := deckTrigger.Handle(authedCtx, body)
			if herr != nil {
				return herr
			}
			if handled {
				fmt.Fprintf(os.Stdout, "gh-agent: hosted bug-report deck %s\n", url)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "gh-agent: deck trigger failed: %v\n", err)
		}
	}()
}

func withGHAgentAuth(ctx context.Context, opts ghAgentServeOptions, fn func(context.Context) error) error {
	authedCtx, restoreGHToken, err := setupGitHubAppAuth(ctx, opts.UseGitHubApp, opts.AppID, opts.InstallationID, opts.AppKeyFile)
	if err != nil {
		return err
	}
	defer restoreGHToken()
	return fn(authedCtx)
}

var ghAgentDispatchMention = dispatchGHAgentMention

func dispatchGHAgentMention(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions, mention ghagent.Mention, labels []string) (*jobs.GHJob, error) {
	var comments *ghagent.CommentStore
	if strings.TrimSpace(opts.CommentMode) != "none" {
		comments = &ghagent.CommentStore{Exec: host.GitHubTicketHandler, Repo: mention.Repo}
	}
	var incidentFn func(context.Context, *jobs.GHJob, string) (string, error)
	if comments != nil {
		incidentFn = func(ctx context.Context, job *jobs.GHJob, errMsg string) (string, error) {
			return fileGHAgentIncident(ctx, opts, job, errMsg)
		}
	}
	d := &ghagent.Dispatcher{
		Jobs:              store,
		Routes:            ghagent.DefaultLabelStoryMap(),
		Comments:          comments,
		WorkerID:          opts.Worker,
		PublicBaseURL:     opts.PublicBaseURL,
		ProjectRoutes:     ghagent.ProjectRouteResolver{Root: opts.ProjectRoot},
		SpawnFn:           ghagent.RunStorySession,
		IncidentFn:        incidentFn,
		IntegrationBranch: opts.IntegrationBranch,
	}
	return d.Dispatch(ctx, mention, labels)
}

func fileGHAgentIncident(ctx context.Context, opts ghAgentServeOptions, job *jobs.GHJob, errMsg string) (string, error) {
	repo := strings.TrimSpace(opts.IncidentRepo)
	if repo == "" {
		repo = strings.TrimSpace(opts.Repo)
	}
	if repo == "" {
		return "", nil
	}
	runURL := job.RunURL
	if runURL == "" {
		runURL = publicRunURLForServe(opts.PublicBaseURL, job.JobID)
	}
	body := fmt.Sprintf(`A hosted GitHub-agent job needs operator attention.

- Job: %s
- Origin: %s
- Source: %s
- State: %s
- Story: %s
- Run: %s

Error:

%s
`, job.JobID, job.OriginRef, ghAgentJobSourceURL(job), job.State, job.Story, runURL, errMsg)
	res, err := host.GitHubTicketHandler(ctx, map[string]any{
		"op":        "create",
		"repo":      repo,
		"title":     "gh-agent incident: " + job.OriginRef,
		"body":      body,
		"labels":    []string{"source-autonomous", "comp:github-agent", "incident"},
		"severity":  "P1",
		"component": "github-agent",
		"target":    "kitsoki",
		"trace_ref": runURL,
		"filed_by":  "kitsoki-gh-agent",
	})
	if err != nil {
		return "", err
	}
	if res.Error != "" {
		return "", errors.New(res.Error)
	}
	url, _ := res.Data["url"].(string)
	return strings.TrimSpace(url), nil
}

func publicRunURLForServe(base, jobID string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(jobID) == "" {
		return ""
	}
	return base + "/run/" + jobID
}

func ghAgentReadyAPIHandler(store *jobs.GHJobStore, opts ghAgentServeOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ready" {
			http.NotFound(w, r)
			return
		}
		recent, err := store.ListRecent(r.Context(), 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		stateCounts := map[string]int{}
		for _, job := range recent {
			stateCounts[job.State]++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":              "ready",
			"repo":                strings.TrimSpace(opts.Repo),
			"public_base_url":     strings.TrimRight(strings.TrimSpace(opts.PublicBaseURL), "/"),
			"worker":              strings.TrimSpace(opts.Worker),
			"trigger":             strings.TrimSpace(opts.Trigger),
			"drain_enabled":       opts.DrainInterval > 0,
			"poll_enabled":        opts.PollInterval > 0,
			"reconcile_enabled":   opts.ReconcileInterval > 0 && opts.StuckAfter > 0,
			"project_root":        strings.TrimSpace(opts.ProjectRoot),
			"incident_repo":       strings.TrimSpace(firstNonBlank(opts.IncidentRepo, opts.Repo)),
			"recent_job_count":    len(recent),
			"recent_state_counts": stateCounts,
		})
	}
}

func ghAgentRunsHandler(store *jobs.GHJobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runs" {
			http.NotFound(w, r)
			return
		}
		recent, err := store.ListRecent(r.Context(), 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><title>kitsoki GitHub runs</title>
<style>body{font:14px/1.45 system-ui,sans-serif;max-width:1180px;margin:40px auto;padding:0 24px;color:#17202a}table{border-collapse:collapse;width:100%%}th,td{border-bottom:1px solid #e5e7eb;padding:8px 10px;text-align:left;vertical-align:top}th{font-size:12px;text-transform:uppercase;color:#5f6b7a}.state{display:inline-block;padding:2px 8px;border-radius:999px;background:#ecfdf5;color:#065f46}.failed{background:#fef2f2;color:#991b1b}.muted{color:#5f6b7a}code{background:#f3f5f7;padding:2px 5px;border-radius:4px}</style></head>
<body><h1>kitsoki GitHub runs</h1><p class="muted">Recent webhook and poll jobs. <a href="/api/runs">JSON</a></p><table>
<thead><tr><th>Updated</th><th>State</th><th>Origin</th><th>Story</th><th>Run</th><th>Comment</th><th>Incident</th><th>Error</th></tr></thead><tbody>`)
		if len(recent) == 0 {
			fmt.Fprint(w, `<tr><td colspan="8" class="muted">No jobs recorded.</td></tr>`)
		}
		for _, job := range recent {
			fmt.Fprintf(w, `<tr><td>%s</td><td><span class="%s">%s</span></td><td>%s<br><span class="muted">%s #%s</span></td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(job.UpdatedAt.Format(time.RFC3339)),
				html.EscapeString(ghAgentStateClass(job.State)),
				html.EscapeString(job.State),
				htmlLinkOrCode(ghAgentJobSourceURL(job)),
				html.EscapeString(job.ObjectKind),
				html.EscapeString(job.ObjectNumber),
				html.EscapeString(emptyAsDash(job.Story)),
				htmlLinkOrCode(firstNonEmpty(job.RunURL, "/run/"+job.JobID)),
				htmlLinkOrCode(job.CommentID),
				htmlLinkOrCode(job.IncidentURL),
				html.EscapeString(emptyAsDash(job.ErrMsg)),
			)
		}
		fmt.Fprint(w, `</tbody></table></body></html>`)
	}
}

func ghAgentRunsAPIHandler(store *jobs.GHJobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runs" {
			http.NotFound(w, r)
			return
		}
		recent, err := store.ListRecent(r.Context(), 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := make([]map[string]any, 0, len(recent))
		for _, job := range recent {
			out = append(out, ghAgentJobJSON(job))
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func ghAgentRunHandler(store *jobs.GHJobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/run/"), "/")
		if jobID == "" {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(jobID, "/assets/")
		if len(parts) == 2 {
			actualJobID := parts[0]
			assetName := parts[1]
			data, mimeType, err := store.GetAssetData(r.Context(), actualJobID, assetName)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mimeType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			_, _ = w.Write(data)
			return
		}
		job, err := store.GetJob(r.Context(), jobID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		events, _ := store.Events(r.Context(), job.JobID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		sourceURL := ghAgentJobSourceURL(job)
		commentURL := job.CommentID
		fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><title>kitsoki run %s</title>
<style>body{font:16px/1.45 system-ui,sans-serif;max-width:860px;margin:48px auto;padding:0 24px;color:#17202a}dt{font-weight:700;margin-top:14px}dd{margin:4px 0 0}code{background:#f3f5f7;padding:2px 5px;border-radius:4px}.state{display:inline-block;padding:2px 8px;border-radius:999px;background:#ecfdf5;color:#065f46}.failed{background:#fef2f2;color:#991b1b}.muted{color:#5f6b7a}</style></head>
<body><h1>kitsoki GitHub run</h1><dl>
<dt>Job</dt><dd><code>%s</code></dd>
<dt>Origin</dt><dd><code>%s</code></dd>
<dt>Source</dt><dd>%s</dd>
<dt>Story</dt><dd><code>%s</code></dd>
<dt>State</dt><dd><span class="%s">%s</span></dd>
<dt>Issue / PR</dt><dd>%s #%s</dd>
<dt>Comment</dt><dd>%s</dd>
<dt>Attempts</dt><dd>%d</dd>
<dt>Incident</dt><dd>%s</dd>
<dt>Error</dt><dd>%s</dd>
<dt>Updated</dt><dd>%s</dd>
</dl><h2>Timeline</h2>%s<p class="muted"><a href="/api/run/%s">JSON</a></p></body></html>`,
			html.EscapeString(job.JobID),
			html.EscapeString(job.JobID),
			html.EscapeString(job.OriginRef),
			htmlLinkOrCode(sourceURL),
			html.EscapeString(job.Story),
			html.EscapeString(ghAgentStateClass(job.State)),
			html.EscapeString(job.State),
			html.EscapeString(job.ObjectKind),
			html.EscapeString(job.ObjectNumber),
			htmlLinkOrCode(commentURL),
			job.AttemptCount,
			htmlLinkOrCode(job.IncidentURL),
			html.EscapeString(emptyAsDash(job.ErrMsg)),
			html.EscapeString(job.UpdatedAt.Format(time.RFC3339)),
			renderGHAgentEventsHTML(events),
			html.EscapeString(job.JobID),
		)
	}
}

func ghAgentRunAPIHandler(store *jobs.GHJobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/run/"), "/")
		if jobID == "" {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(jobID, "/assets") && r.Method == http.MethodGet {
			actualJobID := strings.TrimSuffix(jobID, "/assets")
			assets, err := store.ListAssets(r.Context(), actualJobID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(assets)
			return
		}
		if parts := strings.Split(jobID, "/assets/"); len(parts) == 2 {
			actualJobID := parts[0]
			assetName := parts[1]
			if r.Method == http.MethodPut {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
					return
				}
				mimeType := r.Header.Get("Content-Type")
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				err = store.PutAsset(r.Context(), actualJobID, assetName, mimeType, data)
				if err != nil {
					http.Error(w, "failed to store asset: "+err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
				return
			}
		}
		job, err := store.GetJob(r.Context(), jobID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		events, _ := store.Events(r.Context(), jobID)
		w.Header().Set("Content-Type", "application/json")
		payload := ghAgentJobJSON(job)
		payload["events"] = ghAgentEventsJSON(events)
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func ghAgentJobJSON(job *jobs.GHJob) map[string]any {
	return map[string]any{
		"job_id":        job.JobID,
		"origin_ref":    job.OriginRef,
		"repo":          job.Repo,
		"object_kind":   job.ObjectKind,
		"object_number": job.ObjectNumber,
		"source_url":    ghAgentJobSourceURL(job),
		"story":         job.Story,
		"state":         job.State,
		"run_id":        job.RunID,
		"run_url":       job.RunURL,
		"comment_url":   job.CommentID,
		"attempt_count": job.AttemptCount,
		"incident_url":  job.IncidentURL,
		"err_msg":       job.ErrMsg,
		"created_at":    job.CreatedAt.Format(time.RFC3339),
		"updated_at":    job.UpdatedAt.Format(time.RFC3339),
	}
}

func ghAgentJobSourceURL(job *jobs.GHJob) string {
	repo := strings.TrimSpace(job.Repo)
	number := strings.TrimSpace(job.ObjectNumber)
	if repo == "" || number == "" {
		return ""
	}
	switch strings.TrimSpace(job.ObjectKind) {
	case "pr":
		return "https://github.com/" + repo + "/pull/" + number
	default:
		return "https://github.com/" + repo + "/issues/" + number
	}
}

func ghAgentStateClass(state string) string {
	if state == jobs.GHFailed {
		return "state failed"
	}
	return "state"
}

func htmlLinkOrCode(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return `<span class="muted">-</span>`
	}
	escaped := html.EscapeString(v)
	if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "/") {
		return `<a href="` + escaped + `">` + escaped + `</a>`
	}
	return `<code>` + escaped + `</code>`
}

func emptyAsDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func renderGHAgentEventsHTML(events []jobs.GHJobEvent) string {
	if len(events) == 0 {
		return `<p class="muted">No lifecycle events recorded.</p>`
	}
	var b strings.Builder
	b.WriteString(`<ol>`)
	for _, ev := range events {
		b.WriteString(`<li><code>`)
		b.WriteString(html.EscapeString(ev.CreatedAt.Format(time.RFC3339)))
		b.WriteString(`</code> <strong>`)
		b.WriteString(html.EscapeString(ev.State))
		b.WriteString(`</strong>`)
		if strings.TrimSpace(ev.Message) != "" {
			b.WriteString(` — `)
			b.WriteString(html.EscapeString(ev.Message))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ol>`)
	return b.String()
}

func ghAgentEventsJSON(events []jobs.GHJobEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		out = append(out, map[string]any{
			"id":         ev.ID,
			"state":      ev.State,
			"message":    ev.Message,
			"created_at": ev.CreatedAt.Format(time.RFC3339),
		})
	}
	return out
}

func webhookMention(body []byte, fallbackRepo, trigger string) (ghagent.Mention, []string, bool, error) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			Body    string `json:"body"`
			Labels  []struct {
				Name string `json:"name"`
			} `json:"labels"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
		PullRequest struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			Body    string `json:"body"`
			Labels  []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"pull_request"`
		Review struct {
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"review"`
		Comment struct {
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ghagent.Mention{}, nil, false, fmt.Errorf("parse webhook payload: %w", err)
	}
	switch payload.Action {
	case "deleted", "unassigned", "unlabeled", "closed":
		return ghagent.Mention{}, nil, false, nil
	}
	if strings.TrimSpace(trigger) == "" {
		trigger = ghagent.DefaultMentionTrigger
	}
	mentionBody := firstContainingTrigger(trigger, payload.Comment.Body, payload.Review.Body, payload.Issue.Title, payload.PullRequest.Title, payload.Issue.Body, payload.PullRequest.Body)
	if mentionBody == "" {
		return ghagent.Mention{}, nil, false, nil
	}
	repo := strings.TrimSpace(payload.Repository.FullName)
	if repo == "" {
		repo = fallbackRepo
	}
	if repo == "" {
		return ghagent.Mention{}, nil, false, fmt.Errorf("webhook payload has no repository.full_name and --repo is empty")
	}

	item := host.GitHubInboxItem{
		Kind:   "issue",
		Author: firstNonEmpty(payload.Comment.User.Login, payload.Review.User.Login),
		Title:  mentionBody,
		URL:    firstNonEmpty(payload.Comment.HTMLURL, payload.Review.HTMLURL),
	}
	var labels []string
	switch {
	case payload.PullRequest.Number > 0:
		item.Kind = "pr"
		item.Number = fmt.Sprintf("%d", payload.PullRequest.Number)
		if item.URL == "" {
			item.URL = payload.PullRequest.HTMLURL
		}
		for _, l := range payload.PullRequest.Labels {
			labels = append(labels, l.Name)
		}
	case payload.Issue.Number > 0:
		if payload.Issue.PullRequest != nil {
			item.Kind = "pr"
		}
		item.Number = fmt.Sprintf("%d", payload.Issue.Number)
		if item.URL == "" {
			item.URL = payload.Issue.HTMLURL
		}
		for _, l := range payload.Issue.Labels {
			labels = append(labels, l.Name)
		}
	default:
		return ghagent.Mention{}, nil, false, fmt.Errorf("webhook payload has no issue or pull_request number")
	}
	mention := ghagent.Mention{
		Item:      item,
		Repo:      repo,
		OriginRef: inbox.GitHubOriginRef(repo, item),
		Trigger:   trigger,
	}
	return mention, labels, true, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstContainingTrigger(trigger string, values ...string) string {
	needle := strings.ToLower(strings.TrimSpace(trigger))
	if needle == "" {
		return ""
	}
	for _, v := range values {
		if strings.Contains(strings.ToLower(v), needle) {
			return v
		}
	}
	return ""
}
