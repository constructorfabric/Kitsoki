package ghagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/testrunner"
)

// RunResult is the outcome of one spawned story run.
type RunResult struct {
	RunURL     string
	FinalState string // story terminal state, for the ack
	Turns      int
	Summary    string
	// IntegrationBranch is the branch that received the autonomous fix after
	// the story completed. CommitSHA/CommitURL identify the landed change.
	IntegrationBranch string
	CommitSHA         string
	CommitURL         string
	// Stubbed is true when the spawn path ran the beat fixture's inline
	// host.agent.* stub handlers instead of a real agent turn — no LLM ran,
	// no code changed. The comment-render step must never say "Done" for a
	// stubbed run; see docs/proposals/gh-agent-honest-issues.md task 0.
	Stubbed bool
	// StubReason explains why the run was stubbed, for the trace record and
	// (eventually) operator-facing diagnostics. Empty when Stubbed is false.
	StubReason string
	// Harness is which harness served the run's host.agent.* calls: "replay"
	// (a recorded host cassette — no LLM, no cost) or "live" (the real
	// agent subprocess; operator-invoked only). Empty for the PR sentinel
	// beats and the pre-real-dispatch stub path, which don't select a
	// harness. See docs/proposals/gh-agent-honest-issues.md task 2.2.
	Harness string
	// Worktree is the absolute path of the per-job worktree the run used
	// (task 2.1), when one applies. Empty when the route has no real
	// dispatch plan yet (stub path) or the harness never materialized a
	// worktree on disk (replay mode — the cassette serves the workspace
	// host calls, so nothing is actually checked out).
	Worktree string
	// RealHostCalls counts host.* invocations this run made that were
	// NOT served by an unconditional "always succeed" stub map — i.e. a
	// real subprocess (live) or a recorded, arg-matched cassette episode
	// (replay). Zero for a Stubbed run. Used by the no-"Done"-without-
	// real-work invariant test to prove the real path only ever reports
	// completion when real work actually happened.
	RealHostCalls int
	// Assets are review artifacts produced by the run and persisted to the
	// gh-agent job store for human review. Successful fix routes should include
	// a report and, when available, a patch/diff.
	Assets []RunAsset
}

// RunAsset is one reviewable artifact produced by a spawned story run.
type RunAsset struct {
	Name     string
	MimeType string
	Data     []byte
}

// Harness mode constants for real dispatch (task 2.2). Only two values are
// ever valid; "live" spawns the real agent subprocess (real cost — operator-
// invoked only) while "replay" serves host.agent.*/host.git/host.local calls
// from a recorded cassette (no LLM, no network, CI-safe). This mirrors the
// same live-or-replay seam `kitsoki turn`/`web` expose via --harness, but
// deliberately does NOT reuse cmd/kitsoki's autoSelectHarness: that helper
// sniffs ambient credentials/PATH and silently goes live, which is unsafe for
// an unattended dispatcher (see the HARD RULE in AGENTS.md: tests/CI must
// default to cassettes/replay; live is operator-invoked only).
const (
	HarnessReplay = "replay"
	HarnessLive   = "live"
)

// EnvGHAgentHarness is the operator-facing override for the dispatcher's
// harness mode. Unset (or any value other than "live") means replay. Only an
// operator standing up `gh-agent serve` in a deliberate live posture should
// ever set this to "live"; it must never be sniffed from ambient credentials.
const EnvGHAgentHarness = "KITSOKI_GHAGENT_HARNESS"

// resolveHarnessMode picks the real-dispatch harness mode. explicit (e.g.
// Dispatcher.HarnessMode, wired by an operator's CLI flag) wins over the
// EnvGHAgentHarness env var, which wins over the default ("replay"). Any
// value other than "replay"/"live" is a configuration error — fail loudly
// rather than silently falling back, since a mistyped mode should never
// quietly downgrade (or upgrade) the safety posture.
func resolveHarnessMode(explicit string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(explicit))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv(EnvGHAgentHarness)))
	}
	if mode == "" {
		mode = HarnessReplay
	}
	if mode != HarnessReplay && mode != HarnessLive {
		return "", fmt.Errorf("ghagent: unknown harness mode %q (want %q or %q)", mode, HarnessReplay, HarnessLive)
	}
	return mode, nil
}

// harnessModeKey is the context key RunStorySession reads the resolved
// harness mode from. Threaded via context (rather than widening the SpawnFn
// signature) so injected test SpawnFns and the PR-beat paths stay untouched.
type harnessModeKey struct{}

func withHarnessMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, harnessModeKey{}, mode)
}

// harnessModeFromContext reads the mode dispatchRouted set, defaulting to the
// safe replay posture when called out of that path (e.g. tests calling
// RunStorySession directly without going through Dispatch).
func harnessModeFromContext(ctx context.Context) string {
	if v, _ := ctx.Value(harnessModeKey{}).(string); v == HarnessLive {
		return HarnessLive
	}
	return HarnessReplay
}

// Dispatcher claims a job for a mention and spawns the mapped story no-LLM.
type Dispatcher struct {
	Jobs     *jobs.GHJobStore
	Routes   LabelStoryMap
	Comments *CommentStore
	WorkerID string
	// PublicBaseURL, when set, replaces the local kitsoki:// run placeholder
	// with a browser-openable URL: <base>/run/<job_id>.
	PublicBaseURL string
	// ProjectRoutes, when configured, maps eligible issue mentions onto the
	// onboarded project checkout before spawning the route.
	ProjectRoutes ProjectRouteResolver
	// SpawnFn runs the mapped story for a claimed job in no-LLM posture.
	// Defaults to RunStorySession (testrunner.RunFlows-backed); injectable for
	// tests (spy / assertion).
	SpawnFn func(ctx context.Context, route Route, job *jobs.GHJob) (RunResult, error)
	// IncidentFn files an operator-facing incident for non-recoverable failures.
	// It is injected so tests stay offline and production can use host.gh.ticket.
	IncidentFn func(ctx context.Context, job *jobs.GHJob, errMsg string) (string, error)
	// HarnessMode explicitly selects the real-dispatch harness ("live" or
	// "replay"), overriding EnvGHAgentHarness. Empty defers to the env var,
	// which defers to "replay". Only an operator wiring `gh-agent serve`
	// deliberately live should ever set this to "live" — see
	// resolveHarnessMode's doc for why this is never auto-detected.
	HarnessMode string
	// IntegrationBranch, when set, is the shared branch every real-dispatch
	// job in this drain cycle lands its verified fix onto (one branch per
	// marathon cycle, per docs/proposals' batch-review model). Threaded into
	// route.World["integration_branch"] before spawn; empty falls back to
	// runRealDispatch's per-job branch naming.
	IntegrationBranch string
}

// Dispatch runs ONE mention end-to-end. On a fresh claim (won): Post the initial
// ack, Classify, Advance(running), SpawnFn, Advance(done|failed), Update ack. On
// a re-mention (attach): Update the ack with the existing run_url and do NOT
// respawn. Idempotent on mention.OriginRef.
func (d *Dispatcher) Dispatch(ctx context.Context, mention Mention, labels []string) (*jobs.GHJob, error) {
	job, won, err := d.Jobs.Claim(ctx, jobs.GHMention{
		OriginRef:    mention.OriginRef,
		Repo:         mention.Repo,
		ObjectKind:   mention.Item.Kind,
		ObjectNumber: mention.Item.Number,
	}, d.WorkerID)
	if err != nil {
		return nil, err
	}

	if !won {
		if route, ok, err := d.classifyRoute(mention, labels); err != nil {
			return nil, err
		} else if ok && shouldRerunTerminalPR(job, route) {
			return d.dispatchRouted(ctx, mention, job, route)
		}
		if job.State == jobs.GHAwaitingGuidance {
			if route, ok, err := d.classifyRoute(mention, labels); err != nil {
				return nil, err
			} else if ok {
				return d.dispatchRouted(ctx, mention, job, route)
			}
		}
		// Re-mention: attach. Update the ack carrying the existing run_url.
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: job.Story, State: job.State, RunURL: job.RunURL}
		if d.Comments != nil && job.CommentID != "" {
			nextID, updateErr := d.Comments.Update(ctx, mention.Item.Number, job.CommentID,
				fmt.Sprintf("Already on it — attached to existing run for `%s`.", job.OriginRef), meta)
			if updateErr != nil {
				_ = d.Jobs.RecordEvent(ctx, job.JobID, "comment_update_failed", updateErr.Error())
			}
			if nextID != "" && nextID != job.CommentID {
				_ = d.Jobs.SetComment(ctx, job.JobID, nextID)
				job.CommentID = nextID
			}
		}
		return job, nil
	}

	// Won: classify + post the initial ack.
	route, ok, classifyErr := d.classifyRoute(mention, labels)
	if classifyErr != nil {
		return d.failBeforeRun(ctx, job, "route_classify_failed", classifyErr)
	}
	if !ok {
		if err := d.Jobs.Advance(ctx, job.JobID, jobs.GHAwaitingGuidance, "unclassifiable mention"); err != nil {
			return nil, err
		}
		job.State = jobs.GHAwaitingGuidance
		if runURL := publicRunURL(d.PublicBaseURL, job.JobID); runURL != "" {
			if err := d.Jobs.SetRunURL(ctx, job.JobID, job.JobID, runURL); err != nil {
				return nil, err
			}
			job.RunURL = runURL
		}
		if d.Comments != nil {
			meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, State: jobs.GHAwaitingGuidance, RunURL: job.RunURL}
			prose := "I need a bit more direction before I can route this. Please add a `bug`, `feature`, or `enhancement` label, or reply with the path you want me to take."
			if job.RunURL != "" {
				prose += "\n\nRun page: " + job.RunURL
			}
			commentID, err := d.Comments.Post(ctx, mention.Item.Number,
				prose, meta)
			if err != nil {
				return nil, err
			}
			if commentID != "" {
				if err := d.Jobs.SetComment(ctx, job.JobID, commentID); err != nil {
					return nil, err
				}
				job.CommentID = commentID
			}
		}
		job, _ = d.Jobs.GetJob(ctx, job.JobID)
		return job, nil
	}
	if err := d.Jobs.SetStory(ctx, job.JobID, route.Story); err != nil {
		return nil, err
	}
	job.Story = route.Story

	return d.dispatchRouted(ctx, mention, job, route)
}

func (d *Dispatcher) classifyRoute(mention Mention, labels []string) (Route, bool, error) {
	route, ok := d.Routes.Classify(mention, labels)
	if !ok {
		return Route{}, false, nil
	}
	if projectRoute, applied, err := d.ProjectRoutes.Apply(route, mention); err != nil {
		return Route{}, false, err
	} else if applied {
		return projectRoute, true, nil
	}
	return route, true, nil
}

func (d *Dispatcher) dispatchRouted(ctx context.Context, mention Mention, job *jobs.GHJob, route Route) (*jobs.GHJob, error) {
	if strings.TrimSpace(d.IntegrationBranch) != "" {
		world := make(map[string]any, len(route.World)+1)
		for k, v := range route.World {
			world[k] = v
		}
		world["integration_branch"] = d.IntegrationBranch
		route.World = world
	}
	if job.Story != route.Story {
		if err := d.Jobs.SetStory(ctx, job.JobID, route.Story); err != nil {
			return nil, err
		}
		job.Story = route.Story
	}
	if d.Comments != nil {
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: route.Story, State: jobs.GHClaimed}
		prose := fmt.Sprintf("On it — dispatching `%s` for `%s`.", route.Story, job.OriginRef)
		var (
			commentID string
			err       error
		)
		if strings.TrimSpace(job.CommentID) != "" {
			commentID, err = d.Comments.Update(ctx, mention.Item.Number, job.CommentID, prose, meta)
		} else {
			commentID, err = d.Comments.Post(ctx, mention.Item.Number, prose, meta)
		}
		if err != nil {
			return d.failBeforeRun(ctx, job, "comment_ack_failed", err)
		}
		if commentID != "" {
			if err := d.Jobs.SetComment(ctx, job.JobID, commentID); err != nil {
				return nil, err
			}
			job.CommentID = commentID
		}
	}

	if err := d.Jobs.Advance(ctx, job.JobID, jobs.GHRunning, ""); err != nil {
		return nil, err
	}
	job.State = jobs.GHRunning

	mode, modeErr := resolveHarnessMode(d.HarnessMode)
	if modeErr != nil {
		return d.failBeforeRun(ctx, job, "harness_mode_invalid", modeErr)
	}
	ctx = withHarnessMode(ctx, mode)

	spawn := d.SpawnFn
	if spawn == nil {
		spawn = RunStorySession
	}
	result, spawnErr := spawn(ctx, route, job)
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelPersist()
	if url := publicRunURL(d.PublicBaseURL, job.JobID); url != "" {
		result.RunURL = url
	}

	finalState := jobs.GHDone
	errMsg := ""
	if spawnErr != nil {
		finalState = jobs.GHFailed
		errMsg = spawnErr.Error()
	}
	if result.RunURL != "" {
		_ = d.Jobs.SetRunURL(persistCtx, job.JobID, job.JobID, result.RunURL)
		job.RunURL = result.RunURL
	}
	if spawnErr == nil {
		landing := map[string]string{
			"integration_branch": result.IntegrationBranch,
			"commit_sha":         result.CommitSHA,
			"commit_url":         result.CommitURL,
		}
		if err := d.Jobs.MergeMetadata(persistCtx, job.JobID, landing); err != nil {
			finalState = jobs.GHFailed
			errMsg = err.Error()
			spawnErr = err
		}
	}
	if spawnErr == nil && len(result.Assets) > 0 {
		if assetErr := d.persistRunAssets(persistCtx, job, result.Assets); assetErr != nil {
			finalState = jobs.GHFailed
			errMsg = assetErr.Error()
			spawnErr = assetErr
		}
	}
	if err := d.Jobs.Advance(persistCtx, job.JobID, finalState, errMsg); err != nil {
		return nil, err
	}
	job.State = finalState
	if result.Stubbed {
		_ = d.Jobs.RecordEvent(persistCtx, job.JobID, "stubbed", result.StubReason)
	}
	if result.Harness != "" {
		detail := result.Harness
		if result.Worktree != "" {
			detail = fmt.Sprintf("%s worktree=%s", result.Harness, result.Worktree)
		}
		_ = d.Jobs.RecordEvent(persistCtx, job.JobID, "harness", detail)
	}
	if spawnErr != nil && d.IncidentFn != nil && strings.TrimSpace(job.IncidentURL) == "" {
		if incidentURL, incidentErr := d.IncidentFn(persistCtx, job, errMsg); incidentErr == nil && strings.TrimSpace(incidentURL) != "" {
			_ = d.Jobs.SetIncidentURL(persistCtx, job.JobID, incidentURL)
			job.IncidentURL = incidentURL
		} else if incidentErr != nil {
			_ = d.Jobs.RecordEvent(persistCtx, job.JobID, "incident_failed", incidentErr.Error())
		}
	}

	if d.Comments != nil && job.CommentID != "" {
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: route.Story, State: finalState, RunURL: job.RunURL}
		// Honest predicate (rendering-time only, not a routing decision — see
		// docs/proposals/gh-agent-honest-issues.md tasks 0 and 2): a stubbed
		// run never gets to say "Done". Extended for real dispatch (task 2.4):
		// a route that went through the real-dispatch harness (result.Harness
		// set) must ALSO show at least one real host call before it may claim
		// completion — a real-dispatch plan that somehow produced zero host
		// calls is a bug, not a completed run, and must not be allowed to
		// render "Done" over nothing. Routes that never select a harness (the
		// PR-status/rebase beats, or a test double that doesn't populate
		// RealHostCalls) are unaffected — Harness == "" skips this check.
		realWorkProven := !result.Stubbed && (result.Harness == "" || result.RealHostCalls > 0)
		var prose string
		switch {
		case result.Stubbed:
			prose = "acknowledged — pipeline not yet enabled for this route"
		case !realWorkProven:
			prose = fmt.Sprintf("acknowledged — `%s` real-dispatch harness reported no real host calls; treating as inconclusive, not done", route.Story)
		case strings.TrimSpace(result.Summary) != "":
			prose = result.Summary
		default:
			prose = fmt.Sprintf("Done — `%s` finished in state `%s` (%d turn(s)).", route.Story, result.FinalState, result.Turns)
		}
		if spawnErr != nil {
			prose = fmt.Sprintf("Run failed: %s", spawnErr.Error())
			if job.IncidentURL != "" {
				prose += "\n\nIncident: " + job.IncidentURL
			}
		}
		nextID, updateErr := d.Comments.Update(persistCtx, mention.Item.Number, job.CommentID, prose, meta)
		if updateErr != nil {
			_ = d.Jobs.RecordEvent(persistCtx, job.JobID, "comment_update_failed", updateErr.Error())
		}
		if nextID != "" && nextID != job.CommentID {
			_ = d.Jobs.SetComment(persistCtx, job.JobID, nextID)
			job.CommentID = nextID
		}
	}

	job, _ = d.Jobs.GetJob(persistCtx, job.JobID)
	return job, spawnErr
}

func (d *Dispatcher) persistRunAssets(ctx context.Context, job *jobs.GHJob, assets []RunAsset) error {
	if d == nil || d.Jobs == nil || job == nil {
		return nil
	}
	for _, asset := range assets {
		name := strings.TrimSpace(asset.Name)
		if name == "" || len(asset.Data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(asset.MimeType)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if err := d.Jobs.PutAsset(ctx, job.JobID, name, mimeType, asset.Data); err != nil {
			_ = d.Jobs.RecordEvent(ctx, job.JobID, "asset_persist_failed", err.Error())
			return fmt.Errorf("ghagent: persist run asset %q: %w", name, err)
		}
		_ = d.Jobs.RecordEvent(ctx, job.JobID, "asset_persisted", name)
	}
	return nil
}

func (d *Dispatcher) failBeforeRun(ctx context.Context, job *jobs.GHJob, event string, cause error) (*jobs.GHJob, error) {
	if job == nil {
		return nil, cause
	}
	errMsg := cause.Error()
	_ = d.Jobs.RecordEvent(ctx, job.JobID, event, errMsg)
	if advanceErr := d.Jobs.Advance(ctx, job.JobID, jobs.GHFailed, errMsg); advanceErr != nil {
		return job, advanceErr
	}
	job.State = jobs.GHFailed
	job.ErrMsg = errMsg
	if d.IncidentFn != nil && strings.TrimSpace(job.IncidentURL) == "" {
		if incidentURL, incidentErr := d.IncidentFn(ctx, job, errMsg); incidentErr == nil && strings.TrimSpace(incidentURL) != "" {
			_ = d.Jobs.SetIncidentURL(ctx, job.JobID, incidentURL)
			job.IncidentURL = incidentURL
		} else if incidentErr != nil {
			_ = d.Jobs.RecordEvent(ctx, job.JobID, "incident_failed", incidentErr.Error())
		}
	}
	if latest, latestErr := d.Jobs.GetJob(ctx, job.JobID); latestErr == nil {
		job = latest
	}
	return job, cause
}

func shouldRerunTerminalPR(job *jobs.GHJob, route Route) bool {
	if job == nil || job.ObjectKind != "pr" {
		return false
	}
	if route.Story == "" {
		return false
	}
	if route.Story == job.Story {
		return route.Story == StoryPRRebase && (job.State == jobs.GHDone || job.State == jobs.GHFailed)
	}
	return job.State == jobs.GHDone || job.State == jobs.GHFailed
}

func publicRunURL(baseURL, jobID string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(jobID) == "" {
		return ""
	}
	return baseURL + "/run/" + jobID
}

// RunStorySession is the default SpawnFn. Routes with a registered real-
// dispatch plan (realDispatchPlans — currently stories/bugfix only) drive the
// REAL story end-to-end in a per-job worktree through a live-or-replay
// harness (task 2 of gh-agent-honest-issues.md). Routes without one still run
// the honest beat-fixture stub path from task 0/1 — Stubbed stays true and
// the comment substrate never says "Done" for them (dispatch.go's rendering
// predicate). The PR-status/rebase routes read the live PR through host.git's
// pr_status/pr_rebase seams so the GitHub comment reports the actual state.
func RunStorySession(ctx context.Context, route Route, job *jobs.GHJob) (RunResult, error) {
	if route.Story == StoryPRBeat {
		return RunPRStatusBeat(ctx, job)
	}
	if route.Story == StoryPRRebase {
		return RunPRRebaseBeat(ctx, job)
	}

	root, err := repoRoot()
	if err != nil {
		return RunResult{}, err
	}

	if plan, ok := realDispatchPlans[route.Story]; ok && strings.TrimSpace(route.BeatFixture) == "" {
		return runRealDispatch(ctx, root, route, job, plan)
	}
	return runStubBeatFixture(ctx, root, route, job)
}

// runStubBeatFixture is the pre-real-dispatch path (task 0/1): it points
// testrunner.RunFlows at the route's story app.yaml + the per-job beat
// fixture (internal/ghagent/testdata/<story>.beat.yaml), whose host.agent.*
// handlers are inline "always succeed" stubs — no LLM ran, no code changed,
// no matter how many turns "passed". Retained as the flow-test-only fallback
// for routes real dispatch hasn't reached yet (task 2.3), and still the path
// a caller-supplied route.BeatFixture (project routes) opts into explicitly.
func runStubBeatFixture(ctx context.Context, root string, route Route, job *jobs.GHJob) (RunResult, error) {
	var appPath, beatFixture string
	if strings.TrimSpace(route.AppPath) != "" {
		appPath = route.AppPath
	} else {
		appPath = filepath.Join(root, route.Story, "app.yaml")
	}
	if strings.TrimSpace(route.BeatFixture) != "" {
		beatFixture = route.BeatFixture
	} else {
		base := filepath.Base(route.Story) // e.g. "bugfix"
		beatFixture = filepath.Join(root, "internal", "ghagent", "testdata", base+".beat.yaml")
	}

	flowFixture, cleanup, err := materializeJobFlowFixture(beatFixture, job, route.World)
	if err != nil {
		return RunResult{}, err
	}
	defer cleanup()

	report, err := testrunner.RunFlows(ctx, appPath, flowFixture, testrunner.FlowOptions{})
	if err != nil {
		return RunResult{}, fmt.Errorf("ghagent: run story %q: %w", route.Story, err)
	}
	if report.Passed < 1 {
		return RunResult{}, fmt.Errorf("ghagent: story %q ran no passing turn (passed=%d failed=%d): %s", route.Story, report.Passed, report.Failed, summarizeFlowFailures(report))
	}

	turns := 0
	for _, r := range report.Results {
		turns += len(r.Turns)
	}
	return RunResult{
		RunURL:     "kitsoki://run/" + job.JobID,
		FinalState: "passed",
		Turns:      turns,
		// This spawn path always points RunFlows at a beat fixture whose
		// host.agent.task/ask/decide handlers are inline stubs (see
		// internal/ghagent/testdata/*.beat.yaml) — no LLM ran and no code
		// changed, regardless of how many turns "passed". Real dispatch
		// (task 2 of gh-agent-honest-issues.md) replaces this path for
		// routes with a registered realDispatchPlans entry.
		Stubbed:    true,
		StubReason: "issue route ran the beat-fixture stub (no real dispatch plan registered for this story yet)",
	}, nil
}

// RunPRRebaseBeat is the production PR conflict-resolution path. It delegates
// live git/gh work to host.git so tests can replace the CLI seam.
func RunPRRebaseBeat(ctx context.Context, job *jobs.GHJob) (RunResult, error) {
	res, err := host.GitVCSHandler(ctx, map[string]any{
		"op":    "pr_rebase",
		"repo":  job.Repo,
		"pr_id": job.ObjectNumber,
	})
	if err != nil {
		return RunResult{}, err
	}
	if res.Error != "" {
		return RunResult{}, errors.New(res.Error)
	}
	summary, _ := res.Data["summary"].(string)
	if strings.TrimSpace(summary) == "" {
		summary = fmt.Sprintf("Rebased PR #%s onto its base branch and pushed the updated head.", job.ObjectNumber)
	}
	return RunResult{
		RunURL:     "kitsoki://run/" + job.JobID,
		FinalState: "pr_rebased",
		Turns:      1,
		Summary:    summary,
	}, nil
}

// RunPRStatusBeat is the production PR-status path. It reads the actual PR
// status through host.git/gh and returns a human-readable summary for the
// rolling GitHub comment.
func RunPRStatusBeat(ctx context.Context, job *jobs.GHJob) (RunResult, error) {
	res, err := host.GitVCSHandler(ctx, map[string]any{
		"op":    "pr_status",
		"repo":  job.Repo,
		"pr_id": job.ObjectNumber,
	})
	if err != nil {
		return RunResult{}, err
	}
	if res.Error != "" {
		return RunResult{}, errors.New(res.Error)
	}
	stateRaw, _ := res.Data["state"].(string)
	return RunResult{
		RunURL:     "kitsoki://run/" + job.JobID,
		FinalState: "pr_status_read",
		Turns:      1,
		Summary:    summarizePRStatus(job, stateRaw),
	}, nil
}

func summarizePRStatus(job *jobs.GHJob, stateRaw string) string {
	type check struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	var parsed struct {
		State             string  `json:"state"`
		StatusCheckRollup []check `json:"statusCheckRollup"`
	}
	state := strings.TrimSpace(stateRaw)
	if err := json.Unmarshal([]byte(stateRaw), &parsed); err == nil {
		if strings.TrimSpace(parsed.State) != "" {
			state = parsed.State
		}
	}
	if state == "" {
		state = "unknown"
	}
	var checks []string
	for _, c := range parsed.StatusCheckRollup {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = "check"
		}
		outcome := firstNonEmpty(c.Conclusion, c.Status, "unknown")
		checks = append(checks, fmt.Sprintf("%s=%s", name, outcome))
	}
	checkLine := "No status checks reported."
	if len(checks) > 0 {
		checkLine = "Checks: " + strings.Join(checks, ", ") + "."
	}
	return fmt.Sprintf("PR #%s status: `%s`. %s", job.ObjectNumber, state, checkLine)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func summarizeFlowFailures(report *testrunner.FlowReport) string {
	if report == nil {
		return "no report"
	}
	var parts []string
	for _, result := range report.Results {
		if result.Passed || result.Skipped {
			continue
		}
		label := filepath.Base(result.File)
		for _, turn := range result.Turns {
			for _, failure := range turn.Failures {
				parts = append(parts, label+": "+failure)
			}
		}
	}
	if len(parts) == 0 {
		return "no failure details"
	}
	return strings.Join(parts, "; ")
}

func materializeJobFlowFixture(fixturePath string, job *jobs.GHJob, routeWorld map[string]any) (string, func(), error) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		return "", func() {}, fmt.Errorf("ghagent: read flow fixture %q: %w", fixturePath, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", func() {}, fmt.Errorf("ghagent: parse flow fixture %q: %w", fixturePath, err)
	}
	initialWorld, _ := doc["initial_world"].(map[string]any)
	if initialWorld == nil {
		initialWorld = map[string]any{}
		doc["initial_world"] = initialWorld
	}
	for k, v := range jobFlowWorld(job) {
		if strings.TrimSpace(v) != "" {
			initialWorld[k] = v
		}
	}
	for k, v := range routeWorld {
		initialWorld[k] = v
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", func() {}, fmt.Errorf("ghagent: render job flow fixture: %w", err)
	}
	dir, err := os.MkdirTemp("", "kitsoki-ghagent-flow-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("ghagent: create temp flow dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, filepath.Base(fixturePath))
	if err := os.WriteFile(path, out, 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("ghagent: write job flow fixture: %w", err)
	}
	return path, cleanup, nil
}

func jobFlowWorld(job *jobs.GHJob) map[string]string {
	out := map[string]string{
		"gh_job_id":     job.JobID,
		"gh_origin_ref": job.OriginRef,
		"repo":          job.Repo,
		"thread":        job.OriginRef,
	}
	switch job.ObjectKind {
	case "pr":
		out["pr_id"] = job.ObjectNumber
		out["pr_url"] = githubObjectURL(job)
	case "issue":
		out["ticket_id"] = job.ObjectNumber
		out["ticket_url"] = githubObjectURL(job)
		for _, key := range []string{"ticket_title", "ticket_body", "ticket_source_mode", "ticket_source_ref"} {
			if value := strings.TrimSpace(job.Metadata[key]); value != "" {
				out[key] = value
			}
		}
	}
	return out
}

func githubObjectURL(job *jobs.GHJob) string {
	repo := strings.TrimSpace(job.Repo)
	number := strings.TrimSpace(job.ObjectNumber)
	if repo == "" || number == "" {
		return ""
	}
	switch job.ObjectKind {
	case "pr":
		return "https://github.com/" + repo + "/pull/" + number
	default:
		return "https://github.com/" + repo + "/issues/" + number
	}
}

// repoRoot walks up from this source file's directory to the nearest go.mod.
// Anchoring on go.mod (rather than hardcoded ../ counts) keeps the on-disk
// story + cassette paths robust to where the test binary runs from.
func repoRoot() (string, error) {
	if envRoot := strings.TrimSpace(os.Getenv("KITSOKI_REPO")); envRoot != "" {
		if _, err := os.Stat(filepath.Join(envRoot, "go.mod")); err == nil {
			return envRoot, nil
		}
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("ghagent: cannot resolve caller for repo root")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("ghagent: go.mod not found walking up from " + thisFile)
		}
		dir = parent
	}
}
