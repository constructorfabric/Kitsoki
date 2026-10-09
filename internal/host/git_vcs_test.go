package host_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// fakeRunner produces a deterministic mock for git/gh exec calls.
// Each Call records what was asked; each Response is keyed by the
// joined cmd+args so different invocations can return different things.
type fakeRunner struct {
	calls       []string
	responses   map[string]fakeResp
	defaultResp fakeResp
}

type fakeResp struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (f *fakeRunner) run(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
	key := name + " " + strings.Join(args, " ")
	dirKey := dir + "|" + key
	f.calls = append(f.calls, key)
	if r, ok := f.responses[dirKey]; ok {
		return r.stdout, r.stderr, r.code, r.err
	}
	if r, ok := f.responses[key]; ok {
		return r.stdout, r.stderr, r.code, r.err
	}
	// Substring / prefix-matched response — pick the longest matching prefix.
	var bestKey string
	for k := range f.responses {
		if strings.HasPrefix(key, k) && len(k) > len(bestKey) {
			bestKey = k
		}
	}
	if bestKey != "" {
		r := f.responses[bestKey]
		return r.stdout, r.stderr, r.code, r.err
	}
	return f.defaultResp.stdout, f.defaultResp.stderr, f.defaultResp.code, f.defaultResp.err
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{responses: map[string]fakeResp{}}
}

func TestGitVCS_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, name := range []string{
		"host.git",
		"host.git.branch",
		"host.git.diff",
		"host.git.commit",
		"host.git.push",
		"host.git.open_pr",
		"host.git.split_prs",
		"host.git.pr_status",
		"host.git.pr_comment",
	} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("registry: %s missing", name)
		}
	}
}

func TestGitVCS_SplitPRs_DryRun(t *testing.T) {
	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":                 "split_prs",
		"source_branch":      "feat/x",
		"integration_branch": "main",
		"dry_run":            true,
		"buckets_json":       `{"buckets":[{"concern":"Core API","title":"core","body":"body","shas":["aaaaaaa1"]}]}`,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	opened := res.Data["opened_prs"].(map[string]any)
	items := opened["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %#v", items)
	}
	item := items[0].(map[string]any)
	if item["branch"] != "feat/x-split-core-api" || item["url"] != "" || item["commits"] != 1 {
		t.Fatalf("item: %#v", item)
	}
}

func TestGitVCS_SplitPRs_NativeDoesNotRequireGh(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/pulls" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["base"] != "main" || payload["head"] != "feat/x-split-core" {
			t.Fatalf("payload: %#v", payload)
		}
		writeJSON(w, map[string]any{"number": 9, "html_url": "https://github.com/o/r/pull/9"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	fr := newFakeRunner()
	fr.responses["git worktree add"] = fakeResp{}
	fr.responses["git switch -q -c feat/x-split-core"] = fakeResp{}
	fr.responses["git cherry-pick aaaaaaa1"] = fakeResp{}
	fr.responses["git push -u origin HEAD"] = fakeResp{}
	fr.responses["git worktree remove"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":                 "split_prs",
		"workdir":            ".",
		"repo":               "o/r",
		"source_branch":      "feat/x",
		"integration_branch": "main",
		"buckets_json":       `{"buckets":[{"concern":"core","title":"core: fix","body":"body","shas":["aaaaaaa1"]}]}`,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s; calls=%v", res.Error, fr.calls)
	}
	opened := res.Data["opened_prs"].(map[string]any)
	items := opened["items"].([]any)
	item := items[0].(map[string]any)
	if item["url"] != "https://github.com/o/r/pull/9" {
		t.Fatalf("item: %#v", item)
	}
	for _, call := range fr.calls {
		if strings.HasPrefix(call, "gh ") {
			t.Fatalf("split_prs must not invoke gh; calls=%v", fr.calls)
		}
	}
}

func TestGitVCS_MissingOp(t *testing.T) {
	res, err := host.GitVCSHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing op")
	}
}

func TestGitVCS_UnknownOp(t *testing.T) {
	res, err := host.GitVCSHandler(context.Background(), map[string]any{"op": "fly"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for unknown op")
	}
}

func TestGitVCS_Branch_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git checkout -b feature/x main"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":      "branch",
		"workdir": "/tmp",
		"name":    "feature/x",
		"base":    "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["branch"] != "feature/x" {
		t.Fatalf("branch: %v", res.Data["branch"])
	}
	if len(fr.calls) != 1 || !strings.Contains(fr.calls[0], "checkout -b feature/x main") {
		t.Fatalf("calls: %v", fr.calls)
	}
}

func TestGitVCS_Branch_MissingName(t *testing.T) {
	restore := host.SetExecRunnerForTest(newFakeRunner().run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{"op": "branch"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing name")
	}
}

func TestGitVCS_Branch_ExitNonZero(t *testing.T) {
	fr := newFakeRunner()
	fr.defaultResp = fakeResp{stderr: "fatal: branch already exists", code: 128}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":   "branch",
		"name": "feature/x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if !strings.Contains(res.Error, "branch already exists") {
		t.Fatalf("expected stderr in error, got: %s", res.Error)
	}
}

func TestGitVCS_Diff_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git diff --patch"] = fakeResp{stdout: "diff --git a/x b/x\n+hello\n"}
	fr.responses["git diff --name-only"] = fakeResp{stdout: "x\ny\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{"op": "diff"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	diff, _ := res.Data["diff"].(string)
	if !strings.Contains(diff, "hello") {
		t.Fatalf("missing diff: %q", diff)
	}
	files, _ := res.Data["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
}

func TestGitVCS_Commit_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git commit -a -m fix: it"] = fakeResp{}
	fr.responses["git rev-parse HEAD"] = fakeResp{stdout: "deadbeefcafe\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":      "commit",
		"message": "fix: it",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["sha"] != "deadbeefcafe" {
		t.Fatalf("sha: %v", res.Data["sha"])
	}
}

func TestGitVCS_Commit_MissingMessage(t *testing.T) {
	restore := host.SetExecRunnerForTest(newFakeRunner().run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{"op": "commit"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing message")
	}
}

func TestGitVCS_Push_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git push -u origin HEAD"] = fakeResp{}
	fr.responses["git remote get-url origin"] = fakeResp{stdout: "git@github.com:owner/repo.git\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{"op": "push"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if !strings.Contains(res.Data["url"].(string), "github.com") {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

func TestGitVCS_OpenPR_NativeDoesNotRequireGh(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r":
			writeJSON(w, map[string]any{"default_branch": "main"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			want := map[string]any{"title": "PR", "body": "body", "head": "feature/native", "base": "main"}
			for k, v := range want {
				if payload[k] != v {
					t.Fatalf("payload[%s] = %v, want %v (payload=%#v)", k, payload[k], v, payload)
				}
			}
			writeJSON(w, map[string]any{"number": 42, "html_url": "https://github.com/o/r/pull/42"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	fr := newFakeRunner()
	fr.responses["git push -u origin HEAD"] = fakeResp{}
	fr.responses["git rev-parse --abbrev-ref HEAD"] = fakeResp{stdout: "feature/native\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "open_pr",
		"repo":  "o/r",
		"title": "PR",
		"body":  "body",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["pr_id"] != "42" {
		t.Fatalf("pr_id: %v", res.Data["pr_id"])
	}
	if res.Data["outcome"] != "opened" || res.Data["repo"] != "o/r" {
		t.Fatalf("open_pr result: %#v", res.Data)
	}
	for _, call := range fr.calls {
		if strings.HasPrefix(call, "gh ") {
			t.Fatalf("open_pr must not invoke gh; calls=%v", fr.calls)
		}
	}
}

func TestGitVCS_OpenPR_UsesExplicitBaseAndHead(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/pulls" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["base"] != "release" || payload["head"] != "fork:topic" {
			t.Fatalf("payload: %#v", payload)
		}
		if payload["draft"] != true {
			t.Fatalf("draft payload: %#v", payload)
		}
		writeJSON(w, map[string]any{"number": 43, "html_url": "https://github.com/o/r/pull/43"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	fr := newFakeRunner()
	fr.responses["git push -u origin HEAD"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "open_pr",
		"repo":  "o/r",
		"title": "PR",
		"body":  "body",
		"base":  "release",
		"head":  "fork:topic",
		"draft": true,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["pr_id"] != "43" {
		t.Fatalf("pr_id: %v", res.Data["pr_id"])
	}
	if !strings.Contains(res.Data["url"].(string), "/pull/43") {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

func TestGitVCS_PRStatus_NativeDoesNotRequireGh(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7":
			writeJSON(w, map[string]any{
				"state":    "open",
				"merged":   false,
				"html_url": "https://github.com/o/r/pull/7",
				"head":     map[string]any{"sha": "abc123"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7/comments":
			writeJSON(w, []map[string]any{{
				"id":       99,
				"body":     "please fix",
				"html_url": "https://github.com/o/r/pull/7#issuecomment-99",
				"user":     map[string]any{"login": "reviewer"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/status":
			writeJSON(w, map[string]any{"state": "success", "statuses": []map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
			writeJSON(w, map[string]any{"check_runs": []map[string]any{{"name": "test", "status": "completed", "conclusion": "success"}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		if name == "gh" {
			t.Fatalf("pr_status must not invoke gh: %s %s", name, strings.Join(args, " "))
		}
		return "", "", 1, fmt.Errorf("unexpected exec: %s", name)
	})
	defer restoreExec()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "pr_status",
		"repo":  "o/r",
		"pr_id": "7",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["state"] != "success" {
		t.Fatalf("state: %v", res.Data["state"])
	}
	if res.Data["pr_state"] != "open" {
		t.Fatalf("pr_state: %v", res.Data["pr_state"])
	}
	comments, _ := res.Data["comments"].([]map[string]any)
	if len(comments) != 1 || comments[0]["author"] != "reviewer" {
		t.Fatalf("comments: %#v", res.Data["comments"])
	}
}

func TestGitVCS_PRStatus_FailedChecks(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/8":
			writeJSON(w, map[string]any{"state": "open", "head": map[string]any{"sha": "def456"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/8/comments":
			writeJSON(w, []map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/def456/status":
			writeJSON(w, map[string]any{"state": "success", "statuses": []map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/def456/check-runs":
			writeJSON(w, map[string]any{"check_runs": []map[string]any{{
				"name":       "unit",
				"status":     "completed",
				"conclusion": "failure",
				"html_url":   "https://ci.example/unit",
			}}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "pr_status",
		"repo":  "o/r",
		"pr_id": "8",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["state"] != "failure" {
		t.Fatalf("state: %v", res.Data["state"])
	}
	checks, _ := res.Data["checks"].([]map[string]any)
	if len(checks) != 1 || checks[0]["name"] != "unit" {
		t.Fatalf("checks: %#v", res.Data["checks"])
	}
	if summary, _ := res.Data["checks_summary"].(string); !strings.Contains(summary, "state: failure") || !strings.Contains(summary, "unit") {
		t.Fatalf("checks_summary: %q", summary)
	}
	if failedLog, _ := res.Data["failed_log"].(string); !strings.Contains(failedLog, "unit") || !strings.Contains(failedLog, "https://ci.example/unit") {
		t.Fatalf("failed_log: %q", failedLog)
	}
}

func TestGitVCS_PRStatus_InfersRepoFromGitRemote(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7":
			writeJSON(w, map[string]any{"state": "open", "head": map[string]any{"sha": "abc123"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/7/comments":
			writeJSON(w, []map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/status":
			writeJSON(w, map[string]any{"state": "pending", "statuses": []map[string]any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
			writeJSON(w, map[string]any{"check_runs": []map[string]any{}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	fr := newFakeRunner()
	fr.responses["git remote get-url origin"] = fakeResp{stdout: "git@github.com:o/r.git\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "pr_status",
		"pr_id": "7",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["state"] != "pending" {
		t.Fatalf("state: %v", res.Data["state"])
	}
	if containsCall(fr.calls, "gh --version") {
		t.Fatalf("pr_status should not check gh availability; calls=%v", fr.calls)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestGitVCS_Commit_StageAll_IncludesNewFile(t *testing.T) {
	fr := newFakeRunner()
	// stage_all: git add -A runs first, then git commit -m (no -a flag).
	fr.responses["git add -A"] = fakeResp{}
	fr.responses["git commit -m feat: new file"] = fakeResp{}
	fr.responses["git rev-parse HEAD"] = fakeResp{stdout: "abc123\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":        "commit",
		"message":   "feat: new file",
		"stage_all": true,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["sha"] != "abc123" {
		t.Fatalf("sha: %v", res.Data["sha"])
	}
	// Verify git add -A was called before git commit.
	if len(fr.calls) < 2 {
		t.Fatalf("expected at least 2 calls, got: %v", fr.calls)
	}
	if fr.calls[0] != "git add -A" {
		t.Fatalf("expected first call to be 'git add -A', got: %q", fr.calls[0])
	}
	// Verify commit used plain -m (not -a), since stage_all already staged everything.
	if strings.Contains(fr.calls[1], "commit -a") {
		t.Fatalf("commit with stage_all should not use -a flag, got: %q", fr.calls[1])
	}
}

func TestGitVCS_PRComment_RequiresArgs(t *testing.T) {
	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op": "pr_comment",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when pr_id missing")
	}
}

func TestGitVCS_PRComment_NativeIssueComment(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues/7/comments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["body"] != "looks good" {
			t.Fatalf("payload: %#v", payload)
		}
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/pull/7#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		if name == "gh" {
			t.Fatalf("pr_comment must not invoke gh: %s %s", name, strings.Join(args, " "))
		}
		return "", "", 1, fmt.Errorf("unexpected exec: %s", name)
	})
	defer restoreExec()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "pr_comment",
		"repo":  "o/r",
		"pr_id": "7",
		"body":  "looks good",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true || !strings.Contains(res.Data["url"].(string), "issuecomment-1") {
		t.Fatalf("data: %#v", res.Data)
	}
}

func TestGitVCS_PRComment_NativeReviewEvent(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/pulls/7/reviews" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["event"] != "REQUEST_CHANGES" || payload["body"] != "please fix" {
			t.Fatalf("payload: %#v", payload)
		}
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/pull/7#pullrequestreview-2"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":              "pr_comment",
		"repo":            "o/r",
		"pr_id":           "7",
		"body":            "please fix",
		"request_changes": true,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("data: %#v", res.Data)
	}
}
