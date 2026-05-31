package orchestrator_test

// Tests for the execution-modes engine mechanism: a synthetic
// emit_intent chain auto-advances through a multi-way decision gate in
// one-shot mode (the historical default) but STOPS at it in staged mode,
// ending the turn so a human picks the next intent.
//
// App shape:
//   ready --start--> working
//   working.on_enter: host.work binds done_flag="ok", then post-bind
//                     emit_intent go (working has a single forward intent
//                     `go`, which is the emit target → NOT a decision gate)
//   gate.on_enter:    post-bind emit_intent go2.  gate exposes go2 (emit
//                     target → auto path) AND alt (operator-only forward →
//                     ready) → gate IS a decision gate.
//   finished: terminal.
//
// One-shot: SubmitDirect("start") chains working→gate→finished in one turn.
// Staged:   SubmitDirect("start") chains working→gate then STOPS at gate;
//           a GateDecided event records the stop; a follow-up
//           SubmitDirect("go2") advances to finished.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// captureSink records OnRoomEnter calls so a test can assert that per-room
// progress breadcrumbs streamed live during a one-shot synthetic chain.
type captureSink struct {
	mu     sync.Mutex
	states []string
	says   []string
}

func (c *captureSink) OnRoomEnter(state app.StatePath, banner string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = append(c.states, string(state))
	c.says = append(c.says, banner)
}

func (c *captureSink) snapshot() ([]string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.states...), append([]string(nil), c.says...)
}

const gateModeYAML = `
app:
  id: gate-mode
  version: 0.1.0
hosts:
  - host.work
intents:
  start: {}
  go: {}
  go2: {}
  alt: {}
root: ready
states:
  ready:
    on:
      start:
        - target: working
  working:
    on_enter:
      - invoke: host.work
        with: {}
        bind:
          done_flag: "value"
      - emit_intent: go
        when: "world.done_flag == 'ok'"
    on:
      go:
        - target: gate
  gate:
    on_enter:
      - say: "entered the gate"
      - emit_intent: go2
        when: "world.done_flag == 'ok'"
    on:
      go2:
        - target: finished
      alt:
        - target: ready
  finished:
    terminal: true
    on_enter:
      - say: "reached finished"
`

func newGateModeOrchestrator(t *testing.T, opts ...orchestrator.Option) (*orchestrator.Orchestrator, store.Store) {
	t.Helper()
	def, err := app.LoadBytes([]byte(gateModeYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.work", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"value": "ok"}}, nil
	})

	allOpts := append([]orchestrator.Option{orchestrator.WithHostRegistry(reg)}, opts...)
	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{}, allOpts...)
	return orch, s
}

// TestExecutionMode_OneShot_AdvancesThroughGate is the control: without a
// staged mode the chain runs all the way to the terminal state in one turn,
// exactly as it did before the execution-modes change.
func TestExecutionMode_OneShot_AdvancesThroughGate(t *testing.T) {
	orch, _ := newGateModeOrchestrator(t) // zero value == ExecOneShot

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("finished"), out.NewState,
		"one-shot must auto-advance through the decision gate to the terminal state")

	// One-shot resolves the gate via its conditional-default emit (go2) and
	// records that as a GateDecided{default} — never a human bail.
	var defaultDecisions int
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			defaultDecisions++
			require.Contains(t, string(ev.Payload), `"decider":"default"`)
			require.Contains(t, string(ev.Payload), `"bailed_to_human":false`)
		}
	}
	require.Equal(t, 1, defaultDecisions,
		"the default-emit gate resolution must be recorded once")
}

// TestExecutionMode_Staged_StopsAtGate is the behaviour under test: staged
// mode ends the turn at the multi-way decision gate.  This FAILS without the
// engine change (the chain would advance to `finished` like one-shot).
func TestExecutionMode_Staged_StopsAtGate(t *testing.T) {
	orch, _ := newGateModeOrchestrator(t, orchestrator.WithExecutionMode(orchestrator.ExecStaged))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("gate"), out.NewState,
		"staged must STOP at the decision gate, not auto-advance through it")

	// The stop is recorded.
	var gateEvents int
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			gateEvents++
			require.Contains(t, string(ev.Payload), `"state":"gate"`)
			require.Contains(t, string(ev.Payload), `"decider":"human"`)
			require.Contains(t, string(ev.Payload), `"bailed_to_human":true`)
		}
	}
	require.Equal(t, 1, gateEvents, "exactly one GateDecided event must record the staged stop")

	// The operator can still advance manually from the gate.
	out2, err := orch.SubmitDirect(ctx, sid, "go2", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("finished"), out2.NewState,
		"a manual intent must advance past the gate the staged turn stopped at")
}

// TestExecutionMode_OneShot_StreamsPerRoomSay verifies that in one-shot mode
// each room's say-text streams live (per room) through the RoomEnterSink as
// the synthetic chain advances, rather than being merged into one blob. This
// is the "some text update between the rooms in one-shot mode" behaviour.
func TestExecutionMode_OneShot_StreamsPerRoomSay(t *testing.T) {
	sink := &captureSink{}
	orch, _ := newGateModeOrchestrator(t, orchestrator.WithRoomEnterSink(sink))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)

	states, says := sink.snapshot()
	// gate and finished are entered during the post-bind chain and each
	// carries a say; both must have streamed, in order.
	require.Equal(t, []string{"gate", "finished"}, states,
		"each chained room's say must stream live, in chain order")
	require.Equal(t, []string{"entered the gate", "reached finished"}, says)
}

// TestExecutionMode_WorkingRoomNotAGate confirms the single-forward-intent
// room (working) auto-advances even in staged mode — "if there's only one
// intent, always auto-advance".  Verified indirectly: the staged turn above
// passes THROUGH working (binding done_flag and firing `go`) to reach gate;
// if working were treated as a gate the staged turn would have stopped at
// `working` instead.  Asserted explicitly here for clarity.
func TestExecutionMode_WorkingRoomNotAGate(t *testing.T) {
	orch, _ := newGateModeOrchestrator(t, orchestrator.WithExecutionMode(orchestrator.ExecStaged))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.NotEqual(t, app.StatePath("working"), out.NewState,
		"staged must NOT stop at the single-forward-intent working room")
	require.Equal(t, app.StatePath("gate"), out.NewState)
	require.False(t, strings.Contains(string(out.View), "ERROR"))
}
