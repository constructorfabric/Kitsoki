package host_test

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

const sampleWorktreePorcelain = `worktree /repo
HEAD aaaaaaaa
branch refs/heads/main

worktree /repo/.worktrees/feature-x
HEAD bbbbbbbb
branch refs/heads/feature/x

`

func TestGitWorktree_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, n := range []string{
		"host.git_worktree",
		"host.git_worktree.list",
		"host.git_worktree.get",
		"host.git_worktree.create",
		"host.git_worktree.sync",
		"host.git_worktree.cleanup_scan",
		"host.git_worktree.cleanup_apply",
	} {
		if _, ok := r.Get(n); !ok {
			t.Fatalf("registry: %s missing", n)
		}
	}
}

func TestGitWorktree_MissingOp(t *testing.T) {
	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error")
	}
}

func TestGitWorktree_List_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: sampleWorktreePorcelain}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{"op": "list"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	wts, _ := res.Data["workspaces"].([]map[string]any)
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d (%v)", len(wts), wts)
	}
	// Last worktree should be feature-x.
	if wts[1]["id"] != "feature-x" {
		t.Fatalf("id: %v", wts[1]["id"])
	}
	if wts[1]["branch"] != "feature/x" {
		t.Fatalf("branch: %v", wts[1]["branch"])
	}
}

func TestGitWorktree_Get_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: sampleWorktreePorcelain}
	fr.responses["git status --porcelain"] = fakeResp{stdout: " M file.go\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op": "get",
		"id": "feature-x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["dirty"] != true {
		t.Fatalf("dirty: %v", res.Data["dirty"])
	}
	if res.Data["branch"] != "feature/x" {
		t.Fatalf("branch: %v", res.Data["branch"])
	}
}

func TestGitWorktree_Get_NotFound(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: sampleWorktreePorcelain}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op": "get",
		"id": "nope",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error")
	}
}

func TestGitWorktree_Create_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree add -b feature/x"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": "/repo",
		"name": "feature/x",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	path, _ := res.Data["path"].(string)
	if !strings.Contains(path, "/repo/.worktrees/feature-x") {
		t.Fatalf("path: %s", path)
	}
}

func TestGitWorktree_Create_EmptyRepoAnchorsAtGitTopLevel(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git rev-parse --show-toplevel"] = fakeResp{stdout: "/repo\n"}
	fr.responses["git worktree add -b feature/x /repo/.worktrees/feature-x main"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"name": "feature/x",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["path"] != "/repo/.worktrees/feature-x" {
		t.Fatalf("path: %v", res.Data["path"])
	}
	var sawRevParse, sawAdd bool
	for _, c := range fr.calls {
		if c == "git rev-parse --show-toplevel" {
			sawRevParse = true
		}
		if c == "git worktree add -b feature/x /repo/.worktrees/feature-x main" {
			sawAdd = true
		}
	}
	if !sawRevParse {
		t.Fatalf("expected git toplevel probe, got %v", fr.calls)
	}
	if !sawAdd {
		t.Fatalf("expected worktree add anchored under git toplevel, got %v", fr.calls)
	}
}

func TestGitWorktree_Create_EmptyRepoResolveFailure(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git rev-parse --show-toplevel"] = fakeResp{
		stderr: "fatal: not a git repository",
		code:   128,
	}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"name": "feature/x",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if !strings.Contains(res.Error, "not a git repository") {
		t.Fatalf("expected repo resolution error, got: %s", res.Error)
	}
	for _, c := range fr.calls {
		if strings.Contains(c, "worktree add") {
			t.Fatalf("should not create a worktree after repo resolution failure: %v", fr.calls)
		}
	}
}

// Authors that bind `workspace_id` from world state pass it as `id:` so
// the on-disk dir basename matches what `sync` looks up by. Without
// honouring `id:`, the dir is derived from `name` (slashes flattened)
// and worktreeSync (keyed on workspace_id) can't find what create just
// made — the silent-bounce-to-idle that surfaced in dogfood.
func TestGitWorktree_Create_IDOverridesDir(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree add -b fix/T1"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": "/repo",
		"id":   "bf-T1",
		"name": "fix/T1",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	path, _ := res.Data["path"].(string)
	if path != "/repo/.worktrees/bf-T1" {
		t.Fatalf("path: %s (want /repo/.worktrees/bf-T1)", path)
	}
	// Confirm the git invocation used the id-derived path, not the
	// name-derived one.
	var sawAdd bool
	for _, c := range fr.calls {
		if strings.Contains(c, "git worktree add -b fix/T1 /repo/.worktrees/bf-T1 main") {
			sawAdd = true
		}
		if strings.Contains(c, "/repo/.worktrees/fix-T1") {
			t.Fatalf("call used name-derived dir, not id: %s", c)
		}
	}
	if !sawAdd {
		t.Fatalf("expected `git worktree add -b fix/T1 /repo/.worktrees/bf-T1 main`, got %v", fr.calls)
	}
}

// Stale-branch recovery: a previous run left `fix/X` behind but the
// worktree dir was removed. A naive `git worktree add -b` fails with
// "a branch named ... already exists"; we retry without `-b` to
// reattach the existing branch. Without this, the operator hits a
// permanently-failing create that `on_error: idle` silently swallows.
func TestGitWorktree_Create_ReattachStaleBranch(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree add -b fix/T2 /repo/.worktrees/bf-T2 main"] = fakeResp{
		stderr: "fatal: a branch named 'fix/T2' already exists",
		code:   128,
	}
	fr.responses["git worktree add /repo/.worktrees/bf-T2 fix/T2"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": "/repo",
		"id":   "bf-T2",
		"name": "fix/T2",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["reused"] != true {
		t.Fatalf("expected reused=true, got %#v", res.Data)
	}
	if res.Data["path"] != "/repo/.worktrees/bf-T2" {
		t.Fatalf("path: %v", res.Data["path"])
	}
}

// Idempotency: a worktree already registered at our target path with
// our target branch is treated as success. Lets bf.idle re-enter
// (post-restart, post-restart_from) without re-running create against
// a workspace that's already on disk.
func TestGitWorktree_Create_IdempotentExistingWorktree(t *testing.T) {
	porcelain := "worktree /repo\nHEAD aaaa\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/bf-T3\nHEAD bbbb\nbranch refs/heads/fix/T3\n\n"
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: porcelain}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": "/repo",
		"id":   "bf-T3",
		"name": "fix/T3",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["path"] != "/repo/.worktrees/bf-T3" {
		t.Fatalf("path: %v", res.Data["path"])
	}
	// No `worktree add` should have been issued — we found the
	// existing one and short-circuited.
	for _, c := range fr.calls {
		if strings.Contains(c, "worktree add") {
			t.Fatalf("unexpected `worktree add`: %s", c)
		}
	}
}

// Same dir, wrong branch: report rather than silently overwrite. The
// operator likely has a parallel session or a misconfigured workspace.
func TestGitWorktree_Create_PathHeldByOtherBranch(t *testing.T) {
	porcelain := "worktree /repo\nHEAD aaaa\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/bf-T4\nHEAD bbbb\nbranch refs/heads/other-branch\n\n"
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: porcelain}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": "/repo",
		"id":   "bf-T4",
		"name": "fix/T4",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error (path held by other branch)")
	}
	if !strings.Contains(res.Error, "other-branch") {
		t.Fatalf("error should name the conflicting branch, got: %s", res.Error)
	}
}

func TestGitWorktree_Create_MissingName(t *testing.T) {
	restore := host.SetExecRunnerForTest(newFakeRunner().run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{"op": "create"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error")
	}
}

func TestGitWorktree_Sync_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: sampleWorktreePorcelain}
	fr.responses["git pull --ff-only"] = fakeResp{stdout: "Already up to date.\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op": "sync",
		"id": "feature-x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
}

func TestGitWorktree_CleanupScan_RecommendsOnlyMergedCleanUnprotectedCandidates(t *testing.T) {
	porcelain := `worktree /repo
HEAD aaaaaaaa
branch refs/heads/main

worktree /repo/.worktrees/merged-clean
HEAD bbbbbbbb
branch refs/heads/feature/merged-clean

worktree /repo/.worktrees/dirty
HEAD cccccccc
branch refs/heads/feature/dirty

worktree /repo/.worktrees/unmerged
HEAD dddddddd
branch refs/heads/feature/unmerged

worktree /repo/.worktrees/main-copy
HEAD eeeeeeee
branch refs/heads/main

`
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: porcelain}
	fr.responses["git status --porcelain"] = fakeResp{}
	fr.responses["/repo/.worktrees/dirty|git status --porcelain"] = fakeResp{stdout: " M file.go\n"}
	fr.responses["git branch --format=%(refname:short)"] = fakeResp{stdout: "main\nfeature/merged-clean\nfeature/dirty\nfeature/unmerged\nfeature/stale\n"}
	fr.responses["git merge-base --is-ancestor feature/merged-clean main"] = fakeResp{}
	fr.responses["git merge-base --is-ancestor feature/dirty main"] = fakeResp{}
	fr.responses["git merge-base --is-ancestor feature/unmerged main"] = fakeResp{code: 1}
	fr.responses["git merge-base --is-ancestor feature/stale main"] = fakeResp{}
	fr.responses["git merge-base --is-ancestor main main"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "cleanup_scan",
		"repo": "/repo",
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["recommended_count"] != 2 {
		t.Fatalf("recommended_count: %v", res.Data["recommended_count"])
	}
	candidates, _ := res.Data["candidates"].([]map[string]any)
	byBranch := map[string]map[string]any{}
	for _, c := range candidates {
		byBranch[c["branch"].(string)] = c
	}
	if byBranch["feature/merged-clean"]["recommended"] != true {
		t.Fatalf("merged clean worktree should be recommended: %#v", byBranch["feature/merged-clean"])
	}
	if byBranch["feature/stale"]["recommended"] != true || byBranch["feature/stale"]["kind"] != "branch" {
		t.Fatalf("merged branch-only candidate should be recommended: %#v", byBranch["feature/stale"])
	}
	if byBranch["feature/dirty"]["recommended"] != false {
		t.Fatalf("dirty worktree should not be recommended: %#v", byBranch["feature/dirty"])
	}
	if byBranch["feature/unmerged"]["recommended"] != false {
		t.Fatalf("unmerged branch should not be recommended: %#v", byBranch["feature/unmerged"])
	}
	if byBranch["main"]["recommended"] != false {
		t.Fatalf("protected main should not be recommended: %#v", byBranch["main"])
	}
}

func TestGitWorktree_CleanupScan_RefineExcludesMatchingCandidate(t *testing.T) {
	porcelain := `worktree /repo
HEAD aaaaaaaa
branch refs/heads/main

worktree /repo/.worktrees/merged-clean
HEAD bbbbbbbb
branch refs/heads/feature/merged-clean

`
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: porcelain}
	fr.responses["git status --porcelain"] = fakeResp{}
	fr.responses["git branch --format=%(refname:short)"] = fakeResp{stdout: "main\nfeature/merged-clean\n"}
	fr.responses["git merge-base --is-ancestor feature/merged-clean main"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":      "cleanup_scan",
		"repo":    "/repo",
		"base":    "main",
		"exclude": "merged-clean",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["recommended_count"] != 0 {
		t.Fatalf("recommended_count: %v", res.Data["recommended_count"])
	}
	candidates, _ := res.Data["candidates"].([]map[string]any)
	if candidates[0]["recommended"] != false || candidates[0]["reason"] != "excluded by refinement" {
		t.Fatalf("expected refined exclusion, got %#v", candidates[0])
	}
}

func TestGitWorktree_CleanupApply_RemovesOnlyRecommendedCandidates(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree remove /repo/.worktrees/merged-clean"] = fakeResp{}
	fr.responses["git branch -d feature/merged-clean"] = fakeResp{}
	fr.responses["git branch -d feature/stale"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "cleanup_apply",
		"repo": "/repo",
		"candidates": []any{
			map[string]any{"branch": "feature/merged-clean", "path": "/repo/.worktrees/merged-clean", "recommended": true},
			map[string]any{"branch": "feature/unmerged", "path": "/repo/.worktrees/unmerged", "recommended": false},
			map[string]any{"branch": "feature/stale", "path": "", "recommended": true},
		},
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	for _, want := range []string{
		"git worktree remove /repo/.worktrees/merged-clean",
		"git branch -d feature/merged-clean",
		"git branch -d feature/stale",
	} {
		var saw bool
		for _, call := range fr.calls {
			if call == want {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("missing call %q in %v", want, fr.calls)
		}
	}
	for _, call := range fr.calls {
		if strings.Contains(call, "feature/unmerged") {
			t.Fatalf("unrecommended candidate should not be deleted: %v", fr.calls)
		}
	}
}

func TestGitWorktree_CleanupApply_AcceptsJSONCandidateList(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git worktree remove /repo/.worktrees/merged-clean"] = fakeResp{}
	fr.responses["git branch -d feature/merged-clean"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":         "cleanup_apply",
		"repo":       "/repo",
		"candidates": `[{"branch":"feature/merged-clean","path":"/repo/.worktrees/merged-clean","recommended":true}]`,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls: %v", fr.calls)
	}
}
