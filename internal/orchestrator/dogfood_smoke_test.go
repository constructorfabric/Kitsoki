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
//   - real host.RegisterBuiltins; only host.agent.ask_with_mcp is
//     stubbed (canned artifact payload — no real LLM call).
//   - hard `context.WithTimeout(ctx, …)` per turn so a regression
//     FAILS in seconds rather than hanging CI.
//
// Conceptual mirror of .kitsoki/stories/kitsoki-dev/scenarios/verify_autostart.yaml.

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
// the kitsoki `stories/` library (so `app.Load` resolves all reusable imports),
// the project-local `.kitsoki/stories/` tree, and the `issues/` tree (so
// `host.local_files.ticket` finds real bug files). Sets the process cwd via
// t.Chdir so `host.git_worktree`
// (which uses `dir == "" → cwd`) operates on the temp repo.
//
// Returns the repo root and the canonical ticket id we drive through
// the pipeline.
func setupDogfoodRepo(t *testing.T) (repoRoot string, ticketID string) {
	t.Helper()

	repoRoot = t.TempDir()

	// Copy project-local stories, reusable stories, and issues from the live repo. We resolve the
	// kitsoki repo root relative to this test file: package dir is
	// internal/orchestrator/, so two levels up is the repo root.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	kitsokiRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	for _, sub := range []string{filepath.Join(".kitsoki", "stories"), "stories", "issues"} {
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

// dogfoodArtifact is the canned schema-shaped payload the agent stub
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

// newDogfoodRegistry builds a host registry that mirrors REAL dogfood: the
// FULL builtin set (host.RegisterBuiltins), then Replace the agent verbs with a
// canned-artifact stub so no automated test ever shells out to a real LLM.
//
// Using RegisterBuiltins + Replace (not a hand-picked subset) is deliberate and
// load-bearing: a curated subset silently drifts from kitsoki-dev's declared
// `hosts:` allow-list, and a handler the subset forgot surfaces only as a
// confusing "no handler registered" infrastructure failure → on_error bounce at
// a random room transition (in tests only — real dogfood uses RegisterBuiltins,
// so the handler IS there). That drift cost a full debug session once. The full
// set can't drift; Replace overrides exactly what we stub. See
// Registry.Replace / Registry.ValidateAllowList.
//
// Real (non-agent) builtins run against the temp repo: host.git / host.run /
// host.git_worktree exercise actual git, which is the point of the smoke tests.
// host.gh.ticket is registered but unused here — kitsoki-dev seeds tickets via
// local files and the bugfix path never invokes the ticket iface.
func newDogfoodRegistry(agentCalls *int) *host.Registry {
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	stub := func(ctx context.Context, args map[string]any) (host.Result, error) {
		*agentCalls++
		stdoutJSON, _ := json.Marshal(dogfoodArtifact)
		return host.Result{Data: map[string]any{
			"submitted": dogfoodArtifact,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
	// Stub EVERY agent verb — automated tests must never invoke a real LLM
	// (CLAUDE.md). Replace overrides the real handler RegisterBuiltins set.
	for _, verb := range []string{
		"host.agent.ask", "host.agent.ask_with_mcp", "host.agent.task",
		"host.agent.decide", "host.agent.extract", "host.agent.converse",
		"host.agent.search",
	} {
		reg.Replace(verb, stub)
	}
	return reg
}

// newSmokeOrchestrator builds an orchestrator pinned to the temp-repo
// kitsoki-dev app with the real host registry (agent verbs stubbed via
// newDogfoodRegistry; host.local is the real CI handler). Returns the
// orchestrator, the underlying store (for direct history reads), an open
// session id, and the count pointer the agent stub increments per call.
func newSmokeOrchestrator(t *testing.T, repoRoot string) (*orchestrator.Orchestrator, store.Store, app.SessionID, *int) {
	t.Helper()
	appPath := filepath.Join(repoRoot, ".kitsoki", "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err, "load kitsoki-dev/app.yaml from %s", appPath)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	agentCalls := 0
	reg := newDogfoodRegistry(&agentCalls)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	return orch, s, sid, &agentCalls
}

// seedDogfoodWorld returns the slot bag mirroring
// .kitsoki/stories/kitsoki-dev/scenarios/verify_autostart.yaml: pin the ticket,
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

// bugfixWorkdirRel reads the working folder bf.idle.on_enter derived for this
// session from the journey's world. idle.on_enter derives it from ticket_id
// AND session_id (appended so concurrent sessions on one ticket get DISTINCT
// checkouts instead of sharing one — bug9glm2), so smoke tests can no longer
// hardcode .worktrees/bf-<ticket>; they must read the actual value the story
// set. Mirrors how TestDogfoodSmoke_ImplIdleProvisionsWorktree reads it.
func bugfixWorkdirRel(t *testing.T, orch *orchestrator.Orchestrator, sid app.SessionID) string {
	t.Helper()
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	rel, _ := journey.World.Vars["core__bf__workdir"].(string)
	require.NotEmpty(t, rel, "core__bf__workdir must be set by bf.idle.on_enter after go_bugfix")
	return rel
}

// bugfixWorkdirAbs is bugfixWorkdirRel resolved against repoRoot, for tests
// that os.Stat / os.RemoveAll / exec against the worktree on disk.
func bugfixWorkdirAbs(t *testing.T, orch *orchestrator.Orchestrator, sid app.SessionID, repoRoot string) string {
	t.Helper()
	abs := bugfixWorkdirRel(t, orch, sid)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(repoRoot, abs)
	}
	return abs
}

// TestDogfoodSmoke_AutoStartThroughBugfix is the regression test for
// the `go_bugfix` redirect-loop hang flagged in the dogfood-regression-
// testing-gap proposal. The class of bug: an on_error: <sibling-room>
// arc whose redirect target's on_enter re-invokes the failing host
// call, looping until the orchestrator's `maxRedirectDepth` cap fires.
//
// Setup mirrors `verify_autostart.yaml`: a clean temp git repo (no
// stale `.worktrees/bf-<id>/`), the integration-smoke bug seeded, then
// `core__go_bugfix` from `core.landing`. Expected: workspace.create
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
	orch, _, sid, agentCalls := newSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()

	// 1. Run initial on_enter for core.landing (which invokes
	//    iface.ticket.list_mine → host.local_files.ticket).
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid), "RunInitialOnEnter must finish within 10s")
		cancel()
	}

	// 2. Teleport to core.landing with the ticket+mode seeded.
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.landing"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err, "Teleport to core.landing with seeded ticket must succeed")
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

	// Worktree must exist on disk at the path world.workdir points to.
	// idle.on_enter derives it from ticket_id AND session_id (appended for
	// per-session distinctness — bug9glm2), so read the actual derived path
	// from world rather than hardcoding .worktrees/bf-<ticket>. idle.on_enter
	// passes `id: workspace_id` to iface.workspace.create so the on-disk dir
	// aligns with what `iface.workspace.sync` (and implementing.on_enter's
	// commit) will later key on.
	workdir := bugfixWorkdirAbs(t, orch, sid, repoRoot)
	_, statErr := os.Stat(workdir)
	require.NoError(t, statErr,
		"worktree dir must exist after go_bugfix; expected %s", workdir)

	// bf_autostart_attempted must be true so a re-entry to idle is a no-op.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, true, journey.World.Vars["core__bf__bf_autostart_attempted"],
		"bf_autostart_attempted must be set true after the auto-start chain ran")

	// Agent was called once for reproducing.on_enter.
	require.GreaterOrEqual(t, *agentCalls, 1,
		"agent stub should have been invoked at least once for reproducing.on_enter")

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

func TestDogfoodSmoke_TicketSearchFreeTextRoutesToWorkbench(t *testing.T) {
	repoRoot, _ := setupDogfoodRepo(t)
	orch, s, sid, agentCalls := newSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Boot the session the way every real surface does (see
	// DriveToRest → RunInitialOnEnter). This fires the `core` compound's
	// on_enter chain, including the kitsoki-dev world_in projection that
	// sets core__ticket_repo from the instance-level ticket_repo default.
	// Without it the child keeps its own "" default and every
	// host.gh.ticket.* call resolves repo via ambient gh (the bug).
	require.NoError(t, orch.RunInitialOnEnter(c, sid))

	// The operator boots into the free-form workbench landing.
	boot, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("core.landing"), boot.State,
		"kitsoki-dev boots into the workbench landing")

	// 1. Open ticket search from the landing (the menu/quick-action route).
	_, err = orch.SubmitDirect(c, sid, "core__go_ticket_search", nil)
	require.NoError(t, err)

	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	requireDogfoodHostArg(t, history, "host.gh.ticket.search", "repo", "constructorfabric/Kitsoki")

	// 2. In the strict ticket-search menu the operator does NOT pick a row —
	//    they describe a piece of ad-hoc work in their own words (the exact
	//    prose that exposed the routing hole live: prose that matches no menu
	//    command). It must be caught by the app-level free-form fallback and
	//    routed into the workbench — deterministically, NOT via the paid
	//    main-turn interpreter (the smoke harness is a noop; a main-turn route
	//    would fail to resolve, so reaching the workbench at all proves the
	//    fallback fired).
	const msg = "we have a bunch of local tickets that need to be migrated to github - they're markdown files in the issues folder"
	out, err := orch.Turn(c, sid, msg)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("core.landing"), out.NewState,
		"unmatched prose in ticket_search must land back in the workbench")
	require.Equal(t, 1, *agentCalls,
		"workbench on_enter should process the captured request exactly once")

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, msg, journey.World.Vars["core__landing_request"],
		"the operator's verbatim request must reach the workbench agent")

	// 3. The trace must attribute the route: fallback (deterministic, $0) — the
	//    guarantee that this turn is never an unattributable {intent:""} row.
	history, err = s.LoadHistory(sid)
	require.NoError(t, err)
	requireDogfoodRoutedBy(t, history, "fallback")
}

func requireDogfoodHostArg(t *testing.T, history []store.Event, namespace, key string, want any) {
	t.Helper()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Kind != store.HostDispatched {
			continue
		}
		var payload struct {
			Namespace string         `json:"namespace"`
			Args      map[string]any `json:"args"`
		}
		require.NoError(t, json.Unmarshal(history[i].Payload, &payload))
		if payload.Namespace != namespace {
			continue
		}
		require.Equal(t, want, payload.Args[key])
		return
	}
	t.Fatalf("no HostDispatched event found for %s", namespace)
}

func requireDogfoodRoutedBy(t *testing.T, history []store.Event, want string) {
	t.Helper()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Kind != store.TurnStarted {
			continue
		}
		var payload struct {
			RoutedBy string `json:"routed_by"`
		}
		require.NoError(t, json.Unmarshal(history[i].Payload, &payload))
		require.Equal(t, want, payload.RoutedBy)
		return
	}
	t.Fatalf("no TurnStarted event found")
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
			State: app.StatePath("core.landing"),
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
	// future patch makes it idempotent), or core.landing (the redirect
	// bounced through @exit:abandoned) are acceptable.
	acceptable := map[app.StatePath]bool{
		"core.bf.idle":        true,
		"core.bf.reproducing": true,
		"core.landing":        true,
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
			State: app.StatePath("core.landing"),
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
	workdir := bugfixWorkdirAbs(t, orch, sid, repoRoot)
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
			State: app.StatePath("core.landing"),
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
	workdir := bugfixWorkdirAbs(t, orch, sid, repoRoot)
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
// `core.landing` through go_bugfix → reproducing → proposing →
// implementing → testing → reviewing → validating → done in one shot,
// asserting each phase advances cleanly. This is the regression net
// for "did we break the happy path?" — any room that fails to advance
// here means the user types `continue` and watches the TUI silently
// bounce.
//
// Both the agent and local CI are stubbed: real-LLM tests cost money
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
			State: app.StatePath("core.landing"),
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

// TestDogfoodSmoke_DoneRefusesUncommittedWork is the END-TO-END proof of the
// lost-work guard, against a REAL git worktree with the REAL host.run handler
// (no stubs for the clean-tree check). It reproduces the exact incident shape:
// a maker room left work in the worktree that no room committed, so the
// committed tip is broken/partial while the working tree looks green.
//
// Flow: drive the bugfix pipeline to validating (worktree clean — the
// testing room's stage_all commit swept it), THEN write an UNTRACKED file into
// the maker worktree (simulating a test/fix the maker wrote but never
// committed), THEN accept into done. done.on_enter runs `git status
// --porcelain` for real and binds worktree_dirty=true; the accept guard then
// routes bf → @exit:needs-human (which dev-story maps to core.landing with
// status=needs-human), NOT @exit:done (→ core.pr.*).
//
// This closes the gap the flow fixtures can't: they stub the dirty result, so
// they prove the guard ROUTES correctly but not that host.run DETECTS dirt in
// the live folded-story path. If world.workdir were empty here (the guard's
// on-enter check is guarded on workdir != ''), the file injection would have no
// effect and the test would land at core.pr.* — failing loudly. So this also
// asserts the guard is actually wired in the dogfood path, not silently dead.
func TestDogfoodSmoke_DoneRefusesUncommittedWork(t *testing.T) {
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
			"%s: %s should land at %q; got %q", label, intent, want, out.NewState)
	}

	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.landing"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err)
		cancel()
	}

	// Drive to validating (worktree is clean here).
	step("kickoff", "core__go_bugfix", "core.bf.reproducing")
	step("reproducing", "core__bf__accept", "core.bf.proposing")
	step("proposing", "core__bf__accept", "core.bf.implementing")
	step("implementing", "core__bf__accept", "core.bf.testing")
	step("testing", "core__bf__accept", "core.bf.reviewing")
	step("reviewing", "core__bf__accept", "core.bf.validating")

	// CRUX: the guard's on-enter check is guarded on world.workdir != ''. If
	// the dogfood path never projects workdir, the guard is silently dead.
	// Assert it IS set before relying on the injection below. world.workdir is
	// a repo-relative path (host.run executes in the repo-root cwd, so
	// `git -C .worktrees/...` resolves); the absolute path is for file I/O here.
	// The path is now session-distinct (ticket_id + session_id — bug9glm2), so
	// read it from the story's world rather than hardcoding bf-<ticket>.
	workdirRel := bugfixWorkdirRel(t, orch, sid)
	workdirAbs := filepath.Join(repoRoot, workdirRel)

	// Inject lost work: an UNTRACKED file no room committed. This is the
	// incident — the working tree looks green to CI but the commit is partial.
	require.NoError(t, os.WriteFile(
		filepath.Join(workdirAbs, "uncommitted_lostwork_test.go"),
		[]byte("package app\n// the maker wrote this test but never committed it\n"),
		0o644,
	), "must be able to write the uncommitted file into the maker worktree")

	// accept into done: done.on_enter runs the REAL git status check (real
	// host.run, real git) and binds worktree_dirty=true.
	step("validating", "core__bf__accept", "core.bf.done")
	{
		journey, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		require.Equal(t, true, journey.World.Vars["core__bf__worktree_dirty"],
			"done.on_enter's real git status check must detect the uncommitted file")
		require.Equal(t, "?? uncommitted_lostwork_test.go", journey.World.Vars["core__bf__worktree_dirty_files"],
			"the porcelain listing of the lost work must be captured for the operator")
	}

	// accept at done: the guard fires → bf @exit:needs-human → dev-story
	// lands at the human review report (NOT core.pr.* which is the @exit:done handoff).
	{
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
		cancel()
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, app.StatePath("core.human_review_report"), out.NewState,
			"a dirty worktree must route bf to needs-human (→ core.human_review_report), "+
				"NOT ship via @exit:done (→ core.pr.*); got %q", out.NewState)

		journey, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		require.Equal(t, "needs-human", journey.World.Vars["core__status"],
			"the needs-human exit projection must set status=needs-human")
	}
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
			State: app.StatePath("core.landing"),
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

// TestDogfoodSmoke_ImplHandoffRefusesUncommittedWork is the impl analogue of
// TestDogfoodSmoke_DoneRefusesUncommittedWork: the END-TO-END proof of the
// lost-work guard ported into the implementation pipeline, against a REAL git
// worktree with the REAL host.run handler (no stub for the clean-tree check).
// It reproduces the exact incident shape: a maker room left work in the
// worktree that no room committed, so the committed tip is broken/partial while
// the working tree looks green.
//
// Flow: drive impl to handoff (worktree clean — the write_code + test stage_all
// commits swept it), THEN write an UNTRACKED file into the maker worktree
// (simulating a test/fix the maker wrote but never committed), THEN `open`.
// handoff.on_enter ran the REAL `git status --porcelain` and bound
// worktree_dirty=true; the open guard then routes impl → @exit:abandoned (which
// dev-story maps to core.landing with status=abandoned), NOT into the pr import
// (core.impl.pr.*) which would open + merge the partial tip.
//
// This closes the gap the flow fixture can't: it stubs the dirty result, so it
// proves the guard ROUTES correctly but not that host.run DETECTS dirt in the
// live folded-story path.
func TestDogfoodSmoke_ImplHandoffRefusesUncommittedWork(t *testing.T) {
	repoRoot, _ := setupDogfoodRepo(t)
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
			State: app.StatePath("core.landing"),
			Slots: seed,
		})
		require.NoError(t, err)
		cancel()
	}

	// Drive to handoff (worktree is clean here — the stage_all commits in
	// write_code + test swept the stubbed maker work).
	step("kickoff", "core__go_implementation", "core.impl.idle")
	step("idle → review_task", "core__impl__start", "core.impl.review_task_executing")
	step("review_task → wait", "core__impl__proceed", "core.impl.review_task_awaiting_reply")
	step("review_task → write", "core__impl__accept", "core.impl.write_code_executing")
	step("write → wait", "core__impl__proceed", "core.impl.write_code_awaiting_reply")
	step("write → test", "core__impl__accept", "core.impl.test_executing")
	step("test → wait", "core__impl__proceed", "core.impl.test_awaiting_reply")
	step("test → review", "core__impl__accept", "core.impl.review_executing")
	step("review → wait", "core__impl__proceed", "core.impl.review_awaiting_reply")

	// CRUX: the guard's on-enter check is guarded on world.workdir != ''. If
	// the dogfood path never projects workdir, the guard is silently dead.
	// Assert it IS set before relying on the injection below. world.workdir is
	// a repo-relative path (host.run executes in the repo-root cwd, so
	// `git -C .worktrees/...` resolves); the absolute path is for file I/O here.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	workdirRel, _ := journey.World.Vars["core__impl__workdir"].(string)
	require.NotEmpty(t, workdirRel,
		"world.workdir must be the maker worktree for the clean-tree guard to fire; "+
			"if empty the guard is dead in the dogfood path")
	workdirAbs := workdirRel
	if !filepath.IsAbs(workdirAbs) {
		workdirAbs = filepath.Join(repoRoot, workdirRel)
	}

	// Inject lost work: an UNTRACKED file no room committed. This is the
	// incident — the working tree looks green to CI but the commit is partial.
	require.NoError(t, os.WriteFile(
		filepath.Join(workdirAbs, "uncommitted_lostwork_test.go"),
		[]byte("package app\n// the maker wrote this test but never committed it\n"),
		0o644,
	), "must be able to write the uncommitted file into the maker worktree")

	// accept into handoff: handoff.on_enter runs the REAL git status check
	// (real host.run, real git) and binds worktree_dirty=true.
	step("review → handoff", "core__impl__accept", "core.impl.handoff")
	{
		journey, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		require.Equal(t, true, journey.World.Vars["core__impl__worktree_dirty"],
			"handoff.on_enter's real git status check must detect the uncommitted file")
		require.Equal(t, "?? uncommitted_lostwork_test.go", journey.World.Vars["core__impl__worktree_dirty_files"],
			"the porcelain listing of the lost work must be captured for the operator")
	}

	// proceed at handoff: the guard fires → impl @exit:abandoned → dev-story
	// lands at core.landing (NOT core.impl.pr.* which is the @exit:done handoff).
	// (proceed is the exported handoff arc; it carries the same dirty guard as
	// open.)
	{
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := orch.SubmitDirect(c, sid, "core__impl__proceed", nil)
		cancel()
		require.NoError(t, err)
		require.NotNil(t, out)
		require.Equal(t, app.StatePath("core.landing"), out.NewState,
			"a dirty worktree must route impl to abandoned (→ core.landing), "+
				"NOT ship via the pr import; got %q", out.NewState)

		journey, err := orch.LoadJourney(sid)
		require.NoError(t, err)
		require.Equal(t, "abandoned", journey.World.Vars["core__status"],
			"the abandoned exit projection must set status=abandoned")
	}
}

// TestDogfoodSmoke_ImplIdleProvisionsWorktree is the regression guard
// for the "impl pipeline runs against an empty workdir" bug: until this
// fix, stories/implementation/rooms/idle.yaml had NO on_enter — it
// assumed the parent projected workspace_id / workdir / feature_branch
// in via world_in:. The bugfix entry path (bf.idle) self-provisions a
// worktree, but the implementation entry paths (dev-story `drive` /
// `go_implementation` / design_done `implement`) never did. A live
// dogfood run drove a feature ticket into impl and the very first
// agent call dispatched `workdir:""` — the model got "confused".
//
// The pre-existing TestDogfoodSmoke_FullImplementationPipeline only
// asserts next_state advanced, so it passed straight through the bug
// (the rooms tolerate an empty workdir against the CI stub). This test
// asserts the SIDE EFFECT the skill demands: after entering impl.idle
// with no pre-seeded workdir, world.workdir is populated AND the
// worktree dir exists on disk.
func TestDogfoodSmoke_ImplIdleProvisionsWorktree(t *testing.T) {
	repoRoot, _ := setupDogfoodRepo(t)
	ticketID := "2026-05-17T111838Z-integration-smoke-bug-picked-up-by-dogfood"
	orch, _, sid, _ := newSmokeOrchestratorWithCIStub(t, repoRoot)

	ctx := context.Background()
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}

	// Teleport to main with a FEATURE ticket but deliberately NO
	// workspace_id / workdir / feature_branch — exactly the shape the
	// live `drive` path produces (pick_ticket sets ticket_type but never
	// a workdir). If impl.idle.on_enter fails to provision, workdir
	// stays "" and the assertions below fail.
	seed := seedDogfoodWorld(ticketID)
	seed["core__ticket_type"] = "feature"
	delete(seed, "core__workspace_id")
	delete(seed, "core__workdir")
	delete(seed, "core__feature_branch")
	{
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.landing"),
			Slots: seed,
		})
		require.NoError(t, err)
		cancel()
	}

	// drive a feature → impl.idle, whose on_enter must provision.
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := orch.SubmitDirect(c, sid, "core__drive", nil)
	require.NoError(t, err, "core__drive(feature) from main")
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("core.impl.idle"), out.NewState,
		"drive on a feature ticket must route to impl.idle; got %q", out.NewState)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	workdir, _ := journey.World.Vars["core__impl__workdir"].(string)
	require.NotEmpty(t, workdir,
		"impl.idle.on_enter must derive a workdir; got empty (pipeline would run against repo root)")
	require.Equal(t, true, journey.World.Vars["core__impl__impl_provision_attempted"],
		"impl_provision_attempted must be true after the provisioning chain ran")

	// The worktree must exist on disk — proves workspace.create ran with
	// the derived id, not just that a set: wrote a string.
	abs := workdir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(repoRoot, workdir)
	}
	_, statErr := os.Stat(abs)
	require.NoError(t, statErr,
		"impl.idle.on_enter must have created the worktree at %s", abs)
}

// TestDogfoodSmoke_ImplementingActuallyEditsFiles is the regression
// guard for the "implementing room is a no-op" bug: until 2026-05-19,
// implementing.on_enter ran workspace.sync + vcs.commit + a misleading
// `say "Fix applied"` and advanced, but nothing actually edited the
// code. Earlier happy-path smoke tests didn't notice because the
// agent was stubbed and they only checked `next_state`. This test
// stubs the implementing agent to ACTUALLY write a file in the
// worktree, then asserts post-pipeline:
//
//  1. The file shows up in `git diff HEAD~ HEAD` on the feature
//     branch — proves the commit step picked up the agent's edits.
//  2. `world.implement_artifact.files_changed` lists the file —
//     proves the artifact binding flowed.
//
// A future "implementing-is-a-no-op" regression would fail both
// assertions before testing room ran.
func TestDogfoodSmoke_ImplementingActuallyEditsFiles(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	appPath := filepath.Join(repoRoot, ".kitsoki", "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	agentCalls := 0
	reg := newDogfoodRegistry(&agentCalls)

	// Per-prompt-path agent dispatch: the implementing prompt's stub
	// edits a marker file in the worktree; every other prompt returns
	// the generic canned artifact.
	//
	// Registered for both ask_with_mcp (stories/implementation —
	// out of Phase 8 scope) and task (bugfix rooms post Phase 8).
	markerFile := "STUB_EDITED_BY_IMPLEMENTING_AGENT.txt"
	implementingAgentStub := func(ctx context.Context, args map[string]any) (host.Result, error) {
		// Read prompt from either flat ask_with_mcp shape (args["prompt"]) or
		// the new task shape (args["context"]["prompt"]) — see agent-split C2.
		promptArg, _ := args["prompt"].(string)
		if promptArg == "" {
			if ctxBlock, ok := args["context"].(map[string]any); ok {
				promptArg, _ = ctxBlock["prompt"].(string)
			}
		}
		wd, _ := args["working_dir"].(string)
		if wd == "" {
			if ctxBlock, ok := args["context"].(map[string]any); ok {
				wd, _ = ctxBlock["working_dir"].(string)
			}
		}
		// Any dogfood agent call that receives the bugfix worktree is allowed to
		// write the marker. The assertion below still proves the pipeline commits
		// real worktree edits before leaving implementation.
		if strings.Contains(promptArg, "implementing_executing") || wd != "" {
			markerPath := filepath.Join(repoRoot, wd, markerFile)
			if writeErr := os.WriteFile(markerPath, []byte("written by stub agent\n"), 0o644); writeErr != nil {
				return host.Result{Error: fmt.Sprintf("stub write: %v", writeErr)}, nil
			}
			artifact := map[string]any{
				"summary_title":    "Stub implementing — wrote marker file",
				"summary_markdown": "Wrote " + markerFile + " in the worktree to prove the agent ran and the commit step picked it up.\n\nFull text: written by stub agent.\n",
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
	reg.Replace("host.agent.ask_with_mcp", implementingAgentStub)
	// agent-split Phase 8: bugfix uses task/ask/decide instead of ask_with_mcp.
	reg.Replace("host.agent.task", implementingAgentStub)
	reg.Replace("host.agent.ask", implementingAgentStub)
	reg.Replace("host.agent.decide", implementingAgentStub)
	reg.Replace("host.local", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"ok": true, "passed": 1, "failed": 0, "log": "PASS (stub)",
		}}, nil
	})

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
			State: app.StatePath("core.landing"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, tErr)
		cancel()
	}
	step("kickoff", "core__go_bugfix", "core.bf.reproducing")
	step("reproducing", "core__bf__accept", "core.bf.proposing")
	step("proposing", "core__bf__accept", "core.bf.implementing")

	// 1. The marker file must be committed on the feature branch.
	workdir := bugfixWorkdirAbs(t, orch, sid, repoRoot)
	showCmd := exec.Command("git", "show", "--name-only", "--format=%H", "HEAD")
	showCmd.Dir = workdir
	showOut, showErr := showCmd.CombinedOutput()
	require.NoError(t, showErr, "git show: %s", string(showOut))
	require.Contains(t, string(showOut), markerFile,
		"HEAD commit must include the marker file written by the agent stub; got: %s", string(showOut))

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
	appPath := filepath.Join(repoRoot, ".kitsoki", "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	agentCalls := 0
	// Full builtin set + agent stubs (see newDogfoodRegistry), then Replace
	// host.local with a CI stub so go test/build don't run for real against the
	// temp repo. host.run stays the REAL handler (the testing regression gate +
	// done lost-work clean-tree check exercise actual git).
	reg := newDogfoodRegistry(&agentCalls)
	reg.Replace("host.local", func(ctx context.Context, args map[string]any) (host.Result, error) {
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

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, s, sid, &agentCalls
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
// Each test below stubs the agent per-prompt so a specific gate room
// produces a non-pass artifact while every other room produces the
// canonical pass artifact. The smoke test then asserts:
//   - the room routes to implementing (not advancing forward),
//   - no transport.post (iface.transport.post → host.append_to_file)
//     fired on the failure arm,
//   - the corresponding cycle counter was bumped,
//   - every agent invocation received working_dir == world.workdir,
//   - and iface.ci.build was not handed a spurious target arg.
// =============================================================================

// promptArtifact maps a prompt path fragment (e.g. "validating_executing")
// to the artifact payload the stub returns. The fallback is the canonical
// `dogfoodArtifact` so unconfigured prompts get the happy-path shape.
type promptArtifact map[string]map[string]any

// agentRouter returns a host.agent.ask_with_mcp stub that:
//   - picks the artifact for the matching prompt fragment, falling back
//     to dogfoodArtifact for prompts not in the map,
//   - records every (prompt, working_dir) pair into *seen for the
//     working-dir assertion below.
type agentSeen struct {
	prompt     string
	workingDir string
}

func agentRouter(artifacts promptArtifact, seen *[]agentSeen) host.Handler {
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
		*seen = append(*seen, agentSeen{prompt: promptArg, workingDir: workingDir})

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
// flexible agent + ci stubs. Returns the orchestrator, store, session
// id, and the (agentSeen, hostLocalSeen) capture slices.
func newSmokeOrchestratorWithRouters(t *testing.T, repoRoot string, artifacts promptArtifact) (*orchestrator.Orchestrator, store.Store, app.SessionID, *[]agentSeen, *[]hostLocalSeen) {
	t.Helper()
	appPath := filepath.Join(repoRoot, ".kitsoki", "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	agentSeenSlice := []agentSeen{}
	hostLocalSeenSlice := []hostLocalSeen{}

	reg := host.NewRegistry()
	agentRouterFn := agentRouter(artifacts, &agentSeenSlice)
	reg.Register("host.agent.ask_with_mcp", agentRouterFn)
	// agent-split Phase 8 verbs used by bugfix rooms. All route through
	// the same prompt-dispatch logic so per-phase artifact overrides apply.
	reg.Register("host.agent.task", agentRouterFn)
	reg.Register("host.agent.ask", agentRouterFn)
	reg.Register("host.agent.decide", agentRouterFn)
	reg.Register("host.local", hostLocalCapture(&hostLocalSeenSlice))
	reg.Register("host.local_files.ticket", host.LocalFilesTicketHandler)
	reg.Register("host.git", host.GitVCSHandler)
	reg.Register("host.git_worktree", host.GitWorktreeHandler)
	reg.Register("host.append_to_file", host.AppendFileTransportHandler)
	reg.Register("host.inbox.add", host.InboxAddHandler)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, s, sid, &agentSeenSlice, &hostLocalSeenSlice
}

// driveBugfixPipelineTo runs the on-enter chain plus a teleport to
// core.landing, then submits accept intents in sequence to walk the bugfix
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
			State: app.StatePath("core.landing"),
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
// agent returns outcome=fail_short, `core__bf__accept` MUST route to
// implementing (not advance to done), MUST NOT fire transport.post,
// and MUST bump implementing_cycle. See rooms/validating.yaml header
// for the dogfood-trace context that motivated this fix.
func TestDogfoodSmoke_ValidatingFailShortRoutesToImplementing(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	// Override the validating agent ONLY — every other room sees the
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
// rooms/testing.yaml's status-aware accept arc: when the testing agent
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

// TestDogfoodSmoke_AgentAlwaysReceivesWorkingDir locks in the second
// half of the dogfood fix: every bugfix-room agent invocation must pass
// `working_dir` = the bugfix worktree. Without it the agent's MCP
// filesystem tools default to kitsoki's cwd (the main worktree) and
// any grep/read the agent runs lands on the wrong tree.
//
// Drives the full pass path (so every room's agent fires at least
// once), then asserts each phase-executing prompt was invoked with
// working_dir set and pointing to .worktrees/bf-<ticket>.
func TestDogfoodSmoke_AgentAlwaysReceivesWorkingDir(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)
	orch, _, sid, agentCalls, _ := newSmokeOrchestratorWithRouters(t, repoRoot, promptArtifact{})

	driveBugfixPipelineTo(t, orch, sid, ticketID, "core.bf.validating")
	// One more accept to land at done — exercises validating's agent on
	// the pass arm and done's agent on entry.
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	out, err := orch.SubmitDirect(c, sid, "core__bf__accept", nil)
	cancel()
	require.NoError(t, err)
	require.Equal(t, app.StatePath("core.bf.done"), out.NewState)

	// The agent working_dir is the session-distinct workdir bf.idle.on_enter
	// derived (ticket_id + session_id — bug9glm2); read it from world rather
	// than hardcoding .worktrees/bf-<ticket>.
	expected := bugfixWorkdirRel(t, orch, sid)

	// For each phase-executing prompt, find the matching agent call and
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
		for _, call := range *agentCalls {
			if !strings.Contains(call.prompt, phase) {
				continue
			}
			found = true
			require.NotEmpty(t, call.workingDir,
				"%s agent invocation must set working_dir; got empty (prompt=%q)",
				phase, call.prompt)
			require.True(t, strings.HasSuffix(call.workingDir, expected) || call.workingDir == expected,
				"%s agent working_dir must point at the bugfix worktree (want suffix %q); got %q",
				phase, expected, call.workingDir)
			break
		}
		require.True(t, found, "expected at least one agent call for prompt %s", phase)
	}
}

// TestDogfoodSmoke_ValidatingBuildSkipsSpuriousTargetArg locks in the
// third dogfood fix: rooms/validating.yaml no longer passes
// `target: "default"` to iface.ci.build. Pre-fix, ciBuild appended
// the literal string "default" to `go build ./...`, synthesising a
// `package default is not in std` error on every full-pipeline run.
// The agent then mistook that synthesised error for evidence the fix
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
