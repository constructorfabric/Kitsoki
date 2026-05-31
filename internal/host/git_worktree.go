// Package host — host.git_worktree — git-worktree-backed workspace provider.
//
// Implements the `workspace` host_interface (see docs/architecture/hosts.md).  A
// single prefix-fallback handler dispatches the four workspace ops via
// the `op` arg.  Operations shell out to `git worktree`.
//
// Convention: workspace ID == the worktree's directory basename; the
// worktrees live under `<repo>/.worktrees/<id>` (matching the
// kitsoki-dev dogfood path).
package host

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// GitWorktreeHandler implements host.git_worktree (prefix-fallback).
//
// Required args:
//   - op (string): one of list, get, create, sync.
//
// Optional/per-op args:
//   - repo (string): path to the main repository.  Defaults to cwd if absent.
//   - id   (string): workspace id (== basename of the worktree dir).
//
// The `create` op additionally requires `name` (the new branch).
// Optional create args:
//   - id   (string): explicit workspace id.  Becomes the worktree's
//     directory basename.  When absent, falls back to the legacy
//     slashes-flattened `name` (`feature/foo` → `feature-foo`) for
//     back-compat with callers that only supply the branch.  Authors
//     that bind `workspace_id` from world state should pass it as
//     `id:` so the on-disk dir matches what `sync` looks up by.
//   - base (string): branch the new worktree is rooted at.
func GitWorktreeHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.git_worktree: op argument is required"}, nil
	}
	repo, _ := args["repo"].(string)

	switch op {
	case "list":
		return worktreeList(ctx, repo)
	case "get":
		return worktreeGet(ctx, repo, args)
	case "create":
		return worktreeCreate(ctx, repo, args)
	case "sync":
		return worktreeSync(ctx, repo, args)
	default:
		return Result{Error: fmt.Sprintf("host.git_worktree: unknown op %q", op)}, nil
	}
}

// worktreeList parses `git worktree list --porcelain` into a slice of
// {id, path, branch, dirty} maps.
func worktreeList(ctx context.Context, repo string) (Result, error) {
	stdout, stderr, code, err := cliExec(ctx, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return Result{Error: fmt.Sprintf("workspace.list: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("workspace.list: %s", strings.TrimSpace(stderr))}, nil
	}
	wts := parseWorktreePorcelain(stdout)
	out := make([]map[string]any, 0, len(wts))
	for _, wt := range wts {
		out = append(out, worktreeSummary(wt))
	}
	return Result{Data: map[string]any{"workspaces": out}}, nil
}

func worktreeGet(ctx context.Context, repo string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "workspace.get: id argument is required"}, nil
	}
	stdout, _, _, err := cliExec(ctx, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return Result{Error: fmt.Sprintf("workspace.get: exec: %v", err)}, nil
	}
	for _, wt := range parseWorktreePorcelain(stdout) {
		if filepath.Base(wt.Path) == id {
			// Also probe `git status --porcelain` in the worktree to
			// resolve dirty.
			dirty := false
			if statusOut, _, _, sErr := cliExec(ctx, wt.Path, "git", "status", "--porcelain"); sErr == nil {
				dirty = strings.TrimSpace(statusOut) != ""
			}
			wt.Dirty = dirty
			data := worktreeSummary(wt)
			return Result{Data: data}, nil
		}
	}
	return Result{Error: fmt.Sprintf("workspace.get: %q not found", id)}, nil
}

func worktreeCreate(ctx context.Context, repo string, args map[string]any) (Result, error) {
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return Result{Error: "workspace.create: name argument is required"}, nil
	}
	base, _ := args["base"].(string)
	// Explicit `id:` (from world.workspace_id) wins; fall back to the
	// slashes-flattened branch for callers that only supply `name`.
	// Without the explicit id, the on-disk dir basename diverged from
	// the workspace_id authors wrote into world state, so worktreeSync
	// (which keys on workspace_id) couldn't find the dir worktreeCreate
	// had just made. Symptom: implementing.on_enter → workspace.sync
	// errors with "not found" → on_error: idle quietly bounced the
	// operator back to the parked room.
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		id = strings.ReplaceAll(name, "/", "-")
	}
	path := filepath.Join(repo, ".worktrees", id)

	// Idempotency: if a worktree is already registered at our path
	// with our target branch, treat as success. This keeps re-entry
	// to bf.idle (e.g. after a process restart that lost
	// bf_autostart_attempted=true) from failing on a workspace that
	// already exists from a prior run.
	if existing, ok := findWorktreeByPath(ctx, repo, path); ok {
		if existing.Branch == name {
			return Result{Data: map[string]any{"ok": true, "path": path}}, nil
		}
		return Result{Error: fmt.Sprintf("workspace.create: %q already exists at %s but holds branch %q (wanted %q)", id, path, existing.Branch, name)}, nil
	}

	// Try the new-branch path first. The common case is a fresh
	// ticket where neither the branch nor the dir exists.
	gitArgs := []string{"worktree", "add", "-b", name, path}
	if base != "" {
		gitArgs = append(gitArgs, base)
	}
	_, stderr, code, err := cliExec(ctx, repo, "git", gitArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("workspace.create: exec: %v", err)}, nil
	}
	if code == 0 {
		return Result{Data: map[string]any{"ok": true, "path": path}}, nil
	}

	// Branch-already-exists recovery. Happens when a previous run
	// created the branch but the worktree dir was later removed
	// without `git branch -d`. Without this, the operator hits a
	// permanently-failing create that on_error: idle silently swallows
	// and has to clean up by hand. Re-attach the existing branch
	// to a fresh worktree at our path instead.
	if branchExistsError(stderr, name) {
		retryArgs := []string{"worktree", "add", path, name}
		_, retryStderr, retryCode, retryErr := cliExec(ctx, repo, "git", retryArgs...)
		if retryErr != nil {
			return Result{Error: fmt.Sprintf("workspace.create: exec (reattach): %v", retryErr)}, nil
		}
		if retryCode == 0 {
			return Result{Data: map[string]any{
				"ok":     true,
				"path":   path,
				"reused": true,
				"branch": name,
			}}, nil
		}
		// Reattach can fail when the branch is checked out at *another*
		// worktree (a parallel session, an unrelated dir). Report the
		// underlying git message so the operator can locate the holder.
		return Result{Error: fmt.Sprintf("workspace.create: branch %q exists but reattach failed: %s", name, strings.TrimSpace(retryStderr))}, nil
	}

	return Result{Error: fmt.Sprintf("workspace.create: %s", strings.TrimSpace(stderr))}, nil
}

// findWorktreeByPath returns the worktreeInfo registered for the
// workspace whose path matches `path`. `git worktree list --porcelain`
// always emits absolute paths, but callers commonly construct `path`
// relative to `repo` (which itself may be empty / cwd), so the
// straight `wt.Path == path` comparison silently misses every
// re-entry — which is exactly what made the dogfood session loop:
// the worktree at `/repo/.worktrees/bf-X` was actually registered,
// but we couldn't see it, so we fell through to `git worktree add`
// which then failed with `<path> already exists`.
//
// Normalise both sides via filepath.Abs (resolving `path` against the
// process cwd when `repo` is empty, which mirrors cliExec's behaviour)
// and also accept a basename match as a last resort — workspace ids
// are unique by convention in `.worktrees/<id>`.
func findWorktreeByPath(ctx context.Context, repo, path string) (worktreeInfo, bool) {
	stdout, _, _, err := cliExec(ctx, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return worktreeInfo{}, false
	}
	absPath, _ := filepath.Abs(path)
	base := filepath.Base(path)
	for _, wt := range parseWorktreePorcelain(stdout) {
		if wt.Path == path || wt.Path == absPath {
			return wt, true
		}
		if base != "" && filepath.Base(wt.Path) == base {
			return wt, true
		}
	}
	return worktreeInfo{}, false
}

// branchExistsError reports whether the stderr from `git worktree add
// -b` indicates the branch already exists locally. Git's exact phrasing
// is "fatal: a branch named '<name>' already exists" (with surrounding
// noise from the porcelain). Match on the stable middle so phrasing
// drift between git versions doesn't silently break the recovery path.
func branchExistsError(stderr, name string) bool {
	s := strings.ToLower(stderr)
	if !strings.Contains(s, "already exists") {
		return false
	}
	return strings.Contains(stderr, "'"+name+"'") || strings.Contains(s, "branch named")
}

func worktreeSync(ctx context.Context, repo string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "workspace.sync: id argument is required"}, nil
	}
	// Find the path for the named workspace.
	stdout, _, _, err := cliExec(ctx, repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return Result{Error: fmt.Sprintf("workspace.sync: exec: %v", err)}, nil
	}
	var target *worktreeInfo
	for _, wt := range parseWorktreePorcelain(stdout) {
		if filepath.Base(wt.Path) == id {
			w := wt
			target = &w
			break
		}
	}
	if target == nil {
		return Result{Error: fmt.Sprintf("workspace.sync: %q not found", id)}, nil
	}
	// No-op if the branch has no upstream tracking. A fresh
	// `fix/<ticket>` feature branch typically has no remote yet —
	// `git pull --ff-only` would fail with `There is no tracking
	// information for the current branch`, on_error: idle would
	// silently bounce us back to a parked room, and the operator
	// would have no signal as to why. Detect via
	// `git rev-parse --abbrev-ref @{u}` (non-zero exit when no
	// upstream is set) and skip the pull in that case.
	if _, _, upstreamCode, upstreamErr := cliExec(ctx, target.Path, "git", "rev-parse", "--abbrev-ref", "@{u}"); upstreamErr != nil || upstreamCode != 0 {
		return Result{Data: map[string]any{
			"ok":             true,
			"log":            "",
			"skipped_reason": "no upstream tracking",
		}}, nil
	}
	// Pull --ff-only from the upstream — non-destructive, returns
	// error if the branch has diverged.
	pullOut, stderr, code, err := cliExec(ctx, target.Path, "git", "pull", "--ff-only")
	if err != nil {
		return Result{Error: fmt.Sprintf("workspace.sync: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("workspace.sync: %s", strings.TrimSpace(stderr))}, nil
	}
	return Result{Data: map[string]any{
		"ok":  true,
		"log": pullOut,
	}}, nil
}

// ─── porcelain parser ───────────────────────────────────────────────────────

type worktreeInfo struct {
	Path   string
	Branch string
	Head   string
	Dirty  bool
}

// parseWorktreePorcelain reads `git worktree list --porcelain` output.
// Records are separated by blank lines; within each record, keys are
// "worktree <path>", "HEAD <sha>", "branch <refs/heads/...>" lines.
func parseWorktreePorcelain(s string) []worktreeInfo {
	var out []worktreeInfo
	var cur worktreeInfo
	flush := func() {
		if cur.Path != "" {
			out = append(out, cur)
		}
		cur = worktreeInfo{}
	}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(ln, "worktree "):
			cur.Path = strings.TrimPrefix(ln, "worktree ")
		case strings.HasPrefix(ln, "HEAD "):
			cur.Head = strings.TrimPrefix(ln, "HEAD ")
		case strings.HasPrefix(ln, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(ln, "branch "), "refs/heads/")
		}
	}
	flush()
	return out
}

func worktreeSummary(wt worktreeInfo) map[string]any {
	id := filepath.Base(wt.Path)
	return map[string]any{
		"id":     id,
		"path":   wt.Path,
		"branch": wt.Branch,
		"dirty":  wt.Dirty,
	}
}
