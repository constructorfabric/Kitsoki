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
	// SpawnFn runs the mapped story for a claimed job in no-LLM posture.
	// Defaults to RunStorySession (testrunner.RunFlows-backed); injectable for
	// tests (spy / assertion).
	SpawnFn func(ctx context.Context, route Route, job *jobs.GHJob) (RunResult, error)
	// IncidentFn files an operator-facing incident for non-recoverable failures.
	// It is injected so tests stay offline and production can use host.gh.ticket.
	IncidentFn func(ctx context.Context, job *jobs.GHJob, errMsg string) (string, error)
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
		if job.State == jobs.GHAwaitingGuidance {
			if route, ok := d.Routes.Classify(mention, labels); ok {
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
	route, ok := d.Routes.Classify(mention, labels)
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

func (d *Dispatcher) dispatchRouted(ctx context.Context, mention Mention, job *jobs.GHJob, route Route) (*jobs.GHJob, error) {
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
			return nil, err
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

	spawn := d.SpawnFn
	if spawn == nil {
		spawn = RunStorySession
	}
	result, spawnErr := spawn(ctx, route, job)
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
		_ = d.Jobs.SetRunURL(ctx, job.JobID, job.JobID, result.RunURL)
		job.RunURL = result.RunURL
	}
	if err := d.Jobs.Advance(ctx, job.JobID, finalState, errMsg); err != nil {
		return nil, err
	}
	job.State = finalState
	if spawnErr != nil && d.IncidentFn != nil {
		if incidentURL, incidentErr := d.IncidentFn(ctx, job, errMsg); incidentErr == nil && strings.TrimSpace(incidentURL) != "" {
			_ = d.Jobs.SetIncidentURL(ctx, job.JobID, incidentURL)
			job.IncidentURL = incidentURL
		} else if incidentErr != nil {
			_ = d.Jobs.RecordEvent(ctx, job.JobID, "incident_failed", incidentErr.Error())
		}
	}

	if d.Comments != nil && job.CommentID != "" {
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: route.Story, State: finalState, RunURL: job.RunURL}
		prose := fmt.Sprintf("Done — `%s` finished in state `%s` (%d turn(s)).", route.Story, result.FinalState, result.Turns)
		if strings.TrimSpace(result.Summary) != "" {
			prose = result.Summary
		}
		if spawnErr != nil {
			prose = fmt.Sprintf("Run failed: %s", spawnErr.Error())
			if job.IncidentURL != "" {
				prose += "\n\nIncident: " + job.IncidentURL
			}
		}
		nextID, updateErr := d.Comments.Update(ctx, mention.Item.Number, job.CommentID, prose, meta)
		if updateErr != nil {
			_ = d.Jobs.RecordEvent(ctx, job.JobID, "comment_update_failed", updateErr.Error())
		}
		if nextID != "" && nextID != job.CommentID {
			_ = d.Jobs.SetComment(ctx, job.JobID, nextID)
			job.CommentID = nextID
		}
	}

	job, _ = d.Jobs.GetJob(ctx, job.JobID)
	return job, spawnErr
}

func publicRunURL(baseURL, jobID string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(jobID) == "" {
		return ""
	}
	return baseURL + "/run/" + jobID
}

// RunStorySession is the default SpawnFn. Issue routes point
// testrunner.RunFlows at the route's story app.yaml + the per-job beat fixture
// (authored under internal/ghagent/testdata/<story>.beat.yaml) and assert the
// story ran >=1 turn. The PR-status route reads the live PR through host.git's
// pr_status seam so the GitHub comment reports the actual PR state.
func RunStorySession(ctx context.Context, route Route, job *jobs.GHJob) (RunResult, error) {
	if route.Story == StoryPRBeat {
		return RunPRStatusBeat(ctx, job)
	}

	root, err := repoRoot()
	if err != nil {
		return RunResult{}, err
	}

	var appPath, beatFixture string
	appPath = filepath.Join(root, route.Story, "app.yaml")
	base := filepath.Base(route.Story) // e.g. "bugfix"
	beatFixture = filepath.Join(root, "internal", "ghagent", "testdata", base+".beat.yaml")

	flowFixture, cleanup, err := materializeJobFlowFixture(beatFixture, job)
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

func materializeJobFlowFixture(fixturePath string, job *jobs.GHJob) (string, func(), error) {
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
