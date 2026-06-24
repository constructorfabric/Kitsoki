package orchestrator_test

// once_emit_staged_test.go — reproduction test for:
//   "once: on_enter host.run drops its bind + post-bind emit_intent on room re-entry"
//
// Root cause: DispatchPostBindEmits calls dispatchEmittedIntents with
// staged=o.execMode.staged(). In ExecStaged mode (kitsoki web / TUI), when the
// starting state (idle) is a decision gate, isStagedGate(idle, staged=true) fires
// at the TOP of dispatchEmittedIntents and immediately returns bailed_to_human=true,
// dropping every emit without routing.
//
// idle IS a decision gate because it has operator-accessible intents (look, quit)
// that are NOT emit targets. The templated emit_intent "{{ world.route }}" matches
// no static intent name, so isDecisionGate classifies every forward intent as
// "operator-only" → isDecisionGate(idle)=true.
//
// Concretely: when detect_context (once: true) runs and binds world.route="on_branch",
// the subsequent settlePostBindEmits call:
//
//   DispatchPostBindEmits(idle, world{route:"on_branch"}, staged=true)
//     → dispatchEmittedIntents("on_branch", staged=true)
//     → isStagedGate(idle, true)=true
//     → return (idle, world, nil, "", [GateDecided{bailed_to_human=true}], nil)
//
// The emit "on_branch" is silently dropped. The session remains at idle instead of
// routing to branch_ops. Because the world is saved with route="on_branch", every
// subsequent look→target:. self-loop turn hits the same gate stop again — the
// session is permanently stuck at idle.
//
// Affected code paths:
//   internal/machine/machine.go      dispatchEmittedIntents: isStagedGate check at line ~1029
//   internal/orchestrator/orchestrator.go  settlePostBindEmits: passes staged=o.execMode.staged()
//
// This test is expected to FAIL against the unfixed codebase.
// See: issues/bugs/2026-06-10T140218Z-once-onenter-hostrun-bind-and-emit-dropped-on-reentry.md

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOnce_EmitIntent_StagedMode_DropsBoot is the primary reproduction test.
//
// In ExecStaged mode (kitsoki web / TUI), idle's post-bind emit_intent that
// should route the session to branch_ops after detect_context binds world.route
// is silently dropped by the isStagedGate check in dispatchEmittedIntents.
// The session remains stuck at idle with route="on_branch" in the world.
//
// Expected (correct): session at branch_ops after RunInitialOnEnter.
// Actual   (buggy):   session stays at idle; route is bound but emit is dropped.
func TestOnce_EmitIntent_StagedMode_DropsBoot(t *testing.T) {
	def, err := app.Load("testdata/once_emit_staged/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var detectCalls atomic.Int64
	reg := host.NewRegistry()
	reg.Register("host.detect", func(_ context.Context, _ map[string]any) (host.Result, error) {
		detectCalls.Add(1)
		return host.Result{Data: map[string]any{
			"route":    "on_branch",
			"detected": "feat/repro",
		}}, nil
	})

	// ExecStaged is the mode kitsoki web / TUI uses.  The bug only manifests
	// here because isStagedGate(idle, staged=true) = true for idle, which has
	// the user-accessible intents "look" and "quit" that are NOT emit targets.
	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithExecutionMode(orchestrator.ExecStaged),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// world.detected defaults to "" (NOT set), so allBindTargetsSet returns false
	// and the once: guard does NOT skip detect_context on first entry.
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	assert.Equal(t, int64(1), detectCalls.Load(),
		"detect_context must run once — once: must not skip on initial entry when detected='' is unset")

	j, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	// Post-bind world must reflect detect_context's bind results regardless of
	// whether the routing succeeded — the EffectApplied events must persist.
	assert.Equal(t, "on_branch", j.World.Vars["route"],
		"detect_context must have bound route=on_branch")
	assert.Equal(t, "feat/repro", j.World.Vars["detected"],
		"detect_context must have bound detected=feat/repro")

	// The post-bind emit_intent must have been followed: session must be at
	// branch_ops, not stuck at idle.
	//
	// BUG: in ExecStaged mode, DispatchPostBindEmits calls dispatchEmittedIntents
	// with staged=true. At the starting state (idle), isStagedGate(idle, true)
	// returns true because idle has operator-accessible forward intents (look, quit).
	// The emit "on_branch" is dropped before it can route to branch_ops.
	require.Equal(t, "branch_ops", string(j.State),
		"post-bind emit_intent must route to branch_ops even in ExecStaged mode: "+
			"isStagedGate at the starting state (idle) must not block post-bind routing emits")
}

// TestOnce_EmitIntent_OneShotMode_RoutesCorrectly confirms the control case:
// in ExecOneShot mode the same scenario succeeds. This verifies the test story
// and detect stub are correct before the staged-mode assertion above fails.
func TestOnce_EmitIntent_OneShotMode_RoutesCorrectly(t *testing.T) {
	def, err := app.Load("testdata/once_emit_staged/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var detectCalls atomic.Int64
	reg := host.NewRegistry()
	reg.Register("host.detect", func(_ context.Context, _ map[string]any) (host.Result, error) {
		detectCalls.Add(1)
		return host.Result{Data: map[string]any{
			"route":    "on_branch",
			"detected": "feat/repro",
		}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithExecutionMode(orchestrator.ExecOneShot),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	assert.Equal(t, int64(1), detectCalls.Load(),
		"detect_context runs once in one-shot mode")

	j, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	require.Equal(t, "branch_ops", string(j.State),
		"one-shot boot must follow emit_intent to branch_ops (control case)")
	assert.Equal(t, "on_branch", j.World.Vars["route"])
	assert.Equal(t, "feat/repro", j.World.Vars["detected"])
}

// TestOnce_EmitIntent_StagedMode_ReentryAlsoDrops demonstrates that AFTER the
// session is stuck at idle (from the boot bug above), subsequent look→target:.
// self-loop turns also fail to route to branch_ops via settlePostBindEmits.
// The session is permanently stuck at idle in ExecStaged mode.
func TestOnce_EmitIntent_StagedMode_ReentryAlsoDrops(t *testing.T) {
	def, err := app.Load("testdata/once_emit_staged/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var detectCalls atomic.Int64
	reg := host.NewRegistry()
	reg.Register("host.detect", func(_ context.Context, _ map[string]any) (host.Result, error) {
		detectCalls.Add(1)
		return host.Result{Data: map[string]any{
			"route":    "on_branch",
			"detected": "feat/repro",
		}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithExecutionMode(orchestrator.ExecStaged),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Boot: session ends up at idle (bug), but world.route="on_branch" is saved.
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	j0, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	// Pre-condition for this test: boot left session at idle (the first bug).
	if string(j0.State) == "branch_ops" {
		t.Skip("boot bug already fixed — re-entry test only makes sense when boot is stuck")
	}
	require.Equal(t, "idle", string(j0.State), "boot must be stuck at idle for re-entry test")

	// User fires "look" (the stay-here idiom). idle has look→target:., which
	// skips on_enter. settlePostBindEmits fires DispatchPostBindEmits with the
	// already-bound world (route="on_branch"), but the same staged-gate stop
	// drops the emit again.
	out, err := orch.SubmitDirect(ctx, sid, "look", nil)
	require.NoError(t, err)

	// Even with route="on_branch" in world, the look turn must NOT route to idle —
	// the once: guard now fires (detected="feat/repro" is set), so the invoke is
	// skipped; but the emit_intent must STILL route to branch_ops.
	//
	// BUG: the same isStagedGate drop that affected boot also affects the
	// post-bind settle on this turn, so the session stays at idle again.
	require.Equal(t, "branch_ops", out.NewState,
		"after look self-loop, settlePostBindEmits must route to branch_ops via cached route")
}
