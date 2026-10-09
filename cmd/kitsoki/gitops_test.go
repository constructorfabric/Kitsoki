package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func TestGitopsIssueStatusUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	body := "Replay sessions must never fall through to a live agent.\n\n```kitsoki\nlegacy_id: 2026-07-05T021109Z-replay-miss\n```\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105":
			writeJSON(w, map[string]any{
				"number":   105,
				"title":    "Replay miss must fail closed",
				"body":     body,
				"state":    "closed",
				"html_url": "https://github.com/o/r/issues/105",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105/comments":
			writeJSON(w, []map[string]any{{
				"html_url": "https://github.com/o/r/issues/105#issuecomment-1",
				"body":     "kitsoki-fixed-in",
			}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-status must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-status", "--repo", "o/r", "--id", "105", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_status" || got["issue_state"] != "closed" {
		t.Fatalf("status output = %+v", got)
	}
	if got["issue_id"] != "105" || got["title"] != "Replay miss must fail closed" {
		t.Fatalf("issue identity = %+v", got)
	}
	if got["legacy_id"] != "2026-07-05T021109Z-replay-miss" {
		t.Fatalf("legacy id = %+v", got)
	}
}

func TestGitopsIssueStatusSupportsPublicReadWithoutToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want none for public read", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105":
			writeJSON(w, map[string]any{
				"number":   105,
				"title":    "Native public issue read",
				"body":     "No gh CLI required.",
				"state":    "open",
				"html_url": "https://github.com/o/r/issues/105",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105/comments":
			writeJSON(w, []map[string]any{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-status must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-status", "--repo", "o/r", "--id", "105", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_status" || got["issue_state"] != "open" {
		t.Fatalf("status output = %+v", got)
	}
	if got["issue_id"] != "105" || got["title"] != "Native public issue read" {
		t.Fatalf("issue identity = %+v", got)
	}
}

func TestGitopsIssueCreateUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		writeJSON(w, map[string]any{
			"number":   106,
			"html_url": "https://github.com/o/r/issues/106",
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-create must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"issue-create",
		"--repo", "o/r",
		"--title", "Replay miss must fail closed",
		"--body", "Replay sessions must never fall through to a live agent.",
		"--labels", "bug,source-autonomous",
		"--severity", "P1",
		"--trace-ref", "trace://105",
		"--legacy-id", "2026-07-05T021109Z-replay-miss",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-create: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_created" || got["issue_id"] != "106" || got["issue_state"] != "created" {
		t.Fatalf("create output = %+v", got)
	}
	if got["issue_url"] != "https://github.com/o/r/issues/106" {
		t.Fatalf("issue url = %+v", got)
	}
	labels := anyStringsFromInterface(payload["labels"])
	for _, want := range []string{"bug", "source-autonomous", "P1"} {
		if !containsString(labels, want) {
			t.Fatalf("labels %v missing %q", labels, want)
		}
	}
	body, _ := payload["body"].(string)
	for _, want := range []string{"Replay sessions", "trace_ref: trace://105", "legacy_id: 2026-07-05T021109Z-replay-miss"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestGitopsIssueCommentUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues/105/comments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		body, _ = payload["body"].(string)
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/105#issuecomment-2"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-comment must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-comment", "--repo", "o/r", "--id", "105", "--body", "Fixed in 60867ddb.", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-comment: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_commented" || got["comment_url"] != "https://github.com/o/r/issues/105#issuecomment-2" {
		t.Fatalf("comment output = %+v", got)
	}
	if body != "Fixed in 60867ddb." {
		t.Fatalf("comment body = %q", body)
	}
}

func TestGitopsIssueTransitionUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var state string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/105" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		state, _ = payload["state"].(string)
		writeJSON(w, map[string]any{"state": state})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-transition must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-transition", "--repo", "o/r", "--id", "105", "--to", "resolved", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-transition: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_transitioned" || got["issue_state"] != "closed" || state != "closed" {
		t.Fatalf("transition output = %+v, state=%q", got, state)
	}
}

func TestGitopsIssueCloseCommentsAndClosesThroughNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var requests []string
	var commentBody string
	var state string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/105/comments":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode comment payload: %v", err)
			}
			commentBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{
				"id":       9001,
				"html_url": "https://github.com/o/r/issues/105#issuecomment-9001",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/105":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode close payload: %v", err)
			}
			state, _ = payload["state"].(string)
			writeJSON(w, map[string]any{"state": state})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-close must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"issue-close",
		"--repo", "o/r",
		"--id", "105",
		"--comment-body", "kitsoki-fixed-in: b19e3242\n\nVerification: autonomous marathon gates passed.",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-close: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_closed" || got["issue_state"] != "closed" || state != "closed" {
		t.Fatalf("close output = %+v, state=%q", got, state)
	}
	if got["comment_url"] != "https://github.com/o/r/issues/105#issuecomment-9001" {
		t.Fatalf("comment url = %+v", got)
	}
	if !strings.Contains(commentBody, "kitsoki-fixed-in: b19e3242") {
		t.Fatalf("comment body = %q", commentBody)
	}
	wantRequests := []string{
		"POST /repos/o/r/issues/105/comments",
		"PATCH /repos/o/r/issues/105",
	}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests:\n%s", strings.Join(requests, "\n"))
	}
}

func TestGitopsIssueCloseWithoutCommentUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/105" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		writeJSON(w, map[string]any{"state": "closed"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-close must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-close", "--repo", "o/r", "--id", "105", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-close: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_closed" || got["issue_state"] != "closed" {
		t.Fatalf("close output = %+v", got)
	}
	if _, ok := got["comment_url"]; ok {
		t.Fatalf("comment_url should be absent without --comment-body: %+v", got)
	}
	if strings.Join(requests, "\n") != "PATCH /repos/o/r/issues/105" {
		t.Fatalf("requests:\n%s", strings.Join(requests, "\n"))
	}
}

func TestGitopsIssueCloseoutCommandUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	runDir := t.TempDir()
	findings := map[string]any{
		"run_id": "run-1",
		"items": []any{
			map[string]any{
				"id":            "finding-1",
				"kind":          "issue",
				"title":         "Done room should close tickets",
				"summary":       "manual close-out should be story-owned",
				"scenario":      "bugfix",
				"severity":      "high",
				"evidence_path": "trace.md",
				"status":        "blocked",
				"origin":        "observed",
				"github_issue": map[string]any{
					"url":    "https://github.com/o/r/issues/42",
					"repo":   "o/r",
					"number": "42",
				},
			},
		},
		"gh_agent": map[string]any{
			"drained_jobs": []any{
				map[string]any{
					"origin_ref": "github:o/r/issue/42",
					"job_id":     "job-42",
					"state":      "done",
					"run_url":    "https://agent.example/run/job-42",
					"assets": []any{
						map[string]any{"name": "fix-report.md", "url": "https://agent.example/run/job-42/assets/fix-report.md"},
						map[string]any{"name": "independent-verify.md", "url": "https://agent.example/run/job-42/assets/independent-verify.md"},
					},
				},
			},
		},
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "findings.json"), findings); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	var commentBody string
	var closedState string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/42/comments":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode comment: %v", err)
			}
			commentBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/42#issuecomment-9"})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/42":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode transition: %v", err)
			}
			closedState, _ = payload["state"].(string)
			writeJSON(w, map[string]any{"state": closedState})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-closeout must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"issue-closeout",
		"--run-dir", runDir,
		"--ticket-repo", "o/r",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-closeout: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["issue_closeout_status"] != "closed" || intValue(got, "issue_closeout_count") != 1 {
		t.Fatalf("closeout output = %+v", got)
	}
	if closedState != "closed" {
		t.Fatalf("closed state = %q, want closed", closedState)
	}
	for _, want := range []string{"kitsoki-fixed-in", "job-42", "independent-verify.md", "https://agent.example/run/job-42"} {
		if !strings.Contains(commentBody, want) {
			t.Fatalf("comment body missing %q:\n%s", want, commentBody)
		}
	}

	updated, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	issue := mapValue(gitopsFindingsItems(updated)[0], "github_issue")
	if stringValue(issue, "state") != "closed" || stringValue(issue, "closeout_comment_url") == "" {
		t.Fatalf("issue state not persisted: %+v", issue)
	}
}

func TestGitopsIssueStateCacheUsesNativeTicketProvider(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	root := t.TempDir()
	findings := map[string]any{
		"items": []any{
			map[string]any{
				"id":     "finding-1",
				"kind":   "issue",
				"title":  "Replay miss must fail closed",
				"origin": "observed",
				"github_issue": map[string]any{
					"url":    "https://github.com/o/r/issues/105",
					"repo":   "o/r",
					"number": "105",
				},
			},
			map[string]any{
				"id":     "finding-duplicate",
				"kind":   "issue",
				"title":  "Same issue from another scenario",
				"origin": "observed",
				"github_issue": map[string]any{
					"url":    "https://github.com/o/r/issues/105",
					"repo":   "o/r",
					"number": "105",
				},
			},
			map[string]any{
				"id":     "finding-2",
				"kind":   "issue",
				"title":  "Done room does not close tickets",
				"origin": "observed",
				"github_issue": map[string]any{
					"url": "https://github.com/o/r/issues/117",
				},
			},
			map[string]any{
				"id":     "unfiled",
				"kind":   "issue",
				"title":  "No GitHub issue yet",
				"origin": "observed",
			},
		},
	}
	runDir := filepath.Join(root, "run-a")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "findings.json"), findings); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105":
			writeJSON(w, map[string]any{
				"number":   105,
				"title":    "Replay miss must fail closed",
				"body":     "body",
				"state":    "closed",
				"html_url": "https://github.com/o/r/issues/105",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105/comments":
			writeJSON(w, []map[string]any{{"body": "kitsoki-fixed-in: 23bbf28a"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/117":
			writeJSON(w, map[string]any{
				"number":   117,
				"title":    "Done room does not close tickets",
				"body":     "body",
				"state":    "open",
				"html_url": "https://github.com/o/r/issues/117",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/117/comments":
			writeJSON(w, []map[string]any{{"body": "kitsoki-fixed-in: 4165e298"}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops issue-state-cache must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	output := filepath.Join(root, "stats", "issue-state.json")
	cmd := gitopsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"issue-state-cache", "--findings-root", root, "--output", output, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("issue-state-cache: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got["status"] != "issue_state_cached" || intValue(got, "issues_count") != 2 {
		t.Fatalf("cache output = %+v", got)
	}
	if got["output"] != output {
		t.Fatalf("output path = %+v", got)
	}
	wantPaths := []string{
		"GET /repos/o/r/issues/105",
		"GET /repos/o/r/issues/105/comments",
		"GET /repos/o/r/issues/117",
		"GET /repos/o/r/issues/117/comments",
	}
	if strings.Join(gotPaths, "\n") != strings.Join(wantPaths, "\n") {
		t.Fatalf("native requests:\n%s", strings.Join(gotPaths, "\n"))
	}
	cache, err := gitopsReadJSONFile(output)
	if err != nil {
		t.Fatalf("read output cache: %v", err)
	}
	issues := mapSliceValue(cache, "issues")
	if len(issues) != 2 {
		t.Fatalf("issues = %+v", issues)
	}
	if stringValue(issues[0], "issue_state") != "closed" || len(mapSliceValue(issues[0], "comments")) != 1 {
		t.Fatalf("first issue = %+v", issues[0])
	}
	if stringValue(issues[1], "issue_state") != "open" || stringValue(issues[1], "url") != "https://github.com/o/r/issues/117" {
		t.Fatalf("second issue = %+v", issues[1])
	}
}

func TestGitopsGHAgentGateRequiresIndependentVerifyEvidence(t *testing.T) {
	result := map[string]any{
		"gh_agent_enqueue_status":           "queued",
		"gh_agent_enqueued_count":           1,
		"gh_agent_drain_status":             "drained",
		"gh_agent_failed_count":             0,
		"gh_agent_active_count":             0,
		"gh_agent_done_count":               1,
		"gh_agent_fix_evidence_count":       1,
		"gh_agent_triage_evidence_count":    1,
		"gh_agent_independent_verify_count": 1,
		"gh_agent_missing_evidence_count":   0,
		"gh_agent_missing_triage_count":     0,
		"gh_agent_missing_verify_count":     0,
		"gh_agent_missing_run_url_count":    0,
	}
	if !gitopsGHAgentGateOK(result) {
		t.Fatalf("complete gh-agent result should pass")
	}
	result["gh_agent_missing_verify_count"] = 1
	if gitopsGHAgentGateOK(result) {
		t.Fatalf("missing independent verification must fail the gh-agent evidence gate")
	}
	result["gh_agent_missing_verify_count"] = 0
	result["gh_agent_missing_triage_count"] = 1
	if gitopsGHAgentGateOK(result) {
		t.Fatalf("missing triage preflight evidence must fail the gh-agent gate")
	}
}

func TestGitopsGHAgentGateRequiresReviewArtifactsForEveryJob(t *testing.T) {
	base := map[string]any{
		"gh_agent_enqueue_status":           "queued",
		"gh_agent_enqueued_count":           2,
		"gh_agent_drain_status":             "drained",
		"gh_agent_failed_count":             0,
		"gh_agent_active_count":             0,
		"gh_agent_done_count":               2,
		"gh_agent_fix_evidence_count":       2,
		"gh_agent_triage_evidence_count":    2,
		"gh_agent_independent_verify_count": 2,
		"gh_agent_missing_evidence_count":   0,
		"gh_agent_missing_triage_count":     0,
		"gh_agent_missing_verify_count":     0,
		"gh_agent_missing_run_url_count":    0,
	}
	if !gitopsGHAgentGateOK(base) {
		t.Fatalf("complete gh-agent artifact set should pass")
	}
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"missing fix evidence count", "gh_agent_fix_evidence_count", 1},
		{"missing triage evidence count", "gh_agent_triage_evidence_count", 1},
		{"missing independent verify count", "gh_agent_independent_verify_count", 1},
		{"missing run url count", "gh_agent_missing_run_url_count", 1},
		{"implicit zero evidence count", "gh_agent_fix_evidence_count", 0},
	} {
		result := make(map[string]any, len(base))
		for k, v := range base {
			result[k] = v
		}
		result[tc.field] = tc.value
		if gitopsGHAgentGateOK(result) {
			t.Fatalf("%s should fail gate: %+v", tc.name, result)
		}
	}
}

func TestGitopsIndependentVerifyGate(t *testing.T) {
	result := map[string]any{
		"gh_agent_enqueue_status":           "queued",
		"gh_agent_enqueued_count":           2,
		"gh_agent_independent_verify_count": 2,
		"gh_agent_missing_verify_count":     0,
	}
	if !gitopsIndependentVerifyGateOK(result) {
		t.Fatalf("complete independent verification should pass")
	}
	if got := gitopsIndependentVerifySummary(result); got != "verified=2/2" {
		t.Fatalf("summary = %q", got)
	}
	result["gh_agent_missing_verify_count"] = 1
	result["gh_agent_independent_verify_count"] = 1
	if gitopsIndependentVerifyGateOK(result) {
		t.Fatalf("missing independent verification should fail the independent gate")
	}
	if got := gitopsIndependentVerifySummary(result); got != "missing=1, verified=1/2" {
		t.Fatalf("missing summary = %q", got)
	}
}

func TestGitopsAutonomousFixChecksHostedAgentReadinessBeforeFiling(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/ready":
			writeJSON(w, map[string]any{
				"status":          "ready",
				"repo":            "other/repo",
				"public_base_url": agentURL(r),
				"worker":          "test-worker",
				"drain_enabled":   true,
			})
		default:
			t.Fatalf("unexpected agent request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer agent.Close()

	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("autonomous-fix must not touch GitHub after readiness fails: %s %s", r.Method, r.URL.String())
	}))
	defer github.Close()
	restoreAPI := host.SetGitHubAPIForTest(github.URL, github.Client())
	defer restoreAPI()

	runDir := t.TempDir()
	writeValidAutonomousControl(t, runDir)
	result, err := runGitopsAutonomousFix(context.Background(), gitopsAutonomousFixOptions{
		RunDir:        runDir,
		TicketRepo:    "o/r",
		AgentDB:       filepath.Join(runDir, "gh-agent.sqlite"),
		AgentStory:    "stories/bugfix",
		PublicBaseURL: agent.URL,
		CommentMode:   "none",
	})
	if err != nil {
		t.Fatalf("autonomous-fix readiness invalid result: %v", err)
	}
	if stringValue(result, "autonomous_fix_status") != "autonomous_fix_invalid" ||
		stringValue(result, "filing_status") != "not_run" ||
		stringValue(result, "validation_issue_summary") != "gh-agent-readiness" {
		t.Fatalf("result = %+v", result)
	}
	if stringValue(result, "gh_agent_health_status") != "pass" ||
		stringValue(result, "gh_agent_readiness_status") != "fail" ||
		!strings.Contains(stringValue(result, "gh_agent_readiness_summary"), "/api/ready") {
		t.Fatalf("readiness fields = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(runDir, "autonomous-fix-report.md")); err != nil {
		t.Fatalf("autonomous fix report not written: %v", err)
	}
}

func TestGitopsAutonomousFixTestBackendRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE", "1")

	runDir := t.TempDir()
	_, err := runGitopsAutonomousFix(context.Background(), gitopsAutonomousFixOptions{
		RunDir:        runDir,
		TicketRepo:    "o/r",
		AgentDB:       filepath.Join(runDir, "gh-agent.sqlite"),
		AgentStory:    "stories/bugfix",
		PublicBaseURL: "https://agent.example",
		CommentMode:   "none",
	})
	if err == nil {
		t.Fatal("expected fake autonomous-fix backend to require explicit opt-in")
	}
	if !strings.Contains(err.Error(), "--allow-test-backend") {
		t.Fatalf("expected --allow-test-backend guidance, got %v", err)
	}
}

func TestGitopsAutonomousFixChecksWatchdogBeforeAgentOrGitHub(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("autonomous-fix must not check hosted agent before watchdog passes: %s %s", r.Method, r.URL.String())
	}))
	defer agent.Close()
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("autonomous-fix must not touch GitHub before watchdog passes: %s %s", r.Method, r.URL.String())
	}))
	defer github.Close()
	restoreAPI := host.SetGitHubAPIForTest(github.URL, github.Client())
	defer restoreAPI()

	runDir := t.TempDir()
	result, err := runGitopsAutonomousFix(context.Background(), gitopsAutonomousFixOptions{
		RunDir:        runDir,
		TicketRepo:    "o/r",
		AgentDB:       filepath.Join(runDir, "gh-agent.sqlite"),
		AgentStory:    "stories/bugfix",
		PublicBaseURL: agent.URL,
		CommentMode:   "none",
	})
	if err != nil {
		t.Fatalf("autonomous-fix watchdog invalid result: %v", err)
	}
	if stringValue(result, "autonomous_fix_status") != "autonomous_fix_invalid" ||
		stringValue(result, "validation_issue_summary") != "autonomous-watchdog" ||
		stringValue(result, "filing_status") != "not_run" ||
		stringValue(result, "gh_agent_enqueue_status") != "not_run" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(stringValue(result, "autonomous_watchdog_summary"), "missing autonomous-marathon-control.json") {
		t.Fatalf("watchdog summary = %q", stringValue(result, "autonomous_watchdog_summary"))
	}
	if _, err := os.Stat(filepath.Join(runDir, "autonomous-marathon-watchdog.md")); err != nil {
		t.Fatalf("watchdog report not written: %v", err)
	}
}

func TestGitopsAutonomousFixAgentReadinessPassesForMatchingRepo(t *testing.T) {
	var base string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/healthz":
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/ready":
			writeJSON(w, map[string]any{
				"status":          "ready",
				"repo":            "o/r",
				"public_base_url": base,
				"worker":          "test-worker",
				"drain_enabled":   true,
			})
		default:
			t.Fatalf("unexpected agent request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer agent.Close()
	base = agent.URL

	health, readiness, ok, issue, summary := gitopsAutonomousFixAgentReady(context.Background(), agent.URL, "o/r")
	if !ok || issue != "" || summary != "" {
		t.Fatalf("readiness = ok %v issue %q summary %q", ok, issue, summary)
	}
	if stringValue(health, "status") != "pass" || stringValue(readiness, "status") != "pass" {
		t.Fatalf("health/readiness = %+v %+v", health, readiness)
	}
}

func TestGitopsAutonomousReportIncludesHostedAgentReadinessProof(t *testing.T) {
	runDir := t.TempDir()
	findings := map[string]any{
		"items": []any{
			map[string]any{
				"id":    "finding-1",
				"title": "reviewable autonomous issue",
				"github_issue": map[string]any{
					"url": "https://github.com/o/r/issues/42",
				},
			},
		},
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "findings.json"), findings); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	status := map[string]any{
		"ticket_repo":                "o/r",
		"autonomous_fix_status":      "autonomous_fix_valid",
		"autonomous_gate_summary":    "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass",
		"independent_verify_status":  "pass",
		"independent_verify_summary": "verified=1/1",
		"issue_closeout_status":      "closed",
		"issue_closeout_count":       1,
	}
	gitopsMergeAutonomousWatchdogProof(status, map[string]any{
		"autonomous_watchdog_status":        "autonomous_watchdog_ok",
		"autonomous_watchdog_summary":       "heartbeat age 10m within watchdog=45m",
		"autonomous_watchdog_markdown_path": filepath.Join(runDir, "autonomous-marathon-watchdog.md"),
		"heartbeat_age_minutes":             10,
	})
	gitopsMergeGHAgentReadinessProof(status,
		map[string]any{"status": "pass", "summary": "https://agent.example/healthz: ok"},
		map[string]any{"status": "pass", "summary": "https://agent.example/api/ready: ready for o/r as worker-1"},
	)
	path, err := writeGitopsAutonomousReport(runDir, status, nil, nil)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	for _, want := range []string{
		"## Autonomous Watchdog",
		"Status: `autonomous_watchdog_ok` - heartbeat age 10m within watchdog=45m",
		"Age: 10 minute(s)",
		"## Hosted GH-agent",
		"Health: `pass` - https://agent.example/healthz: ok",
		"Readiness: `pass` - https://agent.example/api/ready: ready for o/r as worker-1",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("report missing %q:\n%s", want, string(body))
		}
	}
}

func writeValidAutonomousControl(t *testing.T, runDir string) {
	t.Helper()
	control := map[string]any{
		"cadence": map[string]any{
			"created_at": time.Now().UTC().Format("2006-01-02T15:04:05+00:00"),
		},
		"watchdog": map[string]any{
			"heartbeat_minutes":   15,
			"watchdog_minutes":    45,
			"on_missed_heartbeat": "record_blocker_and_stop_before_spend",
		},
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "autonomous-marathon-control.json"), control); err != nil {
		t.Fatalf("write control: %v", err)
	}
}

func agentURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestGitopsEnqueueFixesClaimsGitHubIssueAndPersistsState(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	runDir := t.TempDir()
	findings := map[string]any{
		"run_id": "run-claim",
		"items": []any{
			map[string]any{
				"id":            "finding-claim",
				"kind":          "issue",
				"title":         "Parallel agents should see in-flight work",
				"summary":       "Claim comments and job context must be native gitops state.",
				"scenario":      "bugfix",
				"status":        "open",
				"origin":        "observed",
				"severity":      "high",
				"evidence_path": "evidence/bugfix.md",
				"github_issue": map[string]any{
					"url":    "https://github.com/o/r/issues/77",
					"repo":   "o/r",
					"number": "77",
				},
			},
		},
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "findings.json"), findings); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	var commentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/77/comments":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode comment: %v", err)
			}
			commentBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/77#issuecomment-claim"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops enqueue claim must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	dbPath := filepath.Join(runDir, "gh-agent.sqlite")
	result, err := gitopsEnqueueFixes(context.Background(), runDir, dbPath, "o/r", "stories/bugfix")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if intValue(result, "gh_agent_enqueued_count") != 1 || intValue(result, "gh_agent_claim_count") != 1 {
		t.Fatalf("enqueue result = %+v", result)
	}
	if stringValue(result, "gh_agent_claim_status") != "claimed" {
		t.Fatalf("claim status = %q", stringValue(result, "gh_agent_claim_status"))
	}
	for _, want := range []string{"kitsoki-autofix-claim", "finding-claim", "stories/bugfix", "github:o/r/issue/77"} {
		if !strings.Contains(commentBody, want) {
			t.Fatalf("claim body missing %q:\n%s", want, commentBody)
		}
	}

	updated, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	issue := mapValue(gitopsFindingsItems(updated)[0], "github_issue")
	if stringValue(issue, "claim_comment_url") != "https://github.com/o/r/issues/77#issuecomment-claim" {
		t.Fatalf("claim URL not persisted: %+v", issue)
	}
	if stringValue(issue, "claim_job_id") == "" || stringValue(issue, "claimed_by") != "kitsoki gitops autonomous-fix" {
		t.Fatalf("claim metadata not persisted: %+v", issue)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	job, err := store.GetJob(context.Background(), stringValue(mapSliceValue(result, "gh_agent_jobs")[0], "job_id"))
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.OriginRef != "github:o/r/issue/77" || job.Story != "stories/bugfix" {
		t.Fatalf("job = %+v", job)
	}
	if job.Metadata["ticket_title"] != "Parallel agents should see in-flight work" {
		t.Fatalf("ticket title metadata = %q", job.Metadata["ticket_title"])
	}
	for _, want := range []string{"Claim comments and job context", "bugfix", "evidence/bugfix.md", "https://github.com/o/r/issues/77"} {
		if !strings.Contains(job.Metadata["ticket_body"], want) {
			t.Fatalf("ticket body metadata missing %q:\n%s", want, job.Metadata["ticket_body"])
		}
	}
	if job.Metadata["ticket_source_mode"] != "remote" || job.Metadata["ticket_source_ref"] != "https://github.com/o/r/issues/77" {
		t.Fatalf("ticket source metadata = %+v", job.Metadata)
	}
	if got := mapSliceValue(result, "gh_agent_jobs")[0]["triage_context"]; got != true {
		t.Fatalf("triage_context = %v, want true", got)
	}
}

func TestGitopsCloseoutFixedIssuesCommentsClosesAndPersistsState(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	runDir := t.TempDir()
	findings := map[string]any{
		"run_id": "run-1",
		"items": []any{
			map[string]any{
				"id":            "finding-1",
				"kind":          "issue",
				"title":         "Done room should close tickets",
				"summary":       "manual close-out should be story-owned",
				"scenario":      "bugfix",
				"severity":      "high",
				"evidence_path": "trace.md",
				"status":        "blocked",
				"origin":        "observed",
				"github_issue": map[string]any{
					"url":    "https://github.com/o/r/issues/42",
					"repo":   "o/r",
					"number": "42",
				},
			},
		},
	}
	if err := gitopsWriteJSONFile(filepath.Join(runDir, "findings.json"), findings); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	var commentBody string
	var closedState string
	commentCalls := 0
	closeCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues/42/comments":
			commentCalls++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode comment: %v", err)
			}
			commentBody, _ = payload["body"].(string)
			writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/42#issuecomment-9"})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/issues/42":
			closeCalls++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode transition: %v", err)
			}
			closedState, _ = payload["state"].(string)
			writeJSON(w, map[string]any{"state": closedState})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("gitops closeout must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	status := map[string]any{
		"gh_agent_drained_jobs": []any{
			map[string]any{
				"origin_ref": "github:o/r/issue/42",
				"job_id":     "job-42",
				"state":      "done",
				"run_url":    "https://agent.example/run/job-42",
				"assets": []any{
					map[string]any{"name": "fix-report.md", "url": "https://agent.example/run/job-42/assets/fix-report.md"},
					map[string]any{"name": "independent-verify.md", "url": "https://agent.example/run/job-42/assets/independent-verify.md"},
				},
			},
		},
	}
	result, err := gitopsCloseoutFixedIssues(context.Background(), runDir, "o/r", status)
	if err != nil {
		t.Fatalf("closeout: %v", err)
	}
	if stringValue(result, "issue_closeout_status") != "closed" || intValue(result, "issue_closeout_count") != 1 {
		t.Fatalf("closeout result = %+v", result)
	}
	if closedState != "closed" {
		t.Fatalf("closed state = %q, want closed", closedState)
	}
	if commentCalls != 1 || closeCalls != 1 {
		t.Fatalf("first closeout calls: comments=%d closes=%d, want 1/1", commentCalls, closeCalls)
	}
	for _, want := range []string{"kitsoki-fixed-in", "job-42", "independent-verify.md", "https://agent.example/run/job-42"} {
		if !strings.Contains(commentBody, want) {
			t.Fatalf("comment body missing %q:\n%s", want, commentBody)
		}
	}
	updated, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	item := gitopsFindingsItems(updated)[0]
	issue := mapValue(item, "github_issue")
	if stringValue(item, "status") != "fixed" || stringValue(issue, "state") != "closed" {
		t.Fatalf("finding/issue not marked fixed/closed: item=%+v issue=%+v", item, issue)
	}
	if stringValue(issue, "closeout_comment_url") != "https://github.com/o/r/issues/42#issuecomment-9" {
		t.Fatalf("closeout comment url not persisted: %+v", issue)
	}
	closeout := mapValue(updated, "issue_closeout")
	if stringValue(closeout, "status") != "closed" || intValue(closeout, "count") != 1 {
		t.Fatalf("top-level closeout summary not persisted: %+v", closeout)
	}
	comments := mapSliceValue(issue, "comments")
	if len(comments) != 1 || !strings.Contains(stringValue(comments[0], "body"), "kitsoki-fixed-in") {
		t.Fatalf("fixed marker comment not persisted: %+v", comments)
	}

	second, err := gitopsCloseoutFixedIssues(context.Background(), runDir, "o/r", status)
	if err != nil {
		t.Fatalf("second closeout: %v", err)
	}
	if stringValue(second, "issue_closeout_status") != "closed" || intValue(second, "issue_closeout_count") != 1 {
		t.Fatalf("second closeout result = %+v", second)
	}
	if intValue(second, "issue_already_closed_count") != 1 {
		t.Fatalf("second closeout did not report already closed issue: %+v", second)
	}
	if commentCalls != 1 || closeCalls != 1 {
		t.Fatalf("second closeout must not repeat GitHub mutations: comments=%d closes=%d", commentCalls, closeCalls)
	}
	updatedAgain, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		t.Fatalf("read second findings: %v", err)
	}
	secondComments := mapSliceValue(mapValue(gitopsFindingsItems(updatedAgain)[0], "github_issue"), "comments")
	if len(secondComments) != 1 {
		t.Fatalf("second closeout duplicated comments: %+v", secondComments)
	}
}

func anyStringsFromInterface(value any) []string {
	values, _ := value.([]any)
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
