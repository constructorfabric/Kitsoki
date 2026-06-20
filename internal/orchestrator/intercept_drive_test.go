package orchestrator_test

// E2E tests for Orchestrator.DriveToRest — the synchronous "drive a matched
// command to rest" path the pre-LLM intercept gate escalates to when a command
// enters a room flagged intercept_drive: rest (conflict-capable intercept).
//
// Both tests drive the REAL git-ops story against a REAL rebase conflict with
// the conflict_resolver oracle stubbed (no LLM, free):
//
//   - ResolvesAndReports: a resolving stub drives the whole conflict loop to
//     branch_ops; DriveToRest reports Resolved with a multi-hop Rounds count and
//     leaves a clean tree.
//   - EscalationSafeAborts: an escalating stub (resolved:false) rests the flow
//     AT the flagged conflict room; DriveToRest detects the escalation, fires
//     the safe-abort arc, and leaves the tree clean (NOT mid-rebase) — the
//     "never strand the tree" invariant.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// cwdResolvingConflictResolver is a host.oracle.task stub that resolves the
// file.txt conflict relative to the process cwd (git-ops working_dir="." +
// t.Chdir into the repo). It edits the working tree only — the resolver's fence
// — leaving the story to stage and continue.
func cwdResolvingConflictResolver(ctx context.Context, args map[string]any) (host.Result, error) {
	wd, _ := args["working_dir"].(string)
	if wd == "" {
		wd = "."
	}
	if err := os.WriteFile(filepath.Join(wd, "file.txt"), []byte("feature change\nmain change\n"), 0o644); err != nil {
		return host.Result{Error: "stub resolver write: " + err.Error()}, nil
	}
	verdict := map[string]any{
		"resolved":           true,
		"resolution_summary": "kept both sides of file.txt",
		"unresolvable_files": "",
		"reason":             "",
	}
	stdoutJSON, _ := json.Marshal(verdict)
	return host.Result{Data: map[string]any{
		"submitted": verdict,
		"stdout":    string(stdoutJSON),
		"ok":        true,
	}}, nil
}

// newGitOpsOrchForDrive builds a git-ops orchestrator wired with the real
// host.run plus the given oracle stub, chdir'd into a fresh conflict repo.
// Returns the orchestrator and the repo root.
func newGitOpsOrchForDrive(t *testing.T, oracleStub host.Handler) (*orchestrator.Orchestrator, string) {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	appPath := filepath.Join(cwd, "..", "..", "stories", "git-ops", "app.yaml")

	def, err := app.Load(appPath)
	require.NoError(t, err, "load git-ops/app.yaml")
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.run", host.RunHandler)
	reg.Register("host.oracle.task", oracleStub)
	reg.Register("host.oracle.decide", oracleStub)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	repoRoot := setupConflictRepo(t)
	t.Chdir(repoRoot)
	return orch, repoRoot
}

// TestDriveToRest_ResolvesAndReports: a resolving conflict drives all the way to
// branch_ops, reported as Resolved with multiple hops and a clean tree.
func TestDriveToRest_ResolvesAndReports(t *testing.T) {
	// A cwd-based resolving stub: the test chdir's into the repo and git-ops's
	// working_dir defaults to ".", so the conflicted file is at ./file.txt.
	orch, repoRoot := newGitOpsOrchForDrive(t, cwdResolvingConflictResolver)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := orch.DriveToRest(ctx, "rebase", nil, orchestrator.DriveOptions{
		Input:        "rebase onto main and resolve conflicts",
		InitialWorld: map[string]any{"build_check_disabled": true},
	})
	require.NoError(t, err, "DriveToRest infra must not error")
	require.True(t, out.Resolved, "a resolved conflict must report Resolved; outcome=%q final=%q", out.Outcome, out.FinalState)
	require.False(t, out.Aborted, "a resolved conflict must not safe-abort")
	require.Equal(t, "resolved", out.Outcome)
	require.Equal(t, app.StatePath("branch_ops"), out.FinalState)
	require.GreaterOrEqual(t, out.Rounds, 2, "a conflict resolution is a multi-hop drive")
	require.False(t, midRebase(t, repoRoot), "tree must be clean after a resolved drive")
}

// TestDriveToRest_EscalationSafeAborts: an escalating resolver leaves the flow
// at the flagged conflict room; DriveToRest must safe-abort and leave a clean
// (not mid-rebase) tree.
func TestDriveToRest_EscalationSafeAborts(t *testing.T) {
	orch, repoRoot := newGitOpsOrchForDrive(t, escalatingConflictResolver)

	// Sanity: the tree starts NOT mid-rebase.
	require.False(t, midRebase(t, repoRoot))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := orch.DriveToRest(ctx, "rebase", nil, orchestrator.DriveOptions{
		Input:        "rebase onto main",
		InitialWorld: map[string]any{"build_check_disabled": true},
	})
	require.NoError(t, err, "DriveToRest infra must not error even on escalation")
	require.False(t, out.Resolved, "an unresolvable conflict must not report Resolved")
	require.True(t, out.Aborted, "an escalation must trigger safe-abort")
	require.Equal(t, "escalation", out.Outcome)

	// The invariant: the safe-abort (git rebase --abort) leaves a clean tree.
	require.False(t, midRebase(t, repoRoot),
		"safe-abort must leave the tree NOT mid-rebase; final=%q", out.FinalState)
	// And the abort routes back to the branch hub, not the conflict room.
	require.Equal(t, app.StatePath("branch_ops"), out.FinalState,
		"safe-abort must route back to the branch hub")
}
