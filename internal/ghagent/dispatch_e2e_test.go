package ghagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

// stubGitHubInboxAPI installs a fake GitHub Search API for inbox polling.
// issuesJSON/prsJSON are JSON arrays used as search/issues `items`.
func stubGitHubInboxAPI(t *testing.T, issuesJSON, prsJSON string) func() {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "is:issue"):
			writeRawSearchItemsForDispatchTest(t, w, issuesJSON)
		case strings.Contains(q, "is:pr"):
			writeRawSearchItemsForDispatchTest(t, w, prsJSON)
		default:
			t.Fatalf("unexpected search query: %q", q)
		}
	}))
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	return func() {
		restoreAPI()
		srv.Close()
	}
}

func TestCommentUpdateRetriesWithoutPostingDuplicate(t *testing.T) {
	ctx := context.Background()
	var ops []string
	calls := 0
	comments := &CommentStore{
		Repo:              "o/r",
		MaxUpdateAttempts: 2,
		RetryDelay:        time.Millisecond,
		Exec: func(_ context.Context, args map[string]any) (host.Result, error) {
			op, _ := args["op"].(string)
			ops = append(ops, op)
			calls++
			return host.Result{Error: "temporary edit failure"}, nil
		},
	}
	_, err := comments.Update(ctx, "42", "https://github.com/o/r/issues/42#issuecomment-1", "body", Meta{JobID: "j1"})
	if err == nil {
		t.Fatal("Update succeeded unexpectedly")
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
	for _, op := range ops {
		if op != "comment_edit" {
			t.Fatalf("Update posted a duplicate instead of editing only: ops=%v", ops)
		}
	}
}

func TestCommentPostRecoversExistingStatusComment(t *testing.T) {
	ctx := context.Background()
	var ops []string
	comments := &CommentStore{
		Repo:              "o/r",
		MaxUpdateAttempts: 1,
		Exec: func(_ context.Context, args map[string]any) (host.Result, error) {
			op, _ := args["op"].(string)
			ops = append(ops, op)
			switch op {
			case "get":
				return host.Result{Data: map[string]any{
					"comments": []any{
						map[string]any{
							"id":   "123",
							"body": "old\n\n" + RenderMeta(Meta{JobID: "job-1", OriginRef: "github:o/r/issue/42"}),
						},
					},
				}}, nil
			case "comment_edit":
				return host.Result{Data: map[string]any{"comment_id": "123"}}, nil
			case "comment":
				t.Fatalf("Post should edit the recovered status comment, not post a duplicate")
			}
			return host.Result{}, nil
		},
	}
	id, err := comments.Post(ctx, "42", "new body", Meta{JobID: "job-1", OriginRef: "github:o/r/issue/42"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if id != "123" {
		t.Fatalf("comment id = %q, want recovered id 123", id)
	}
	if strings.Join(ops, ",") != "get,comment_edit" {
		t.Fatalf("ops = %v, want get then comment_edit", ops)
	}
}

func TestDispatchFailureFilesIncident(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{Kind: "issue", Number: "42", Title: "@kitsoki bug: broken"},
		Repo: "o/r", OriginRef: "github:o/r/issue/42", Trigger: DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/42#issuecomment-1"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-fail",
		PublicBaseURL: "https://example.invalid",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			return RunResult{}, errors.New("boom")
		},
		IncidentFn: func(_ context.Context, job *jobs.GHJob, errMsg string) (string, error) {
			if job.JobID == "" || !strings.Contains(errMsg, "boom") {
				t.Fatalf("bad incident input: job=%+v err=%q", job, errMsg)
			}
			return "https://github.com/o/r/issues/500", nil
		},
	}
	job, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err == nil {
		t.Fatal("Dispatch succeeded unexpectedly")
	}
	if job.State != jobs.GHFailed {
		t.Fatalf("State=%q, want failed", job.State)
	}
	if job.IncidentURL != "https://github.com/o/r/issues/500" {
		t.Fatalf("IncidentURL=%q", job.IncidentURL)
	}
	rec.mu.Lock()
	last := rec.bodies[len(rec.bodies)-1]
	rec.mu.Unlock()
	if !strings.Contains(last, "Incident: https://github.com/o/r/issues/500") {
		t.Fatalf("final comment missing incident URL:\n%s", last)
	}
}

func TestDispatchReplayCassetteMissFailsClosed(t *testing.T) {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	mention := Mention{
		Item:      host.GitHubInboxItem{Kind: "issue", Number: "105", Title: "@kitsoki bug: replay cassette miss"},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/105",
		Trigger:   DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/105#issuecomment-1"}
	plan := realDispatchPlans["stories/bugfix"]
	plan.CassetteRelPath = filepath.Join("stories", "bugfix", "cassettes", "missing-replay.cassette.yaml")
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-replay-miss",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(spawnCtx context.Context, route Route, job *jobs.GHJob) (RunResult, error) {
			return runRealDispatch(spawnCtx, root, route, job, plan)
		},
	}

	job, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err == nil {
		t.Fatal("Dispatch succeeded despite missing replay cassette")
	}
	if !strings.Contains(err.Error(), "missing-replay.cassette.yaml") {
		t.Fatalf("error should name the missing replay cassette, got: %v", err)
	}
	if job.State != jobs.GHFailed {
		t.Fatalf("State=%q, want failed", job.State)
	}
	if !strings.Contains(job.ErrMsg, "missing-replay.cassette.yaml") {
		t.Fatalf("ErrMsg should name the missing replay cassette, got: %q", job.ErrMsg)
	}
	worktree := filepath.Join(root, jobWorktreeRelDir(job.JobID))
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatalf("replay cassette miss must not materialize a live worktree at %s: %v", worktree, statErr)
	}
	rec.mu.Lock()
	last := rec.bodies[len(rec.bodies)-1]
	rec.mu.Unlock()
	if !strings.Contains(last, "Run failed:") || !strings.Contains(last, "missing-replay.cassette.yaml") {
		t.Fatalf("final comment should surface the replay failure:\n%s", last)
	}
	if strings.Contains(last, "Done") {
		t.Fatalf("replay cassette miss must not render a done comment:\n%s", last)
	}
	events, eventsErr := store.Events(ctx, job.JobID)
	if eventsErr != nil {
		t.Fatalf("Events: %v", eventsErr)
	}
	if !hasEvent(events, jobs.GHFailed) {
		t.Fatalf("events missing failed transition: %+v", events)
	}
	if !hasEvent(events, "harness") {
		t.Fatalf("events missing replay harness record: %+v", events)
	}
}

func TestDispatchInitialAckFailureMarksJobFailed(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{Kind: "pr", Number: "56", Title: "@kitsoki resolve the conflicts"},
		Repo: "o/r", OriginRef: "github:o/r/pr/56", Trigger: DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	spawnCalls := 0
	incidentCalls := 0
	d := &Dispatcher{
		Jobs:   store,
		Routes: DefaultLabelStoryMap(),
		Comments: &CommentStore{Repo: "o/r", Exec: func(_ context.Context, args map[string]any) (host.Result, error) {
			if op, _ := args["op"].(string); op == "get" {
				return host.Result{Data: map[string]any{"comments": []any{}}}, nil
			}
			return host.Result{Error: "Bad credentials"}, nil
		}},
		WorkerID:      "worker-pr",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			spawnCalls++
			return RunResult{FinalState: "should-not-run", Turns: 1}, nil
		},
		IncidentFn: func(_ context.Context, job *jobs.GHJob, errMsg string) (string, error) {
			incidentCalls++
			if job.State != jobs.GHFailed {
				t.Fatalf("incident saw job state %q, want failed", job.State)
			}
			if !strings.Contains(errMsg, "Bad credentials") {
				t.Fatalf("incident errMsg = %q, want Bad credentials", errMsg)
			}
			return "https://github.com/o/r/issues/500", nil
		},
	}
	job, err := d.Dispatch(ctx, mention, nil)
	if err == nil {
		t.Fatal("Dispatch succeeded despite failed initial ack")
	}
	if spawnCalls != 0 {
		t.Fatalf("spawn ran %d time(s) after initial ack failed", spawnCalls)
	}
	if incidentCalls != 1 {
		t.Fatalf("incident calls = %d, want 1", incidentCalls)
	}
	got, getErr := store.GetByOriginRef(ctx, "github:o/r/pr/56")
	if getErr != nil {
		t.Fatalf("GetByOriginRef: %v", getErr)
	}
	if got.JobID != job.JobID {
		t.Fatalf("job id = %q, want %q", got.JobID, job.JobID)
	}
	if got.State != jobs.GHFailed {
		t.Fatalf("State = %q, want failed; job=%+v", got.State, got)
	}
	if got.Story != StoryPRRebase {
		t.Fatalf("Story = %q, want %q", got.Story, StoryPRRebase)
	}
	if !strings.Contains(got.ErrMsg, "Bad credentials") {
		t.Fatalf("ErrMsg = %q, want Bad credentials", got.ErrMsg)
	}
	if got.CommentID != "" {
		t.Fatalf("CommentID = %q, want empty after failed post", got.CommentID)
	}
	if got.IncidentURL != "https://github.com/o/r/issues/500" {
		t.Fatalf("IncidentURL = %q", got.IncidentURL)
	}
	events, eventsErr := store.Events(ctx, got.JobID)
	if eventsErr != nil {
		t.Fatalf("Events: %v", eventsErr)
	}
	if !hasEvent(events, "comment_ack_failed") {
		t.Fatalf("events missing comment_ack_failed: %+v", events)
	}
	if !hasEvent(events, jobs.GHFailed) {
		t.Fatalf("events missing failed transition: %+v", events)
	}
}

func TestDispatchPersistsRunAssets(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item:      host.GitHubInboxItem{Kind: "issue", Number: "77", Title: "@kitsoki bug: broken"},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/77",
		Trigger:   DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	d := &Dispatcher{
		Jobs:     store,
		Routes:   DefaultLabelStoryMap(),
		WorkerID: "worker-assets",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			return RunResult{
				RunURL:     "kitsoki://run/assets",
				FinalState: "done",
				Turns:      1,
				Summary:    "done with assets",
				Assets: []RunAsset{
					{Name: "fix-report.md", MimeType: "text/markdown", Data: []byte("# Fix report\n")},
					{Name: "fix.patch", MimeType: "text/x-diff", Data: []byte("diff --git a/a b/a\n")},
				},
			}, nil
		},
	}
	job, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("State=%q, want done", job.State)
	}
	assets, err := store.ListAssets(ctx, job.JobID)
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets len=%d, want 2: %+v", len(assets), assets)
	}
	patch, mimeType, err := store.GetAssetData(ctx, job.JobID, "fix.patch")
	if err != nil {
		t.Fatalf("GetAssetData(fix.patch): %v", err)
	}
	if mimeType != "text/x-diff" || !strings.Contains(string(patch), "diff --git") {
		t.Fatalf("unexpected patch asset mime=%q body=%q", mimeType, string(patch))
	}
}

func TestDispatchTerminalPRStatusJobReroutesToRebaseRequest(t *testing.T) {
	ctx := context.Background()
	store := newGHJobStore(t)
	initial, won, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/pr/56",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "56",
	}, "worker-old")
	if err != nil {
		t.Fatalf("Claim initial: %v", err)
	}
	if !won {
		t.Fatal("initial claim did not win")
	}
	if err := store.SetStory(ctx, initial.JobID, StoryPRBeat); err != nil {
		t.Fatalf("SetStory: %v", err)
	}
	if err := store.Advance(ctx, initial.JobID, jobs.GHDone, ""); err != nil {
		t.Fatalf("Advance done: %v", err)
	}

	var spawned Route
	rec := &recordingComments{commentID: "https://github.com/o/r/pull/56#issuecomment-1"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-new",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(_ context.Context, route Route, _ *jobs.GHJob) (RunResult, error) {
			spawned = route
			return RunResult{FinalState: "pr_rebased", Turns: 1, Summary: "rebased"}, nil
		},
	}
	job, err := d.Dispatch(ctx, Mention{
		Item: host.GitHubInboxItem{Kind: "pr", Number: "56", Title: "@kitsoki resolve the merge conflicts"},
		Repo: "o/r", OriginRef: "github:o/r/pr/56", Trigger: DefaultMentionTrigger,
	}, nil)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if spawned.Story != StoryPRRebase {
		t.Fatalf("spawned story=%q, want %q", spawned.Story, StoryPRRebase)
	}
	if job.JobID != initial.JobID {
		t.Fatalf("reroute should reuse existing job id %q, got %q", initial.JobID, job.JobID)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("state=%q, want done", job.State)
	}
	if job.Story != StoryPRRebase {
		t.Fatalf("job story=%q, want %q", job.Story, StoryPRRebase)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) == 0 || !strings.Contains(rec.bodies[len(rec.bodies)-1], "rebased") {
		t.Fatalf("final comment bodies=%v, want rebased summary", rec.bodies)
	}
}

func TestDispatchFailedPRRebaseRetriesRebaseRequest(t *testing.T) {
	ctx := context.Background()
	store := newGHJobStore(t)
	initial, won, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/pr/56",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "56",
	}, "worker-old")
	if err != nil {
		t.Fatalf("Claim initial: %v", err)
	}
	if !won {
		t.Fatal("initial claim did not win")
	}
	if err := store.SetStory(ctx, initial.JobID, StoryPRRebase); err != nil {
		t.Fatalf("SetStory: %v", err)
	}
	if err := store.Advance(ctx, initial.JobID, jobs.GHFailed, "previous auth failure"); err != nil {
		t.Fatalf("Advance failed: %v", err)
	}

	spawnCalls := 0
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: (&recordingComments{commentID: "https://github.com/o/r/pull/56#issuecomment-1"}).handler, Repo: "o/r"},
		WorkerID:      "worker-new",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(_ context.Context, route Route, _ *jobs.GHJob) (RunResult, error) {
			spawnCalls++
			if route.Story != StoryPRRebase {
				t.Fatalf("route story=%q, want %q", route.Story, StoryPRRebase)
			}
			return RunResult{FinalState: "pr_rebased", Turns: 1, Summary: "rebased"}, nil
		},
	}
	job, err := d.Dispatch(ctx, Mention{
		Item: host.GitHubInboxItem{Kind: "pr", Number: "56", Title: "@kitsoki resolve the merge conflicts"},
		Repo: "o/r", OriginRef: "github:o/r/pr/56", Trigger: DefaultMentionTrigger,
	}, nil)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if spawnCalls != 1 {
		t.Fatalf("spawnCalls=%d, want 1", spawnCalls)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("state=%q, want done", job.State)
	}
}

// TestDispatchRepeatedTerminalPRFailureFilesOnlyOneIncident guards against a
// runaway-incident regression: shouldRerunTerminalPR deliberately re-dispatches
// the same job on every re-mention of a persistently-failing PR (e.g. a rebase
// that can never succeed). Each re-dispatch must NOT file a fresh operator
// incident once one is already on record for the job — otherwise a single
// stuck PR spams the incident tracker with one duplicate issue per poll cycle.
func TestDispatchRepeatedTerminalPRFailureFilesOnlyOneIncident(t *testing.T) {
	ctx := context.Background()
	store := newGHJobStore(t)
	mention := Mention{
		Item: host.GitHubInboxItem{Kind: "pr", Number: "56", Title: "@kitsoki resolve the merge conflicts"},
		Repo: "o/r", OriginRef: "github:o/r/pr/56", Trigger: DefaultMentionTrigger,
	}
	incidentCalls := 0
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: (&recordingComments{commentID: "https://github.com/o/r/pull/56#issuecomment-1"}).handler, Repo: "o/r"},
		WorkerID:      "worker-repeat",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			return RunResult{}, errors.New("rebase always fails: pr already merged")
		},
		IncidentFn: func(_ context.Context, job *jobs.GHJob, _ string) (string, error) {
			incidentCalls++
			return "https://github.com/o/r/issues/900", nil
		},
	}
	for i := 0; i < 5; i++ {
		job, err := d.Dispatch(ctx, mention, nil)
		if err == nil {
			t.Fatalf("iteration %d: Dispatch succeeded unexpectedly", i)
		}
		if job.State != jobs.GHFailed {
			t.Fatalf("iteration %d: state=%q, want failed", i, job.State)
		}
		if job.IncidentURL != "https://github.com/o/r/issues/900" {
			t.Fatalf("iteration %d: IncidentURL=%q", i, job.IncidentURL)
		}
	}
	if incidentCalls != 1 {
		t.Fatalf("incidentCalls=%d, want 1 — repeated re-dispatch of the same failing PR must not refile", incidentCalls)
	}
}

func TestDispatchPRRebaseRequestCancellationDoesNotStrandRunningJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/pull/56#issuecomment-1"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-cancelled",
		PublicBaseURL: "https://agent.example",
		SpawnFn: func(spawnCtx context.Context, route Route, _ *jobs.GHJob) (RunResult, error) {
			if route.Story != StoryPRRebase {
				t.Fatalf("route story=%q, want %q", route.Story, StoryPRRebase)
			}
			cancel()
			<-spawnCtx.Done()
			return RunResult{}, spawnCtx.Err()
		},
	}

	job, err := d.Dispatch(ctx, Mention{
		Item: host.GitHubInboxItem{Kind: "pr", Number: "56", Title: "@kitsoki resolve the merge conflicts"},
		Repo: "o/r", OriginRef: "github:o/r/pr/56", Trigger: DefaultMentionTrigger,
	}, nil)
	if err == nil {
		t.Fatal("Dispatch succeeded despite cancelled pr-rebase context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dispatch error = %v, want context.Canceled", err)
	}
	if job == nil {
		var getErr error
		job, getErr = store.GetByOriginRef(context.Background(), "github:o/r/pr/56")
		if getErr != nil {
			t.Fatalf("GetByOriginRef: %v", getErr)
		}
	}
	got, getErr := store.GetJob(context.Background(), job.JobID)
	if getErr != nil {
		t.Fatalf("GetJob: %v", getErr)
	}
	if got.Story != StoryPRRebase {
		t.Fatalf("Story=%q, want %q", got.Story, StoryPRRebase)
	}
	if got.State != jobs.GHFailed {
		t.Fatalf("State=%q, want failed so the run page/API do not strand an active job; job=%+v", got.State, got)
	}
	if !strings.Contains(got.ErrMsg, "context canceled") {
		t.Fatalf("ErrMsg=%q, want context canceled", got.ErrMsg)
	}
	events, eventsErr := store.Events(context.Background(), got.JobID)
	if eventsErr != nil {
		t.Fatalf("Events: %v", eventsErr)
	}
	if !hasEvent(events, jobs.GHFailed) {
		t.Fatalf("events missing failed terminal transition: %+v", events)
	}
}

// recordingComments is a host.Handler bound as the CommentStore.Exec seam. It
// captures every op=comment body (so the test can assert the fenced metadata
// block) and returns a synthetic comment id. This is the DI seam in place of a
// real gh — no network, no cassette file needed.
type recordingComments struct {
	mu        sync.Mutex
	ops       []string
	bodies    []string
	commentID string
}

func (r *recordingComments) handler(_ context.Context, args map[string]any) (host.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, _ := args["op"].(string)
	r.ops = append(r.ops, op)
	if body, _ := args["body"].(string); body != "" {
		r.bodies = append(r.bodies, body)
	}
	return host.Result{Data: map[string]any{"comment_id": r.commentID}}, nil
}

func newGHJobStore(t *testing.T) *jobs.GHJobStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	store.DataDir = t.TempDir()
	return store
}

func TestMaterializeJobFlowFixtureOverlaysJobWorld(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "flow.yaml")
	if err := os.WriteFile(fixture, []byte(`test_kind: flow
initial_world:
  gh_job_id: job-stub
  gh_origin_ref: github:o/r/pr/7
  repo: o/r
  pr_id: "7"
turns: []
`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	path, cleanup, err := materializeJobFlowFixture(fixture, &jobs.GHJob{
		JobID:        "job-live",
		OriginRef:    "github:o/r/pr/77",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "77",
	}, nil)
	if err != nil {
		t.Fatalf("materializeJobFlowFixture: %v", err)
	}
	defer cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read materialized fixture: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse materialized fixture: %v", err)
	}
	initialWorld, _ := doc["initial_world"].(map[string]any)
	for k, want := range map[string]string{
		"gh_job_id":     "job-live",
		"gh_origin_ref": "github:o/r/pr/77",
		"repo":          "o/r",
		"pr_id":         "77",
		"pr_url":        "https://github.com/o/r/pull/77",
		"thread":        "github:o/r/pr/77",
	} {
		if got := initialWorld[k]; got != want {
			t.Fatalf("initial_world[%s] = %v, want %q\n%s", k, got, want, string(raw))
		}
	}
}

func TestRepoRootPrefersKitsokiRepoEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fake\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	t.Setenv("KITSOKI_REPO", dir)

	got, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	if got != dir {
		t.Fatalf("repoRoot = %q, want KITSOKI_REPO %q", got, dir)
	}
}

// TestConcurrentDispatch_NoAppDirCrossContamination drives two DIFFERENT
// stories' RunStorySession spawns concurrently in the same process (task 1.2,
// docs/proposals/gh-agent-honest-issues.md). Before the appDirLoadMu fix
// (internal/testrunner/flows.go), two concurrent RunFlows calls racing to
// setenv KITSOKI_APP_DIR before their own app.Load could cross-contaminate:
// whichever call's Load ran while the OTHER job's app dir was published could
// silently resolve `${KITSOKI_APP_DIR}`-templated fields (e.g.
// meta_modes[*].cwd) against the wrong story's directory. Run with -race to
// also catch any unsynchronized access to the global env var directly.
func TestConcurrentDispatch_NoAppDirCrossContamination(t *testing.T) {
	ctx := context.Background()
	routes := []Route{
		DefaultLabelStoryMap()["bug"],     // stories/bugfix
		DefaultLabelStoryMap()["feature"], // stories/dev-story
	}

	results := make([]RunResult, len(routes))
	errs := make([]error, len(routes))
	var wg sync.WaitGroup
	for i, route := range routes {
		i, route := i, route
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := &jobs.GHJob{
				JobID:        fmt.Sprintf("job-%d", i),
				OriginRef:    fmt.Sprintf("github:o/r/issue/%d", 100+i),
				Repo:         "o/r",
				ObjectKind:   "issue",
				ObjectNumber: fmt.Sprintf("%d", 100+i),
			}
			results[i], errs[i] = RunStorySession(ctx, route, job)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("route %d (%s) RunStorySession: %v", i, routes[i].Story, err)
		}
	}
	for i, res := range results {
		wantRunURL := fmt.Sprintf("kitsoki://run/job-%d", i)
		if res.RunURL != wantRunURL {
			t.Errorf("route %d (%s) RunURL = %q, want %q — job identity crossed streams", i, routes[i].Story, res.RunURL, wantRunURL)
		}
		if res.Turns < 1 {
			t.Errorf("route %d (%s) ran %d turns, want >=1 (app dir may have resolved against the other job's story)", i, routes[i].Story, res.Turns)
		}
		// stories/bugfix has a registered real-dispatch plan (task 2) and runs
		// unstubbed by default (replay harness, no LLM); stories/dev-story has
		// no plan yet and still runs the honest beat-fixture stub. Proving
		// BOTH shapes stay concurrency-safe in one process is the point of
		// this test — a real-dispatch job loading a real cassette must not
		// cross-contaminate a concurrently-dispatched stub job's app dir.
		wantStubbed := routes[i].Story != "stories/bugfix"
		if res.Stubbed != wantStubbed {
			t.Errorf("route %d (%s) Stubbed = %v, want %v", i, routes[i].Story, res.Stubbed, wantStubbed)
		}
		if routes[i].Story == "stories/bugfix" && res.Worktree == "" {
			t.Errorf("route %d (%s) real dispatch recorded no worktree path", i, routes[i].Story)
		}
	}
}

// TestDispatch_MentionToAckLoop drives the FULL @kitsoki loop end-to-end across
// package boundaries: cliExec-stubbed ingress -> FilterMentions -> Classify ->
// Claim (SQLite) -> Dispatcher -> a REAL no-LLM story spawn via
// testrunner.RunFlows -> rolling-status ack comment. Fully offline, zero LLM,
// zero network.
func TestDispatch_MentionToAckLoop(t *testing.T) {
	ctx := context.Background()

	issuesJSON := `[{"number":42,"title":"@kitsoki please fix the crash","assignees":[{"login":"alice"}],"url":"https://github.com/o/r/issues/42"}]`
	restore := stubGitHubInboxAPI(t, issuesJSON, `[]`)
	defer restore()

	// Real ingress: ListGitHubInboxItems uses the native GitHub Search API.
	items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo: "o/r", IncludeIssues: true, IncludePRs: true,
	})
	if err != nil {
		t.Fatalf("ListGitHubInboxItems: %v", err)
	}
	mentions := FilterMentions(items, "o/r", DefaultMentionTrigger)
	if len(mentions) != 1 {
		t.Fatalf("FilterMentions: want 1, got %d", len(mentions))
	}
	if got, want := mentions[0].OriginRef, "github:o/r/issue/42"; got != want {
		t.Fatalf("OriginRef = %q, want %q", got, want)
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/42#issuecomment-1"}
	d := &Dispatcher{
		Jobs:     store,
		Routes:   DefaultLabelStoryMap(),
		Comments: &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID: "worker-test",
		SpawnFn:  RunStorySession, // the REAL spawn through testrunner.RunFlows
	}

	job, err := d.Dispatch(ctx, mentions[0], []string{"bug"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Assertion A: the gh_jobs row advanced to done with the bug story routed.
	got, err := store.GetByOriginRef(ctx, "github:o/r/issue/42")
	if err != nil {
		t.Fatalf("GetByOriginRef: %v", err)
	}
	if got.Story != "stories/bugfix" {
		t.Errorf("Story = %q, want stories/bugfix", got.Story)
	}
	if got.State != jobs.GHDone {
		t.Errorf("State = %q, want %q", got.State, jobs.GHDone)
	}

	// Assertion B: the mapped story actually ran >= 1 turn through the real
	// machine. Dispatch synthesises a run URL only on a successful spawn.
	if got.RunURL == "" {
		t.Errorf("RunURL empty — story spawn did not complete")
	}

	// Assertion C: the ack comment body carries the fenced ```kitsoki block and
	// host.GHParseMetadata round-trips job_id + origin_ref + story + run_url.
	rec.mu.Lock()
	bodies := append([]string(nil), rec.bodies...)
	ops := append([]string(nil), rec.ops...)
	rec.mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("want >=2 ack comments (post + update), got %d", len(bodies))
	}
	if !containsString(ops, "comment_edit") {
		t.Fatalf("final status should edit the first comment, ops=%v", ops)
	}
	last := bodies[len(bodies)-1]
	meta := host.GHParseMetadata(last)
	if meta == nil {
		t.Fatalf("no ```kitsoki block in final ack body:\n%s", last)
	}
	if meta["job_id"] != job.JobID {
		t.Errorf("meta job_id = %v, want %s", meta["job_id"], job.JobID)
	}
	if meta["origin_ref"] != "github:o/r/issue/42" {
		t.Errorf("meta origin_ref = %v", meta["origin_ref"])
	}
	if meta["story"] != "stories/bugfix" {
		t.Errorf("meta story = %v", meta["story"])
	}
	if meta["run_url"] != got.RunURL {
		t.Errorf("meta run_url = %v, want %s", meta["run_url"], got.RunURL)
	}

	// Assertion E (real dispatch, gh-agent-honest-issues.md task 2): bugfix now
	// has a registered real-dispatch plan, so this route runs the REAL
	// machine end-to-end via the replay harness (a recorded cassette, no
	// LLM) instead of the beat-fixture stub — the ack carries a real summary,
	// never the task-0/1 "acknowledged — pipeline not yet enabled" stub
	// prose, and the job's state=done metadata reflects an actual completed
	// run, not a synthesized "Done — ..." string over stub data.
	if strings.Contains(last, "acknowledged — pipeline not yet enabled for this route") {
		t.Fatalf("real-dispatch run's ack must not carry the stub-path honesty prose:\n%s", last)
	}
	if !strings.Contains(last, "Ran `stories/bugfix` end-to-end via the replay harness") {
		t.Fatalf("real-dispatch run's ack missing the real-dispatch summary:\n%s", last)
	}
	assets, assetErr := store.ListAssets(ctx, got.JobID)
	if assetErr != nil {
		t.Fatalf("ListAssets: %v", assetErr)
	}
	assetNames := make(map[string]bool, len(assets))
	for _, asset := range assets {
		assetNames[asset.Name] = true
	}
	if !assetNames["fix-report.md"] {
		t.Fatalf("real-dispatch assets = %+v, want fix-report.md", assets)
	}
	reportData, reportMime, reportErr := store.GetAssetData(ctx, got.JobID, "fix-report.md")
	if reportErr != nil {
		t.Fatalf("GetAssetData(fix-report.md): %v", reportErr)
	}
	if reportMime != "text/markdown" || !strings.Contains(string(reportData), "Ran `stories/bugfix` end-to-end via the replay harness") {
		t.Fatalf("unexpected fix-report asset mime=%q body=%q", reportMime, string(reportData))
	}

	// Assertion D: idempotency. A second Dispatch of the same mention ATTACHES
	// (won=false) and does NOT respawn the story.
	spawnCalls := 0
	d.SpawnFn = func(ctx context.Context, route Route, j *jobs.GHJob) (RunResult, error) {
		spawnCalls++
		return RunStorySession(ctx, route, j)
	}
	job2, err := d.Dispatch(ctx, mentions[0], []string{"bug"})
	if err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}
	if spawnCalls != 0 {
		t.Errorf("re-mention respawned the story %d time(s); want 0", spawnCalls)
	}
	if job2.JobID != job.JobID {
		t.Errorf("re-mention minted a new job %q; want %q", job2.JobID, job.JobID)
	}
	if job2.CommentID != job.CommentID {
		t.Errorf("re-mention comment id drift: %q vs %q", job2.CommentID, job.CommentID)
	}
}

func TestDispatch_UnclassifiedMentionPostsGuidance(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{
			Kind:   "issue",
			Number: "99",
			Title:  "@kitsoki please handle this broad initiative",
		},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/99",
		Trigger:   DefaultMentionTrigger,
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/99#issuecomment-2"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-guidance",
		PublicBaseURL: "https://kitsoki-test.slothattax.me",
		SpawnFn: func(ctx context.Context, route Route, j *jobs.GHJob) (RunResult, error) {
			t.Fatalf("ambiguous mention should park for guidance, not spawn route %+v", route)
			return RunResult{}, nil
		},
	}

	job, err := d.Dispatch(ctx, mention, []string{"epic"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if job.State != jobs.GHAwaitingGuidance {
		t.Fatalf("State = %q, want %q", job.State, jobs.GHAwaitingGuidance)
	}
	if job.Story != "" {
		t.Fatalf("Story = %q, want empty while awaiting guidance", job.Story)
	}
	if job.CommentID == "" {
		t.Fatal("guidance comment id was not stored")
	}
	if !strings.HasPrefix(job.RunURL, "https://kitsoki-test.slothattax.me/run/") {
		t.Fatalf("RunURL = %q, want public run URL", job.RunURL)
	}
	rec.mu.Lock()
	ops := append([]string(nil), rec.ops...)
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if strings.Join(ops, ",") != "get,comment" {
		t.Fatalf("guidance should check for an existing status comment then post once, ops=%v", ops)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "need a bit more direction") {
		t.Fatalf("guidance body missing expected prose:\n%v", bodies)
	}
	if !strings.Contains(bodies[0], job.RunURL) {
		t.Fatalf("guidance body missing run URL %q:\n%s", job.RunURL, bodies[0])
	}
	meta := host.GHParseMetadata(bodies[0])
	if meta == nil {
		t.Fatalf("guidance body missing metadata:\n%s", bodies[0])
	}
	if meta["state"] != jobs.GHAwaitingGuidance {
		t.Fatalf("meta state = %v, want %s", meta["state"], jobs.GHAwaitingGuidance)
	}
	if meta["origin_ref"] != "github:o/r/issue/99" {
		t.Fatalf("meta origin_ref = %v", meta["origin_ref"])
	}
	if meta["run_url"] != job.RunURL {
		t.Fatalf("meta run_url = %v, want %s", meta["run_url"], job.RunURL)
	}
}

func TestDispatch_UnlabelledMentionPostsGuidance(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{
			Kind:   "issue",
			Number: "100",
			Title:  "@kitsoki please take a look",
		},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/100",
		Trigger:   DefaultMentionTrigger,
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/100#issuecomment-3"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-guidance-unlabelled",
		PublicBaseURL: "https://kitsoki-test.slothattax.me",
		SpawnFn: func(ctx context.Context, route Route, j *jobs.GHJob) (RunResult, error) {
			t.Fatalf("unlabelled mention should ask guidance, not spawn route %+v", route)
			return RunResult{}, nil
		},
	}

	job, err := d.Dispatch(ctx, mention, nil)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if job.State != jobs.GHAwaitingGuidance {
		t.Fatalf("State = %q, want %q", job.State, jobs.GHAwaitingGuidance)
	}
	if !strings.HasPrefix(job.RunURL, "https://kitsoki-test.slothattax.me/run/") {
		t.Fatalf("RunURL = %q, want public run URL", job.RunURL)
	}
	rec.mu.Lock()
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "need a bit more direction") {
		t.Fatalf("guidance body missing expected prose:\n%v", bodies)
	}
	if !strings.Contains(bodies[0], job.RunURL) {
		t.Fatalf("guidance body missing run URL %q:\n%s", job.RunURL, bodies[0])
	}
}

func TestDispatch_AwaitingGuidanceCanResumeWithRoutingSignal(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{
			Kind:   "issue",
			Number: "101",
			Title:  "@kitsoki please handle this",
		},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/101",
		Trigger:   DefaultMentionTrigger,
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/101#issuecomment-4"}
	spawnCalls := 0
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-guidance-resume",
		PublicBaseURL: "https://kitsoki-test.slothattax.me",
		SpawnFn: func(ctx context.Context, route Route, j *jobs.GHJob) (RunResult, error) {
			spawnCalls++
			if route.Story != "stories/bugfix" {
				t.Fatalf("resumed route Story=%q, want stories/bugfix", route.Story)
			}
			return RunResult{RunURL: "kitsoki://run/" + j.JobID, FinalState: "passed", Turns: 1}, nil
		},
	}

	first, err := d.Dispatch(ctx, mention, nil)
	if err != nil {
		t.Fatalf("initial Dispatch: %v", err)
	}
	if first.State != jobs.GHAwaitingGuidance {
		t.Fatalf("initial State=%q, want %q", first.State, jobs.GHAwaitingGuidance)
	}
	if spawnCalls != 0 {
		t.Fatalf("initial guidance path spawned %d time(s)", spawnCalls)
	}

	resumed, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err != nil {
		t.Fatalf("resume Dispatch: %v", err)
	}
	if resumed.JobID != first.JobID {
		t.Fatalf("resume minted new job %q, want %q", resumed.JobID, first.JobID)
	}
	if spawnCalls != 1 {
		t.Fatalf("resume spawned %d time(s), want 1", spawnCalls)
	}
	if resumed.State != jobs.GHDone {
		t.Fatalf("resumed State=%q, want %q", resumed.State, jobs.GHDone)
	}
	if resumed.Story != "stories/bugfix" {
		t.Fatalf("resumed Story=%q, want stories/bugfix", resumed.Story)
	}
	if resumed.CommentID != first.CommentID {
		t.Fatalf("comment drifted to %q, want %q", resumed.CommentID, first.CommentID)
	}

	rec.mu.Lock()
	ops := append([]string(nil), rec.ops...)
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if strings.Join(ops, ",") != "get,comment,comment_edit,comment_edit" {
		t.Fatalf("resume should edit the guidance comment in place, ops=%v", ops)
	}
	last := bodies[len(bodies)-1]
	meta := host.GHParseMetadata(last)
	if meta == nil {
		t.Fatalf("final resumed comment missing metadata:\n%s", last)
	}
	if meta["job_id"] != first.JobID {
		t.Fatalf("meta job_id=%v, want %s", meta["job_id"], first.JobID)
	}
	if meta["story"] != "stories/bugfix" {
		t.Fatalf("meta story=%v", meta["story"])
	}
	if meta["state"] != jobs.GHDone {
		t.Fatalf("meta state=%v, want %s", meta["state"], jobs.GHDone)
	}
}

func TestDispatch_FeatureDevStoryBeat(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{
			Kind:   "issue",
			Number: "123",
			Title:  "@kitsoki draft the design direction",
		},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/123",
		Trigger:   DefaultMentionTrigger,
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/99#issuecomment-4"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-feature",
		PublicBaseURL: "https://kitsoki-test.slothattax.me",
		SpawnFn:       RunStorySession,
	}

	job, err := d.Dispatch(ctx, mention, []string{"enhancement"})
	if err != nil {
		t.Fatalf("Dispatch feature: %v", err)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("feature job State = %q, want %q", job.State, jobs.GHDone)
	}
	if job.Story != "stories/dev-story" {
		t.Fatalf("feature job Story = %q, want stories/dev-story", job.Story)
	}
	if !strings.HasPrefix(job.RunURL, "https://kitsoki-test.slothattax.me/run/") {
		t.Fatalf("RunURL = %q, want public run URL", job.RunURL)
	}
	rec.mu.Lock()
	ops := append([]string(nil), rec.ops...)
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if !containsString(ops, "comment_edit") {
		t.Fatalf("feature final status should edit the first comment, ops=%v", ops)
	}
	last := bodies[len(bodies)-1]
	meta := host.GHParseMetadata(last)
	if meta == nil {
		t.Fatalf("feature final comment missing metadata:\n%s", last)
	}
	if meta["story"] != "stories/dev-story" {
		t.Fatalf("meta story = %v", meta["story"])
	}
	if meta["run_url"] != job.RunURL {
		t.Fatalf("meta run_url = %v, want %s", meta["run_url"], job.RunURL)
	}
	if job.ObjectNumber != "123" {
		t.Fatalf("ObjectNumber = %q, want dynamic issue number", job.ObjectNumber)
	}

	// Honesty (gh-agent-honest-issues.md task 0): dev-story also spawns
	// through the beat-fixture stub today — same "never say Done" rule.
	if strings.Contains(last, "Done") {
		t.Fatalf("stubbed feature run's ack must never contain \"Done\":\n%s", last)
	}
	if !strings.Contains(last, "acknowledged — pipeline not yet enabled for this route") {
		t.Fatalf("stubbed feature run's ack missing honest prose:\n%s", last)
	}
}

// TestDispatch_PRBeat routes a pr-kind mention to the PR status beat: one real
// host.git pr_status read through the native GitHub API + one status comment.
func TestDispatch_PRBeat(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_TOKEN", "test-token")

	prsJSON := `[{"number":77,"title":"@kitsoki review this PR","user":{"login":"bob"},"html_url":"https://github.com/o/r/pull/77"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/search/issues":
			q := r.URL.Query().Get("q")
			switch {
			case strings.Contains(q, "is:issue"):
				writeRawSearchItemsForDispatchTest(t, w, `[]`)
			case strings.Contains(q, "is:pr"):
				writeRawSearchItemsForDispatchTest(t, w, prsJSON)
			default:
				t.Fatalf("unexpected search query: %q", q)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/77":
			writeJSONForDispatchTest(t, w, map[string]any{
				"state":    "open",
				"html_url": "https://github.com/o/r/pull/77",
				"head":     map[string]any{"sha": "abc123"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/77/comments":
			writeJSONForDispatchTest(t, w, []map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/status":
			writeJSONForDispatchTest(t, w, map[string]any{"state": "success", "statuses": []map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
			writeJSONForDispatchTest(t, w, map[string]any{"check_runs": []map[string]any{{"name": "ci", "status": "completed", "conclusion": "success"}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo: "o/r", IncludeIssues: true, IncludePRs: true,
	})
	if err != nil {
		t.Fatalf("ListGitHubInboxItems: %v", err)
	}
	mentions := FilterMentions(items, "o/r", DefaultMentionTrigger)
	if len(mentions) != 1 || mentions[0].Item.Kind != "pr" {
		t.Fatalf("want 1 pr mention, got %+v", mentions)
	}

	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/pull/77#issuecomment-9"}
	d := &Dispatcher{
		Jobs:          store,
		Routes:        DefaultLabelStoryMap(),
		Comments:      &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID:      "worker-pr",
		PublicBaseURL: "https://kitsoki-test.slothattax.me",
		SpawnFn:       RunStorySession,
	}

	job, err := d.Dispatch(ctx, mentions[0], nil)
	if err != nil {
		t.Fatalf("Dispatch pr: %v", err)
	}
	if job.State != jobs.GHDone {
		t.Errorf("pr job State = %q, want done", job.State)
	}
	if job.Story != StoryPRBeat {
		t.Errorf("pr job Story = %q, want %q", job.Story, StoryPRBeat)
	}
	if job.ObjectNumber != "77" {
		t.Errorf("pr job ObjectNumber = %q, want dynamic PR number", job.ObjectNumber)
	}
	if !strings.HasPrefix(job.RunURL, "https://kitsoki-test.slothattax.me/run/") {
		t.Fatalf("RunURL = %q, want public run URL", job.RunURL)
	}
	rec.mu.Lock()
	ops := append([]string(nil), rec.ops...)
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if len(bodies) < 2 {
		t.Errorf("pr beat posted no status comment")
	}
	if !containsString(ops, "comment_edit") {
		t.Fatalf("pr final status should edit the first comment, ops=%v", ops)
	}
	last := bodies[len(bodies)-1]
	meta := host.GHParseMetadata(last)
	if meta == nil {
		t.Fatalf("pr final comment missing metadata:\n%s", last)
	}
	if meta["story"] != StoryPRBeat {
		t.Fatalf("meta story = %v, want %s", meta["story"], StoryPRBeat)
	}
	if meta["run_url"] != job.RunURL {
		t.Fatalf("meta run_url = %v, want %s", meta["run_url"], job.RunURL)
	}
	if meta["origin_ref"] != "github:o/r/pr/77" {
		t.Fatalf("meta origin_ref = %v", meta["origin_ref"])
	}
	if !strings.Contains(last, "PR #77 status: `success`") {
		t.Fatalf("PR final comment missing status reasoning:\n%s", last)
	}
}

func writeJSONForDispatchTest(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func writeRawSearchItemsForDispatchTest(t *testing.T, w http.ResponseWriter, rawItems string) {
	t.Helper()
	var items json.RawMessage
	if err := json.Unmarshal([]byte(rawItems), &items); err != nil {
		t.Fatalf("decode search items: %v", err)
	}
	writeJSONForDispatchTest(t, w, map[string]any{"items": items})
}

// TestDispatchStubbedRunNeverSaysDone is the direct unit test for
// gh-agent-honest-issues.md's runtime invariant: no gh-agent comment may
// contain "Done" unless the spawned run reports Stubbed == false. It injects
// a SpawnFn that returns a RunResult shaped exactly like a "successful" stub
// run (FinalState/Turns/Summary all populated as a real completion would be)
// but with Stubbed: true, and asserts the rendered prose is the honest
// "acknowledged" line — never the synthesized "Done — ..." string — and that
// the stub is recorded on the job's trace.
func TestDispatchStubbedRunNeverSaysDone(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{Kind: "issue", Number: "9", Title: "@kitsoki fix the thing"},
		Repo: "o/r", OriginRef: "github:o/r/issue/9", Trigger: DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/9#issuecomment-1"}
	d := &Dispatcher{
		Jobs:     store,
		Routes:   DefaultLabelStoryMap(),
		Comments: &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID: "worker-honesty",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			return RunResult{
				RunURL:     "kitsoki://run/stub",
				FinalState: "reproducing",
				Turns:      1,
				Summary:    "", // a real completion would often carry no summary either
				Stubbed:    true,
				StubReason: "issue route ran the beat-fixture stub",
			}, nil
		},
	}

	job, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("State=%q, want done (stubbing is a rendering decision, not a routing one)", job.State)
	}

	rec.mu.Lock()
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("want >=2 comments (initial ack + final), got %d", len(bodies))
	}
	last := bodies[len(bodies)-1]
	if strings.Contains(last, "Done") {
		t.Fatalf("stubbed run's comment must never contain \"Done\":\n%s", last)
	}
	if !strings.Contains(last, "acknowledged — pipeline not yet enabled for this route") {
		t.Fatalf("stubbed run's comment missing honest prose:\n%s", last)
	}

	events, err := store.Events(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if !hasEvent(events, "stubbed") {
		t.Fatalf("trace missing \"stubbed\" event: %+v", events)
	}
}

// TestRunStorySession_RealDispatch_BugfixReplay drives RunStorySession's real
// path directly (task 2 of gh-agent-honest-issues.md): stories/bugfix has a
// registered real-dispatch plan, so a bug-labelled issue mention now runs the
// REAL machine end-to-end (idle -> ... -> done) via the replay harness — a
// recorded cassette captured once against the real Claude CLI
// (stories/bugfix/cassettes/happy_human.cassette.yaml), not the beat
// fixture's inline "always succeed" stub map. No LLM call, no network, fully
// offline — but Stubbed is false and RealHostCalls > 0, because the events
// this run produced came from a real (if replayed) host dispatch, not a
// canned map indifferent to the call's actual args.
func TestRunStorySession_RealDispatch_BugfixReplay(t *testing.T) {
	ctx := context.Background()
	job := &jobs.GHJob{
		JobID:        "job-real-1",
		OriginRef:    "github:o/r/issue/42",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "42",
	}
	route := DefaultLabelStoryMap()["bug"]

	result, err := RunStorySession(ctx, route, job)
	if err != nil {
		t.Fatalf("RunStorySession: %v", err)
	}
	if result.Stubbed {
		t.Fatalf("Stubbed = true, want false (stories/bugfix has a registered real-dispatch plan): reason=%q", result.StubReason)
	}
	if result.Harness != HarnessReplay {
		t.Errorf("Harness = %q, want %q (default posture without an operator override)", result.Harness, HarnessReplay)
	}
	if result.RealHostCalls == 0 {
		t.Errorf("RealHostCalls = 0, want > 0 — no evidence of real host dispatch")
	}
	if result.Worktree == "" {
		t.Errorf("Worktree is empty, want the per-job worktree path this run seeded")
	}
	wantWorktreeSuffix := filepath.Join(".capsules", "workspaces", "gh-job-job-real-1")
	if !strings.HasSuffix(result.Worktree, wantWorktreeSuffix) {
		t.Errorf("Worktree = %q, want suffix %q", result.Worktree, wantWorktreeSuffix)
	}
	if result.FinalState != "done" {
		t.Errorf("FinalState = %q, want done", result.FinalState)
	}
	if result.Turns < 8 {
		t.Errorf("Turns = %d, want >= 8 (triage preflight plus the recorded pipeline's full checkpoint walk)", result.Turns)
	}
	if strings.TrimSpace(result.Summary) == "" {
		t.Fatal("Summary is empty — real dispatch should carry a real completion summary")
	}
	if !strings.Contains(result.Summary, "workspace `.capsules/workspaces/gh-job-job-real-1`") {
		t.Errorf("Summary missing the workspace it ran in:\n%s", result.Summary)
	}
	var sawTriageVerdict, sawIndependentVerify bool
	for _, asset := range result.Assets {
		switch asset.Name {
		case "triage-verdict.md":
			sawTriageVerdict = true
			if !strings.Contains(string(asset.Data), "STILL-LIVE") {
				t.Fatalf("triage-verdict.md missing STILL-LIVE verdict:\n%s", string(asset.Data))
			}
		case "independent-verify.md":
			sawIndependentVerify = true
			if !strings.Contains(string(asset.Data), "Triage preflight: STILL-LIVE") {
				t.Fatalf("independent-verify.md missing triage preflight evidence:\n%s", string(asset.Data))
			}
		}
	}
	if !sawTriageVerdict || !sawIndependentVerify {
		t.Fatalf("missing expected assets: triage=%v independent_verify=%v assets=%+v", sawTriageVerdict, sawIndependentVerify, result.Assets)
	}
}

func TestRunStorySession_RealDispatch_AlreadyFixedTriageSkipsMakerPipeline(t *testing.T) {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	job := &jobs.GHJob{
		JobID:        "job-already-fixed",
		OriginRef:    "github:o/r/issue/44",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "44",
		Metadata: map[string]string{
			"ticket_title":       "Already fixed issue",
			"ticket_body":        "The issue body cites a defect that no longer exists.",
			"ticket_source_mode": "remote",
			"ticket_source_ref":  "https://github.com/o/r/issues/44",
		},
	}
	route := DefaultLabelStoryMap()["bug"]
	plan := realDispatchPlans["stories/bugfix"]
	plan.TriageReplayVerdict = map[string]any{
		"verdict":             "ALREADY-FIXED",
		"confidence":          0.91,
		"summary_title":       "ALREADY-FIXED — regression already landed",
		"evidence":            "internal/example.go:12 already contains the fixed branch.",
		"summary_markdown":    "The current tree already contains the cited fix, so the maker pipeline should not run.",
		"suggested_action":    "close as fixed",
		"fixed_in_ref":        "internal/example.go:12",
		"involved_components": []any{"internal/example.go"},
	}

	result, err := runRealDispatch(ctx, root, route, job, plan)
	if err != nil {
		t.Fatalf("runRealDispatch: %v", err)
	}
	if result.FinalState != "triaged" {
		t.Fatalf("FinalState = %q, want triaged", result.FinalState)
	}
	if result.Turns != 1 {
		t.Fatalf("Turns = %d, want only the triage preflight turn", result.Turns)
	}
	if result.Stubbed {
		t.Fatal("already-fixed triage must still be a real story preflight, not a stubbed beat")
	}
	if result.RealHostCalls == 0 {
		t.Fatal("already-fixed triage should carry host-call evidence")
	}
	if !strings.Contains(result.Summary, "ALREADY-FIXED") || strings.Contains(result.Summary, "end-to-end") {
		t.Fatalf("summary should report triage skip, not full maker completion:\n%s", result.Summary)
	}
	var verify, triage string
	for _, asset := range result.Assets {
		switch asset.Name {
		case "independent-verify.md":
			verify = string(asset.Data)
		case "triage-verdict.md":
			triage = string(asset.Data)
		}
	}
	if !strings.Contains(verify, "Full maker pipeline: skipped") {
		t.Fatalf("independent verify should explain skipped maker pipeline:\n%s", verify)
	}
	if !strings.Contains(triage, "ALREADY-FIXED") || !strings.Contains(triage, "internal/example.go:12") {
		t.Fatalf("triage artifact missing verdict/evidence:\n%s", triage)
	}
}

func TestRunStorySession_RealDispatch_UnclearTriageFailsClosed(t *testing.T) {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	job := &jobs.GHJob{
		JobID:        "job-unclear",
		OriginRef:    "github:o/r/issue/45",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "45",
	}
	route := DefaultLabelStoryMap()["bug"]
	plan := realDispatchPlans["stories/bugfix"]
	plan.TriageReplayVerdict = map[string]any{
		"verdict":             "UNCLEAR",
		"confidence":          0.2,
		"summary_title":       "UNCLEAR — issue lacks enough evidence",
		"evidence":            "No cited code path was available.",
		"summary_markdown":    "The triager could not determine whether the defect is still live.",
		"suggested_action":    "needs human repro",
		"fixed_in_ref":        nil,
		"involved_components": []any{},
	}

	result, err := runRealDispatch(ctx, root, route, job, plan)
	if err == nil {
		t.Fatal("runRealDispatch succeeded; UNCLEAR triage should fail closed")
	}
	if !strings.Contains(err.Error(), "UNCLEAR") {
		t.Fatalf("error should include unclear verdict, got: %v", err)
	}
	if result.Turns != 0 || result.Summary != "" {
		t.Fatalf("UNCLEAR triage should not synthesize maker completion: %+v", result)
	}
}

func TestJobFlowWorldLeavesGitHubCloseoutToOrchestrator(t *testing.T) {
	job := &jobs.GHJob{
		JobID:        "job-real-claim",
		OriginRef:    "github:o/r/issue/42",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "42",
		Metadata: map[string]string{
			"ticket_title":       "Claimed product-journey finding",
			"ticket_body":        "Evidence path and summary for triage",
			"ticket_source_mode": "remote",
			"ticket_source_ref":  "https://github.com/o/r/issues/42",
			"ticket_repo":        "o/r",
			"unrelated":          "must not leak",
		},
	}

	world := jobFlowWorld(job)
	if got := world["ticket_id"]; got != "42" {
		t.Fatalf("ticket_id = %q, want issue number", got)
	}
	if got := world["ticket_url"]; got != "https://github.com/o/r/issues/42" {
		t.Fatalf("ticket_url = %q, want GitHub issue URL", got)
	}
	if _, ok := world["ticket_repo"]; ok {
		t.Fatalf("gh-agent bugfix world must not seed ticket_repo; gitops owns issue claim/closeout: %+v", world)
	}
	for key, want := range map[string]string{
		"ticket_title":       "Claimed product-journey finding",
		"ticket_body":        "Evidence path and summary for triage",
		"ticket_source_mode": "remote",
		"ticket_source_ref":  "https://github.com/o/r/issues/42",
	} {
		if got := world[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := world["unrelated"]; ok {
		t.Fatalf("job metadata must be allowlisted before entering the story world: %+v", world)
	}
}

// TestDispatchRealDispatchOnlyDoneWithRealHostCalls is the task 2.4 invariant:
// a real-dispatch route (Harness set) may only claim completion when it also
// shows evidence of real host calls. A RunResult that claims Harness but
// RealHostCalls == 0 (a hypothetical bug in a future real-dispatch plan) must
// render as inconclusive, never "Done" or the plan's synthesized summary —
// this is the same honesty invariant TestDispatchStubbedRunNeverSaysDone
// enforces for the Stubbed case, extended to the real-dispatch case.
func TestDispatchRealDispatchOnlyDoneWithRealHostCalls(t *testing.T) {
	ctx := context.Background()
	mention := Mention{
		Item: host.GitHubInboxItem{Kind: "issue", Number: "11", Title: "@kitsoki fix the thing"},
		Repo: "o/r", OriginRef: "github:o/r/issue/11", Trigger: DefaultMentionTrigger,
	}
	store := newGHJobStore(t)
	rec := &recordingComments{commentID: "https://github.com/o/r/issues/11#issuecomment-1"}
	d := &Dispatcher{
		Jobs:     store,
		Routes:   DefaultLabelStoryMap(),
		Comments: &CommentStore{Exec: rec.handler, Repo: "o/r"},
		WorkerID: "worker-real-honesty",
		SpawnFn: func(context.Context, Route, *jobs.GHJob) (RunResult, error) {
			return RunResult{
				RunURL:     "kitsoki://run/no-evidence",
				FinalState: "done",
				Turns:      8,
				Stubbed:    false,
				Harness:    HarnessReplay,
				Worktree:   "/tmp/.worktrees/gh-job-x",
				// RealHostCalls deliberately left at zero — the bug this
				// invariant catches.
			}, nil
		},
	}

	job, err := d.Dispatch(ctx, mention, []string{"bug"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if job.State != jobs.GHDone {
		t.Fatalf("State = %q, want done (rendering honesty is not a routing decision)", job.State)
	}

	rec.mu.Lock()
	bodies := append([]string(nil), rec.bodies...)
	rec.mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("want >=2 comments, got %d", len(bodies))
	}
	last := bodies[len(bodies)-1]
	if strings.Contains(last, "Done —") {
		t.Fatalf("a real-dispatch result with zero real host calls must never render the synthesized Done prose:\n%s", last)
	}
	if !strings.Contains(last, "no real host calls") {
		t.Fatalf("missing the inconclusive-not-done prose:\n%s", last)
	}
}

// TestRunStubBeatFixture_BugfixPlumbingStillValid proves the beat-fixture
// dispatch plumbing (task 2.3) still works even though RunStorySession no
// longer reaches internal/ghagent/testdata/bugfix.beat.yaml for the "bug"
// route in production (that route now runs the real-dispatch plan). Calling
// runStubBeatFixture directly is the flow-test-only coverage the fixture is
// retained for.
func TestRunStubBeatFixture_BugfixPlumbingStillValid(t *testing.T) {
	ctx := context.Background()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	job := &jobs.GHJob{
		JobID:        "job-plumbing-1",
		OriginRef:    "github:o/r/issue/7",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "7",
	}
	route := DefaultLabelStoryMap()["bug"]

	result, err := runStubBeatFixture(ctx, root, route, job)
	if err != nil {
		t.Fatalf("runStubBeatFixture: %v", err)
	}
	if !result.Stubbed {
		t.Fatalf("Stubbed = false, want true — this is the stub-fixture plumbing path")
	}
	if result.Turns < 1 {
		t.Errorf("Turns = %d, want >= 1", result.Turns)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func hasEvent(events []jobs.GHJobEvent, state string) bool {
	for _, event := range events {
		if event.State == state {
			return true
		}
	}
	return false
}
