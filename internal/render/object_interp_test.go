package render

import (
	"encoding/json"
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

// TestPongo_ObjectInterpolatedIntoString reproduces bug
// template-object-interp-placeholder: when a world key holding an OBJECT
// (map / slice) is interpolated into a STRING template via `{{ world.key }}`,
// the pongo2 render path stringifies the underlying interface{} as the Go
// placeholder "<map[string]interface {} Value>" instead of usable data.
//
// Real impact (stories/prd/rooms/clarifying.yaml): the room passes
// `questions: "{{ world.clarifications }}"` to the answer_matcher agent, whose
// prompt renders that arg through this same pongo2 path. The matcher therefore
// receives the literal placeholder, cannot match anything, and silently drops
// every clarification answer.
//
// Correct behaviour: a non-scalar value interpolated into a string context must
// render as deterministic, machine-readable JSON that contains the data — never
// the Go fmt placeholder. This test asserts the OBSERVABLE rendered string the
// downstream agent actually receives.
func TestPongo_ObjectInterpolatedIntoString(t *testing.T) {
	env := expr.Env{World: map[string]any{
		"clarifications": map[string]any{
			"questions": []any{
				map[string]any{"id": "q1", "question": "Who are the users?"},
				map[string]any{"id": "q2", "question": "What platform?"},
			},
		},
	}}

	out, err := Pongo("{{ world.clarifications }}", env)
	if err != nil {
		t.Fatalf("Pongo returned error: %v", err)
	}

	// The Go fmt / pongo2 placeholder must never leak into a string context.
	if strings.Contains(out, "<map[") {
		t.Fatalf("object rendered as Go placeholder, not usable data: %q", out)
	}

	// The output must be valid JSON the downstream consumer can parse.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("rendered output is not valid JSON: %v\noutput: %q", err, out)
	}

	// And it must actually carry the data (not an empty/placeholder shell).
	if !strings.Contains(out, "Who are the users?") || !strings.Contains(out, "q2") {
		t.Fatalf("rendered JSON is missing the interpolated data: %q", out)
	}
}

// TestPongo_SliceInterpolatedIntoString covers the slice case of the same bug:
// a top-level list value interpolated into a string template must also render
// as JSON, not the "<[]interface {} Value>" placeholder.
func TestPongo_SliceInterpolatedIntoString(t *testing.T) {
	env := expr.Env{World: map[string]any{
		"tags": []any{"alpha", "beta", "gamma"},
	}}

	out, err := Pongo("{{ world.tags }}", env)
	if err != nil {
		t.Fatalf("Pongo returned error: %v", err)
	}

	if strings.Contains(out, "Value>") {
		t.Fatalf("slice rendered as Go placeholder, not usable data: %q", out)
	}

	var decoded []any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("rendered slice output is not valid JSON: %v\noutput: %q", err, out)
	}
	if len(decoded) != 3 {
		t.Fatalf("expected 3 elements, got %d: %q", len(decoded), out)
	}
}

func TestPongo_CyclicObjectDoesNotOverflowStack(t *testing.T) {
	cyclic := map[string]any{"name": "loop"}
	cyclic["self"] = cyclic

	out, err := Pongo("{{ world.cyclic }}", expr.Env{World: map[string]any{"cyclic": cyclic}})
	if err != nil {
		t.Fatalf("Pongo returned error: %v", err)
	}
	if out == "" {
		t.Fatal("cyclic object rendered empty")
	}
}
