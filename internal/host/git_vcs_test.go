package host_test

import (
	"context"
	"fmt"
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
		"host.git.pr_status",
		"host.git.pr_comment",
	} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("registry: %s missing", name)
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

func TestGitVCS_OpenPR_NoGh(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{err: fmt.Errorf("not found")}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "open_pr",
		"title": "x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected clean domain error when gh missing")
	}
	if !strings.Contains(res.Error, "gh") {
		t.Fatalf("error should mention gh: %s", res.Error)
	}
}

func TestGitVCS_OpenPR_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{stdout: "gh version 2.x\n"}
	fr.responses["gh pr create --title PR --body body"] = fakeResp{
		stdout: "https://github.com/o/r/pull/42\n",
	}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "open_pr",
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
	if !strings.Contains(res.Data["url"].(string), "/pull/42") {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

func TestGitVCS_PRStatus_NoGh(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{code: 127}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":    "pr_status",
		"pr_id": "1",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected clean domain error when gh missing")
	}
}

func TestGitVCS_PRStatus_UsesRepoFlag(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{stdout: "gh version 2.0.0\n"}
	fr.responses["gh pr view 7 --repo o/r --json state,statusCheckRollup"] = fakeResp{stdout: `{"state":"OPEN"}`}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

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
	if res.Data["state"] != `{"state":"OPEN"}` {
		t.Fatalf("state: %v", res.Data["state"])
	}
	if !containsCall(fr.calls, "gh pr view 7 --repo o/r --json state,statusCheckRollup") {
		t.Fatalf("repo-scoped pr view not called; calls=%v", fr.calls)
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
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

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
