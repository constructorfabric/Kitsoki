package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		"stories/bugfix",
		"https://github.com/o/r/issues/42",
		"https://github.com/o/r/issues/42#issuecomment-1",
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
	if got["state"] != jobs.GHDone {
		t.Fatalf("state = %v", got["state"])
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
