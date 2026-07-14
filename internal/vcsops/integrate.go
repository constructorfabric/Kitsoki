// Package vcsops holds the plain-Go git-integration primitives shared by the
// studio MCP's vcs.integrate tool (internal/mcp/studio/vcs_tools.go) and the
// gh-agent real-dispatch fix-landing path (internal/ghagent/realdispatch.go).
// It has no MCP/Cobra dependencies so either caller can import it without
// pulling in the other's package graph.
//
// Integrate replaces the by-hand squash-merge ritual
//
//	git -C wt add -A && git -C wt reset --soft main && git -C wt commit \
//	  && git merge --ff-only && git worktree remove …
//
// which once DESTROYED main: `reset --soft main` inside a stale worktree
// commits that worktree's stale tree, reverting everything landed on main
// since the worktree's base. Integrate instead runs a guarded 3-way squash
// merge FROM the target checkout, which cannot revert post-base work.
package vcsops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kitsoki/internal/host"
)

// IntegrateOptions carries Integrate's optional post-land cleanup.
type IntegrateOptions struct {
	// WorktreePath, when set, is removed (`git worktree remove --force`)
	// after a successful land.
	WorktreePath string
	// DeleteBranch, when true, deletes Branch (`git branch -D`) after a
	// successful land.
	DeleteBranch bool
}

// Result is Integrate's outcome. Exactly one of Integrated / Refused /
// Conflicts is meaningful: Refused names a precondition that failed (a guard
// fired), Conflicts lists the paths a squash merge could not auto-resolve
// (the tip was restored), Commit is the squash commit on success.
type Result struct {
	Integrated bool
	Commit     string
	Conflicts  []string
	Refused    string
}

// Integrate squash-merges branch onto onto, run FROM dir — a checkout that
// must already be on onto. This guard is why main survives: Integrate never
// switches branches under the caller, and a merge conflict restores dir's
// clean tip via `reset --hard` rather than leaving a half-merged tree.
func Integrate(ctx context.Context, dir, branch, onto, message string, opts IntegrateOptions) (Result, error) {
	if strings.TrimSpace(dir) == "" {
		return Result{}, errors.New("vcsops.Integrate: dir is required")
	}
	if strings.TrimSpace(branch) == "" {
		return Result{}, errors.New("vcsops.Integrate: branch is required")
	}
	if strings.TrimSpace(message) == "" {
		return Result{}, errors.New("vcsops.Integrate: message is required (the squash commit message)")
	}
	if strings.TrimSpace(onto) == "" {
		onto = "main"
	}

	if helper := mergeToMainHelper(dir, onto); helper != "" {
		return integrateViaMergeHelper(ctx, dir, helper, branch, opts), nil
	}

	// Guard 1: dir must be ON onto.
	head, _, herr := RunGit(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if herr != nil {
		return Result{}, fmt.Errorf("vcsops.Integrate: read HEAD: %w", herr)
	}
	if cur := strings.TrimSpace(head); cur != onto {
		return Result{Integrated: false, Refused: fmt.Sprintf("dir is on %q, not the integration branch %q — check out %q first (this guard is why main survives)", cur, onto, onto)}, nil
	}

	// Guard 2: onto's tree must be clean, so the squash-conflict undo (reset
	// --hard to the tip) can never discard a caller's unrelated work.
	st, _, _ := RunGit(ctx, dir, "status", "--porcelain")
	if strings.TrimSpace(st) != "" {
		return Result{Integrated: false, Refused: fmt.Sprintf("%q has uncommitted changes; commit or stash them before integrating", onto)}, nil
	}

	// Guard 3: branch must carry commits beyond its merge-base with onto.
	cnt, _, _ := RunGit(ctx, dir, "rev-list", "--count", onto+".."+branch)
	if strings.TrimSpace(cnt) == "0" {
		return Result{Integrated: false, Refused: fmt.Sprintf("%q has no commits beyond %q — nothing to integrate", branch, onto)}, nil
	}

	// The safe land: a real 3-way squash merge against the CURRENT onto tip.
	mout, mexit, merr := RunGit(ctx, dir, "merge", "--squash", branch)
	if merr != nil {
		return Result{}, fmt.Errorf("vcsops.Integrate: merge: %w", merr)
	}
	if mexit != 0 {
		conflicts := unmergedPaths(ctx, dir)
		_, _, _ = RunGit(ctx, dir, "reset", "--hard", "HEAD")
		if len(conflicts) == 0 {
			return Result{}, fmt.Errorf("vcsops.Integrate: merge --squash failed: %s", strings.TrimSpace(mout))
		}
		return Result{Integrated: false, Conflicts: conflicts}, nil
	}

	if out, exit, err := RunGit(ctx, dir, "commit", "-m", message); err != nil || exit != 0 {
		return Result{}, fmt.Errorf("vcsops.Integrate: commit: %w: %s", err, strings.TrimSpace(out))
	}
	commit, _, _ := RunGit(ctx, dir, "rev-parse", "HEAD")
	res := Result{Integrated: true, Commit: strings.TrimSpace(commit)}

	// Optional cleanup — only after a successful land.
	if opts.WorktreePath != "" {
		_, _, _ = RunGit(ctx, dir, "worktree", "remove", "--force", opts.WorktreePath)
	}
	if opts.DeleteBranch {
		_, _, _ = RunGit(ctx, dir, "branch", "-D", branch)
	}
	return res, nil
}

func mergeToMainHelper(dir, onto string) string {
	if onto != "main" {
		return ""
	}
	helper := filepath.Join(dir, "scripts", "merge-to-main.sh")
	if st, err := os.Stat(helper); err == nil && !st.IsDir() {
		return "scripts/merge-to-main.sh"
	}
	return ""
}

func integrateViaMergeHelper(ctx context.Context, dir, helper, branch string, opts IntegrateOptions) Result {
	out, exit, err := runCommand(ctx, dir, "bash", helper, branch)
	if err != nil {
		return Result{Integrated: false, Refused: fmt.Sprintf("repo guard helper %s failed to start: %v", helper, err)}
	}
	if exit != 0 {
		return Result{Integrated: false, Refused: fmt.Sprintf("repo guard helper %s refused integration: %s", helper, strings.TrimSpace(out))}
	}
	commit, _, _ := RunGit(ctx, dir, "rev-parse", "HEAD")
	res := Result{Integrated: true, Commit: strings.TrimSpace(commit)}
	if opts.WorktreePath != "" {
		_, _, _ = RunGit(ctx, dir, "worktree", "remove", "--force", opts.WorktreePath)
	}
	if opts.DeleteBranch {
		_, _, _ = RunGit(ctx, dir, "branch", "-D", branch)
	}
	return res
}

func unmergedPaths(ctx context.Context, dir string) []string {
	out, _, _ := RunGit(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// RunGit runs git in argv mode (no shell) via the shared host.RunHandler and
// returns combined stdout, the exit code, and an infra error (exec could not
// start). A non-zero exit is data, not err.
func RunGit(ctx context.Context, dir string, args ...string) (stdout string, exit int, err error) {
	return runCommand(ctx, dir, "git", args...)
}

func runCommand(ctx context.Context, dir, cmd string, args ...string) (stdout string, exit int, err error) {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	res, err := host.RunHandler(ctx, map[string]any{"cmd": cmd, "args": anyArgs, "cwd": dir})
	if err != nil {
		return "", -1, err
	}
	if res.Error != "" && res.Data == nil {
		return "", -1, errors.New(res.Error)
	}
	exit, _ = res.Data["exit_code"].(int)
	stdout, _ = res.Data["stdout"].(string)
	return stdout, exit, nil
}
