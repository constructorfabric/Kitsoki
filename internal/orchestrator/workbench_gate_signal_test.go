package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
)

// workbenchGateSignal unit tests — room-workbench Task 1.4(b). Pure function,
// no orchestrator/session machinery needed: a minimal *app.AppDef with one
// workbench: state is enough to pin the cases the doc comment promises.

func testDefWithWorkbenchState(t *testing.T) *app.AppDef {
	t.Helper()
	return &app.AppDef{
		States: map[string]*app.State{
			"bench": {
				Workbench: &app.WorkbenchDecl{Agent: "bench_agent"},
			},
			"plain": {},
		},
	}
}

func TestWorkbenchGateSignal_NonWorkbenchState_ReturnsNil(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	if got := workbenchGateSignal(def, "plain", false, "some rendered view", nil); got != nil {
		t.Fatalf("expected nil (dispatching state is not a workbench: room), got %#v", got)
	}
}

func TestWorkbenchGateSignal_UnknownState_ReturnsNil(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	if got := workbenchGateSignal(def, "does-not-exist", false, "view", nil); got != nil {
		t.Fatalf("expected nil for an unknown state path, got %#v", got)
	}
}

func TestWorkbenchGateSignal_NilDef_ReturnsNil(t *testing.T) {
	if got := workbenchGateSignal(nil, "bench", false, "view", nil); got != nil {
		t.Fatalf("expected nil for nil def, got %#v", got)
	}
}

func TestWorkbenchGateSignal_EmptyDispatchingState_ReturnsNil(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	if got := workbenchGateSignal(def, "", false, "view", nil); got != nil {
		t.Fatalf("expected nil for empty dispatching state, got %#v", got)
	}
}

func TestWorkbenchGateSignal_DispatchSucceeded_CandidateCompletedTrue(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	got := workbenchGateSignal(def, "bench", false /* dispatchFailed */, "rendered narration", nil)
	if got == nil {
		t.Fatal("expected a non-nil signal for a workbench-origin dispatch")
	}
	sig, ok := got["usable_kitsoki_gate"].(map[string]any)
	if !ok {
		t.Fatalf("expected usable_kitsoki_gate map, got %#v", got)
	}
	if sig["candidate_completed"] != true {
		t.Errorf("candidate_completed = %v, want true", sig["candidate_completed"])
	}
	if sig["silent_bounce"] != false {
		t.Errorf("silent_bounce = %v, want false (dispatch did not fail)", sig["silent_bounce"])
	}
	if sig["misroute_adjacent"] != false {
		t.Errorf("misroute_adjacent = %v, want false (never computed here — documented S4 gap)", sig["misroute_adjacent"])
	}
	refs, ok := sig["evidence_refs"].([]string)
	if !ok || len(refs) != 0 {
		t.Errorf("evidence_refs = %#v, want empty []string", sig["evidence_refs"])
	}
}

func TestWorkbenchGateSignal_DispatchFailedWithRenderedView_CandidateCompletedFalse(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	got := workbenchGateSignal(def, "bench", true /* dispatchFailed */, "an explanation was rendered", nil)
	if got == nil {
		t.Fatal("expected a non-nil signal")
	}
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != false {
		t.Errorf("candidate_completed = %v, want false after a failed dispatch", sig["candidate_completed"])
	}
	if sig["silent_bounce"] != false {
		t.Errorf("silent_bounce = %v, want false (a rendered explanation is present)", sig["silent_bounce"])
	}
}

func TestWorkbenchGateSignal_DispatchFailedWithEmptyView_SilentBounceTrue(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	got := workbenchGateSignal(def, "bench", true /* dispatchFailed */, "   ", nil)
	if got == nil {
		t.Fatal("expected a non-nil signal")
	}
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["silent_bounce"] != true {
		t.Errorf("silent_bounce = %v, want true (dispatch failed with no rendered view this turn)", sig["silent_bounce"])
	}
}

// TestWorkbenchGateSignal_FieldNamesSubsetOfGateSchema is the "covered by a
// deterministic test validating against
// tools/arena/arena/plugins/usable_kitsoki_gate_schema.json field names" half
// of Task 1.4(b): every key workbenchGateSignal emits under
// usable_kitsoki_gate must be a real property name in S6's schema (never a
// name S6 wouldn't recognize), so a rollup reading this turn-level signal
// finds fields it already knows how to interpret. This does NOT assert the
// reverse (that we emit every schema field) — scenario_id/persona/surface are
// cell-identity fields the S6 rollup supplies from outside the trace, not
// something a single turn.end payload carries; see workbench_gate_signal.go's
// evidence_refs note for why this turn-level signal is deliberately not a
// complete, schema-valid parity record on its own.
func TestWorkbenchGateSignal_FieldNamesSubsetOfGateSchema(t *testing.T) {
	def := testDefWithWorkbenchState(t)
	got := workbenchGateSignal(def, "bench", false, "view", nil)
	sig := got["usable_kitsoki_gate"].(map[string]any)

	schemaPath := filepath.Join("..", "..", "tools", "arena", "arena", "plugins", "usable_kitsoki_gate_schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read gate schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse gate schema: %v", err)
	}
	if len(schema.Properties) == 0 {
		t.Fatal("gate schema declared no properties — did the path change?")
	}

	for field := range sig {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("workbenchGateSignal emits field %q which is not a property in %s", field, schemaPath)
		}
	}
}

// testDefWithRealWorkbenchState mirrors internal/app/workbench.go's actual
// desugared shape (the synthesized on_enter host.agent.task Effect with a
// single-entry Bind) rather than the bare WorkbenchDecl-only stub above —
// needed so workbenchNoteKey has something real to find.
func testDefWithRealWorkbenchState(t *testing.T) *app.AppDef {
	t.Helper()
	return &app.AppDef{
		States: map[string]*app.State{
			"bench": {
				Workbench: &app.WorkbenchDecl{Agent: "bench_agent"},
				OnEnter: []app.Effect{
					{
						Invoke: "host.agent.task",
						Bind:   map[string]string{"bench_note": "submitted"},
					},
				},
			},
		},
	}
}

// S6 real expected_effects join — room-workbench's S6 projection (see
// tools/session-mining/flow_fixture_compiler.py's real-workbench target).

func TestWorkbenchGateSignal_ExpectedEffectsSatisfied_CandidateCompletedTrue(t *testing.T) {
	def := testDefWithRealWorkbenchState(t)
	world := map[string]any{
		"bench_expected_effects": []any{"ran the tests", "opened a PR"},
		"bench_note": map[string]any{
			"summary": "Ran the tests and opened a PR for review.",
		},
	}
	got := workbenchGateSignal(def, "bench", false /* dispatchFailed */, "view", world)
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != true {
		t.Errorf("candidate_completed = %v, want true (note covers every expected effect)", sig["candidate_completed"])
	}
}

func TestWorkbenchGateSignal_ExpectedEffectsUnsatisfied_CandidateCompletedFalse(t *testing.T) {
	def := testDefWithRealWorkbenchState(t)
	world := map[string]any{
		"bench_expected_effects": []any{"ran the tests", "opened a PR"},
		"bench_note": map[string]any{
			// Honest stub: only reflects a partial effect, doesn't claim
			// the PR was opened — the join must not paper over this.
			"summary": "Ran the tests.",
		},
	}
	got := workbenchGateSignal(def, "bench", false /* dispatchFailed */, "view", world)
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != false {
		t.Errorf("candidate_completed = %v, want false (note omits an expected effect — must not be hardcoded true)", sig["candidate_completed"])
	}
}

func TestWorkbenchGateSignal_ExpectedEffectsPresentButDispatchFailed_CandidateCompletedFalse(t *testing.T) {
	def := testDefWithRealWorkbenchState(t)
	world := map[string]any{
		"bench_expected_effects": []any{"ran the tests"},
		"bench_note": map[string]any{
			"summary": "Ran the tests.",
		},
	}
	got := workbenchGateSignal(def, "bench", true /* dispatchFailed */, "an explanation", world)
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != false {
		t.Errorf("candidate_completed = %v, want false (dispatch itself failed, regardless of note content)", sig["candidate_completed"])
	}
}

func TestWorkbenchGateSignal_NoExpectedEffectsWorldVar_FallsBackToDispatchProxy(t *testing.T) {
	def := testDefWithRealWorkbenchState(t)
	world := map[string]any{
		"bench_note": map[string]any{"summary": "did something unrelated"},
	}
	got := workbenchGateSignal(def, "bench", false /* dispatchFailed */, "view", world)
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != true {
		t.Errorf("candidate_completed = %v, want true (no expected_effects join input present — falls back to the dispatch-only proxy, unchanged behavior)", sig["candidate_completed"])
	}
}

func TestWorkbenchGateSignal_EmptyExpectedEffectsList_FallsBackToDispatchProxy(t *testing.T) {
	def := testDefWithRealWorkbenchState(t)
	world := map[string]any{
		"bench_expected_effects": []any{},
		"bench_note":             map[string]any{"summary": "did something unrelated"},
	}
	got := workbenchGateSignal(def, "bench", false /* dispatchFailed */, "view", world)
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != true {
		t.Errorf("candidate_completed = %v, want true (an empty expected_effects list is treated as absent)", sig["candidate_completed"])
	}
}

// A workbench room imported into a parent story (pets-dev, slidey-dev) lives
// at a dotted, compound-wrapped path like "core.landing" — def.LookupState
// (not a flat def.States[...] index) must resolve it, and the join's world
// keys must be the FOLDED (alias__-prefixed) names, exactly as import-folding
// would actually produce them (see internal/app/imports_rewriter.go's Bind
// rewrite: both `<room>_note` and `<room>_expected_effects` get the same
// alias__ prefix, so deriving one from the other post-fold stays correct).
func TestWorkbenchGateSignal_ImportFoldedNestedPath_RealJoinStillWorks(t *testing.T) {
	def := &app.AppDef{
		States: map[string]*app.State{
			"core": {
				States: map[string]*app.State{
					"landing": {
						Workbench: &app.WorkbenchDecl{Agent: "bench_agent"},
						OnEnter: []app.Effect{
							{
								Invoke: "host.agent.task",
								Bind:   map[string]string{"core__landing_note": "submitted"},
							},
						},
					},
				},
			},
		},
	}
	world := map[string]any{
		"core__landing_expected_effects": []any{"filed the ticket"},
		"core__landing_note": map[string]any{
			"summary": "Filed the ticket.",
		},
	}
	got := workbenchGateSignal(def, "core.landing", false /* dispatchFailed */, "view", world)
	if got == nil {
		t.Fatal("expected a non-nil signal for a workbench room nested under an import alias")
	}
	sig := got["usable_kitsoki_gate"].(map[string]any)
	if sig["candidate_completed"] != true {
		t.Errorf("candidate_completed = %v, want true (nested-path lookup + folded-key join both resolved correctly)", sig["candidate_completed"])
	}
}
