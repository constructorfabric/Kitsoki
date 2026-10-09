package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/githubapp"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

type staticServeTokenSource struct{ token string }

func (s staticServeTokenSource) InstallationToken(context.Context) (string, time.Time, error) {
	return s.token, time.Now().Add(time.Hour), nil
}

func TestWebhookMentionIssueComment(t *testing.T) {
	body := []byte(`{
	  "action":"created",
	  "repository":{"full_name":"o/r"},
	  "issue":{
	    "number":42,
	    "title":"button crashes",
	    "html_url":"https://github.com/o/r/issues/42",
	    "labels":[{"name":"bug"}]
	  },
	  "comment":{
	    "body":"@kitsoki please fix this",
	    "html_url":"https://github.com/o/r/issues/42#issuecomment-1",
	    "user":{"login":"alice"}
	  }
	}`)
	mention, labels, ok, err := webhookMention(body, "", "@kitsoki")
	if err != nil {
		t.Fatalf("webhookMention: %v", err)
	}
	if !ok {
		t.Fatal("webhookMention ignored a matching comment")
	}
	if mention.Repo != "o/r" {
		t.Fatalf("Repo=%q", mention.Repo)
	}
	if mention.Item.Kind != "issue" || mention.Item.Number != "42" {
		t.Fatalf("Item=%+v", mention.Item)
	}
	if mention.OriginRef != "github:o/r/issue/42" {
		t.Fatalf("OriginRef=%q", mention.OriginRef)
	}
	if len(labels) != 1 || labels[0] != "bug" {
		t.Fatalf("labels=%v", labels)
	}
}

func TestGHAgentEnqueueCmdQueuesIssue(t *testing.T) {
	dbPath := t.TempDir() + "/gh-jobs.sqlite"
	cmd := newGHAgentCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"enqueue",
		"--db", dbPath,
		"--repo", "o/r",
		"--issue", "105",
		"--story", "stories/bugfix",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("enqueue command: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode enqueue JSON %q: %v", out.String(), err)
	}
	if payload["origin_ref"] != "github:o/r/issue/105" || payload["story"] != "stories/bugfix" || payload["state"] != jobs.GHQueued {
		t.Fatalf("unexpected enqueue payload: %#v", payload)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	queued, err := store.ListQueued(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 1 || queued[0].OriginRef != "github:o/r/issue/105" {
		t.Fatalf("queued=%+v", queued)
	}
}

func TestGHAgentDrainCmdDrainsQueuedIssue(t *testing.T) {
	dbPath := t.TempDir() + "/gh-jobs.sqlite"
	assetDir := t.TempDir()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	job, _, err := store.Enqueue(context.Background(), jobs.GHMention{
		OriginRef:    "github:o/r/issue/106",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "106",
	}, "stories/bugfix")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close setup db: %v", err)
	}

	prev := ghAgentDispatchMention
	t.Cleanup(func() { ghAgentDispatchMention = prev })
	ghAgentDispatchMention = func(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions, mention ghagent.Mention, labels []string) (*jobs.GHJob, error) {
		if mention.OriginRef != "github:o/r/issue/106" {
			t.Fatalf("OriginRef=%q", mention.OriginRef)
		}
		if len(labels) != 1 || labels[0] != "bug" {
			t.Fatalf("labels=%v, want bug label from queued bugfix story", labels)
		}
		if err := store.SetStory(ctx, job.JobID, "stories/bugfix"); err != nil {
			return nil, err
		}
		if err := store.SetRunURL(ctx, job.JobID, job.JobID, "https://agent.example/run/"+job.JobID); err != nil {
			return nil, err
		}
		if err := store.PutAsset(ctx, job.JobID, "fix-report.md", "text/markdown", []byte("# Fix report\n")); err != nil {
			return nil, err
		}
		if err := store.Advance(ctx, job.JobID, jobs.GHDone, ""); err != nil {
			return nil, err
		}
		return store.GetJob(ctx, job.JobID)
	}

	cmd := newGHAgentCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"drain",
		"--db", dbPath,
		"--repo", "o/r",
		"--public-base-url", "https://agent.example",
		"--asset-dir", assetDir,
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("drain command: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode drain JSON %q: %v", out.String(), err)
	}
	if payload["status"] != "drained" || payload["drained_count"].(float64) != 1 || payload["done_count"].(float64) != 1 {
		t.Fatalf("unexpected drain payload: %#v", payload)
	}
	jobsPayload := payload["jobs"].([]any)
	assets := jobsPayload[0].(map[string]any)["assets"].([]any)
	if len(assets) != 1 {
		t.Fatalf("assets len=%d, want 1", len(assets))
	}
	asset := assets[0].(map[string]any)
	if asset["name"] != "fix-report.md" || asset["url"] != "https://agent.example/api/run/"+job.JobID+"/assets/fix-report.md" {
		t.Fatalf("unexpected asset payload: %#v", asset)
	}
}

func TestGHAgentReadyAPIReportsConfiguredAgent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	if _, _, err := store.Enqueue(context.Background(), jobs.GHMention{
		OriginRef:    "github:o/r/issue/105",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "105",
	}, "stories/bugfix"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	handler := ghAgentReadyAPIHandler(store, ghAgentServeOptions{
		Repo:              "o/r",
		PublicBaseURL:     "https://agent.example/",
		Worker:            "worker-1",
		Trigger:           "@kitsoki",
		DrainInterval:     time.Second,
		PollInterval:      0,
		ReconcileInterval: time.Minute,
		StuckAfter:        time.Minute,
		IncidentRepo:      "o/incidents",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode ready JSON %q: %v", rec.Body.String(), err)
	}
	if payload["status"] != "ready" || payload["repo"] != "o/r" || payload["public_base_url"] != "https://agent.example" {
		t.Fatalf("unexpected ready payload: %#v", payload)
	}
	if payload["drain_enabled"] != true || payload["poll_enabled"] != false || payload["worker"] != "worker-1" {
		t.Fatalf("unexpected readiness flags: %#v", payload)
	}
	counts := payload["recent_state_counts"].(map[string]any)
	if counts[jobs.GHQueued].(float64) != 1 {
		t.Fatalf("queued count=%#v", counts)
	}
}

func TestWithGHAgentAuthInstallsCLIExecEnv(t *testing.T) {
	prevTokenSource := newGitHubAppTokenSource
	newGitHubAppTokenSource = func(*githubapp.Config, githubapp.Doer) (githubapp.TokenSource, error) {
		return staticServeTokenSource{token: "installation-token"}, nil
	}
	t.Cleanup(func() { newGitHubAppTokenSource = prevTokenSource })
	t.Setenv("GH_TOKEN", "ambient-token")

	var seen map[string]string
	err := withGHAgentAuth(context.Background(), ghAgentServeOptions{
		UseGitHubApp:   true,
		AppID:          123,
		InstallationID: 456,
		AppKeyFile:     "unused-test-key.pem",
	}, func(ctx context.Context) error {
		seen = host.CLIExecEnvFromCtx(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("withGHAgentAuth: %v", err)
	}
	if seen["GH_TOKEN"] != "installation-token" {
		t.Fatalf("GH_TOKEN=%q, want installation-token", seen["GH_TOKEN"])
	}
	if seen["GITHUB_TOKEN"] != "installation-token" {
		t.Fatalf("GITHUB_TOKEN=%q, want installation-token", seen["GITHUB_TOKEN"])
	}
	if got := os.Getenv("GH_TOKEN"); got != "ambient-token" {
		t.Fatalf("ambient GH_TOKEN=%q, want restored ambient-token", got)
	}
}

func TestWebhookMentionPullRequestReview(t *testing.T) {
	body := []byte(`{
	  "action":"submitted",
	  "repository":{"full_name":"o/r"},
	  "pull_request":{
	    "number":77,
	    "title":"Renderer cleanup",
	    "html_url":"https://github.com/o/r/pull/77"
	  },
	  "review":{
	    "body":"@kitsoki what is the status here?",
	    "html_url":"https://github.com/o/r/pull/77#pullrequestreview-1",
	    "user":{"login":"reviewer"}
	  }
	}`)
	mention, _, ok, err := webhookMention(body, "", "@kitsoki")
	if err != nil {
		t.Fatalf("webhookMention: %v", err)
	}
	if !ok {
		t.Fatal("review mention was ignored")
	}
	if mention.Item.Kind != "pr" || mention.Item.Number != "77" {
		t.Fatalf("mention item=%+v", mention.Item)
	}
	if mention.Item.Author != "reviewer" {
		t.Fatalf("author=%q", mention.Item.Author)
	}
	if mention.OriginRef != "github:o/r/pr/77" {
		t.Fatalf("OriginRef=%q", mention.OriginRef)
	}
}

func TestWebhookMentionPullRequestFromIssueComment(t *testing.T) {
	body := []byte(`{
	  "action":"created",
	  "repository":{"full_name":"o/r"},
	  "issue":{
	    "number":7,
	    "title":"change the renderer",
	    "html_url":"https://github.com/o/r/pull/7",
	    "pull_request":{}
	  },
	  "comment":{"body":"Could @kitsoki handle review feedback?","user":{"login":"alice"}}
	}`)
	mention, _, ok, err := webhookMention(body, "", "@kitsoki")
	if err != nil {
		t.Fatalf("webhookMention: %v", err)
	}
	if !ok {
		t.Fatal("webhookMention ignored a matching PR comment")
	}
	if mention.Item.Kind != "pr" || mention.OriginRef != "github:o/r/pr/7" {
		t.Fatalf("mention=%+v", mention)
	}
}

func TestWebhookMentionIssueLabeledCanCarryRoutingSignal(t *testing.T) {
	body := []byte(`{
	  "action":"labeled",
	  "repository":{"full_name":"o/r"},
	  "issue":{
	    "number":42,
	    "title":"button crashes",
	    "html_url":"https://github.com/o/r/issues/42",
	    "body":"@kitsoki please handle this",
	    "labels":[{"name":"bug"}]
	  },
	  "label":{"name":"bug"}
	}`)
	mention, labels, ok, err := webhookMention(body, "", "@kitsoki")
	if err != nil {
		t.Fatalf("webhookMention: %v", err)
	}
	if !ok {
		t.Fatal("labeled issue mention was ignored")
	}
	if mention.Item.Kind != "issue" || mention.Item.Number != "42" {
		t.Fatalf("mention item=%+v", mention.Item)
	}
	if len(labels) != 1 || labels[0] != "bug" {
		t.Fatalf("labels=%v", labels)
	}
}

func TestWebhookMentionIgnoresNonMention(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"o/r"},"issue":{"number":1},"comment":{"body":"plain comment"}}`)
	_, _, ok, err := webhookMention(body, "", "@kitsoki")
	if err != nil {
		t.Fatalf("webhookMention: %v", err)
	}
	if ok {
		t.Fatal("non-mention webhook should be ignored")
	}
}

func TestGHAgentRunHandlersShowUsefulJobSummary(t *testing.T) {
	ctx := context.Background()
	store := newServeTestGHJobStore(t)
	job, won, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/issue/42",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "42",
	}, "worker-test")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !won {
		t.Fatal("first claim did not win")
	}
	if err := store.SetStory(ctx, job.JobID, "stories/bugfix"); err != nil {
		t.Fatalf("SetStory: %v", err)
	}
	if err := store.SetRunURL(ctx, job.JobID, job.JobID, "https://kitsoki-test.slothattax.me/run/"+job.JobID); err != nil {
		t.Fatalf("SetRunURL: %v", err)
	}
	if err := store.SetComment(ctx, job.JobID, "https://github.com/o/r/issues/42#issuecomment-1"); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if _, err := store.BumpAttempt(ctx, job.JobID); err != nil {
		t.Fatalf("BumpAttempt: %v", err)
	}
	if err := store.SetIncidentURL(ctx, job.JobID, "https://github.com/o/r/issues/500"); err != nil {
		t.Fatalf("SetIncidentURL: %v", err)
	}
	if err := store.Advance(ctx, job.JobID, jobs.GHDone, ""); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	htmlReq := httptest.NewRequest(http.MethodGet, "/run/"+job.JobID, nil)
	htmlRec := httptest.NewRecorder()
	ghAgentRunHandler(store).ServeHTTP(htmlRec, htmlReq)
	if htmlRec.Code != http.StatusOK {
		t.Fatalf("HTML status = %d, body:\n%s", htmlRec.Code, htmlRec.Body.String())
	}
	body := htmlRec.Body.String()
	for _, want := range []string{
		job.JobID,
		"github:o/r/issue/42",
		"stories/bugfix",
		string(jobs.GHDone),
		"issue #42",
		"https://github.com/o/r/issues/42",
		"https://github.com/o/r/issues/42#issuecomment-1",
		"https://github.com/o/r/issues/500",
		"Timeline",
		"Updated",
		"/api/run/" + job.JobID,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HTML body missing %q:\n%s", want, body)
		}
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/run/"+job.JobID, nil)
	apiRec := httptest.NewRecorder()
	ghAgentRunAPIHandler(store).ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("API status = %d, body:\n%s", apiRec.Code, apiRec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(apiRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode API JSON: %v", err)
	}
	if got["source_url"] != "https://github.com/o/r/issues/42" {
		t.Fatalf("source_url = %v", got["source_url"])
	}
	if got["comment_url"] != "https://github.com/o/r/issues/42#issuecomment-1" {
		t.Fatalf("comment_url = %v", got["comment_url"])
	}
	if got["origin_ref"] != "github:o/r/issue/42" {
		t.Fatalf("origin_ref = %v", got["origin_ref"])
	}
	if got["story"] != "stories/bugfix" {
		t.Fatalf("story = %v", got["story"])
	}
	if got["object_kind"] != "issue" || got["object_number"] != "42" {
		t.Fatalf("object = %v #%v", got["object_kind"], got["object_number"])
	}
	if got["state"] != jobs.GHDone {
		t.Fatalf("state = %v", got["state"])
	}
	if got["updated_at"] == "" {
		t.Fatalf("updated_at missing: %v", got)
	}
	if got["attempt_count"].(float64) != 1 {
		t.Fatalf("attempt_count = %v", got["attempt_count"])
	}
	if got["incident_url"] != "https://github.com/o/r/issues/500" {
		t.Fatalf("incident_url = %v", got["incident_url"])
	}
	events, ok := got["events"].([]any)
	if !ok || len(events) == 0 {
		t.Fatalf("events missing: %v", got["events"])
	}
}

func TestGHAgentRunsHandlersListRecentJobs(t *testing.T) {
	ctx := context.Background()
	store := newServeTestGHJobStore(t)
	job, won, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/pr/56",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "56",
	}, "worker-test")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !won {
		t.Fatal("first claim did not win")
	}
	if err := store.SetStory(ctx, job.JobID, "pr-beat"); err != nil {
		t.Fatalf("SetStory: %v", err)
	}
	if err := store.Advance(ctx, job.JobID, jobs.GHFailed, "ghagent: post comment: Bad credentials"); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	htmlReq := httptest.NewRequest(http.MethodGet, "/runs", nil)
	htmlRec := httptest.NewRecorder()
	ghAgentRunsHandler(store).ServeHTTP(htmlRec, htmlReq)
	if htmlRec.Code != http.StatusOK {
		t.Fatalf("HTML status = %d, body:\n%s", htmlRec.Code, htmlRec.Body.String())
	}
	body := htmlRec.Body.String()
	for _, want := range []string{
		"kitsoki GitHub runs",
		"github.com/o/r/pull/56",
		"pr-beat",
		string(jobs.GHFailed),
		"Bad credentials",
		"/run/" + job.JobID,
		"/api/runs",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("runs HTML missing %q:\n%s", want, body)
		}
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	apiRec := httptest.NewRecorder()
	ghAgentRunsAPIHandler(store).ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("API status = %d, body:\n%s", apiRec.Code, apiRec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(apiRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode API JSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d jobs, want 1: %v", len(got), got)
	}
	if got[0]["origin_ref"] != "github:o/r/pr/56" {
		t.Fatalf("origin_ref = %v", got[0]["origin_ref"])
	}
	if got[0]["source_url"] != "https://github.com/o/r/pull/56" {
		t.Fatalf("source_url = %v", got[0]["source_url"])
	}
	if got[0]["state"] != jobs.GHFailed {
		t.Fatalf("state = %v", got[0]["state"])
	}
	if !strings.Contains(got[0]["err_msg"].(string), "Bad credentials") {
		t.Fatalf("err_msg = %v", got[0]["err_msg"])
	}
}

func TestGHAgentReconcileEscalatesStuckJobs(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	job, _, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/issue/88",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "88",
	}, "worker-test")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Advance(ctx, job.JobID, jobs.GHRunning, ""); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx, `UPDATE gh_jobs SET updated_at=?, attempt_count=1 WHERE job_id=?`, old, job.JobID); err != nil {
		t.Fatalf("age job: %v", err)
	}
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload["title"].(string), "gh-agent incident") {
			t.Fatalf("title=%v", payload["title"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":501,"html_url":"https://github.com/o/r/issues/501"}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()
	if err := runGHAgentReconcileOnce(ctx, store, ghAgentServeOptions{
		Repo:          "o/r",
		PublicBaseURL: "https://agent.example",
		StuckAfter:    time.Minute,
		MaxAttempts:   1,
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := store.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.State != jobs.GHFailed {
		t.Fatalf("State=%q, want failed", got.State)
	}
	if got.IncidentURL != "https://github.com/o/r/issues/501" {
		t.Fatalf("IncidentURL=%q", got.IncidentURL)
	}
}

func TestDrainQueuedGHAgentJobsRedispatchesStoredPRRebase(t *testing.T) {
	ctx := context.Background()
	store := newServeTestGHJobStore(t)
	job, _, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/pr/98",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "98",
	}, "worker-test")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.SetStory(ctx, job.JobID, ghagent.StoryPRRebase); err != nil {
		t.Fatalf("SetStory: %v", err)
	}
	if err := store.SetComment(ctx, job.JobID, "https://github.com/o/r/pull/98#issuecomment-1"); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if err := store.Advance(ctx, job.JobID, jobs.GHQueued, "stuck job queued for retry after 15m0s"); err != nil {
		t.Fatalf("Advance queued: %v", err)
	}

	prev := ghAgentDispatchMention
	t.Cleanup(func() { ghAgentDispatchMention = prev })
	var dispatched ghagent.Mention
	ghAgentDispatchMention = func(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions, mention ghagent.Mention, labels []string) (*jobs.GHJob, error) {
		dispatched = mention
		if mention.OriginRef != "github:o/r/pr/98" {
			t.Fatalf("OriginRef=%q", mention.OriginRef)
		}
		if mention.Item.Kind != "pr" || mention.Item.Number != "98" {
			t.Fatalf("Item=%+v", mention.Item)
		}
		if !strings.Contains(strings.ToLower(mention.Item.Title), "rebase") {
			t.Fatalf("Title=%q, want rebase routing signal", mention.Item.Title)
		}
		if len(labels) != 0 {
			t.Fatalf("labels=%v, want none for PR retry", labels)
		}
		if err := store.Advance(ctx, job.JobID, jobs.GHDone, ""); err != nil {
			return nil, err
		}
		return store.GetJob(ctx, job.JobID)
	}

	drained, err := drainQueuedGHAgentJobs(ctx, store, ghAgentServeOptions{Trigger: "@kitsoki"})
	if err != nil {
		t.Fatalf("drainQueuedGHAgentJobs: %v", err)
	}
	if len(drained) != 1 || drained[0].OriginRef != "github:o/r/pr/98" {
		t.Fatalf("drained=%+v", drained)
	}
	if dispatched.OriginRef == "" {
		t.Fatal("queued job was not dispatched")
	}
	got, err := store.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.State != jobs.GHDone {
		t.Fatalf("State=%q, want done", got.State)
	}
}

// TestGHAgentDrainLoopRunsWithPollDisabled proves task 3's fix: draining
// queued jobs no longer depends on runGHAgentPollLoop. A webhook-only
// deployment sets --poll-interval=0 (poll disabled) but must still make
// forward progress on a job the reconcile loop parked in GHQueued — this
// drives runGHAgentDrainLoop directly (the loop runGHAgentServe wires
// unconditionally on --poll-interval, keyed only on --drain-interval) and
// asserts the job drains within one DrainInterval tick, with no poll loop
// running at all.
func TestGHAgentDrainLoopRunsWithPollDisabled(t *testing.T) {
	ctx := context.Background()
	store := newServeTestGHJobStore(t)
	job, _, err := store.Claim(ctx, jobs.GHMention{
		OriginRef:    "github:o/r/issue/7",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "7",
	}, "worker-test")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Advance(ctx, job.JobID, jobs.GHQueued, "stuck job queued for retry"); err != nil {
		t.Fatalf("Advance queued: %v", err)
	}

	prev := ghAgentDispatchMention
	t.Cleanup(func() { ghAgentDispatchMention = prev })
	dispatched := make(chan string, 1)
	ghAgentDispatchMention = func(ctx context.Context, store *jobs.GHJobStore, opts ghAgentServeOptions, mention ghagent.Mention, labels []string) (*jobs.GHJob, error) {
		if err := store.Advance(ctx, job.JobID, jobs.GHDone, ""); err != nil {
			return nil, err
		}
		got, err := store.GetJob(ctx, job.JobID)
		if err == nil {
			dispatched <- got.OriginRef
		}
		return got, err
	}

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := ghAgentServeOptions{
		Trigger: "@kitsoki",
		// PollInterval is intentionally left zero (poll disabled, e.g. a
		// webhook-only deployment) — runGHAgentDrainLoop must not depend on
		// it.
		PollInterval:  0,
		DrainInterval: 10 * time.Millisecond,
	}
	go runGHAgentDrainLoop(loopCtx, store, opts)

	select {
	case ref := <-dispatched:
		if ref != "github:o/r/issue/7" {
			t.Fatalf("dispatched OriginRef=%q", ref)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued job was not drained within the drain interval; poll being disabled must not block draining")
	}

	got, err := store.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.State != jobs.GHDone {
		t.Fatalf("State=%q, want done", got.State)
	}
}

func newServeTestGHJobStore(t *testing.T) *jobs.GHJobStore {
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
