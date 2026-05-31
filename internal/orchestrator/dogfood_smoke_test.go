package orchestrator_test

// Integration smoke tests for the kitsoki-dev dogfood app — the layer
// the existing flow-fixture suite can't reach (every flow stubs every
// host to {ok:true}, so any code path predicated on a host returning
// Result.Error is invisible to fixtures). See
// docs/tracing/testing.md "Integration tests for host-failure paths"
// and docs/stories/state-machine.md "Effects" for
// the motivation; in particular these tests would have caught the
// 2026-05-18 `go_bugfix` redirect-loop hang (commit 9b58dc4) before
// fa39746's `maxRedirectDepth` cap landed.
//
// Shape:
//   - real on-disk `git init` repo under t.TempDir() with one commit
//     on `main`, so `git worktree add` has a base to root at.
//   - real host.RegisterBuiltins; only host.oracle.ask_with_mcp is
//     stubbed (canned artifact payload — no real LLM call).
//   - hard `context.WithTimeout(ctx, …)` per turn so a regression
//     FAILS in seconds rather than hanging CI.
//
// Conceptual mirror of stories/kitsoki-dev/scenarios/verify_autostart.yaml.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// setupDogfoodRepo builds a self-contained working tree at t.TempDir():
// a fresh `git init` repo with one commit on `main`, plus snapshots of
// the kitsoki `stories/` tree (so `app.Load` resolves all imports) and
// the `issues/` tree (so `host.local_files.ticket` finds real bug
// files). Sets the process cwd via t.Chdir so `host.git_worktree`
// (which uses `dir == "" → cwd`) operates on the temp repo.
//
// Returns the repo root and the canonical ticket id we drive through
// the pipeline.
func setupDogfoodRepo(t *testing.T) (repoRoot string, ticketID string) {
	t.Helper()

	repoRoot = t.TempDir()

	// Copy stories/ and issues/ from the live repo. We resolve the
	// kitsoki repo root relative to this test file: package dir is
	// internal/orchestrator/, so two levels up is the repo root.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	kitsokiRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	for _, sub := range []string{"stories", "issues"} {
		src := filepath.Join(kitsokiRoot, sub)
		dst := filepath.Join(repoRoot, sub)
		require.NoError(t, copyTree(src, dst),
			"copy %s → %s", src, dst)
	}

	// Initialise a real git repo so `git worktree add` has a base.
	gitConfig := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	gitConfig("init", "--quiet", "--initial-branch=main")
	gitConfig("config", "user.email", "smoke@test.invalid")
	gitConfig("config", "user.name", "Smoke Test")
	gitConfig("add", "-A")
	gitConfig("commit", "--quiet", "-m", "init")

	// Chdir into the temp repo so host.git_worktree (dir=cwd) and
	// host.local_files.ticket (root=cwd fallback) both operate here.
	// t.Chdir restores the prior cwd on test completion.
	t.Chdir(repoRoot)

	// Pick the canonical integration-smoke ticket. It lives under
	// issues/bugs/ in the live repo and was copied above.
	ticketID = "2026-05-17T111838Z-integration-smoke-bug-picked-up-by-dogfood"
	bugPath := filepath.Join(repoRoot, "issues", "bugs", ticketID+".md")
	_, statErr := os.Stat(bugPath)
	require.NoError(t, statErr, "integration-smoke ticket must exist at %s", bugPath)

	return repoRoot, ticketID
}

// copyTree mirrors src → dst recursively. Files only — git metadata
// (.git/) is not copied; we run `git init` fresh on dst's parent.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// dogfoodArtifact is the canned schema-shaped payload the oracle stub
// returns. It covers every bf room's bind path (reproduction_artifact,
// propose_fix_artifact, implement_review_artifact, validate_artifact,
// done_artifact, and the llm_verdict shape — though judge_mode=human
// skips the verdict branches). Modeled on
// stories/bugfix/flows/happy_human.yaml's stub.
var dogfoodArtifact = map[string]any{
	"summary_title":    "Stub artifact",
	"summary_markdown": "# Stub artifact body\n\nCanned payload for the dogfood smoke test.\n",
	"bug_verified":     true,
	"steps":            []string{"step A", "step B"},
	"involved_components": []map[string]any{
		{"name": "internal/orchestrator", "reason": "lives here"},
	},
	"fix_description":  "stub",
	"root_cause":       "stub",
	"affected_files":   []string{"internal/orchestrator/orchestrator.go"},
	"confidence":       0.9,
	"reasoning":        "stub",
	"status":           "passed",
	"tests_added":      []string{"internal/orchestrator/dogfood_smoke_test.go"},
	"tests_run":        map[string]any{"passed": 1, "failed": 0, "log": "PASS"},
	"outcome":          "pass",
	"evidence":         map[string]any{"build": "ok", "api": "n/a", "ui": "n/a"},
	"next_action_hint": "",
	"lessons":          []map[string]any{},
	// Verdict-shaped keys so judge branches (when llm_then_human) are
	// also covered if a future change flips judge_mode.
	"verdict": "accept",
	"intent":  "accept",
	"reason":  "stub verdict",
}

// newSmokeOrchestrator builds an orchestrator pinned to the temp-repo
// kitsoki-dev app with the real host registry (oracle stubbed out).
// Returns the orchestrator, the underlying store (for direct history
// reads), an open session id, and the count pointer the oracle stub
// increments per call (handy for sanity asserts).
//
// The oracle stub is registered FIRST and the rest of the builtins are
// registered piecemeal via registerBuiltinsExceptOracle. host.Registry
// panics on duplicate Register, so to override the prod oracle handler
// we have to skip its line in RegisterBuiltins. The set is small and
// the dogfood smoke doesn't need every builtin — just the handlers the
// kitsoki-dev `hosts:` allow-list (kitsoki-dev/app.yaml:64-73) declares.
func newSmokeOrchestrator(t *testing.T, repoRoot string) (*orchestrator.Orchestrator, store.Store, app.SessionID, *int) {
	t.Helper()
	appPath := filepath.Join(repoRoot, "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err, "load kitsoki-dev/app.yaml from %s", appPath)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	oracleCalls := 0
	stub := func(ctx context.Context, args map[string]any) (host.Result, error) {
		oracleCalls++
		stdoutJSON, _ := json.Marshal(dogfoodArtifact)
		return host.Result{Data: map[string]any{
			"submitted": dogfoodArtifact,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
	reg.Register("host.oracle.ask_with_mcp", stub)
	// oracle-split Phase 8 verbs used by bugfix/pr-refinement/dev-story.
	reg.Register("host.oracle.task", stub)
	reg.Register("host.oracle.ask", stub)
	reg.Register("host.oracle.decide", stub)

	// Register the prod handlers kitsoki-dev declares in its hosts
	// allow-list, MINUS the oracle verbs (already stubbed above).
	reg.Register("host.local_files.ticket", host.LocalFilesTicketHandler)
	reg.Register("host.git", host.GitVCSHandler)
	reg.Register("host.local", host.LocalCIHandler)
	reg.Register("host.git_worktree", host.GitWorktreeHandler)
	reg.Register("host.append_to_file", host.AppendFileTransportHandler)
	reg.Register("host.inbox.add", host.InboxAddHandler)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	return orch, s, sid, &oracleCalls
}

// seedDogfoodWorld returns the slot bag mirroring
// stories/kitsoki-dev/scenarios/verify_autostart.yaml: pin the ticket,
// set judge_mode=human + auto_accept_on_post=true at every fold level,
// and seed bf's per-pipeline keys empty so on_enter actually fires the
// auto-create + auto-start chain.
func seedDogfoodWorld(ticketID string) map[string]any {
	threadPath := filepath.Join("issues", "bugs", ticketID+".md")
	return map[string]any{
		"core__ticket_id":    ticketID,
		"core__ticket_title": "integration smoke — bug picked up by dogfood",
		"core__ticket_url":   threadPath,
		"core__ticket_type":  "bug",
		"core__thread":       threadPath,

		"judge_mode":                           "human",
		"judge_confidence_threshold":           0.8,
		"core__judge_mode":                     "human",
		"core__judge_confidence_threshold":     0.8,
		"core__bf__judge_mode":                 "human",
		"core__bf__judge_confidence_threshold": 0.8,

		// auto_accept_on_post was removed when the bugfix story
		// merged _executing+_awaiting_reply into one room per phase.
		// The accept arc posts the artifact AND advances in a single
		// turn, so no separate auto-accept step is needed.

		"core__bf__workspace_id":           "",
		"core__bf__feature_branch":         "",
		"core__bf__workdir":                "",
		"core__bf__base_branch":            "main",
		"core__bf__bf_autostart_attempted": false,
		"core__bf__bugfix_mode":            "full",
	}
}

// TestDogfoodSmoke_AutoStartThroughBugfix is the regression test for
// the `go_bugfix` redirect-loop hang flagged in the dogfood-regression-
// testing-gap proposal. The class of bug: an on_error: <sibling-room>
// arc whose redirect target's on_enter re-invokes the failing host
// call, looping until the orchestrator's `maxRedirectDepth` cap fires.
//
// Setup mirrors `verify_autostart.yaml`: a clean temp git repo (no
// stale `.worktrees/bf-<id>/`), the integration-smoke bug seeded, then
// `core__go_bugfix` from `core.main`. Expected: workspace.create
// succeeds against the real repo, the auto-start emit fires, the
// session lands at `core.bf.reproducing` within seconds.
//
// We then drive ONE `core__bf__accept` to prove the auto-accept arm
// also lands the next phase (reproducing →
// proposing). Walking past `implementing_executing`
// currently requires fixing a separate workspace_id mismatch in
// bf.idle.yaml (`workspace_id="bf-<id>"` vs. the on-disk dir
// `fix-<id>` produced by git_worktree.create from feature_branch
// "fix/<id>"), which is out of scope per the task spec; one proceed
// is enough to prove the auto-start + auto-accept chain doesn't loop.
func TestDogfoodSmoke_AutoStartThroughBugfix(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, oracleCalls := newSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()

	// 1. Run initial on_enter for core.main (which invokes
	//    iface.ticket.list_mine → host.local_files.ticket).
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid), "RunInitialOnEnter must finish within 10s")
		cancel()
	}

	// 2. Teleport to core.main with the ticket+mode seeded.
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err, "Teleport to core.main with seeded ticket must succeed")
		cancel()
	}

	// 3. Submit core__go_bugfix — the regression's trigger. Under the
	//    cap fix the session must land at core.bf.reproducing
	//    (auto-start fired through bf.idle.on_enter; workspace.create
	//    succeeded against the temp git repo; emit_intent advanced the
	//    leaf). A loop regression hits the 10s deadline.
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__go_bugfix", nil)
		cancel()
		require.NoError(t, err, "core__go_bugfix must complete within 10s (loop regression?)")
		require.NotNil(t, out)
		require.Equal(t, app.StatePath("core.bf.reproducing"), out.NewState,
			"go_bugfix should auto-start through bf.idle and land at reproducing; got %q (view: %q)",
			out.NewState, out.View)
	}

	// Worktree must exist on disk at `.worktrees/<workspace_id>` —
	// matching world.workdir (i.e. `bf-<ticket_id>`). idle.on_enter
	// passes `id: workspace_id` to iface.workspace.create so the
	// on-disk dir aligns with what `iface.workspace.sync` (and
	// implementing.on_enter's commit) will later key on.
	workdir := filepath.Join(repoRoot, ".worktrees", "bf-"+ticketID)
	_, statErr := os.Stat(workdir)
	require.NoError(t, statErr,
		"worktree dir must exist after go_bugfix; expected %s", workdir)

	// bf_autostart_attempted must be true so a re-entry to idle is a no-op.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, true, journey.World.Vars["core__bf__bf_autostart_attempted"],
		"bf_autostart_attempted must be set true after the auto-start chain ran")

	// Oracle was called once for reproducing.on_enter.
	require.GreaterOrEqual(t, *oracleCalls, 1,
		"oracle stub should have been invoked at least once for reproducing.on_enter")

	// 4. Drive one proceed → auto-accept fires through
	//    reproducing, lands at proposing.
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
		cancel()
		require.NoError(t, err, "core__bf__accept must complete within 10s")
		require.NotNil(t, out)
		require.Equal(t, app.StatePath("core.bf.proposing"), out.NewState,
			"proceed → auto-accept on _awaiting_reply (auto_accept_on_post=true, judge_mode=human) should land at proposing; got %q",
			out.NewState)
	}
}

// TestDogfoodSmoke_StaleWorktreeRecoversOrFailsCleanly verifies the
// failure-mode the regression actually surfaced in the wild: a stale
// `.worktrees/<dir>/` from a previous aborted run makes `git worktree
// add` exit non-zero. Pre-fa39746 this looped forever; post-fix the
// session must EITHER recover (priority 3 of the proposal — make the
// handler idempotent — not landed) OR surface the failure cleanly
// (current behaviour: the redirect cap fires or the bf_autostart_
// attempted guard parks the session at bf.idle).
//
// Either is a valid contract; the test asserts the session DOES NOT
// hang and lands at a coherent resting place with the error trail
// either in a HarnessError event or pinned via bf_autostart_attempted
// (so a manual `start` won't re-fire the failure).
func TestDogfoodSmoke_StaleWorktreeRecoversOrFailsCleanly(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	// Pre-create the worktree dir AND a sibling branch ref so `git
	// worktree add -b fix/<id> .worktrees/fix-<id> main` fails with
	// `fatal: '<path>' already exists` — exactly the shape the
	// proposal flagged.
	staleDir := filepath.Join(repoRoot, ".worktrees", "fix-"+ticketID)
	require.NoError(t, os.MkdirAll(staleDir, 0o755))
	// Leave a marker file so the dir is non-empty (git refuses to
	// add into a non-empty dir even if the branch ref doesn't exist).
	require.NoError(t, os.WriteFile(filepath.Join(staleDir, "stale"), []byte("stale"), 0o644))

	orch, s, sid, _ := newSmokeOrchestrator(t, repoRoot)
	ctx := context.Background()

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}

	// 5s deadline: a loop regression times out fast. The cap fires
	// at depth 4 + 1 = 5 host invocations max, well under 5s.
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := orch.SubmitDirect(c, sid, "core__go_bugfix", nil)
	require.NoError(t, err,
		"core__go_bugfix with a stale worktree must complete within 5s; a loop regression hangs here")
	require.NotNil(t, out)

	// The session must land at a coherent resting place. The two
	// valid contracts are:
	//   (a) HarnessError event with reason=on_error.depth_cap_exceeded
	//       (the redirect cap fired — proves the cap is doing its job).
	//   (b) bf_autostart_attempted == true and state is bf.idle (the
	//       per-room guard pinned the autostart so a manual `start`
	//       won't re-fire). The proposal's priority-2 defence-in-depth.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	history, histErr := s.LoadHistory(sid)
	require.NoError(t, histErr)

	hasCapHarnessError := false
	for _, ev := range history {
		if ev.Kind != store.HarnessError {
			continue
		}
		var p map[string]any
		if jsonErr := json.Unmarshal(ev.Payload, &p); jsonErr != nil {
			continue
		}
		if reason, _ := p["reason"].(string); reason == "on_error.depth_cap_exceeded" {
			hasCapHarnessError = true
			break
		}
	}

	autostartAttempted, _ := journey.World.Vars["core__bf__bf_autostart_attempted"].(bool)

	t.Logf("stale-worktree landed at state=%q autostart_attempted=%v cap_fired=%v history_events=%d",
		journey.State, autostartAttempted, hasCapHarnessError, len(history))

	require.True(t, hasCapHarnessError || autostartAttempted,
		"stale-worktree run must either fire the redirect cap (HarnessError) or pin bf_autostart_attempted; got state=%q, autostart_flag=%v, cap_fired=%v",
		journey.State, autostartAttempted, hasCapHarnessError)

	// And it must NOT be parked somewhere incoherent (e.g. a half-
	// transitioned compound state). Any of: bf.idle (the guard kept
	// it parked), bf.reproducing (the create somehow
	// succeeded — unlikely with the stale dir but possible if a
	// future patch makes it idempotent), or core.main (the redirect
	// bounced through @exit:abandoned) are acceptable.
	acceptable := map[app.StatePath]bool{
		"core.bf.idle":        true,
		"core.bf.reproducing": true,
		"core.main":           true,
	}
	require.True(t, acceptable[journey.State],
		"session must settle at a coherent resting place after stale-worktree failure; got %q (acceptable: %v)", journey.State, acceptable)
}

// TestDogfoodSmoke_ContinueFromProposingReachesImplementing reproduces
// the exact scenario the user hit on 2026-05-18: at `core.bf.proposing`,
// typing `continue` (intent: core__bf__accept) silently bounced the
// session back to `core.bf.idle` instead of advancing to
// `core.bf.implementing`.
//
// Root cause: bf.idle.on_enter pinned `bf_autostart_attempted=true`,
// so any re-entry to idle is a no-op (workspace.create gated by
// `!bf_autostart_attempted`). If the worktree dir vanishes between
// turns — common after a process restart or a manual `rm -rf` —
// implementing.on_enter's `iface.workspace.sync` fails because git's
// worktree list no longer has an entry for `<workspace_id>`. The
// arc's `on_error: idle` quietly bounces back to idle, and idle's
// on_enter is now a no-op, so the operator sees a parked idle with
// no diagnostic.
//
// This test simulates that state by driving the happy path to
// `proposing`, then `rm -rf`'ing the worktree dir + `git worktree
// prune`-ing git's index. Typing `accept` next MUST land at
// `core.bf.implementing` (the bugfix-room contract), not at idle.
func TestDogfoodSmoke_ContinueFromProposingReachesImplementing(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, _ := newSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__go_bugfix", nil)
		cancel()
		require.NoError(t, err)
		require.Equal(t, app.StatePath("core.bf.reproducing"), out.NewState)
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
		cancel()
		require.NoError(t, err)
		require.Equal(t, app.StatePath("core.bf.proposing"), out.NewState)
	}

	// Simulate the user's broken state: the worktree dir is gone but
	// bf_autostart_attempted is pinned true. A re-entry to idle won't
	// recreate the dir; an arc that depends on it will fail.
	workdir := filepath.Join(repoRoot, ".worktrees", "bf-"+ticketID)
	require.NoError(t, os.RemoveAll(workdir))
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoRoot
	pruneOut, pruneErr := pruneCmd.CombinedOutput()
	require.NoError(t, pruneErr, "git worktree prune: %s", string(pruneOut))

	// THE BUG: typing `accept` at proposing used to silently bounce
	// back to idle because implementing.on_enter's
	// `iface.workspace.sync` failed (no worktree registered for
	// `bf-<id>`) and `on_error: idle` fired. Post-fix, implementing's
	// on_enter idempotently re-creates the worktree first, so the
	// session advances to implementing regardless of whether the
	// worktree survived between turns.
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err, "core__bf__accept from proposing must complete within 10s")
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("core.bf.implementing"), out.NewState,
		"accept at proposing must land at implementing, not bounce to idle; got %q (view: %q)",
		out.NewState, out.View)

	// Worktree must be back on disk after implementing.on_enter ran.
	_, statErr := os.Stat(workdir)
	require.NoError(t, statErr,
		"implementing.on_enter must have re-created the worktree at %s", workdir)
}

// TestDogfoodSmoke_ProposingAccept_RegisteredWorktreeDirtyTree reproduces
// the *production* shape of the bounce-to-idle bug — the one the user
// hit on a live session that the earlier prune-based test couldn't
// reach. The earlier test removes the worktree dir AND prunes the
// registration, then verifies that implementing.on_enter can re-create
// it. But the real bug fires in a different ordering: the worktree dir
// *exists* on disk AND *is registered* with git (via an absolute
// path), the branch is checked out there, and the worktree carries
// some stale dirty file from a prior aborted run. The previous
// implementation of `worktreeCreate.findWorktreeByPath` compared a
// relative path against git's absolute path and silently missed the
// match — falling through to `git worktree add` which then failed with
// `<path> already exists`. on_error: idle fired. Operator sees parked
// idle. No diagnostic.
//
// This test mirrors that exact shape:
//
//  1. Drive go_bugfix → reproducing → accept → proposing (real flow).
//  2. Leave the worktree where it is — registered, on disk, branch
//     checked out — but write an unrelated dirty file in it (mirrors
//     the user's `stories/bugfix/evidence/<ticket>.log` change).
//  3. Submit core__bf__accept.
//
// MUST land at core.bf.implementing. Pre-path-normalisation-fix this
// landed at core.bf.idle.
func TestDogfoodSmoke_ProposingAccept_RegisteredWorktreeDirtyTree(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, s, sid, _ := newSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__go_bugfix", nil)
		cancel()
		require.NoError(t, err)
		require.Equal(t, app.StatePath("core.bf.reproducing"), out.NewState)
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
		cancel()
		require.NoError(t, err)
		require.Equal(t, app.StatePath("core.bf.proposing"), out.NewState)
	}

	// Dirty the worktree with an unrelated change — the actual on-disk
	// shape the user had (stale evidence log from a prior aborted run).
	// The implementation phase's git.commit will see this in the index
	// but it isn't in the proposal's affected_files, so a naive
	// `git add <listed> && git commit -m` stages nothing and `git
	// commit` exits non-zero with "no changes added to commit" on
	// STDOUT (not stderr).
	workdir := filepath.Join(repoRoot, ".worktrees", "bf-"+ticketID)
	staleEvidence := filepath.Join(workdir, "stale_evidence.txt")
	require.NoError(t, os.WriteFile(staleEvidence, []byte("dirty\n"), 0o644))

	// Confirm: the worktree is registered with git via its ABSOLUTE
	// path. The path-comparison bug surfaces when worktreeCreate
	// re-enters and constructs `<repo>/.worktrees/<id>` as a relative
	// path (because repo arg is empty / cwd) then fails to match
	// git's absolute path in `git worktree list --porcelain`.
	listCmd := exec.Command("git", "worktree", "list", "--porcelain")
	listCmd.Dir = repoRoot
	listOut, listErr := listCmd.CombinedOutput()
	require.NoError(t, listErr)
	require.Contains(t, string(listOut), workdir,
		"worktree must be registered at the absolute path %s", workdir)

	// THE BUG: accept at proposing used to bounce back to idle because
	// (a) findWorktreeByPath compared relative vs absolute paths and
	// missed the registered worktree, (b) the fallback `git worktree
	// add` failed because the dir/branch already existed, (c)
	// on_error: idle fired silently. Post-fix this advances cleanly.
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err)
	require.NotNil(t, out)
	if out.NewState != "core.bf.implementing" {
		hist, _ := s.LoadHistory(sid)
		for i := len(hist) - 1; i >= 0 && i > len(hist)-25; i-- {
			ev := hist[i]
			if ev.Kind == store.HostReturned || ev.Kind == store.HostDispatched {
				t.Logf("ev #%d %s: %s", i, ev.Kind, string(ev.Payload))
			}
		}
	}
	require.Equal(t, app.StatePath("core.bf.implementing"), out.NewState,
		"accept at proposing must land at implementing despite a registered/dirty worktree; got %q (view: %q)",
		out.NewState, out.View)
}

// TestDogfoodSmoke_FullBugfixPipeline drives the bugfix pipeline from
// `core.main` through go_bugfix → reproducing → proposing →
// implementing → testing → reviewing → validating → done in one shot,
// asserting each phase advances cleanly. This is the regression net
// for "did we break the happy path?" — any room that fails to advance
// here means the user types `continue` and watches the TUI silently
// bounce.
//
// Both the oracle and local CI are stubbed: real-LLM tests cost money
// and `go test ./...` against the temp repo would just exercise our
// own tests recursively. The git_worktree + git + append_to_file
// handlers run for real so the workspace-side seams get exercised.
func TestDogfoodSmoke_FullBugfixPipeline(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, _ := newSmokeOrchestratorWithCIStub(t, repoRoot)

	ctx := context.Background()

	step := func(label, intent string, want app.StatePath) {
		t.Helper()
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := orch.SubmitDirect(c, sid, intent, nil)
		require.NoError(t, err, "%s: SubmitDirect(%s)", label, intent)
		require.NotNil(t, out, "%s: nil out", label)
		require.Equal(t, want, out.NewState,
			"%s: %s should land at %q; got %q (view: %q)",
			label, intent, want, out.NewState, out.View)
	}

	// Bootstrap into the bugfix pipeline.
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}

	// Drive every accept boundary. Pre-fix any of these could have
	// silently bounced to idle; post-fix they must all advance.
	step("kickoff", "core__go_bugfix", "core.bf.reproducing")
	step("reproducing", "core__bf__accept", "core.bf.proposing")
	step("proposing", "core__bf__accept", "core.bf.implementing")
	step("implementing", "core__bf__accept", "core.bf.testing")
	step("testing", "core__bf__accept", "core.bf.reviewing")
	step("reviewing", "core__bf__accept", "core.bf.validating")
	step("validating", "core__bf__accept", "core.bf.done")
}

// TestDogfoodSmoke_FullImplementationPipeline drives the implementation
// pipeline (small-task / feature flow) end-to-end:
// go_implementation → review_task → write_code → test → review →
// handoff → pr.open_pr → pr.ci → pr.merge → done. Same shape as the
// bugfix-pipeline test but exercises the dev-story `impl` sub-app
// instead of `bf`. Catches "did we break a feature-track room?"
// regressions before users hit them.
//
// Notes on intent prefixing: the impl pipeline keeps separate
// `_executing` / `_awaiting_reply` rooms (unlike bugfix which merged
// them), so the operator types `proceed` then `accept` at each phase.
// After import folding, the intents become `core__impl__proceed` /
// `core__impl__accept`; the PR-refinement sub-import inside impl uses
// `core__pr__proceed` / `core__pr__accept`.
func TestDogfoodSmoke_FullImplementationPipeline(t *testing.T) {
	repoRoot, _ := setupDogfoodRepo(t)
	// Switch the seeded ticket to a feature so go_implementation
	// guard (`world.ticket_type == 'feature'` is implicit via the
	// dev-story drive arc, but here we use go_implementation directly
	// which only checks ticket_id != ''). Reusing the bug ticket is
	// fine for transition exercise.
	ticketID := "2026-05-17T111838Z-integration-smoke-bug-picked-up-by-dogfood"
	orch, _, sid, _ := newSmokeOrchestratorWithCIStub(t, repoRoot)

	ctx := context.Background()

	step := func(label, intent string, want app.StatePath) {
		t.Helper()
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := orch.SubmitDirect(c, sid, intent, nil)
		require.NoError(t, err, "%s: SubmitDirect(%s)", label, intent)
		require.NotNil(t, out, "%s: nil out", label)
		require.Equal(t, want, out.NewState,
			"%s: %s should land at %q; got %q (view: %q)",
			label, intent, want, out.NewState, out.View)
	}

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	seed := seedDogfoodWorld(ticketID)
	seed["core__ticket_type"] = "feature"
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seed,
		})
		require.NoError(t, err)
		cancel()
	}

	step("kickoff", "core__go_implementation", "core.impl.idle")
	step("idle → review_task", "core__impl__start", "core.impl.review_task_executing")
	step("review_task → wait", "core__impl__proceed", "core.impl.review_task_awaiting_reply")
	step("review_task → write", "core__impl__accept", "core.impl.write_code_executing")
	step("write → wait", "core__impl__proceed", "core.impl.write_code_awaiting_reply")
	step("write → test", "core__impl__accept", "core.impl.test_executing")
	step("test → wait", "core__impl__proceed", "core.impl.test_awaiting_reply")
	step("test → review", "core__impl__accept", "core.impl.review_executing")
	step("review → wait", "core__impl__proceed", "core.impl.review_awaiting_reply")
	step("review → handoff", "core__impl__accept", "core.impl.handoff")
}

// TestDogfoodSmoke_ImplementingActuallyEditsFiles is the regression
// guard for the "implementing room is a no-op" bug: until 2026-05-19,
// implementing.on_enter ran workspace.sync + vcs.commit + a misleading
// `say "Fix applied"` and advanced, but nothing actually edited the
// code. Earlier happy-path smoke tests didn't notice because the
// oracle was stubbed and they only checked `next_state`. This test
// stubs the implementing oracle to ACTUALLY write a file in the
// worktree, then asserts post-pipeline:
//
//  1. The file shows up in `git diff HEAD~ HEAD` on the feature
//     branch — proves the commit step picked up the oracle's edits.
//  2. `world.implement_artifact.files_changed` lists the file —
//     proves the artifact binding flowed.
//
// A future "implementing-is-a-no-op" regression would fail both
// assertions before testing room ran.
func TestDogfoodSmoke_ImplementingActuallyEditsFiles(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	appPath := filepath.Join(repoRoot, "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()

	// Per-prompt-path oracle dispatch: the implementing prompt's stub
	// edits a marker file in the worktree; every other prompt returns
	// the generic canned artifact.
	//
	// Registered for both ask_with_mcp (stories/implementation —
	// out of Phase 8 scope) and task (bugfix rooms post Phase 8).
	markerFile := "STUB_EDITED_BY_IMPLEMENTING_ORACLE.txt"
	implementingOracleStub := func(ctx context.Context, args map[string]any) (host.Result, error) {
		// Read prompt from either flat ask_with_mcp shape (args["prompt"]) or
		// the new task shape (args["context"]["prompt"]) — see oracle-split C2.
		promptArg, _ := args["prompt"].(string)
		if promptArg == "" {
			if ctxBlock, ok := args["context"].(map[string]any); ok {
				promptArg, _ = ctxBlock["prompt"].(string)
			}
		}
		// The implementing prompt is the one whose name contains
		// "implementing_executing.md" (after path resolution). The
		// stub WRITES a real file in working_dir to prove edits
		// actually happen.
		if strings.Contains(promptArg, "implementing_executing") {
			wd, _ := args["working_dir"].(string)
			markerPath := filepath.Join(repoRoot, wd, markerFile)
			if writeErr := os.WriteFile(markerPath, []byte("written by stub oracle\n"), 0o644); writeErr != nil {
				return host.Result{Error: fmt.Sprintf("stub write: %v", writeErr)}, nil
			}
			artifact := map[string]any{
				"summary_title":    "Stub implementing — wrote marker file",
				"summary_markdown": "Wrote " + markerFile + " in the worktree to prove the oracle ran and the commit step picked it up.\n\nFull text: written by stub oracle.\n",
				"files_changed":    []string{markerFile},
				"applied":          true,
			}
			stdoutJSON, _ := json.Marshal(artifact)
			return host.Result{Data: map[string]any{
				"submitted": artifact,
				"stdout":    string(stdoutJSON),
				"ok":        true,
			}}, nil
		}
		// All other prompts: generic canned artifact.
		stdoutJSON, _ := json.Marshal(dogfoodArtifact)
		return host.Result{Data: map[string]any{
			"submitted": dogfoodArtifact,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
	reg.Register("host.oracle.ask_with_mcp", implementingOracleStub)
	// oracle-split Phase 8: bugfix uses task/ask/decide instead of ask_with_mcp.
	reg.Register("host.oracle.task", implementingOracleStub)
	reg.Register("host.oracle.ask", implementingOracleStub)
	reg.Register("host.oracle.decide", implementingOracleStub)
	reg.Register("host.local", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"ok": true, "passed": 1, "failed": 0, "log": "PASS (stub)",
		}}, nil
	})
	reg.Register("host.local_files.ticket", host.LocalFilesTicketHandler)
	reg.Register("host.git", host.GitVCSHandler)
	reg.Register("host.git_worktree", host.GitWorktreeHandler)
	reg.Register("host.append_to_file", host.AppendFileTransportHandler)
	reg.Register("host.inbox.add", host.InboxAddHandler)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	ctx := context.Background()
	step := func(label, intent string, want app.StatePath) {
		t.Helper()
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, sErr := orch.SubmitDirect(c, sid, intent, nil)
		require.NoError(t, sErr, "%s: SubmitDirect(%s)", label, intent)
		require.NotNil(t, out, "%s: nil out", label)
		require.Equal(t, want, out.NewState,
			"%s: %s should land at %q; got %q (view: %q)",
			label, intent, want, out.NewState, out.View)
	}

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, tErr := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, tErr)
		cancel()
	}
	step("kickoff", "core__go_bugfix", "core.bf.reproducing")
	step("reproducing", "core__bf__accept", "core.bf.proposing")
	step("proposing", "core__bf__accept", "core.bf.implementing")

	// 1. The marker file must be committed on the feature branch.
	workdir := filepath.Join(repoRoot, ".worktrees", "bf-"+ticketID)
	showCmd := exec.Command("git", "show", "--name-only", "--format=%H", "HEAD")
	showCmd.Dir = workdir
	showOut, showErr := showCmd.CombinedOutput()
	require.NoError(t, showErr, "git show: %s", string(showOut))
	require.Contains(t, string(showOut), markerFile,
		"HEAD commit must include the marker file written by the oracle stub; got: %s", string(showOut))

	// 2. world.implement_artifact must reflect what the stub returned.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	artifact, _ := journey.World.Vars["core__bf__implement_artifact"].(map[string]any)
	require.NotNil(t, artifact, "world.implement_artifact must be bound")
	require.Equal(t, true, artifact["applied"], "applied must be true")
	files, _ := artifact["files_changed"].([]any)
	require.Len(t, files, 1, "files_changed must have one entry")
	require.Equal(t, markerFile, files[0])
}

// newSmokeOrchestratorWithCIStub mirrors newSmokeOrchestrator but also
// stubs `host.local` (the iface.ci default in kitsoki-dev). Real CI
// invokes `go test ./...` inside the temp worktree, which would either
// blow up (no Go files outside the copied trees) or take seconds to
// finish. Returning canned passed/failed counts keeps the smoke test
// fast and focused on the state-machine transitions.
func newSmokeOrchestratorWithCIStub(t *testing.T, repoRoot string) (*orchestrator.Orchestrator, store.Store, app.SessionID, *int) {
	t.Helper()
	appPath := filepath.Join(repoRoot, "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	oracleCalls := 0
	oracleStub2 := func(ctx context.Context, args map[string]any) (host.Result, error) {
		oracleCalls++
		stdoutJSON, _ := json.Marshal(dogfoodArtifact)
		return host.Result{Data: map[string]any{
			"submitted": dogfoodArtifact,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
	reg.Register("host.oracle.ask_with_mcp", oracleStub2)
	// oracle-split Phase 8 verbs used by bugfix rooms.
	reg.Register("host.oracle.task", oracleStub2)
	reg.Register("host.oracle.ask", oracleStub2)
	reg.Register("host.oracle.decide", oracleStub2)
	reg.Register("host.local", func(ctx context.Context, args map[string]any) (host.Result, error) {
		op, _ := args["op"].(string)
		switch op {
		case "run_tests":
			return host.Result{Data: map[string]any{
				"ok": true, "passed": 1, "failed": 0,
				"log": "PASS (stubbed)", "junit": "",
			}}, nil
		case "build":
			return host.Result{Data: map[string]any{
				"ok": true, "log": "build ok (stubbed)",
			}}, nil
		default:
			return host.Result{Data: map[string]any{"ok": true}}, nil
		}
	})

	reg.Register("host.local_files.ticket", host.LocalFilesTicketHandler)
	reg.Register("host.git", host.GitVCSHandler)
	reg.Register("host.git_worktree", host.GitWorktreeHandler)
	reg.Register("host.append_to_file", host.AppendFileTransportHandler)
	reg.Register("host.inbox.add", host.InboxAddHandler)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, s, sid, &oracleCalls
}

// =============================================================================
// Outcome-aware routing tests
//
// These regression-guard the 2026-05-19 fixes to rooms/testing.yaml and
// rooms/validating.yaml: pre-fix the `accept` arc in each room advanced
// (or posted!) regardless of the artifact's verdict. The bugfix room
// trace at /tmp/kitsoki-dogfood-trace.jsonl captured the exact production
// failure — a `fail_short` validation outcome was broadcast as the
// success close-out and the session terminated at done with the actual
// fix never applied.
//
// Each test below stubs the oracle per-prompt so a specific gate room
// produces a non-pass artifact while every other room produces the
// canonical pass artifact. The smoke test then asserts:
//   - the room routes to implementing (not advancing forward),
//   - no transport.post (iface.transport.post → host.append_to_file)
//     fired on the failure arm,
//   - the corresponding cycle counter was bumped,
//   - every oracle invocation received working_dir == world.workdir,
//   - and iface.ci.build was not handed a spurious target arg.
// =============================================================================

// promptArtifact maps a prompt path fragment (e.g. "validating_executing")
// to the artifact payload the stub returns. The fallback is the canonical
// `dogfoodArtifact` so unconfigured prompts get the happy-path shape.
type promptArtifact map[string]map[string]any

// oracleRouter returns a host.oracle.ask_with_mcp stub that:
//   - picks the artifact for the matching prompt fragment, falling back
//     to dogfoodArtifact for prompts not in the map,
//   - records every (prompt, working_dir) pair into *seen for the
//     working-dir assertion below.
type oracleSeen struct {
	prompt     string
	workingDir string
}

func oracleRouter(artifacts promptArtifact, seen *[]oracleSeen) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		promptArg, _ := args["prompt"].(string)
		if promptArg == "" {
			if ctxBlock, ok := args["context"].(map[string]any); ok {
				promptArg, _ = ctxBlock["prompt"].(string)
			}
		}
		if promptArg == "" {
			promptArg, _ = args["prompt_path"].(string)
		}
		workingDir, _ := args["working_dir"].(string)
		*seen = append(*seen, oracleSeen{prompt: promptArg, workingDir: workingDir})

		payload := dogfoodArtifact
		for fragment, override := range artifacts {
			if strings.Contains(promptArg, fragment) {
				payload = override
				break
			}
		}
		stdoutJSON, _ := json.Marshal(payload)
		return host.Result{Data: map[string]any{
			"submitted": payload,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
}

// hostLocalCapture wraps the CI stub used by the existing smoke tests
// and records the args of every "build" invocation so a test can assert
// the spurious `target: "default"` regression doesn't recur.
type hostLocalSeen struct {
	op   string
	args map[string]any
}

func hostLocalCapture(seen *[]hostLocalSeen) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		op, _ := args["op"].(string)
		// Shallow copy so later mutations don't clobber the record.
		snap := make(map[string]any, len(args))
		for k, v := range args {
			snap[k] = v
		}
		*seen = append(*seen, hostLocalSeen{op: op, args: snap})

		switch op {
		case "run_tests":
			return host.Result{Data: map[string]any{
				"ok": true, "passed": 1, "failed": 0,
				"log": "PASS (stubbed)", "junit": "",
			}}, nil
		case "build":
			return host.Result{Data: map[string]any{
				"ok": true, "log": "build ok (stubbed)",
			}}, nil
		default:
			return host.Result{Data: map[string]any{"ok": true}}, nil
		}
	}
}

// newSmokeOrchestratorWithRouters builds an orchestrator pinned to the
// flexible oracle + ci stubs. Returns the orchestrator, store, session
// id, and the (oracleSeen, hostLocalSeen) capture slices.
func newSmokeOrchestratorWithRouters(t *testing.T, repoRoot string, artifacts promptArtifact) (*orchestrator.Orchestrator, store.Store, app.SessionID, *[]oracleSeen, *[]hostLocalSeen) {
	t.Helper()
	appPath := filepath.Join(repoRoot, "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	oracleSeenSlice := []oracleSeen{}
	hostLocalSeenSlice := []hostLocalSeen{}

	reg := host.NewRegistry()
	oracleRouterFn := oracleRouter(artifacts, &oracleSeenSlice)
	reg.Register("host.oracle.ask_with_mcp", oracleRouterFn)
	// oracle-split Phase 8 verbs used by bugfix rooms. All route through
	// the same prompt-dispatch logic so per-phase artifact overrides apply.
	reg.Register("host.oracle.task", oracleRouterFn)
	reg.Register("host.oracle.ask", oracleRouterFn)
	reg.Register("host.oracle.decide", oracleRouterFn)
	reg.Register("host.local", hostLocalCapture(&hostLocalSeenSlice))
	reg.Register("host.local_files.ticket", host.LocalFilesTicketHandler)
	reg.Register("host.git", host.GitVCSHandler)
	reg.Register("host.git_worktree", host.GitWorktreeHandler)
	reg.Register("host.append_to_file", host.AppendFileTransportHandler)
	reg.Register("host.inbox.add", host.InboxAddHandler)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, s, sid, &oracleSeenSlice, &hostLocalSeenSlice
}

// driveBugfixPipelineTo runs the on-enter chain plus a teleport to
// core.main, then submits accept intents in sequence to walk the bugfix
// pipeline. Each step asserts the resulting state matches `want`. Stops
// once `stopAt` is reached (or after all steps if stopAt == "").
//
// Used by both fail-routing tests to set up identical state before the
// fail intent fires at the gate under test.
func driveBugfixPipelineTo(t *testing.T, orch *orchestrator.Orchestrator, sid app.SessionID, ticketID string, stopAt app.StatePath) {
	t.Helper()
	ctx := context.Background()
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.main"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}

	type step struct {
		label  string
		intent string
		want   app.StatePath
	}
	steps := []step{
		{"kickoff", "core__go_bugfix", "core.bf.reproducing"},
		{"reproducing", "core__bf__accept", "core.bf.proposing"},
		{"proposing", "core__bf__accept", "core.bf.implementing"},
		{"implementing", "core__bf__accept", "core.bf.testing"},
		{"testing", "core__bf__accept", "core.bf.reviewing"},
		{"reviewing", "core__bf__accept", "core.bf.validating"},
	}
	for _, st := range steps {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := orch.SubmitDirect(c, sid, st.intent, nil)
		cancel()
		require.NoError(t, err, "%s: SubmitDirect(%s)", st.label, st.intent)
		require.NotNil(t, out, "%s: nil out", st.label)
		require.Equal(t, st.want, out.NewState,
			"%s: %s should land at %q; got %q (view: %q)",
			st.label, st.intent, st.want, out.NewState, out.View)
		if st.want == stopAt {
			return
		}
	}
}

// countTransportPosts returns the number of host.append_to_file
// HostDispatched events in the session history. iface.transport.post is
// bound to host.append_to_file in kitsoki-dev (see app.yaml). Used to
// assert "no post fired" on a failure-routing arc.
func countTransportPosts(t *testing.T, s store.Store, sid app.SessionID) int {
	t.Helper()
	hist, err := s.LoadHistory(sid)
	require.NoError(t, err)
	n := 0
	for _, ev := range hist {
		if ev.Kind != store.HostDispatched {
			continue
		}
		var p map[string]any
		if jsonErr := json.Unmarshal(ev.Payload, &p); jsonErr != nil {
			continue
		}
		ns, _ := p["namespace"].(string)
		if ns == "host.append_to_file" {
			n++
		}
	}
	return n
}

// TestDogfoodSmoke_ValidatingFailShortRoutesToImplementing locks in
// rooms/validating.yaml's outcome-aware accept arc: when the validation
// oracle returns outcome=fail_short, `core__bf__accept` MUST route to
// implementing (not advance to done), MUST NOT fire transport.post,
// and MUST bump implementing_cycle. See rooms/validating.yaml header
// for the dogfood-trace context that motivated this fix.
func TestDogfoodSmoke_ValidatingFailShortRoutesToImplementing(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	// Override the validating oracle ONLY — every other room sees the
	// canonical pass artifact. The failure path lives inside one room.
	failShort := map[string]any{}
	for k, v := range dogfoodArtifact {
		failShort[k] = v
	}
	failShort["outcome"] = "fail_short"
	failShort["next_action_hint"] = "fail_short test hint"
	artifacts := promptArtifact{
		"validating_executing": failShort,
	}
	orch, s, sid, _, _ := newSmokeOrchestratorWithRouters(t, repoRoot, artifacts)

	// Walk the pipeline up to core.bf.validating.
	driveBugfixPipelineTo(t, orch, sid, ticketID, "core.bf.validating")

	// transport.post count at validating's entry (the reviewing.accept
	// posts the test review, plus the testing.accept posts the test
	// review). We capture the count before submitting to detect any new
	// post on this arc.
	postsBefore := countTransportPosts(t, s, sid)

	// THE FAIL_SHORT ARM: accept at validating must route to implementing,
	// not to done, AND must not fire a transport.post.
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("core.bf.implementing"), out.NewState,
		"validating.accept on fail_short must route to implementing; got %q (view: %q)",
		out.NewState, out.View)

	postsAfter := countTransportPosts(t, s, sid)
	require.Equal(t, postsBefore, postsAfter,
		"validating.accept on fail_short must NOT fire transport.post (broadcasting failure as success); got %d new posts",
		postsAfter-postsBefore)

	// implementing_cycle must have been bumped.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, int64(1), worldInt64(journey.World.Vars, "core__bf__implementing_cycle"),
		"implementing_cycle must increment when fail_short re-routes to implementing")
}

// worldInt64 normalises numeric world values to int64. World vars
// typed `int` in app.yaml come back from the orchestrator as int64.
func worldInt64(vars map[string]any, key string) int64 {
	switch v := vars[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// TestDogfoodSmoke_TestingFailedRoutesToImplementing locks in
// rooms/testing.yaml's status-aware accept arc: when the testing oracle
// returns status=failed, accept MUST route to implementing (not advance
// to reviewing or done), MUST NOT fire transport.post, and MUST bump
// implementing_cycle.
func TestDogfoodSmoke_TestingFailedRoutesToImplementing(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	failed := map[string]any{}
	for k, v := range dogfoodArtifact {
		failed[k] = v
	}
	failed["status"] = "failed"
	artifacts := promptArtifact{
		"testing_executing": failed,
	}
	orch, s, sid, _, _ := newSmokeOrchestratorWithRouters(t, repoRoot, artifacts)

	driveBugfixPipelineTo(t, orch, sid, ticketID, "core.bf.testing")

	postsBefore := countTransportPosts(t, s, sid)

	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("core.bf.implementing"), out.NewState,
		"testing.accept on status=failed must route to implementing; got %q (view: %q)",
		out.NewState, out.View)

	postsAfter := countTransportPosts(t, s, sid)
	require.Equal(t, postsBefore, postsAfter,
		"testing.accept on status=failed must NOT fire transport.post; got %d new posts",
		postsAfter-postsBefore)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, int64(1), worldInt64(journey.World.Vars, "core__bf__implementing_cycle"),
		"implementing_cycle must increment when testing-failed re-routes")
}

// TestDogfoodSmoke_OracleAlwaysReceivesWorkingDir locks in the second
// half of the dogfood fix: every bugfix-room oracle invocation must pass
// `working_dir` = the bugfix worktree. Without it the oracle's MCP
// filesystem tools default to kitsoki's cwd (the main worktree) and
// any grep/read the oracle runs lands on the wrong tree.
//
// Drives the full pass path (so every room's oracle fires at least
// once), then asserts each phase-executing prompt was invoked with
// working_dir set and pointing to .worktrees/bf-<ticket>.
func TestDogfoodSmoke_OracleAlwaysReceivesWorkingDir(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, oracleCalls, _ := newSmokeOrchestratorWithRouters(t, repoRoot, promptArtifact{})

	driveBugfixPipelineTo(t, orch, sid, ticketID, "core.bf.validating")
	// One more accept to land at done — exercises validating's oracle on
	// the pass arm and done's oracle on entry.
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err)
	require.Equal(t, app.StatePath("core.bf.done"), out.NewState)

	expected := filepath.Join(".worktrees", "bf-"+ticketID)

	// For each phase-executing prompt, find the matching oracle call and
	// assert working_dir was set and points at the bugfix worktree.
	phasePrompts := []string{
		"reproducing_executing",
		"proposing_executing",
		"implementing_executing",
		"testing_executing",
		"validating_executing",
		"done_executing",
	}
	for _, phase := range phasePrompts {
		found := false
		for _, call := range *oracleCalls {
			if !strings.Contains(call.prompt, phase) {
				continue
			}
			found = true
			require.NotEmpty(t, call.workingDir,
				"%s oracle invocation must set working_dir; got empty (prompt=%q)",
				phase, call.prompt)
			require.True(t, strings.HasSuffix(call.workingDir, expected) || call.workingDir == expected,
				"%s oracle working_dir must point at the bugfix worktree (want suffix %q); got %q",
				phase, expected, call.workingDir)
			break
		}
		require.True(t, found, "expected at least one oracle call for prompt %s", phase)
	}
}

// TestDogfoodSmoke_ValidatingBuildSkipsSpuriousTargetArg locks in the
// third dogfood fix: rooms/validating.yaml no longer passes
// `target: "default"` to iface.ci.build. Pre-fix, ciBuild appended
// the literal string "default" to `go build ./...`, synthesising a
// `package default is not in std` error on every full-pipeline run.
// The oracle then mistook that synthesised error for evidence the fix
// wasn't applied. Now: no target arg at all.
func TestDogfoodSmoke_ValidatingBuildSkipsSpuriousTargetArg(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, _, hostLocalCalls := newSmokeOrchestratorWithRouters(t, repoRoot, promptArtifact{})

	// Drive through to validating so iface.ci.build fires on entry.
	driveBugfixPipelineTo(t, orch, sid, ticketID, "core.bf.validating")

	buildCalls := 0
	for _, call := range *hostLocalCalls {
		if call.op != "build" {
			continue
		}
		buildCalls++
		target, _ := call.args["target"].(string)
		require.Empty(t, target,
			"iface.ci.build target arg must be empty (pre-fix: literal 'default' got appended as a go-package path); got %q",
			target)
	}
	require.GreaterOrEqual(t, buildCalls, 1,
		"validating.on_enter must invoke iface.ci.build at least once")
}
