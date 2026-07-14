package ci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/executor"
)

func TestNormalizeGitHubPullRequestTrigger(t *testing.T) {
	var event GitHubPullRequestEvent
	event.Action = "synchronize"
	event.Number = 42
	event.Repository.FullName = "owner/repo"
	event.PullRequest.Head.Ref = "feature/capsule-ci"
	event.PullRequest.Head.SHA = "abc123"
	event.PullRequest.Base.Ref = "main"
	event.PullRequest.User.Login = "author"
	event.Sender.Login = "sender"
	trigger, err := NormalizeGitHubPullRequestTrigger(event, "change")
	if err != nil {
		t.Fatal(err)
	}
	if trigger.Kind != "pull_request" || trigger.Provider != "github" || trigger.EventID != "owner/repo#42:synchronize" || trigger.HeadSHA != "abc123" || trigger.RequestedPipeline != "change" {
		t.Fatalf("trigger %#v", trigger)
	}
}

func TestBuildGitHubCheckRunUsesCapsuleVerdict(t *testing.T) {
	check, err := BuildGitHubCheckRun(RunResult{
		Job: artifactjob.Job{ID: "job-1"},
		Envelope: executor.Envelope{Trigger: map[string]any{
			"head_sha": "abc123",
		}},
		Verdict: Verdict{Pipeline: "change", Outcome: "passed", Summary: "all gates passed"},
	}, "https://runs.example/job-1")
	if err != nil {
		t.Fatal(err)
	}
	if check.Name != "Kitsoki Capsule CI / change" || check.HeadSHA != "abc123" || check.Conclusion != "success" || check.ExternalID != "job-1" {
		t.Fatalf("check %#v", check)
	}
}

func TestBuildGitHubCheckRunRequiresHeadSHA(t *testing.T) {
	if _, err := BuildGitHubCheckRun(RunResult{Verdict: Verdict{Outcome: "passed"}}, ""); err == nil {
		t.Fatal("expected missing head sha error")
	}
}

func TestBuildGitHubCheckRunRejectsUnsupportedOutcome(t *testing.T) {
	_, err := BuildGitHubCheckRun(RunResult{
		Job: artifactjob.Job{ID: "job-1"},
		Envelope: executor.Envelope{Trigger: map[string]any{
			"head_sha": "abc123",
		}},
		Verdict: Verdict{Pipeline: "change", Outcome: "mystery"},
	}, "")
	if err == nil {
		t.Fatal("expected unsupported outcome error")
	}
}

func TestGitHubCheckPublisherPostsCheckRun(t *testing.T) {
	var gotPath, gotAuth string
	var got GitHubCheckRun
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"html_url":"https://github.example/check/123","url":"https://api.github.example/check-runs/123"}`))
	}))
	defer srv.Close()

	publication, err := (GitHubCheckPublisher{BaseURL: srv.URL, Token: "test-token", HTTPClient: srv.Client()}).PublishCheckRun(context.Background(), "owner/repo", GitHubCheckRun{
		Name:       "Kitsoki Capsule CI / change",
		HeadSHA:    "abc123",
		Status:     "completed",
		Conclusion: "success",
		ExternalID: "job-1",
		Output:     GitHubCheckOutput{Title: "passed", Summary: "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/owner/repo/check-runs" || gotAuth != "Bearer test-token" {
		t.Fatalf("request path/auth path=%q auth=%q", gotPath, gotAuth)
	}
	if got.HeadSHA != "abc123" || got.Conclusion != "success" || got.ExternalID != "job-1" {
		t.Fatalf("request payload %#v", got)
	}
	if publication.Schema != GitHubCheckPublicationSchema || publication.CheckID != 123 || publication.ExternalID != "job-1" {
		t.Fatalf("publication %#v", publication)
	}
}

func TestGitHubCheckPublisherRequiresExplicitAuthority(t *testing.T) {
	if _, err := (GitHubCheckPublisher{BaseURL: "https://api.github.test"}).PublishCheckRun(context.Background(), "owner/repo", GitHubCheckRun{}); err == nil {
		t.Fatal("expected missing token error")
	}
	if _, err := (GitHubCheckPublisher{BaseURL: "https://api.github.test", Token: "token"}).PublishCheckRun(context.Background(), "not-a-repo", GitHubCheckRun{}); err == nil {
		t.Fatal("expected repo validation error")
	}
}
