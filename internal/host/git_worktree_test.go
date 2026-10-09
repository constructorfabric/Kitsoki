package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/host"
)

const sampleWorktreePorcelain = `worktree /repo
HEAD aaaaaaaa
branch refs/heads/main

worktree /repo/.worktrees/feature-x
HEAD bbbbbbbb
branch refs/heads/feature/x

`

func devWorkspaceScriptForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", "scripts", "dev-workspace.sh"))
}

func devWorkspaceCreatePrefix(t *testing.T) string {
	t.Helper()
	return devWorkspaceScriptForTest(t) + " create"
}

func devWorkspaceCreateJSON(t *testing.T, id, path, branch, root string, reused bool) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"ok":     true,
		"id":     id,
		"path":   path,
		"branch": branch,
		"root":   root,
		"reused": reused,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

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
		"host.git_worktree.clone_create",
		"host.git_worktree.clone_cleanup_scan",
		"host.git_worktree.clone_cleanup_apply",
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
	path := "/repo/.capsules/workspaces/feature-x"
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "feature-x", path, "feature/x", "/repo/.capsules/workspaces", false)}
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
	gotPath, _ := res.Data["path"].(string)
	if !strings.Contains(gotPath, "/repo/.capsules/workspaces/feature-x") {
		t.Fatalf("path: %s", gotPath)
	}
}

func TestGitWorktree_Create_EmptyRepoAnchorsAtGitTopLevel(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git rev-parse --show-toplevel"] = fakeResp{stdout: "/repo\n"}
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "feature-x", "/repo/.capsules/workspaces/feature-x", "feature/x", "/repo/.capsules/workspaces", false)}
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
	if res.Data["path"] != "/repo/.capsules/workspaces/feature-x" {
		t.Fatalf("path: %v", res.Data["path"])
	}
	var sawRevParse, sawCreate bool
	for _, c := range fr.calls {
		if c == "git rev-parse --show-toplevel" {
			sawRevParse = true
		}
		if strings.HasPrefix(c, devWorkspaceCreatePrefix(t)) {
			sawCreate = true
		}
	}
	if !sawRevParse {
		t.Fatalf("expected git toplevel probe, got %v", fr.calls)
	}
	if !sawCreate {
		t.Fatalf("expected scripted workspace create anchored under git toplevel, got %v", fr.calls)
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
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "bf-T1", "/repo/.capsules/workspaces/bf-T1", "fix/T1", "/repo/.capsules/workspaces", false)}
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
	if path != "/repo/.capsules/workspaces/bf-T1" {
		t.Fatalf("path: %s (want /repo/.capsules/workspaces/bf-T1)", path)
	}
	// Confirm the script invocation used the id, not the name-derived one.
	var sawCreate bool
	for _, c := range fr.calls {
		if strings.HasPrefix(c, devWorkspaceCreatePrefix(t)) && strings.Contains(c, "--id bf-T1") {
			sawCreate = true
		}
		if strings.Contains(c, "fix-T1") {
			t.Fatalf("call used name-derived dir, not id: %s", c)
		}
	}
	if !sawCreate {
		t.Fatalf("expected scripted create with --id bf-T1, got %v", fr.calls)
	}
}

// Stale-branch recovery: a previous run left `fix/X` behind but the
// worktree dir was removed. A naive `git worktree add -b` fails with
// "a branch named ... already exists"; we retry without `-b` to
// reattach the existing branch. Without this, the operator hits a
// permanently-failing create that `on_error: idle` silently swallows.
func TestGitWorktree_Create_ReattachStaleBranch(t *testing.T) {
	fr := newFakeRunner()
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "bf-T2", "/repo/.capsules/workspaces/bf-T2", "fix/T2", "/repo/.capsules/workspaces", true)}
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
	if res.Data["path"] != "/repo/.capsules/workspaces/bf-T2" {
		t.Fatalf("path: %v", res.Data["path"])
	}
}

// Idempotency: a worktree already registered at our target path with
// our target branch is treated as success. Lets bf.idle re-enter
// (post-restart, post-restart_from) without re-running create against
// a workspace that's already on disk.
func TestGitWorktree_Create_IdempotentExistingWorktree(t *testing.T) {
	fr := newFakeRunner()
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "bf-T3", "/repo/.capsules/workspaces/bf-T3", "fix/T3", "/repo/.capsules/workspaces", true)}
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
	if res.Data["path"] != "/repo/.capsules/workspaces/bf-T3" {
		t.Fatalf("path: %v", res.Data["path"])
	}
	// No raw `git worktree add` should have been issued; the script owns
	// idempotency.
	for _, c := range fr.calls {
		if strings.Contains(c, "worktree add") {
			t.Fatalf("unexpected `worktree add`: %s", c)
		}
	}
}

// Same dir, wrong branch: report rather than silently overwrite. The
// operator likely has a parallel session or a misconfigured workspace.
func TestGitWorktree_Create_PathHeldByOtherBranch(t *testing.T) {
	fr := newFakeRunner()
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stderr: "error: create: /repo/.capsules/workspaces/bf-T4 already holds branch other-branch (wanted fix/T4)", code: 1}
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

func TestGitWorktree_CleanupScan_RecommendsGeneratedCachesInDirtyWorktree(t *testing.T) {
	repo := t.TempDir()
	wtPath := filepath.Join(repo, ".worktrees", "dirty")
	cachePath := filepath.Join(wtPath, ".artifacts", "go-cache")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "obj"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	porcelain := "worktree " + repo + "\nHEAD aaaaaaaa\nbranch refs/heads/main\n\n" +
		"worktree " + wtPath + "\nHEAD bbbbbbbb\nbranch refs/heads/feature/dirty\n\n"
	fr := newFakeRunner()
	fr.responses["git worktree list --porcelain"] = fakeResp{stdout: porcelain}
	fr.responses["git status --porcelain"] = fakeResp{}
	fr.responses[wtPath+"|git status --porcelain"] = fakeResp{stdout: " M file.go\n"}
	fr.responses["git branch --format=%(refname:short)"] = fakeResp{stdout: "main\nfeature/dirty\n"}
	fr.responses["git merge-base --is-ancestor feature/dirty main"] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "cleanup_scan",
		"repo": repo,
		"base": "main",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	candidates, _ := res.Data["candidates"].([]map[string]any)
	var sawDirtyWorktree, sawCache bool
	for _, c := range candidates {
		if c["kind"] == "worktree" && c["branch"] == "feature/dirty" {
			sawDirtyWorktree = true
			if c["recommended"] != false {
				t.Fatalf("dirty worktree itself should not be recommended: %#v", c)
			}
		}
		if c["kind"] == "cache" && c["path"] == cachePath {
			sawCache = true
			if c["recommended"] != true || c["preserves_branch"] != true {
				t.Fatalf("cache should be independently recommended: %#v", c)
			}
			if c["size_bytes"].(int64) == 0 {
				t.Fatalf("cache size should be measured: %#v", c)
			}
		}
	}
	if !sawDirtyWorktree || !sawCache {
		t.Fatalf("expected dirty worktree and cache candidates, got %#v", candidates)
	}
}

func TestGitWorktree_CleanupApply_RemovesCacheWithoutDeletingBranch(t *testing.T) {
	repo := t.TempDir()
	cachePath := filepath.Join(repo, ".worktrees", "dirty", ".cache")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(cachePath, "readonly")
	if err := os.WriteFile(locked, []byte("cached"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cachePath, 0o500); err != nil {
		t.Fatal(err)
	}
	fr := newFakeRunner()
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "cleanup_apply",
		"repo": repo,
		"candidates": []any{
			map[string]any{"kind": "cache", "branch": "feature/dirty", "path": cachePath, "recommended": true},
		},
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("cache still exists or stat failed unexpectedly: %v", err)
	}
	for _, call := range fr.calls {
		if strings.Contains(call, "branch -d") {
			t.Fatalf("cache cleanup must not delete branches: %v", fr.calls)
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

func TestGitWorktree_CloneCreate_CreatesIsolatedCloneWithSentinel(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "case-1")
	fr := newFakeRunner()
	fr.responses[devWorkspaceCreatePrefix(t)] = fakeResp{stdout: devWorkspaceCreateJSON(t, "case-1", path, "fix/case-1", root, false)}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":         "clone_create",
		"repo":       "/repo",
		"root":       root,
		"id":         "case-1",
		"name":       "fix/case-1",
		"base":       "main",
		"session_id": "S1",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["path"] != path {
		t.Fatalf("path: %v", res.Data["path"])
	}
	for _, call := range fr.calls {
		if strings.HasPrefix(call, devWorkspaceCreatePrefix(t)) {
			if !strings.Contains(call, "--id case-1") ||
				!strings.Contains(call, "--branch fix/case-1") ||
				!strings.Contains(call, "--session-id S1") {
				t.Fatalf("script call missing expected args: %s", call)
			}
			return
		}
	}
	t.Fatalf("missing dev-workspace create call in %v", fr.calls)
}

func TestGitWorktree_CloneCleanupScan_RecommendsOnlyOwnedOldCleanClones(t *testing.T) {
	root := t.TempDir()
	oldClean := writeCloneTestDir(t, root, "old-clean", time.Now().Add(-48*time.Hour))
	newClean := writeCloneTestDir(t, root, "new-clean", time.Now())
	dirty := writeCloneTestDir(t, root, "dirty", time.Now().Add(-48*time.Hour))
	if err := os.Mkdir(filepath.Join(root, "not-owned"), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := newFakeRunner()
	fr.responses[oldClean+"|git branch --show-current"] = fakeResp{stdout: "fix/old-clean\n"}
	fr.responses[newClean+"|git branch --show-current"] = fakeResp{stdout: "fix/new-clean\n"}
	fr.responses[dirty+"|git branch --show-current"] = fakeResp{stdout: "fix/dirty\n"}
	fr.responses["git status --porcelain"] = fakeResp{}
	fr.responses[dirty+"|git status --porcelain"] = fakeResp{stdout: " M file.go\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":            "clone_cleanup_scan",
		"repo":          "/repo",
		"root":          root,
		"min_age_hours": "24",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["recommended_count"] != 1 {
		t.Fatalf("recommended_count: %v", res.Data["recommended_count"])
	}
	candidates, _ := res.Data["candidates"].([]map[string]any)
	byID := map[string]map[string]any{}
	for _, c := range candidates {
		byID[c["id"].(string)] = c
	}
	if byID["old-clean"]["recommended"] != true {
		t.Fatalf("old clean clone should be recommended: %#v", byID["old-clean"])
	}
	if byID["new-clean"]["recommended"] != false {
		t.Fatalf("new clone should not be recommended: %#v", byID["new-clean"])
	}
	if byID["dirty"]["recommended"] != false {
		t.Fatalf("dirty clone should not be recommended: %#v", byID["dirty"])
	}
	if _, ok := byID["not-owned"]; ok {
		t.Fatalf("unowned dir should not be a candidate: %#v", byID["not-owned"])
	}
}

func TestGitWorktree_CloneCleanupApply_RemovesOnlyRecommendedOwnedClones(t *testing.T) {
	root := t.TempDir()
	removeMe := writeCloneTestDir(t, root, "remove-me", time.Now().Add(-48*time.Hour))
	keepMe := writeCloneTestDir(t, root, "keep-me", time.Now().Add(-48*time.Hour))
	fr := newFakeRunner()
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitWorktreeHandler(context.Background(), map[string]any{
		"op":   "clone_cleanup_apply",
		"repo": "/repo",
		"root": root,
		"candidates": []any{
			map[string]any{"id": "remove-me", "path": removeMe, "recommended": true},
			map[string]any{"id": "keep-me", "path": keepMe, "recommended": false},
			map[string]any{"id": "outside", "path": filepath.Join(root, "..", "outside"), "recommended": true},
		},
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected outside path error")
	}
	if _, err := os.Stat(removeMe); !os.IsNotExist(err) {
		t.Fatalf("recommended clone should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(keepMe); err != nil {
		t.Fatalf("unrecommended clone should remain: %v", err)
	}
}

func writeCloneTestDir(t *testing.T, root, id string, createdAt time.Time) string {
	t.Helper()
	path := filepath.Join(root, id)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + id + `","branch":"fix/` + id + `","created_at":"` + createdAt.UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(path, ".kitsoki-clone"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
