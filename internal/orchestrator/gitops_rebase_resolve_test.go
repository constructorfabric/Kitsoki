package orchestrator_test

// Real-conflict RESOLUTION test for the git-ops story: the companion to
// gitops_rebase_conflict_test.go. That test proves a conflicting rebase ROUTES
// to the `conflict` room (with an escalating stub that performs no edits); this
// one proves the loop PAST conflict — resolve → rebase_continue → build-check →
// conflict_resolved → branch_ops — actually settles in a single SubmitDirect
// call when the resolver genuinely resolves the conflict.
//
// This is the deterministic, free (no-LLM) validation the conflict-capable
// intercept proposal calls for: a real `git init` conflict repo + the real
// host.run registry + the conflict_resolver agent STUBBED to actually edit the
// conflicted file (the ImplementingActuallyEditsFiles technique), so
// `git rebase --continue` really succeeds and the session really lands
// branch_ops — proving the existing settlePostBindEmits machinery drives the
// whole multi-round conflict flow with no new "driver" code.
//
// The resolver stub mirrors the real conflict_resolver's write-fence
// (tools: [Read, Edit], NO git) — it edits the working tree only. If the story
// fails to stage the resolved file before `git rebase --continue`, this test
// surfaces that gap honestly rather than masking it by staging in the stub.

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

// resolvingConflictResolver is the host.agent.task stub that genuinely
// resolves the file.txt conflict setupConflictRepo creates. It edits the
// working tree only (no git) — exactly the conflict_resolver agent's fence —
// writing a clean, marker-free file that keeps both sides' lines, then reports
// resolved:true so the conflict room emits rebase_continue.
func resolvingConflictResolver(repoRoot string) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		wd, _ := args["working_dir"].(string)
		if wd == "" {
			wd = "."
		}
		target := filepath.Join(repoRoot, wd, "file.txt")
		// A clean resolution: marker-free, both intents preserved.
		if err := os.WriteFile(target, []byte("feature change\nmain change\n"), 0o644); err != nil {
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
}

// TestGitOps_RebaseConflict_ResolvesAndLandsBranchOps drives a real rebase
// conflict and, with a resolver that genuinely edits the conflicted file,
// asserts the session settles all the way to branch_ops in one SubmitDirect —
// the multi-round conflict→continue→resolved loop driven entirely by the
// existing settle machinery.
func TestGitOps_RebaseConflict_ResolvesAndLandsBranchOps(t *testing.T) {
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

	repoRoot := setupConflictRepo(t)

	reg := host.NewRegistry()
	reg.Register("host.run", host.RunHandler)
	reg.Register("host.agent.task", resolvingConflictResolver(repoRoot))
	reg.Register("host.agent.decide", resolvingConflictResolver(repoRoot))

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	t.Chdir(repoRoot)

	ctx := context.Background()
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Drive the rebase to rest. It conflicts, the resolver resolves, and the
	// intercept drive carries the conflict room THROUGH resolve → rebase_continue
	// → conflict_resolved → branch_ops in one call. The drive-through is owned by
	// DriveToRest specifically: a normal operator SubmitDirect rests AT the
	// conflict room by design (intercept_drive: rest — see the git-ops conflict
	// flow fixtures), and only the synchronous intercept drive walks it to a real
	// rest. DriveToRest also boots the session and seeds InitialWorld itself, so
	// build_check_disabled (the temp repo has no Go module) rides in there.
	out, err := orch.DriveToRest(c, "rebase", nil, orchestrator.DriveOptions{
		Input:        "rebase onto main and resolve conflicts",
		InitialWorld: map[string]any{"build_check_disabled": true},
	})
	require.NoError(t, err, "rebase drive must complete")
	require.True(t, out.Resolved,
		"a resolved rebase conflict must report Resolved; outcome=%q final=%q", out.Outcome, out.FinalState)
	require.Equal(t, app.StatePath("branch_ops"), out.FinalState,
		"a resolved rebase conflict must settle to branch_ops; got %q (view: %q)",
		out.FinalState, out.View)

	// The tree must be clean — not mid-rebase — after a resolved continue.
	require.False(t, midRebase(t, repoRoot), "working tree must not be mid-rebase after resolution")

	j1, err := orch.LoadJourney(out.SessionID)
	require.NoError(t, err)
	require.Equal(t, true, j1.World.Vars["rebase_done"],
		"rebase_done must be true after a resolved conflict")
}

// midRebase reports whether the repo is in the middle of a rebase (a
// rebase-merge or rebase-apply dir exists under .git).
func midRebase(t *testing.T, repoRoot string) bool {
	t.Helper()
	gitDir := filepath.Join(repoRoot, ".git")
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, d)); err == nil {
			return true
		}
	}
	return false
}

// mustJSONBytes marshals v or fails the calling test setup loudly.
func mustJSONBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mustJSONBytes: " + err.Error())
	}
	return b
}
