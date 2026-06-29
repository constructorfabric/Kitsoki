// Package bugfixsynth holds a deterministic, no-LLM integration test that drives
// the stories/bugfix SYNTHESISED-GATE path over a REAL git worktree.
//
// Why this lives in its own package, away from the flow fixtures: the existing
// no-LLM flows (stories/bugfix/flows/bugfix_synthesizes_gate_and_ships.yaml and
// bugfix_synthesis_noop_commit_needs_human.yaml) STUB host.run / host.git, which
// is exactly why they cannot catch the deterministic git-ordering/staging bug
// that four live gpt-5.5 dogfoods caught and that commit ba29f824 fixed. The
// flow harness's host_handlers: stubs return canned data — they cannot (a) run
// the synthesis commit's poll + `git add -A` against a real index, nor (b) write
// the reproducer's RED test file to the worktree at a DELAYED, MIS-PATHED
// location (the two real failure modes). And a single host.run stub cannot be
// "real for call X, canned for call Y" — RegisterHostStubs falls through to its
// canned Data for any unmatched call rather than delegating to the real builtin.
//
// So this test builds the orchestrator directly (seam #2 in the task brief) with
// a precisely-controlled host.Registry:
//
//   - REAL host.git_worktree / host.git (RegisterBuiltins) so iface.vcs.commit
//     and the workspace ops hit a real temp repo;
//   - a custom host.run dispatcher that runs the REAL bash builtin for the two
//     synthesis-critical call sites (commit_repro_test, regression_gate_exec)
//     and returns canned envelopes for the ship-it tail's host.run sites (not
//     reached here — the test stops at testing, where the regression is proven);
//   - a custom host.agent.task that, on the reproducer call, writes the
//     reproducer's RED test file to the worktree SYNCHRONOUSLY before returning
//     (modelling the real host.agent.task's durability barrier,
//     waitForAgentWorktreeWrites) and at a path that does NOT match the reported
//     repro_test_paths (the mis-path the path-agnostic `git add -A` must capture).
//
// The test then proves the synthesis is robust: a discrete `test(repro): …`
// commit lands BEFORE the fix commit (so HEAD~1 is the test-without-fix
// snapshot), the pre-fix gate is RED there, and repro_committed latched.
//
// On the Starlark question (the user asked whether a starlark script could play
// a role): it cannot author the reproducer's file. host.starlark.run exposes a
// READ-ONLY ctx.fs (read/exists/glob — see internal/host/starlark) and "runs
// without touching the filesystem", so it can inspect a worktree but never WRITE
// the RED test. The file-writing reproducer must therefore be a host.run bash
// step (production) or a Go fake-agent (this test). Starlark's only possible role
// is a deterministic decision helper, which is not what this path needs.
package bugfixsynth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// noopHarness satisfies harness.Harness for the orchestrator; the test drives
// intents directly via RunIntent, never the LLM harness.
type noopHarness struct{}

func (noopHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("noopHarness: RunTurn called (state=%q)", in.StatePath)
}
func (noopHarness) Close() error { return nil }

// gitInit makes dir a git repo with one committed baseline file and a configured
// identity, returning nothing (failures are fatal). The baseline commit is the
// pre-everything root so HEAD~1 has a parent after the test commits land.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
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
	run("init", "-q", "-b", "main")
	// Set the identity in the repo's LOCAL config, not just via env on the
	// commands run() issues here. The synthesis commit under test is made by the
	// production host.git.commit handler (real `git commit` in this workdir),
	// which does NOT inherit run()'s GIT_AUTHOR_* env — so on a host with no
	// global git identity (CI runners) it would fail with "empty ident name".
	// Local config is picked up by every `git commit` in this repo, whoever runs it.
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "baseline.txt"), []byte("base\n"), 0o644))
	// The owner sentinel the synthesis commit deliberately EXCLUDES (`git reset
	// -q -- .kitsoki-owner`). Committing it in the baseline means a later stray
	// owner write would be a no-op for the index, mirroring production.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".kitsoki-owner"), []byte("sess\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "baseline")
}

// repoBugfixAppPath returns the absolute path to the committed bugfix app.yaml.
func repoBugfixAppPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../stories/bugfix/app.yaml")
	require.NoError(t, err)
	return abs
}

// TestBugfixSynthesis_RealGit_DiscretePreFixCommit_HEADminus1_RED is the
// regression test: it FAILS if commit ba29f824 (poll + `git add -A`) is reverted
// to a plain path-specific iface.vcs.commit. See the package doc for the seam.
func TestBugfixSynthesis_RealGit_DiscretePreFixCommit_HEADminus1_RED(t *testing.T) {
	if testing.Short() {
		t.Skip("spins a real temp git repo; skipped under -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH (testing room's gate uses jq)")
	}

	ctx := context.Background()

	// ── Real temp worktree. The story reads world.workdir for both the synthesis
	// commit and the HEAD~1 gate; we point it at this real repo. ─────────────
	workdir := t.TempDir()
	gitInit(t, workdir)

	// The reproducer's RED test as it will actually land on disk — and the
	// command that exercises it. The gate (`go test`-style) is simulated by a
	// tiny bash probe so the test needs no Go toolchain spin-up: the RED test
	// file, when present in a tree, makes the gate FAIL (exit 1); absent, PASS.
	//
	// FAILURE MODE (2): the reproducer REPORTS this path…
	reportedPath := "internal/host/reported_repro_test.go"
	// …but actually WRITES the file HERE (a different real path) — the mis-path.
	realPath := "internal/host/actual_red_test.go"
	gateCmd := fmt.Sprintf(
		`if [ -f %s ]; then echo "FAIL: reproduces"; exit 1; else echo "PASS: gone"; exit 0; fi`,
		realPath)

	// ── Custom host registry. ────────────────────────────────────────────────
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg) // REAL host.git_worktree, host.git, host.run, …
	realRun, _ := reg.Get("host.run")

	// host.run: real bash for the two synthesis-critical sites, canned for any
	// ship-it tail site (unreached here). Dispatch on args["call"] (the room's
	// invoke id, threaded by machine.go).
	reg.Replace("host.run", func(c context.Context, args map[string]any) (host.Result, error) {
		switch call, _ := args["call"].(string); call {
		case "commit_repro_test", "regression_gate_exec":
			return realRun(c, args) // run the REAL poll/add/commit and the REAL gate
		default:
			// Tail integrate/verify/cleanup — not reached (we stop at testing),
			// but answer benignly if the cascade ever touches them.
			return host.Result{Data: map[string]any{"ok": true, "exit_code": 0}}, nil
		}
	})

	// ── FAILURE MODE: the reproducer REPORTS one path but WRITES another (the
	// mis-path). The real host.agent.task carries a durability barrier
	// (waitForAgentWorktreeWrites) that guarantees the maker backend's writes are
	// on disk before the call returns, so the story dropped its old sync-back poll
	// loop. The stub below stands in for host.agent.task and so must honour that
	// same guarantee: it writes the (mis-pathed) RED test SYNCHRONOUSLY on the
	// FIRST call (the reproducer). The synthesis commit then relies on a
	// path-agnostic `git add -A` to capture it whatever path it landed at. ──────
	writerStarted := make(chan struct{}, 1)
	startWriterOnce := false
	reg.Replace("host.agent.task", func(c context.Context, args map[string]any) (host.Result, error) {
		if !startWriterOnce {
			// FIRST host.agent.task = the reproducer (reproducing room). Write the
			// (mis-pathed) RED test SYNCHRONOUSLY before the call returns — this is
			// exactly the contract the real host.agent.task now guarantees via its
			// durability barrier (internal/host/agent_task.go waitForAgentWorktreeWrites,
			// which blocks the call until the maker backend's writes are durable on
			// disk before reproducing/on_enter returns). The story dropped its old
			// poll loop in favour of that barrier, so the test stub — standing in for
			// the real host.agent.task — must honour the same guarantee or the
			// downstream commit_repro_test step (which no longer polls) would `git
			// add -A` an empty index. The SURVIVING failure mode the test still
			// exercises is the MIS-PATH: the reproducer reports reportedPath but
			// writes realPath, so only a path-agnostic `git add -A` captures it.
			startWriterOnce = true
			abs := filepath.Join(workdir, realPath)
			_ = os.MkdirAll(filepath.Dir(abs), 0o755)
			_ = os.WriteFile(abs, []byte("// RED reproducer (mis-pathed)\n"), 0o644)
			select {
			case writerStarted <- struct{}{}:
			default:
			}
		} else {
			// LATER host.agent.task = the implementer (implementing room) applying
			// the fix. It must actually mutate the worktree so iface.vcs.commit of
			// files_changed lands a DISTINCT fix commit on top of the pre-fix test
			// tip — and that fix must make the RED test GREEN (delete the repro
			// file so the gate passes on HEAD, fails on HEAD~1). This is what makes
			// HEAD~1 the test-WITHOUT-fix snapshot.
			_ = os.WriteFile(filepath.Join(workdir, "internal/host/fix.go"),
				[]byte("package host // the fix\n"), 0o644)
			_ = os.Remove(filepath.Join(workdir, realPath)) // fix makes the gate GREEN
		}
		// The reproducer half of the shared artifact carries the NEW repro_command
		// + repro_test_paths the synthesis reads; the implementer/test_author
		// halves carry files_changed/status. One artifact, every room reads its
		// own fields — mirrors the flow fixture. repro_test_paths is the REPORTED
		// (mis-matched) path on purpose.
		return host.Result{Data: map[string]any{
			"ok": true,
			"submitted": map[string]any{
				"summary_title":    "Reproducer authored; fix applied",
				"summary_markdown": "Authored a RED reproducer; committed as the discrete pre-fix test; fix makes it GREEN.",
				"status":           "passed",
				"outcome":          "pass",
				"bug_verified":     true,
				"applied":          true,
				"repro_command":    gateCmd,
				"repro_test_paths": []any{reportedPath}, // MIS-MATCHED on purpose
				"files_changed":    []any{"internal/host/fix.go"},
			},
		}}, nil
	})

	// host.agent.ask / host.agent.decide: canned (proposing + any judge gate).
	reg.Replace("host.agent.ask", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true, "submitted": map[string]any{
			"summary_title": "Fix proposal", "summary_markdown": "Create-on-miss.",
			"fix_description": "x", "root_cause": "y", "affected_files": []any{"internal/host/fix.go"},
		}}}, nil
	})
	reg.Replace("host.agent.decide", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true, "submitted": map[string]any{
			"summary_title": "ok", "summary_markdown": "ok", "outcome": "pass", "status": "passed",
			"verdict": "accept", "intent": "accept", "confidence": 0.9,
		}}}, nil
	})
	// iface.workspace.* / iface.notify default to host.git_worktree /
	// host.append_to_file — workspace ops are guarded on workspace_id (empty
	// here, so skipped), notify writes nowhere harmful. host.local (ci) canned.
	reg.Replace("host.local", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true, "log": "PASS", "passed": 1, "failed": 0}}, nil
	})
	reg.Replace("host.append_to_file", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})
	reg.Replace("host.inbox.add", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})

	// ── Build the orchestrator. Real clock (the poll loop sleeps real time). ──
	def, err := app.Load(repoBugfixAppPath(t))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	st, err := store.OpenMemory()
	require.NoError(t, err)
	defer st.Close()
	js, err := jobs.NewJobStore(st.DB())
	require.NoError(t, err)
	clk := clock.Real()
	sched := jobs.NewScheduler(js, jobs.WithClock(clk))

	orch := orchestrator.New(def, m, st, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(js),
		orchestrator.WithClock(clk),
	)
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// ── Seed initial state (idle) + world. Mirrors the synthesis flow fixture,
	// minus gate_command (the ticket carried no repro_command — it's SYNTHESISED)
	// and minus workspace_id (so workspace.create/sync are guard-skipped; the
	// real worktree already exists at workdir). ──────────────────────────────
	seedInitial(t, st, sid, map[string]any{
		"ticket_id":              "TKT-NOREPRO-1",
		"ticket_title":           "Bare bug report; no repro_command",
		"thread":                 "TKT-NOREPRO-1",
		"workdir":                workdir,
		"feature_branch":         "norepro-fix",
		"base_branch":            "main",
		"bugfix_mode":            "full",
		"judge_mode":             "human",
		"bf_autostart_attempted": true, // park idle; we drive `start`
		"bugfix_exit":            "direct-ship",
	})

	// ── Drive: idle → reproducing → proposing → implementing → testing. ───────
	step := func(intentName string, want app.StatePath) *orchestrator.TurnOutcome {
		out, err := orch.RunIntent(ctx, sid, intentName, nil)
		require.NoErrorf(t, err, "intent %q", intentName)
		require.Equalf(t, want, out.NewState, "after %q: got world=%v", intentName, orch.CurrentWorld(sid))
		return out
	}
	step("start", "reproducing")
	step("accept", "proposing")

	// implementing/on_enter: latch the synthesised gate, then commit the
	// reproducer's RED test as the DISCRETE pre-fix tip (git add -A).
	step("accept", "implementing")
	w := orch.CurrentWorld(sid)
	require.True(t, w.Get("repro_committed") == true, "synthesised gate must latch (repro_committed)")
	require.Equal(t, gateCmd, w.Get("gate_command"), "gate_command synthesised from the artifact's repro_command")

	// The reproducer's RED-test writer must have fired (the durability barrier the
	// stub models guarantees it landed before reproducing/on_enter returned).
	select {
	case <-writerStarted:
	default:
		t.Fatal("reproducer file writer never ran")
	}

	// ── ASSERT the real-git invariants the bug broke. ────────────────────────
	// After implementing/on_enter, the history is:
	//   baseline → test(repro): RED reproducer (HEAD~1) → fix commit (HEAD).
	// The DISCRETE pre-fix test commit landed BEFORE the fix, so HEAD~1 is the
	// test-without-fix snapshot the testing room's gate keys on.
	//
	// (a) The discrete test commit is the PARENT of the fix tip.
	preFixSubject := gitOut(t, workdir, "log", "-1", "--format=%s", "HEAD~1")
	require.Contains(t, preFixSubject, "test(repro):",
		"the pre-fix commit (HEAD~1) must be the discrete reproducer-test — a path-specific add would have no-op'd here, leaving baseline at HEAD~1")
	// (b) It actually CONTAINS the mis-pathed RED test — `git add -A` captured it
	//     whatever path it landed at (the whole point of the ba29f824 fix). A
	//     path-specific `git add internal/host/reported_repro_test.go` would have
	//     staged nothing, since the file really landed at realPath.
	preFixFiles := gitOut(t, workdir, "show", "--name-only", "--format=", "HEAD~1")
	require.Contains(t, preFixFiles, realPath,
		"the discrete commit must include the RED test at its REAL (mis-reported) path")
	require.NotContains(t, preFixFiles, ".kitsoki-owner",
		"the owner sentinel must be excluded from the synthesis commit")
	// (c) HEAD is a distinct fix commit on top (not the test commit).
	require.NotContains(t, gitOut(t, workdir, "log", "-1", "--format=%s"), "test(repro):",
		"the fix must be a SEPARATE commit on top of the discrete test commit")

	// implementing → testing: the HEAD~1 RED gate runs for REAL on the pre-fix
	// snapshot.
	step("accept", "testing")
	w = orch.CurrentWorld(sid)
	require.Equal(t, true, w.Get("regression_gate_checked"), "the HEAD~1 gate must have run")
	require.Equal(t, true, w.Get("regression_red_pre_fix"),
		"HEAD~1 (test, no fix) must be RED — proves a real regression test, not a characterisation test")
}

// gitOut runs git in dir and returns trimmed combined output (fatal on error).
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
	return string(out)
}

// seedInitial persists synthetic turn-0 events that land the session in idle
// with the given world — the same bootstrap testrunner.seedInitialState uses.
func seedInitial(t *testing.T, st store.Store, sid app.SessionID, vars map[string]any) {
	t.Helper()
	events := []store.Event{{
		Kind:      store.TransitionApplied,
		Turn:      0,
		StatePath: "idle",
		Payload:   mustJSON(t, map[string]any{"from": "", "to": "idle", "intent": "__seed__"}),
	}}
	for k, v := range vars {
		events = append(events, store.Event{
			Kind:      store.EffectApplied,
			Turn:      0,
			StatePath: "idle",
			Payload:   mustJSON(t, map[string]any{"set": map[string]any{k: v}}),
		})
	}
	require.NoError(t, store.NewStoreSinkAdapter(st, sid).AppendBatch(events))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
