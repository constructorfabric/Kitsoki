// parallel_emit_drop_test.go covers the contract W2.8 limitation:
// `emit_intent:` is unsupported as the *origin* of a synthetic
// dispatch from a parallel-encoded state.  Three call-sites
// previously disagreed on how to handle this; all three now log+drop
// (trace.EvIntentEmitParallelDropped) and return no error so an
// otherwise-valid story is not bricked.
//
//	- Site A: machine.dispatchEmittedIntents  (active state parallel-encoded)
//	- Site B: parallel.turnParallel           (winning transition or on_enter
//	                                            chain returns parEmits)
//	- Site C: machine.DispatchPostBindEmits   (state is parallel-encoded)
package machine_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// ─── slog capture helper (machine-local, mirrors orchestrator/trace_test.go) ──

type droppedRecord struct {
	Msg   string
	Attrs map[string]any
}

type captureHandler struct {
	mu      sync.Mutex
	records []droppedRecord
}

func newCaptureHandler() *captureHandler { return &captureHandler{} }

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, droppedRecord{Msg: r.Message, Attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// findDroppedFor returns the first captured record whose Msg matches
// EvIntentEmitParallelDropped AND whose `site` attr equals expectSite.
func (h *captureHandler) findDroppedFor(expectSite string) *droppedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		r := &h.records[i]
		if r.Msg != trace.EvIntentEmitParallelDropped {
			continue
		}
		if site, _ := r.Attrs["site"].(string); site == expectSite {
			return r
		}
	}
	return nil
}

// ─── shared test apps ────────────────────────────────────────────────────────

// makeParallelTransitionEmitApp builds a parallel state whose `a` region
// has a transition with effects that include `emit_intent:`.  Firing the
// intent triggers site B (turn_parallel_transition) — drop + succeed.
func makeParallelTransitionEmitApp() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "par-tr-emit"},
		Root: "outside",
		World: map[string]app.VarDef{
			"flag": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"fire":  {Title: "Fire"},
			"poke":  {Title: "Poke"},
		},
		States: map[string]*app.State{
			"outside": {
				View: app.LegacyView("Outside"),
				On: map[string][]app.Transition{
					"enter": {{Target: "shell"}},
				},
			},
			"shell": {
				Type: "parallel",
				View: app.LegacyView("Shell."),
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {
								View: app.LegacyView("A leaf."),
								On: map[string][]app.Transition{
									"fire": {{
										Target: ".",
										Effects: []app.Effect{
											{EmitIntent: "poke"},
											{Set: map[string]any{"flag": "set"}},
										},
									}},
									"poke": {{Target: "."}},
								},
							},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {View: app.LegacyView("B leaf.")},
						},
					},
				},
			},
		},
	}
}

// ─── Site A — machine.dispatchEmittedIntents (active state parallel) ─────────

// TestParallelEmitDropped_DispatchEmittedIntents covers site A: a synthetic
// emit_intent dispatched while the current state is parallel-encoded is
// dropped (no error, no transition; trace event fires).
//
// Concretely, we enter a parallel state via an outside transition whose
// effects include an emit_intent — the captured emit hits
// dispatchEmittedIntents AFTER the state has resolved to parallel-encoded.
func TestParallelEmitDropped_DispatchEmittedIntents(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "site-a"},
		Root: "outside",
		World: map[string]app.VarDef{
			"flag": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"poke":  {Title: "Poke"},
		},
		States: map[string]*app.State{
			"outside": {
				View: app.LegacyView("Outside"),
				On: map[string][]app.Transition{
					"enter": {{
						Target: "shell",
						// Transition effects fire before the parallel
						// target's on_enter; the emit is captured and
						// dispatched against the new parallel-encoded
						// state in dispatchEmittedIntents.
						Effects: []app.Effect{
							{EmitIntent: "poke"},
							{Set: map[string]any{"flag": "set"}},
						},
					}},
				},
			},
			"shell": {
				Type: "parallel",
				View: app.LegacyView("Shell."),
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {
								View: app.LegacyView("A leaf."),
								On: map[string][]app.Transition{
									"poke": {{Target: "."}},
								},
							},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {View: app.LegacyView("B leaf.")},
						},
					},
				},
			},
		},
	}

	h := newCaptureHandler()
	logger := slog.New(h)
	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	res, err := m.Turn(context.Background(), "outside", world.New(), intent.IntentCall{
		Intent: "enter",
		Slots:  world.Slots{},
	})
	require.NoError(t, err, "parallel-state emit_intent must NOT error from dispatchEmittedIntents")
	require.Nil(t, res.ValidationError)

	// Trace event must have fired with site=dispatch_emitted_intents.
	rec := h.findDroppedFor("dispatch_emitted_intents")
	require.NotNil(t, rec,
		"expected EvIntentEmitParallelDropped from dispatch_emitted_intents; got msgs=%v",
		captureMessages(h))
	require.Equal(t, "poke", rec.Attrs["intent"])

	// The non-emit effect in the same chain still applied.
	require.Equal(t, "set", res.World.Vars["flag"])
	// And the state advanced to the parallel-encoded shell.
	require.True(t, machine.IsParallelPath(res.NewState))
}

// ─── Site B(i) — parallel.turnParallel transition emit ───────────────────────

func TestParallelEmitDropped_TurnParallelTransitionEmit(t *testing.T) {
	def := makeParallelTransitionEmitApp()
	h := newCaptureHandler()
	logger := slog.New(h)
	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	// Step 1 — enter the parallel state.
	res, err := m.Turn(context.Background(), "outside", world.New(), intent.IntentCall{
		Intent: "enter",
		Slots:  world.Slots{},
	})
	require.NoError(t, err)
	require.True(t, machine.IsParallelPath(res.NewState))

	// Reset capture so we only see the second turn's events.
	h.mu.Lock()
	h.records = nil
	h.mu.Unlock()

	// Step 2 — fire the intent whose transition effects include an
	// emit_intent. The parallel-state turn handler must log+drop.
	res, err = m.Turn(context.Background(), res.NewState, res.World, intent.IntentCall{
		Intent: "fire",
		Slots:  world.Slots{},
	})
	require.NoError(t, err, "parallel-state emit_intent must NOT error from turn_parallel_transition")
	require.Nil(t, res.ValidationError)

	rec := h.findDroppedFor("turn_parallel_transition")
	require.NotNil(t, rec,
		"expected EvIntentEmitParallelDropped from turn_parallel_transition; got msgs=%v",
		captureMessages(h))
	require.Equal(t, "poke", rec.Attrs["intent"])

	// Sibling effect in the same arm still applied.
	require.Equal(t, "set", res.World.Vars["flag"])
}

// ─── Site B(ii) — parallel.turnParallel on_enter emit ────────────────────────

func TestParallelEmitDropped_TurnParallelOnEnterEmit(t *testing.T) {
	// To exercise the turn_parallel_on_enter site specifically, we need a
	// transition FROM inside the parallel state INTO a sibling leaf whose
	// on_enter emits.  Entering the parallel state from outside goes
	// through Turn (not turnParallel), so the parent-level on_enter emit
	// hits a DIFFERENT call site.  Use a two-state region (first → second)
	// where the emit lives on the `second` leaf.
	def := &app.AppDef{
		App:  app.AppMeta{ID: "par-onenter-emit-2"},
		Root: "outside",
		World: map[string]app.VarDef{
			"flag": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"step":  {Title: "Step"},
			"poke":  {Title: "Poke"},
		},
		States: map[string]*app.State{
			"outside": {
				View: app.LegacyView("Outside"),
				On: map[string][]app.Transition{
					"enter": {{Target: "shell"}},
				},
			},
			"shell": {
				Type: "parallel",
				View: app.LegacyView("Shell."),
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "first",
						States: map[string]*app.State{
							"first": {
								View: app.LegacyView("A first."),
								On: map[string][]app.Transition{
									"step": {{Target: "../second"}},
								},
							},
							"second": {
								View: app.LegacyView("A second."),
								OnEnter: []app.Effect{
									{EmitIntent: "poke"},
									{Set: map[string]any{"flag": "set"}},
								},
								On: map[string][]app.Transition{
									"poke": {{Target: "."}},
								},
							},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {View: app.LegacyView("B leaf.")},
						},
					},
				},
			},
		},
	}

	h := newCaptureHandler()
	logger := slog.New(h)
	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	// Enter the parallel state (origin: outside; goes through Turn).
	res, err := m.Turn(context.Background(), "outside", world.New(), intent.IntentCall{
		Intent: "enter", Slots: world.Slots{},
	})
	require.NoError(t, err)
	require.True(t, machine.IsParallelPath(res.NewState))

	// Step `a` from first → second.  Now origin is parallel-encoded so
	// the dispatch goes through turnParallel, and second.on_enter fires
	// with an emit_intent that must be dropped at site turn_parallel_on_enter.
	h.mu.Lock()
	h.records = nil
	h.mu.Unlock()
	res, err = m.Turn(context.Background(), res.NewState, res.World, intent.IntentCall{
		Intent: "step", Slots: world.Slots{},
	})
	require.NoError(t, err, "parallel-state on_enter emit_intent must NOT error")
	require.Nil(t, res.ValidationError)

	rec := h.findDroppedFor("turn_parallel_on_enter")
	require.NotNil(t, rec,
		"expected EvIntentEmitParallelDropped from turn_parallel_on_enter; got msgs=%v",
		captureMessages(h))
	require.Equal(t, "poke", rec.Attrs["intent"])

	// Sibling set: effect in the same on_enter still applied.
	require.Equal(t, "set", res.World.Vars["flag"])
}

// ─── Site C — machine.DispatchPostBindEmits parallel-encoded state ───────────

// TestParallelEmitDropped_DispatchPostBindEmits covers site C: when the
// orchestrator calls DispatchPostBindEmits on a parallel-encoded state,
// the method previously returned a silent no-op.  Now it walks the
// would-be emits and logs each one before returning the inputs unchanged.
func TestParallelEmitDropped_DispatchPostBindEmits(t *testing.T) {
	// Build an app whose parallel-region leaf has an on_enter emit_intent
	// — we'll then invoke DispatchPostBindEmits with the parallel-encoded
	// path to exercise the dedicated site C branch.
	def := &app.AppDef{
		App:  app.AppMeta{ID: "site-c"},
		Root: "shell",
		World: map[string]app.VarDef{
			"x": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"poke": {Title: "Poke"},
		},
		States: map[string]*app.State{
			"shell": {
				Type: "parallel",
				View: app.LegacyView("Shell."),
				OnEnter: []app.Effect{
					// emit_intent on the parallel parent's on_enter: this
					// is what DispatchPostBindEmits would walk.
					{EmitIntent: "poke"},
				},
				States: map[string]*app.State{
					"a": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {View: app.LegacyView("A.")},
						},
					},
					"b": {
						Type:    "compound",
						Initial: "leaf",
						States: map[string]*app.State{
							"leaf": {View: app.LegacyView("B.")},
						},
					},
				},
			},
		},
	}

	h := newCaptureHandler()
	logger := slog.New(h)
	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	// Build a parallel-encoded path manually via the public sigil.
	parallelPath := app.StatePath("shell#shell.a.leaf|shell.b.leaf")

	finalState, finalWorld, hostCalls, sayText, events, err := m.DispatchPostBindEmits(
		context.Background(), parallelPath, world.New(),
	)
	require.NoError(t, err, "DispatchPostBindEmits on parallel state must NOT error")
	// Inputs returned unchanged (modulo the no-op invariant).
	require.Equal(t, parallelPath, finalState)
	require.Empty(t, hostCalls)
	require.Empty(t, sayText)
	require.Empty(t, events)
	_ = finalWorld

	rec := h.findDroppedFor("dispatch_post_bind_emits")
	require.NotNil(t, rec,
		"expected EvIntentEmitParallelDropped from dispatch_post_bind_emits; got msgs=%v",
		captureMessages(h))
	require.Equal(t, "poke", rec.Attrs["intent"])
	require.Equal(t, string(parallelPath), rec.Attrs["state"])
}

// captureMessages returns the captured log message strings — used in
// failure messages to show what DID arrive when the expected one didn't.
func captureMessages(h *captureHandler) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.records))
	for i, r := range h.records {
		out[i] = r.Msg
	}
	return out
}
