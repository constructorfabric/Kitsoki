package elements

import (
	"testing"

	"kitsoki/internal/expr"
)

// TestTruthy_BasicShapes pins the truthy() function's contract across
// the value shapes a `when:` guard is likely to encounter.
func TestTruthy_BasicShapes(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"nil", nil, false},
		{"bool-true", true, true},
		{"bool-false", false, false},
		{"empty-string", "", false},
		{"non-empty-string", "hi", true},
		{"empty-slice-any", []any{}, false},
		{"non-empty-slice-any", []any{1, 2}, true},
		{"empty-slice-string", []string{}, false},
		{"non-empty-slice-string", []string{"a"}, true},
		{"empty-map", map[string]any{}, false},
		{"non-empty-map", map[string]any{"k": 1}, true},
		{"zero-int", 0, false},
		{"non-zero-int", 7, true},
		{"zero-int64", int64(0), false},
		{"non-zero-int64", int64(-1), true},
		{"zero-float", 0.0, false},
		{"non-zero-float", 0.5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truthy(tc.v); got != tc.want {
				t.Errorf("truthy(%v) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// TestEvalWhen_SliceValuesAreTruthyByLength is the regression guard
// for the 2026-05-20 empty-view dead-end. A `when:` guard pointing
// directly at a slice-typed world value used to error out at
// expr-lang's bool([]any) conversion ("invalid operation"), which
// bubbled up through machine.RenderState → orchestrator's post-bind
// re-render and silently dropped the view to "" — the user saw a
// blank transcript with no diagnostic.
//
// Post-fix: bare-slice `when:` evaluates via the truthy() helper.
// Empty slice → false (hidden), non-empty slice → true (shown).
func TestEvalWhen_SliceValuesAreTruthyByLength(t *testing.T) {
	env := expr.Env{
		World: map[string]any{
			"blockers_empty": []any{},
			"blockers_some":  []any{"a", "b"},
		},
	}
	gotEmpty, err := evalWhen("world.blockers_empty", env)
	if err != nil {
		t.Fatalf("empty-slice when: %v", err)
	}
	if gotEmpty {
		t.Errorf("empty slice should be falsy; got truthy")
	}
	gotSome, err := evalWhen("world.blockers_some", env)
	if err != nil {
		t.Fatalf("non-empty-slice when: %v", err)
	}
	if !gotSome {
		t.Errorf("non-empty slice should be truthy; got falsy")
	}
}

// TestEvalWhen_RuntimeErrorTreatedAsFalsy covers the
// "world.x.y when world.x is nil" case. expr-lang errors out with
// "cannot fetch y from <nil>" — used to bubble up and fail the
// whole render. Now evalWhen treats runtime errors as falsy so an
// optional guard against a missing property doesn't blow up the
// view.
func TestEvalWhen_RuntimeErrorTreatedAsFalsy(t *testing.T) {
	env := expr.Env{
		World: map[string]any{
			"implement_artifact": nil, // .blockers fetch will fail
		},
	}
	got, err := evalWhen("world.implement_artifact.blockers", env)
	if err != nil {
		t.Fatalf("expected nil err (runtime errors are absorbed); got %v", err)
	}
	if got {
		t.Errorf("nil.blockers should be falsy; got truthy")
	}
}

// TestEvalWhen_BoolStillReturnsBool sanity-checks that existing
// strict-bool when: guards (like `world.judge_mode == 'llm'`) still
// work — the truthy() path doesn't change the result for bool inputs.
func TestEvalWhen_BoolStillReturnsBool(t *testing.T) {
	env := expr.Env{World: map[string]any{"mode": "llm"}}
	got, err := evalWhen(`world.mode == "llm"`, env)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got {
		t.Errorf("comparison expression should return true")
	}
}
