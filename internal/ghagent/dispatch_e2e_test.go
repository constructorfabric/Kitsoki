package ghagent

import (
	"context"
	"database/sql"
	"errors"
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

// stubGHCli installs a cliExec fake that answers the three gh calls the ingress
// poll makes (gh --version for ghAvailable, gh issue list, gh pr list) entirely
// offline. issuesJSON/prsJSON are the --json stdout payloads. Returns a restore.
func stubGHCli(t *testing.T, issuesJSON, prsJSON string) func() {
	t.Helper()
	return host.SetExecRunnerForTest(func(_ context.Context, _ /*dir*/ string, name string, args ...string) (string, string, int, error) {
		if name != "gh" {
			t.Fatalf("unexpected exec %q %v", name, args)
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "--version"):
			return "gh version 2.0.0", "", 0, nil
		case strings.HasPrefix(joined, "issue list"):
			return issuesJSON, "", 0, nil
		case strings.HasPrefix(joined, "pr list"):
			return prsJSON, "", 0, nil
		case strings.HasPrefix(joined, "pr view"):
			return `{"state":"OPEN","statusCheckRollup":[{"name":"ci","conclusion":"SUCCESS"}]}`, "", 0, nil
		default:
			t.Fatalf("unexpected gh args: %s", joined)
			return "", "", 1, nil
		}
	})
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
	})
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

// TestDispatch_MentionToAckLoop drives the FULL @kitsoki loop end-to-end across
// package boundaries: cliExec-stubbed ingress -> FilterMentions -> Classify ->
// Claim (SQLite) -> Dispatcher -> a REAL no-LLM story spawn via
// testrunner.RunFlows -> rolling-status ack comment. Fully offline, zero LLM,
// zero network.
func TestDispatch_MentionToAckLoop(t *testing.T) {
	ctx := context.Background()

	issuesJSON := `[{"number":42,"title":"@kitsoki please fix the crash","assignees":[{"login":"alice"}],"url":"https://github.com/o/r/issues/42"}]`
	restore := stubGHCli(t, issuesJSON, `[]`)
	defer restore()

	// Real ingress: ListGitHubInboxItems shells gh through the cliExec seam.
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
}

// TestDispatch_PRBeat routes a pr-kind mention to the PR status beat: one real
// host.git pr_status read through the gh CLI seam + one status comment.
func TestDispatch_PRBeat(t *testing.T) {
	ctx := context.Background()

	prsJSON := `[{"number":77,"title":"@kitsoki review this PR","author":{"login":"bob"},"url":"https://github.com/o/r/pull/77"}]`
	restore := stubGHCli(t, `[]`, prsJSON)
	defer restore()

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
	if !strings.Contains(last, "PR #77 status: `OPEN`") || !strings.Contains(last, "ci=SUCCESS") {
		t.Fatalf("PR final comment missing status reasoning:\n%s", last)
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
