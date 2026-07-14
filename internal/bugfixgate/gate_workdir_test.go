// Package bugfixgate holds a deterministic, no-LLM reproduction for bug 114:
// three bugfix-pipeline gate scripts DOUBLE-APPLY world.workdir and therefore
// self-skip on every live run, silently disabling RED->GREEN discipline.
//
// Root cause (see stories/bugfix/rooms/{reproducing,implementing,testing}.yaml):
// each of `repro_det_gate`, `commit_repro_test`, and `regression_gate_exec`
// sets `cwd: {{ world.workdir }}` on the host.run invoke AND, inside its inline
// bash, re-applies `WT={{ world.workdir }}` then `cd "$WT"` / `git -C "$WT"`.
// host.run already honours `cwd` (internal/host/handlers.go RunHandler sets
// c.Dir = cwd). In production, world.workdir is a RELATIVE path —
// stories/bugfix/rooms/idle.yaml sets it to ".worktrees/bf-<ticket>-<session>"
// — so once host.run has chdir'd the child into the worktree, the inner
// `cd "$WT"` / `git -C "$WT"` re-applies the SAME relative path a second time
// (worktree/.worktrees/bf-<id>), which does not exist. The `|| { … }` fallback
// then fires and the gate emits its "unavailable" sentinel instead of running:
//
//   repro_det_gate      -> {ran:false, summary:"gate skipped: workdir unavailable"}
//   commit_repro_test   -> {sha:"", committed:false}
//   regression_gate_exec-> {checked:false, log:"no pre-fix snapshot …"}
//
// The correct sibling step `repro_gate_exec` (reproducing.yaml, step 0) is the
// cwd-ONLY pattern: it sets `cwd:` and never re-derives WT — it does not
// double-apply, and it is not exercised here because it is already correct.
//
// Why the existing suite never caught this: every current test/flow seeds
// world.workdir as an ABSOLUTE path (e.g. internal/bugfixsynth uses
// t.TempDir()). Chdir'ing to an absolute path is idempotent, so the inner
// `cd "$WT"` is a harmless no-op and the double-apply is masked. Production's
// RELATIVE workdir is the only configuration that triggers the bug, so this
// reproduction pins the process cwd to the fixture repo root and drives each
// gate through the REAL host.RunHandler with cwd = the RELATIVE worktree path,
// exactly as the story renders it.
//
// This test asserts BEHAVIOUR (the gate actually executed / committed /
// checked), not a specific fix mechanism, so it passes for ANY correct fix
// (whether the fix deletes the inner cd/git -C, or rewrites WT to $(pwd)).
package bugfixgate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"

	"kitsoki/internal/host"
)

// relWorkdir is the RELATIVE worktree path production uses (idle.yaml sets
// world.workdir = ".worktrees/bf-<ticket>-<session>"). The double-apply only
// bites for a relative path.
const relWorkdir = ".worktrees/bf-114-repro"

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git", "jq", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH (the gate scripts need it)", tool)
		}
	}
}

// gitRun runs git in dir with a deterministic identity (fatal on error).
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// makeWorktree builds a real git repo at <root>/<relWorkdir> with `commits`
// commits, and returns the absolute repo root (root). The caller chdir's to
// root so that host.run's relative cwd resolves into the worktree.
func makeWorktree(t *testing.T, commits int) string {
	t.Helper()
	root := t.TempDir()
	wt := filepath.Join(root, relWorkdir)
	require.NoError(t, os.MkdirAll(wt, 0o755))
	gitRun(t, wt, "init", "-q", "-b", "main")
	gitRun(t, wt, "config", "user.email", "t@t")
	gitRun(t, wt, "config", "user.name", "t")
	for i := 0; i < commits; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(wt, "f.txt"), []byte(strings.Repeat("x", i+1)+"\n"), 0o644))
		gitRun(t, wt, "add", "-A")
		gitRun(t, wt, "commit", "-q", "-m", "c")
	}
	return root
}

// extractHostRun pulls the real host.run invoke's cmd/args/cwd out of a bugfix
// room YAML for the step with the given id, then substitutes the template
// tokens with test values. This keeps the reproduction anchored to the ACTUAL
// story text — when the gate scripts are fixed, this test re-reads the fixed
// scripts and goes GREEN.
func extractHostRun(t *testing.T, roomYAML, stepID string, vars map[string]string) (cmd string, args []string, cwd string) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("../../stories/bugfix/rooms", roomYAML))
	require.NoError(t, err)
	raw, err := os.ReadFile(abs)
	require.NoError(t, err)
	var doc any
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	with := findWithByID(doc, stepID)
	require.NotNilf(t, with, "step id=%q not found in %s", stepID, roomYAML)

	render := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "{{ "+k+" }}", v)
		}
		return s
	}
	cmd, _ = with["cmd"].(string)
	cmd = render(cmd)
	cwd, _ = with["cwd"].(string)
	cwd = render(cwd)
	if rawArgs, ok := with["args"].([]any); ok {
		for _, a := range rawArgs {
			args = append(args, render(toStr(a)))
		}
	}
	return cmd, args, cwd
}

func toStr(a any) string {
	if s, ok := a.(string); ok {
		return s
	}
	return ""
}

// findWithByID walks the decoded YAML tree looking for a mapping node that has
// key "id" == target, and returns that node's "with" mapping as map[string]any.
func findWithByID(node any, target string) map[string]any {
	switch n := node.(type) {
	case map[string]any:
		if id, ok := n["id"].(string); ok && id == target {
			if w, ok := n["with"].(map[string]any); ok {
				return w
			}
		}
		for _, v := range n {
			if r := findWithByID(v, target); r != nil {
				return r
			}
		}
	case []any:
		for _, v := range n {
			if r := findWithByID(v, target); r != nil {
				return r
			}
		}
	}
	return nil
}

// runGate invokes the REAL host.RunHandler exactly as the orchestrator would:
// cmd/args from the story, cwd = the RELATIVE worktree path. The test's process
// cwd is the fixture repo root, so host.run's c.Dir = relWorkdir resolves into
// the worktree — mirroring production, where the kitsoki daemon runs from the
// repo root and world.workdir is ".worktrees/bf-<id>".
func runGate(t *testing.T, root, cmd string, args []string, cwd string) map[string]any {
	t.Helper()
	t.Chdir(root) // so the relative cwd resolves against the repo root, like prod
	res, err := host.RunHandler(context.Background(), map[string]any{
		"cmd":  cmd,
		"args": toAnySlice(args),
		"cwd":  cwd,
	})
	require.NoError(t, err)
	require.Empty(t, res.Error, "host.run domain error")
	sj, _ := res.Data["stdout_json"].(map[string]any)
	require.NotNilf(t, sj, "gate produced no JSON envelope; stdout=%q", res.Data["stdout"])
	return sj
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// TestReproDetGate_DoubleAppliesWorkdir_SelfSkips reproduces the reproducing.yaml
// deterministic GREEN->RED gate self-skipping under a relative workdir.
//
// EXPECTED (correct) behaviour: with cwd already pinned to the worktree, the
// gate RUNS (ran == true). ACTUAL (bug): the inner `cd "$WT"` double-applies the
// relative path, fails, and the gate emits {ran:false, "gate skipped: workdir
// unavailable"} — so it never proves RED, and every synthesised bugfix run loses
// its reproducer gate.
func TestReproDetGate_DoubleAppliesWorkdir_SelfSkips(t *testing.T) {
	requireTools(t)
	root := makeWorktree(t, 1)
	// Dirty change so the gate has something to stash — proves the body ran.
	require.NoError(t, os.WriteFile(filepath.Join(root, relWorkdir, "dirty.txt"), []byte("d\n"), 0o644))

	cmd, args, cwd := extractHostRun(t, "reproducing.yaml", "repro_det_gate", map[string]string{
		"world.workdir":  relWorkdir,
		"world.test_cmd": "true",
	})
	sj := runGate(t, root, cmd, args, cwd)

	require.NotEqualf(t, "gate skipped: workdir unavailable", sj["summary"],
		"repro_det_gate self-skipped: it double-applied the relative workdir and could not cd into the worktree")
	require.Equalf(t, true, sj["ran"],
		"repro_det_gate must actually EXECUTE under a relative workdir; got ran=%v summary=%q", sj["ran"], sj["summary"])
}

// TestCommitReproGate_DoubleAppliesWorkdir_DoesNotCommit reproduces
// implementing.yaml's commit_repro_test refusing to commit the discrete pre-fix
// reproducer under a relative workdir.
//
// EXPECTED: with a staged/dirty change present, the gate commits it
// (committed == true). ACTUAL (bug): the inner `cd "$WT"` double-applies and
// fails, so the gate emits {committed:false} — the reproducer test is NEVER
// committed as the discrete pre-fix tip, and testing's HEAD~1 gate later finds
// no pre-fix snapshot and dead-ends at needs-human.
func TestCommitReproGate_DoubleAppliesWorkdir_DoesNotCommit(t *testing.T) {
	requireTools(t)
	root := makeWorktree(t, 1)
	// A NEW uncommitted file the gate's `git add -A` + commit must capture.
	require.NoError(t, os.WriteFile(filepath.Join(root, relWorkdir, "repro_red_test.go"), []byte("// red\n"), 0o644))

	cmd, args, cwd := extractHostRun(t, "implementing.yaml", "commit_repro_test", map[string]string{
		"world.workdir":   relWorkdir,
		"world.ticket_id": "TKT-114",
	})
	sj := runGate(t, root, cmd, args, cwd)

	require.Equalf(t, true, sj["committed"],
		"commit_repro_test must commit the pre-fix reproducer under a relative workdir; got %v (double-applied cwd → cd failed → no commit)", sj)
}

// TestRegressionGate_DoubleAppliesWorkdir_SkipsCheck reproduces testing.yaml's
// regression_gate_exec failing to materialise the pre-fix snapshot under a
// relative workdir.
//
// EXPECTED: the gate resolves HEAD~1 and runs the gate command against a scratch
// worktree (checked == true). ACTUAL (bug): every `git -C "$WT"` double-applies
// the relative path from inside the already-chdir'd worktree, so `git -C "$WT"
// rev-parse HEAD~1` fails, PREFIX_SHA is empty, and the gate emits
// {checked:false, "no pre-fix snapshot …"} — the RED->GREEN regression proof is
// silently skipped on every live run.
func TestRegressionGate_DoubleAppliesWorkdir_SkipsCheck(t *testing.T) {
	requireTools(t)
	root := makeWorktree(t, 2) // needs HEAD~1

	cmd, args, cwd := extractHostRun(t, "testing.yaml", "regression_gate_exec", map[string]string{
		"world.workdir":      relWorkdir,
		"world.gate_command": "true",
	})
	sj := runGate(t, root, cmd, args, cwd)

	require.Equalf(t, true, sj["checked"],
		"regression_gate_exec must materialise the pre-fix snapshot and run the gate under a relative workdir; got %v (double-applied `git -C $WT` → no HEAD~1 → check skipped)", sj)
}
