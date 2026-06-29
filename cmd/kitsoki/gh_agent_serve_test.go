package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

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
	restore := host.SetExecRunnerForTest(func(_ context.Context, _ string, name string, args ...string) (string, string, int, error) {
		if name != "gh" {
			t.Fatalf("unexpected command %q", name)
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "--version"):
			return "gh version 2.0.0", "", 0, nil
		case strings.HasPrefix(joined, "issue create"):
			return "https://github.com/o/r/issues/501\n", "", 0, nil
		default:
			t.Fatalf("unexpected gh args: %s", joined)
			return "", "", 1, nil
		}
	})
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
