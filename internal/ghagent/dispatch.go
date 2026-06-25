package ghagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/jobs"
	"kitsoki/internal/testrunner"
)

// RunResult is the outcome of one spawned story run.
type RunResult struct {
	RunURL     string
	FinalState string // story terminal state, for the ack
	Turns      int
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
		// Re-mention: attach. Update the ack carrying the existing run_url.
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: job.Story, State: job.State, RunURL: job.RunURL}
		if d.Comments != nil && job.CommentID != "" {
			nextID, _ := d.Comments.Update(ctx, mention.Item.Number, job.CommentID,
				fmt.Sprintf("Already on it — attached to existing run for `%s`.", job.OriginRef), meta)
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
		if d.Comments != nil {
			meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, State: jobs.GHAwaitingGuidance}
			commentID, err := d.Comments.Post(ctx, mention.Item.Number,
				"I need a bit more direction before I can route this. Please add a `bug`, `feature`, or `enhancement` label, or reply with the path you want me to take.", meta)
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

	if d.Comments != nil {
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: route.Story, State: jobs.GHClaimed}
		commentID, err := d.Comments.Post(ctx, mention.Item.Number,
			fmt.Sprintf("On it — dispatching `%s` for `%s`.", route.Story, job.OriginRef), meta)
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

	if d.Comments != nil && job.CommentID != "" {
		meta := Meta{JobID: job.JobID, OriginRef: job.OriginRef, Story: route.Story, State: finalState, RunURL: job.RunURL}
		prose := fmt.Sprintf("Done — `%s` finished in state `%s` (%d turn(s)).", route.Story, result.FinalState, result.Turns)
		if spawnErr != nil {
			prose = fmt.Sprintf("Run failed: %s", spawnErr.Error())
		}
		nextID, _ := d.Comments.Update(ctx, mention.Item.Number, job.CommentID, prose, meta)
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

// RunStorySession is the default SpawnFn: it points testrunner.RunFlows at the
// route's story app.yaml + the per-job beat fixture (authored under
// internal/ghagent/testdata/<story>.beat.yaml) and asserts the story ran >=1
// turn. This is the REAL no-LLM session spawn — RunFlows builds the real machine
// and replays turns through the real state machine, every host call cassette-
// served. Returns a synthesized RunResult.
func RunStorySession(ctx context.Context, route Route, job *jobs.GHJob) (RunResult, error) {
	root, err := repoRoot()
	if err != nil {
		return RunResult{}, err
	}

	var appPath, beatFixture string
	switch route.Story {
	case StoryPRBeat:
		// The minimal pr-autopilot beat is a self-contained fixture (no story
		// app.yaml). It declares its own thin app inline-by-reference.
		appPath = filepath.Join(root, "internal", "ghagent", "testdata", "pr-beat.app.yaml")
		beatFixture = filepath.Join(root, "internal", "ghagent", "testdata", "pr-beat.beat.yaml")
	default:
		appPath = filepath.Join(root, route.Story, "app.yaml")
		base := filepath.Base(route.Story) // e.g. "bugfix"
		beatFixture = filepath.Join(root, "internal", "ghagent", "testdata", base+".beat.yaml")
	}

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
