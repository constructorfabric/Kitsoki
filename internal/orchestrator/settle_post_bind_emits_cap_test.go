package orchestrator_test

// Regression tests for the P1-A / P1-B fixes surfaced by the
// dev-story-bugfix-unify Opus code review:
//
// P1-A — settlePostBindEmits's outer loop can cycle indefinitely when
// a host call binds a world key AFTER machine.Turn returns and that
// bind gates a deferred emit_intent that lands on a state whose
// on_enter performs the same pattern again.  Each iteration is a
// fresh DispatchPostBindEmits call against a freshly-bound world,
// which resets the machine-side EmitIntentMaxDepth counter.  The
// orchestrator-side OrchestratorPostBindMaxDepth cap closes this
// gap.
//
// P1-B — when DispatchPostBindEmits returns an error (or the depth
// cap fires), the orchestrator must surface it loudly: emit a
// HarnessError event into the journal AND populate
// TurnOutcome.HarnessError so callers see it.  Previously the
// error was logged to trace and the turn returned success.

import (
	"context"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestSettlePostBindEmits_DepthCap exercises the orchestrator-side
// recursion cap by building a two-state cycle where each state's
// on_enter is (host.bump → emit_intent gated on world.bumped).  The
// machine defers each emit_intent (the When reads a key the host
// call hasn't bound yet at machine time) so the orchestrator's
// post-bind settle pass re-enters on each iteration.
//
// Expected: the cap fires (depth=5), a HarnessError event lands in
// the event log, TurnOutcome.HarnessError carries the cap message,
// and the session settles at whatever leaf the last successful
// iteration produced — NOT in an infinite loop.
func TestSettlePostBindEmits_DepthCap(t *testing.T) {
	// Pattern: each state's on_enter is (host.bump → emit_intent gated
	// on a freshly-bound nested key).  host.bump_a writes
	// world.payload={step:"a"}; host.bump_b writes {step:"b"}.  Each
	// state's emit_intent when reads world.payload.step against its
	// own round — so the machine-time eval errors against the initial
	// nil payload and the emit is DEFERRED, and the prior iteration's
	// bind doesn't satisfy the current state's when (because the
	// step value differs).  That keeps the orchestrator-side settle
	// loop iterating without the machine-side EmitIntentMaxDepth
	// firing inside any single dispatchEmittedIntents chain.
	const yamlSrc = `
app:
  id: post-bind-cap
  version: 0.1.0
hosts:
  - host.bump_a
  - host.bump_b
intents:
  start: {}
  loop_back_a: {}
  loop_back_b: {}
root: ready
states:
  ready:
    on:
      start:
        - target: loop_a
  loop_a:
    on_enter:
      - invoke: host.bump_a
        with: {}
        bind:
          payload: "value"
      - emit_intent: loop_back_b
        when: "world.payload.step == 'a'"
    on:
      loop_back_b:
        - target: loop_b
  loop_b:
    on_enter:
      - invoke: host.bump_b
        with: {}
        bind:
          payload: "value"
      - emit_intent: loop_back_a
        when: "world.payload.step == 'b'"
    on:
      loop_back_a:
        - target: loop_a
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// host.bump.mark always succeeds and binds {"ok": true} so the
	// post-bind emit_intent's When passes.  Use SubmitDirect; no
	// LLM needed.
	reg := host.NewRegistry()
	reg.Register("host.bump_a", func(ctx context.Context, args map[string]any) (host.Result, error) {
		// Binds payload = {step: "a"} so loop_a's emit_intent when
		// (`world.payload.step == 'a'`) evaluates true post-bind.
		return host.Result{Data: map[string]any{"value": map[string]any{"step": "a"}}}, nil
	})
	reg.Register("host.bump_b", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"value": map[string]any{"step": "b"}}}, nil
	})

	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err, "depth-cap firing must surface as TurnOutcome.HarnessError, not a Go error")

	require.NotEmpty(t, out.HarnessError,
		"TurnOutcome.HarnessError must be set when settlePostBindEmits exceeds its cap")
	require.True(t,
		strings.Contains(out.HarnessError, "recursion depth") &&
			strings.Contains(out.HarnessError, "exceeded cap"),
		"HarnessError message should describe the depth-cap firing, got %q", out.HarnessError)

	// Verify the synthetic event lands in the persisted history.
	var hits int
	for _, ev := range out.Events {
		if ev.Kind == store.HarnessError {
			hits++
			require.True(t,
				strings.Contains(string(ev.Payload), "settle_post_bind_emits"),
				"HarnessError payload should carry phase=settle_post_bind_emits")
		}
	}
	require.GreaterOrEqual(t, hits, 1,
		"at least one HarnessError event must be appended to the turn's events")

	// Sanity: the session has not vanished — replay must land it
	// somewhere known (loop_a or loop_b), not a half-bound limbo.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.True(t,
		journey.State == app.StatePath("loop_a") || journey.State == app.StatePath("loop_b"),
		"session must settle at a known resting place; got %q", journey.State)
}

// TestSettlePostBindEmits_SurfaceDispatchError exercises the P1-B
// surfacing path: when DispatchPostBindEmits returns an error from
// a guard that fails to evaluate against the post-bind world (e.g.
// `when: world.missing.key.deeper`), the orchestrator must NOT
// silently return success — it must emit a HarnessError event and
// set TurnOutcome.HarnessError.
//
// Setup: the on_enter is a host.bump that binds a key, plus an
// emit_intent whose When references a nested field that doesn't
// exist on the bound payload.  The machine defers the emit at
// machine time (the bind hasn't fired yet) and DispatchPostBindEmits
// then errors on the second eval pass.
func TestSettlePostBindEmits_SurfaceDispatchError(t *testing.T) {
	const yamlSrc = `
app:
  id: post-bind-err
  version: 0.1.0
hosts:
  - host.bump
intents:
  start: {}
  loop_back: {}
root: ready
states:
  ready:
    on:
      start:
        - target: probe
  probe:
    on_enter:
      - invoke: host.bump
        with: {}
        bind:
          payload: "value"
      - emit_intent: loop_back
        when: "world.payload.missing.deeper.value > 0"
    on:
      loop_back:
        - target: ready
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.bump", func(ctx context.Context, args map[string]any) (host.Result, error) {
		// Bind a payload that DOES NOT have missing.deeper.value — the
		// post-bind emit_intent's when will eval-error against this
		// world, causing DispatchPostBindEmits to return an error.
		return host.Result{Data: map[string]any{"value": map[string]any{"ok": true}}}, nil
	})

	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err, "DispatchPostBindEmits error must surface in TurnOutcome, not as a Go error")

	require.NotEmpty(t, out.HarnessError,
		"DispatchPostBindEmits error must surface in TurnOutcome.HarnessError")
	require.True(t,
		strings.Contains(out.HarnessError, "post-bind emit_intent") ||
			strings.Contains(out.HarnessError, "when"),
		"HarnessError should carry the eval/when error message, got %q", out.HarnessError)

	// State must settle at the pre-emit resting place (probe), NOT
	// the emit's target (ready).
	require.Equal(t, app.StatePath("probe"), out.NewState,
		"on dispatch error, state must stay at the pre-emit leaf, not the emit's target")

	// HarnessError event must be in the persisted log.
	var hits int
	for _, ev := range out.Events {
		if ev.Kind == store.HarnessError {
			hits++
		}
	}
	require.GreaterOrEqual(t, hits, 1,
		"HarnessError event must be appended to the turn's events")
}

// noopOrchestratorHarness is a zero-behavior Harness for SubmitDirect tests
// in this file. (Test files don't share types across files in some setups;
// hostdispatch_test.go's noopHarness is reused here via same package, but
// we declare an aliasable type for clarity.)
type noopOrchestratorHarness struct{}

func (noopOrchestratorHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (noopOrchestratorHarness) Close() error { return nil }
