package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/executor"
)

func TestCapsuleCIGitHubTriggerCommandNormalizesPullRequestPayload(t *testing.T) {
	root := t.TempDir()
	payloadPath := filepath.Join(root, "payload.json")
	payload := []byte(`{
  "action": "synchronize",
  "number": 42,
  "repository": {"full_name": "owner/repo"},
  "pull_request": {
    "head": {"ref": "feature/capsule-ci", "sha": "abc123"},
    "base": {"ref": "main", "sha": "base123"},
    "user": {"login": "author"}
  },
  "sender": {"login": "sender"}
}`)
	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "capsule", "ci", "github", "trigger", "--payload", payloadPath, "--pipeline", "change")
	if err != nil {
		t.Fatalf("trigger: %v\n%s", err, out)
	}
	var trigger ci.Trigger
	if err := json.Unmarshal([]byte(out), &trigger); err != nil {
		t.Fatalf("decode trigger: %v\n%s", err, out)
	}
	if trigger.Kind != "pull_request" || trigger.Provider != "github" || trigger.EventID != "owner/repo#42:synchronize" || trigger.HeadSHA != "abc123" || trigger.RequestedPipeline != "change" {
		t.Fatalf("trigger %#v", trigger)
	}
}

func TestCapsuleCIRunTriggerInputConsumesGitHubAdapterOutput(t *testing.T) {
	trigger := ci.Trigger{Kind: "pull_request", Provider: "github", EventID: "owner/repo#42:synchronize", HeadSHA: "0123456789012345678901234567890123456789", RequestedPipeline: "change"}
	raw, err := json.Marshal(trigger)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewReader(raw))
	got, err := capsuleCIReadTrigger(cmd, "-", "change")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "github" || got.HeadSHA != trigger.HeadSHA || got.RequestedPipeline != "change" {
		t.Fatalf("trigger %#v", got)
	}
	cmd.SetIn(bytes.NewReader(raw))
	if _, err := capsuleCIReadTrigger(cmd, "-", "nightly"); err == nil || !strings.Contains(err.Error(), "trigger requested pipeline") {
		t.Fatalf("expected pipeline mismatch, got %v", err)
	}
}

func TestCapsuleCIGitHubCheckCommandProjectsRunRecord(t *testing.T) {
	root := t.TempDir()
	result := ci.RunResult{
		Job: artifactjob.Job{ID: "job-1"},
		Envelope: executor.Envelope{Trigger: map[string]any{
			"head_sha": "abc123",
		}},
		Verdict: ci.Verdict{Pipeline: "change", Outcome: "failed", Checks: []ci.Check{{ID: "tests", Kind: "deterministic", Outcome: "failed"}}},
	}
	if err := (ci.FileRunStore{ProjectRoot: root}).Write(ci.RunRecord{JobID: "job-1", Result: result}); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "capsule", "ci", "github", "check", "--project", root, "--job", "job-1", "--details-url", "https://runs.example/job-1")
	if err != nil {
		t.Fatalf("check: %v\n%s", err, out)
	}
	var check ci.GitHubCheckRun
	if err := json.Unmarshal([]byte(out), &check); err != nil {
		t.Fatalf("decode check: %v\n%s", err, out)
	}
	if check.Name != "Kitsoki Capsule CI / change" || check.HeadSHA != "abc123" || check.Conclusion != "failure" || check.DetailsURL != "https://runs.example/job-1" || check.ExternalID != "job-1" {
		t.Fatalf("check %#v", check)
	}
	if check.Output.Summary == "" {
		t.Fatalf("missing fallback summary %#v", check.Output)
	}
}

func TestCapsuleCIDiagnoseCommandProjectsFailureEvidence(t *testing.T) {
	root := t.TempDir()
	result := ci.RunResult{
		Job:      artifactjob.Job{ID: "job-fail", Status: artifactjob.StatusFailed},
		Envelope: executor.Envelope{Digest: "sha256:envelope"},
		Verdict:  ci.Verdict{Pipeline: "change", Outcome: "failed"},
	}
	if err := (ci.FileRunStore{ProjectRoot: root}).Write(ci.RunRecord{JobID: "job-fail", Result: result, DiagnosticError: "remote timeout"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.WriteFile(filepath.Join(dir, "job-fail.trace.json"), []byte(`{"schema":"capsule-ci-trace/v1","events":[{"kind":"capsule.executor.failed","at":"2026-07-11T00:00:00Z","job_id":"job-fail","envelope_digest":"sha256:envelope","fields":{"error_kind":"timeout","message":"deadline exceeded"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := execRoot(t, "capsule", "ci", "diagnose", "--project", root, "--job", "job-fail", "--json=false")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, out)
	}
	for _, want := range []string{"failure_kind: executor_failed", "failure_summary: timeout", "terminal_error: remote timeout", "trace: .capsules/ci/job-fail.trace.json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnose output missing %q:\n%s", want, out)
		}
	}
}

func TestCapsuleCIGitHubPublishCheckCommandPostsToGitHubAPI(t *testing.T) {
	root := t.TempDir()
	result := ci.RunResult{
		Job: artifactjob.Job{ID: "job-1"},
		Envelope: executor.Envelope{Trigger: map[string]any{
			"head_sha": "abc123",
		}},
		Verdict: ci.Verdict{Pipeline: "change", Outcome: "passed", Summary: "ready"},
	}
	if err := (ci.FileRunStore{ProjectRoot: root}).Write(ci.RunRecord{JobID: "job-1", Result: result}); err != nil {
		t.Fatal(err)
	}
	var gotPath, gotAuth string
	var got ci.GitHubCheckRun
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":456,"html_url":"https://github.example/check/456","url":"https://api.github.example/repos/owner/repo/check-runs/456"}`))
	}))
	defer srv.Close()
	t.Setenv("GH_TOKEN", "publish-token")
	out, err := execRoot(t, "capsule", "ci", "github", "publish-check", "--project", root, "--job", "job-1", "--repo", "owner/repo", "--api-url", srv.URL, "--details-url", "https://runs.example/job-1")
	if err != nil {
		t.Fatalf("publish-check: %v\n%s", err, out)
	}
	if gotPath != "/repos/owner/repo/check-runs" || gotAuth != "Bearer publish-token" {
		t.Fatalf("request path/auth path=%q auth=%q", gotPath, gotAuth)
	}
	if got.HeadSHA != "abc123" || got.Conclusion != "success" || got.DetailsURL != "https://runs.example/job-1" {
		t.Fatalf("check payload %#v", got)
	}
	var publication ci.GitHubCheckPublication
	if err := json.Unmarshal([]byte(out), &publication); err != nil {
		t.Fatalf("decode publication: %v\n%s", err, out)
	}
	if publication.Schema != ci.GitHubCheckPublicationSchema || publication.CheckID != 456 || publication.ExternalID != "job-1" {
		t.Fatalf("publication %#v", publication)
	}
}
