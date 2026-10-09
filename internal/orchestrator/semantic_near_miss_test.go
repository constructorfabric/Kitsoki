// Tests for the near_miss confidence band added to TrySemantic's
// confidence-band switch (see docs/architecture/semantic-routing.md
// "Near-miss band"). A near-miss is a verdict that scored above the
// reject floor (0 — a genuine miss never reaches the switch, see
// semantic.go) but below the accept floor (SemanticMidBar). It must
// never resolve to the nearest authored intent by itself. When a
// room-workbench (internal/app's `workbench:` block) is reachable — the
// current room, or the app's routing.free_form_fallback floor — the
// whole utterance routes straight into that workbench's synthesized
// capture intent; otherwise it falls back to today's off-ramp /
// no-match handling (the next routing tier / harness). Either way the
// decision is recorded on the trace as turn.near_miss with the
// confidence + threshold + resolved destination fields.
//
// The tests manufacture a deterministic near-miss WITHOUT any new
// tunable: they raise app.routing.semantic_mid_bar above
// ConfidenceWholeSynonym (0.90), so an ordinary bare-synonym hit that
// would normally clear the mid-bar now falls strictly between 0 and
// the (raised) mid-bar — exactly the near_miss band — using only the
// existing SemanticHighBar/SemanticMidBar knobs authors already have.
package orchestrator_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// TestSemantic_NearMissFallsThroughWithoutNearestIntent asserts that an
// input scored in the near-miss band (0.90 confidence against a
// deliberately-raised 0.95 mid-bar) never auto-resolves to the intent
// its synonym matched (go_north). Instead it falls through to the next
// routing tier — here the mocked harness — which is configured to
// resolve a DIFFERENT intent (go_east). If TrySemantic ever shortcut
// the near_miss band to "nearest authored intent," this test would
// observe go_north instead of go_east and fail.
func TestSemantic_NearMissFallsThroughWithoutNearestIntent(t *testing.T) {
	t.Parallel()
	const appYAML = `
app:
  id: semroute-near-miss
  version: 0.1.0

world: {}

routing:
  enabled: true
  semantic_high_bar: 0.99
  semantic_mid_bar: 0.95

intents:
  go_north:
    title: "Go north"
    synonyms: ["head north"]
  go_east:
    title: "Go east"
    synonyms: ["wander east"]

root: start

states:
  start:
    view: "compass rose"
    on:
      go_north:
        - target: north_result
      go_east:
        - target: east_result

  north_result:
    terminal: true
    view: "you went north"

  east_result:
    terminal: true
    view: "you went east"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	// Fallback harness resolves to go_east — a DIFFERENT intent than the
	// one "head north" matched via synonym — so a pass proves the
	// near-miss band did not shortcut to the nearest authored intent.
	h := &countingHarness{fall: staticHarness{intentName: "go_east"}}

	orch := orchestrator.New(def, m, s, h, orchestrator.WithLogger(logger))
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "head north")
	require.NoError(t, err)

	require.EqualValues(t, 1, h.calls.Load(),
		"near-miss band must fall through to the next routing tier (today's off-ramp/no-match handling), not resolve directly")
	require.Equal(t, app.StatePath("east_result"), out.NewState,
		"near-miss must never resolve to the nearest authored intent (go_north); the actual outcome is whatever the next tier decided")

	// The trace must show the near_miss verdict with the same
	// confidence-vs-threshold shape the other semantic-tier events
	// record, plus the resolved destination: no workbench is reachable
	// from this app (no `workbench:` room, no routing.free_form_fallback),
	// so it must record "interpreter_fallback".
	var found bool
	for _, r := range handler.allRecords() {
		if r.Message != trace.EvTurnNearMiss {
			continue
		}
		found = true
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "confidence":
				require.InDelta(t, 0.90, a.Value.Float64(), 0.0001, "near_miss must record the verdict's confidence")
			case "threshold":
				require.InDelta(t, 0.95, a.Value.Float64(), 0.0001, "near_miss must record the mid-bar threshold it missed")
			case "destination":
				require.Equal(t, "interpreter_fallback", a.Value.String(),
					"no workbench is reachable from this app, so the near_miss must record the interpreter fallback as its destination")
			}
			return true
		})
	}
	require.True(t, found, "expected a turn.near_miss trace event")
}

// newNearMissWorkbenchApp builds an app whose root room `bench` declares a
// `workbench:` block (internal/app/workbench.go), plus two hand-authored
// command arcs (go_north/go_east) sharing the SAME confidence-band setup as
// [TestSemantic_NearMissFallsThroughWithoutNearestIntent]: semantic_mid_bar
// is raised above ConfidenceWholeSynonym (0.90) so "head north" lands in the
// near-miss band instead of clearing the mid-bar. host.agent.task — the
// workbench macro's synthesized on_enter dispatch — is stubbed via a fake
// registry; no LLM anywhere.
//
// This loads from a temp file via [app.Load] rather than [app.LoadBytes]:
// `workbench:` desugaring (expandWorkbenches) runs inside runLoadPipeline,
// which only the file-backed Load path drives — LoadBytes intentionally
// skips the import-fold/phase-expand/workbench-expand chain (see
// internal/app/loader.go), so a LoadBytes-based fixture here would silently
// exercise a bench room with NO synthesized capture intent.
func newNearMissWorkbenchApp(t *testing.T, logger *slog.Logger) (*orchestrator.Orchestrator, *countingHarness, store.Store, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: semroute-near-miss-workbench
  version: 0.1.0

world:
  bench_request: { type: string, default: "" }
  bench_note:    { type: object, default: {} }

hosts:
  - host.agent.task

toolboxes:
  bench_toolbox:
    tools: [Read, Grep, Glob, Edit, Write, Bash]
    effect: write

routing:
  enabled: true
  semantic_high_bar: 0.99
  semantic_mid_bar: 0.95

intents:
  go_north:
    title: "Go north"
    synonyms: ["head north"]
  go_east:
    title: "Go east"
    synonyms: ["wander east"]

agents:
  bench_agent:
    system_prompt: "You do bench work: pick up the operator's free-form request and get it done."
    toolbox: bench_toolbox

root: bench

states:
  bench:
    workbench:
      agent: bench_agent
      prompt: prompts/bench.md
      acceptance_schema: schemas/bench-note.json

    view:
      - prose: "bench request={{ world.bench_request }} summary={{ world.bench_note.summary }}"

    on:
      go_north:
        - target: north_result
      go_east:
        - target: east_result

  north_result:
    terminal: true
    view: "you went north"

  east_result:
    terminal: true
    view: "you went east"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte(appYAML), 0o644))

	def, err := app.Load(path)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.agent.task", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"ok": true,
			"submitted": map[string]any{
				"summary": "bench processed",
			},
		}}, nil
	})

	// Fallback harness would resolve to go_east if the turn ever reached it —
	// present only so a bug that regresses the workbench routing back to the
	// harness has a distinguishable, non-matching outcome to fail against.
	h := &countingHarness{fall: staticHarness{intentName: "go_east"}}

	opts := []orchestrator.Option{orchestrator.WithHostRegistry(reg)}
	if logger != nil {
		opts = append(opts, orchestrator.WithLogger(logger))
	}
	orch := orchestrator.New(def, m, s, h, opts...)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, h, s, sid
}

// TestSemantic_NearMissRoutesToWorkbenchCaptureIntent is the wiring this
// file exists to prove: a near-miss verdict in a room-workbench room must
// resolve into that room's synthesized capture intent (bench_capture) —
// never the nearest authored intent (go_north) and never the main-turn
// harness/interpreter.
func TestSemantic_NearMissRoutesToWorkbenchCaptureIntent(t *testing.T) {
	t.Parallel()
	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)
	orch, h, s, sid := newNearMissWorkbenchApp(t, logger)
	ctx := context.Background()

	const msg = "head north"
	out, err := orch.Turn(ctx, sid, msg)
	require.NoError(t, err)

	require.EqualValues(t, 0, h.calls.Load(),
		"a workbench-reachable near-miss must resolve without ever reaching the main-turn harness")
	require.Equal(t, app.StatePath("bench"), out.NewState,
		"the workbench capture intent self-loops on bench — never north_result (the nearest authored intent) nor east_result (the harness fallback)")
	require.Contains(t, out.View, "request="+msg,
		"the whole utterance must fill the capture intent's request slot")
	require.Contains(t, out.View, "summary=bench processed",
		"the stubbed host.agent.task dispatch must have run via the synthesized on_enter")

	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	assertRoutedBy(t, history, "near_miss_workbench")

	var found bool
	for _, r := range handler.allRecords() {
		if r.Message != trace.EvTurnNearMiss {
			continue
		}
		found = true
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "destination" {
				require.Equal(t, "bench_capture", a.Value.String(),
					"turn.near_miss must record the resolved workbench capture intent as its destination")
			}
			return true
		})
	}
	require.True(t, found, "expected a turn.near_miss trace event")
}

// TestSemantic_NearMissWorkbenchAppliesToEveryNearMissInput proves the
// workbench wiring is not special-cased to one input: "wander east" is the
// fixture's OTHER synonym, and at the same raised mid-bar (0.95) it also
// only reaches 0.90 confidence — a near-miss too — so it must ALSO resolve
// into the workbench capture intent rather than the east_result arc its
// synonym would otherwise match.
func TestSemantic_NearMissWorkbenchAppliesToEveryNearMissInput(t *testing.T) {
	t.Parallel()
	orch, h, _, sid := newNearMissWorkbenchApp(t, nil)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "wander east")
	require.NoError(t, err)
	require.EqualValues(t, 0, h.calls.Load())
	require.Equal(t, app.StatePath("bench"), out.NewState,
		"a near-miss on 'wander east' must ALSO resolve to the workbench capture intent, not east_result")
}
