package expr_test

import (
	"testing"

	"kitsoki/internal/expr"

	"github.com/stretchr/testify/require"
)

// ─── CompileBool tests ───────────────────────────────────────────────────────

func TestCompileBool_ValidGuards(t *testing.T) {
	cases := []struct {
		name string
		src  string
		env  expr.Env
		want bool
	}{
		{
			name: "direction == south (cloak foyer guard)",
			src:  "slots.direction == 'south'",
			env: expr.Env{
				Slots: map[string]any{"direction": "south"},
				World: map[string]any{},
			},
			want: true,
		},
		{
			name: "direction != south",
			src:  "slots.direction == 'south'",
			env: expr.Env{
				Slots: map[string]any{"direction": "north"},
				World: map[string]any{},
			},
			want: false,
		},
		{
			name: "world.wearing_cloak == true",
			src:  "world.wearing_cloak == true",
			env: expr.Env{
				Slots: map[string]any{},
				World: map[string]any{"wearing_cloak": true},
			},
			want: true,
		},
		{
			name: "world.wearing_cloak == false when true",
			src:  "world.wearing_cloak == false",
			env: expr.Env{
				Slots: map[string]any{},
				World: map[string]any{"wearing_cloak": true},
			},
			want: false,
		},
		{
			name: "world.disturbance > 2 (winning: disturbance=3)",
			src:  "world.disturbance > 2",
			env: expr.Env{
				Slots: map[string]any{},
				World: map[string]any{"disturbance": int64(3)},
			},
			want: true,
		},
		{
			name: "world.disturbance > 2 (losing: disturbance=1)",
			src:  "world.disturbance > 2",
			env: expr.Env{
				Slots: map[string]any{},
				World: map[string]any{"disturbance": int64(1)},
			},
			want: false,
		},
		{
			name: "boolean literal true",
			src:  "true",
			env:  expr.Env{Slots: map[string]any{}, World: map[string]any{}},
			want: true,
		},
		{
			name: "boolean literal false",
			src:  "false",
			env:  expr.Env{Slots: map[string]any{}, World: map[string]any{}},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := expr.CompileBool(tc.src)
			require.NoError(t, err, "CompileBool should succeed")
			got, err := expr.EvalBool(p, tc.env)
			require.NoError(t, err, "EvalBool should succeed")
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCompileBool_RejectedExpressions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// filter() returns a slice, not bool — rejected by type check or whitelist.
			name: "lambda predicate via filter",
			src:  "filter([1,2,3], # > 1)",
		},
		{
			// all() with predicate is rejected by our whitelist (PredicateNode).
			name: "forbidden builtin all()",
			src:  "all([1,2,3], # > 0)",
		},
		{
			// let x = ... is rejected by VariableDeclaratorNode.
			name: "variable declaration",
			src:  "let x = 1; x > 0",
		},
		{
			// Map literal: rejected by MapNode or type check.
			name: "map literal",
			src:  `{"key": "value"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := expr.CompileBool(tc.src)
			require.Error(t, err, "CompileBool should reject %q", tc.src)
		})
	}
}

// ─── Compile (AsAny) tests ────────────────────────────────────────────────────

func TestCompile_Ternary(t *testing.T) {
	// Ternary is used in bar's initial: expression.
	p, err := expr.Compile("world.wearing_cloak ? 'dark' : 'lit'")
	require.NoError(t, err)

	// Wearing cloak → dark.
	v, err := expr.EvalAny(p, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": true},
	})
	require.NoError(t, err)
	require.Equal(t, "dark", v)

	// Not wearing → lit.
	v, err = expr.EvalAny(p, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": false},
	})
	require.NoError(t, err)
	require.Equal(t, "lit", v)
}

func TestCompile_LenNilSafe(t *testing.T) {
	// Regression: `len(world.foo.questions)` must not crash when `foo` is an
	// absent (nil) map — a nil argument counts as length 0 (Go semantics), so a
	// guard reading "no questions yet" evaluates instead of aborting the turn.
	// This is the dev-story prd clarifying emit_intent that crashed `kitsoki
	// record` while `kitsoki test flows` passed.
	p, err := expr.Compile(
		"len(world.prd__clarifications.questions) > 0 && " +
			"int(world.prd__answered_count) >= len(world.prd__clarifications.questions) ? 'submit_answers' : ''")
	require.NoError(t, err)

	// prd__clarifications is present (an initialised map) but its `questions`
	// key is unset → `.questions` is nil → len(nil) must be 0, not a crash.
	// This is the exact world shape that aborted `kitsoki record` at turn 5.
	v, err := expr.EvalAny(p, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"prd__clarifications": map[string]any{}},
	})
	require.NoError(t, err)
	require.Equal(t, "", v)

	// Populated map with two questions and enough answers → 'submit_answers'.
	v, err = expr.EvalAny(p, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{
			"prd__clarifications": map[string]any{"questions": []any{"q1", "q2"}},
			"prd__answered_count": 2,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "submit_answers", v)

	// Bare len(nil) returns 0, not an error.
	p2, err := expr.Compile("len(world.missing)")
	require.NoError(t, err)
	v, err = expr.EvalAny(p2, expr.Env{World: map[string]any{}})
	require.NoError(t, err)
	require.Equal(t, 0, v)
}

func TestCompile_EffectValueExpr(t *testing.T) {
	// set: { message_rumpled: "{{ world.disturbance > 2 }}" }
	// The Render function handles the {{ }} wrapping; the inner expr is plain.
	p, err := expr.Compile("world.disturbance > 2")
	require.NoError(t, err)

	v, err := expr.EvalAny(p, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"disturbance": int64(3)},
	})
	require.NoError(t, err)
	require.Equal(t, true, v)
}

// ─── RenderValue tests ───────────────────────────────────────────────────────

// TestRenderValue_MultiInterpolationIsString pins the bug exposed by the
// PRD clarifying room's `answer` effect: a set value that string-concats
// several blocks — "{{ a }}Q{{ b }}: {{ c }}" — begins with "{{" and ends
// with "}}" but is a TEMPLATE, not a single expression. RenderValue used
// to strip the outer braces and compile the middle ("a }}Q{{ b }}: {{ c")
// as one expr, which fails with `unexpected token Bracket("}")`. It must
// instead render the whole thing to a string.
func TestRenderValue_MultiInterpolationIsString(t *testing.T) {
	env := expr.Env{
		Slots: map[string]any{"n": int64(2), "text": "platform developers"},
		World: map[string]any{"clarification_answers": "Q1: internal\n"},
	}
	got, err := expr.RenderValue("{{ world.clarification_answers }}Q{{ slots.n }}: {{ slots.text }}", env)
	require.NoError(t, err)
	require.Equal(t, "Q1: internal\nQ2: platform developers", got)
}

// TestRenderValue_SingleExprStaysTyped guards that the fix above did not
// regress the genuine single-expression form, which must still return a
// TYPED value (int64/bool), not a string.
func TestRenderValue_SingleExprStaysTyped(t *testing.T) {
	env := expr.Env{World: map[string]any{"clarifying_cycle": int64(1), "disturbance": int64(3)}}

	got, err := expr.RenderValue("{{ world.clarifying_cycle + 1 }}", env)
	require.NoError(t, err)
	require.EqualValues(t, 2, got)

	gotBool, err := expr.RenderValue("{{ world.disturbance > 2 }}", env)
	require.NoError(t, err)
	require.Equal(t, true, gotBool)
}

// ─── Render (template engine) tests ──────────────────────────────────────────

func TestRender_PlainText(t *testing.T) {
	out, err := expr.Render("Hello, world!", expr.Env{})
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", out)
}

func TestRender_SimpleInterpolation(t *testing.T) {
	out, err := expr.Render("Direction: {{ slots.direction }}", expr.Env{
		Slots: map[string]any{"direction": "south"},
		World: map[string]any{},
	})
	require.NoError(t, err)
	require.Equal(t, "Direction: south", out)
}

func TestRender_IfBlock_True(t *testing.T) {
	// {{ if world.wearing_cloak }}You are wearing a velvet cloak.{{ end }}
	tmpl := "{{ if world.wearing_cloak }}wearing{{ end }}"
	out, err := expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": true},
	})
	require.NoError(t, err)
	require.Equal(t, "wearing", out)
}

func TestRender_IfBlock_False(t *testing.T) {
	tmpl := "{{ if world.wearing_cloak }}wearing{{ end }}"
	out, err := expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": false},
	})
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func TestRender_IfElseBlock(t *testing.T) {
	// This mirrors the cloakroom view template.
	tmpl := "{{ if world.wearing_cloak }}wearing{{ else }}hanging{{ end }}"
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": true},
	}
	out, err := expr.Render(tmpl, env)
	require.NoError(t, err)
	require.Equal(t, "wearing", out)

	env.World["wearing_cloak"] = false
	out, err = expr.Render(tmpl, env)
	require.NoError(t, err)
	require.Equal(t, "hanging", out)
}

func TestRender_CloakroomView(t *testing.T) {
	// Approximate the actual cloakroom view from app.yaml.
	tmpl := "{{ if world.wearing_cloak }}You are wearing a velvet cloak.{{ end }}{{ if not world.wearing_cloak }}A velvet cloak hangs on the hook.{{ end }}"

	// Cloak on.
	out, err := expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": true},
	})
	require.NoError(t, err)
	require.Contains(t, out, "You are wearing a velvet cloak.")
	require.NotContains(t, out, "hangs on the hook")

	// Cloak off.
	out, err = expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": false},
	})
	require.NoError(t, err)
	require.Contains(t, out, "hangs on the hook.")
	require.NotContains(t, out, "You are wearing")
}

func TestRender_EndedView_Won(t *testing.T) {
	// The ended state view.
	tmpl := "{{ if world.message_rumpled }}You have lost.{{ else }}You have won.{{ end }}"

	out, err := expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"message_rumpled": false},
	})
	require.NoError(t, err)
	require.Contains(t, out, "You have won.")
}

func TestRender_EndedView_Lost(t *testing.T) {
	tmpl := "{{ if world.message_rumpled }}You have lost.{{ else }}You have won.{{ end }}"

	out, err := expr.Render(tmpl, expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"message_rumpled": true},
	})
	require.NoError(t, err)
	require.Contains(t, out, "You have lost.")
}

func TestRender_TernaryInInitial(t *testing.T) {
	// The bar state's initial: expression.
	out, err := expr.Render("{{ world.wearing_cloak ? 'dark' : 'lit' }}", expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"wearing_cloak": true},
	})
	require.NoError(t, err)
	require.Equal(t, "dark", out)
}

func TestRender_UnclosedBlock(t *testing.T) {
	_, err := expr.Render("{{ unclosed", expr.Env{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unclosed")
}

// TestRender_MapInterpolatesAsJSON pins anyToString's map/slice → JSON behavior.
// Before this change, Go's fmt %v rendered maps as `map[k:v]` which downstream
// CLI parsers (e.g. `bugfix context --input <slot>={{ world.X }}`) couldn't
// decode.  Maps and slices must render as valid JSON so the interpolation
// produces machine-readable output by default.
func TestRender_MapInterpolatesAsJSON(t *testing.T) {
	out, err := expr.Render("{{ world.status }}", expr.Env{
		Slots: map[string]any{},
		World: map[string]any{
			"status": map[string]any{
				"build": "FAILED",
				"pr":    "302",
			},
		},
	})
	require.NoError(t, err)
	// JSON map ordering is alphabetical by key for encoding/json.
	require.Equal(t, `{"build":"FAILED","pr":"302"}`, out)
}

func TestRender_SliceInterpolatesAsJSON(t *testing.T) {
	out, err := expr.Render("{{ world.evidence }}", expr.Env{
		Slots: map[string]any{},
		World: map[string]any{
			"evidence": []any{"jenkins:url#1", "jenkins:url#2"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, `["jenkins:url#1","jenkins:url#2"]`, out)
}

// Scalars must NOT be JSON-encoded — they keep their fmt %v behavior so
// existing templates that interpolate strings/numbers don't gain quotes.
func TestRender_ScalarsKeepFmtVBehavior(t *testing.T) {
	out, err := expr.Render(
		"{{ world.name }} / {{ world.count }} / {{ world.ok }}",
		expr.Env{
			Slots: map[string]any{},
			World: map[string]any{
				"name":  "PLTFRM-89912",
				"count": 42,
				"ok":    true,
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "PLTFRM-89912 / 42 / true", out)
}

// ─── Menu env + helper builtins (in-view menu rendering) ─────────────────────

// sampleMenuEnv returns an Env with a small representative menu populated
// (two primary entries + one blocked entry with a reason), with the helper
// functions bound. Used by the helper-builtin tests below.
func sampleMenuEnv() expr.Env {
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{},
		Menu: map[string]any{
			"primary": []any{
				map[string]any{
					"intent":           "name_party",
					"display":          "name_party <names:string>",
					"reason":           "",
					"destination_hint": "intro",
					"primary":          true,
				},
				map[string]any{
					"intent":           "look",
					"display":          "look",
					"reason":           "",
					"destination_hint": "intro",
					"primary":          true,
				},
			},
			"blocked": []any{
				map[string]any{
					"intent":           "start_journey",
					"display":          "start_journey",
					"reason":           "Name the party, pick a profession, and pick a month before you can leave.",
					"destination_hint": "",
					"primary":          false,
				},
			},
		},
	}
	expr.PopulateMenuHelpers(&env)
	return env
}

func TestRender_MenuPrimaryRange(t *testing.T) {
	// Iterate menu.primary with the range construct; each iteration binds
	// `.display` (rewritten internally to `item.display`).
	tmpl := "{{ range menu.primary }}- {{ .display }}\n{{ end }}"
	out, err := expr.Render(tmpl, sampleMenuEnv())
	require.NoError(t, err)
	require.Equal(t, "- name_party <names:string>\n- look\n", out)
}

func TestRender_MenuBlockedRange(t *testing.T) {
	tmpl := "{{ range menu.blocked }}✗ {{ .intent }} — {{ .reason }}{{ end }}"
	out, err := expr.Render(tmpl, sampleMenuEnv())
	require.NoError(t, err)
	require.Equal(t, "✗ start_journey — Name the party, pick a profession, and pick a month before you can leave.", out)
}

func TestRender_AvailableHelper(t *testing.T) {
	env := sampleMenuEnv()

	out, err := expr.Render(`{{ if available("name_party") }}yes{{ else }}no{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "yes", out)

	out, err = expr.Render(`{{ if available("start_journey") }}yes{{ else }}no{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "no", out)

	// Unknown intent: not in primary → available returns false.
	out, err = expr.Render(`{{ if available("missing_intent") }}yes{{ else }}no{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "no", out)
}

func TestRender_BlockedHelper(t *testing.T) {
	env := sampleMenuEnv()

	out, err := expr.Render(`{{ if blocked("start_journey") }}blocked{{ else }}ok{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "blocked", out)

	out, err = expr.Render(`{{ if blocked("name_party") }}blocked{{ else }}ok{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "ok", out)
}

func TestRender_BlockedReasonHelper(t *testing.T) {
	out, err := expr.Render(`{{ blocked_reason("start_journey") }}`, sampleMenuEnv())
	require.NoError(t, err)
	require.Equal(t, "Name the party, pick a profession, and pick a month before you can leave.", out)

	// Not blocked → empty string.
	out, err = expr.Render(`{{ blocked_reason("name_party") }}`, sampleMenuEnv())
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func TestRender_IntentStatusHelper(t *testing.T) {
	env := sampleMenuEnv()

	out, err := expr.Render(`{{ intent_status("look") }}`, env)
	require.NoError(t, err)
	require.Equal(t, "available", out)

	out, err = expr.Render(`{{ intent_status("start_journey") }}`, env)
	require.NoError(t, err)
	require.Equal(t, "blocked", out)

	out, err = expr.Render(`{{ intent_status("nope") }}`, env)
	require.NoError(t, err)
	require.Equal(t, "unknown", out)
}

func TestRender_OTIntroPattern(t *testing.T) {
	// Mirrors the OT intro.yaml usage: when start_journey is blocked, show
	// the reason inline; when available, show the bare intent name.
	tmpl := `{{ if available("start_journey") }}- start the journey{{ else }}- ✗ start_journey — {{ blocked_reason("start_journey") }}{{ end }}`

	out, err := expr.Render(tmpl, sampleMenuEnv())
	require.NoError(t, err)
	require.Equal(t, "- ✗ start_journey — Name the party, pick a profession, and pick a month before you can leave.", out)

	// Now flip: move start_journey into primary.
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{},
		Menu: map[string]any{
			"primary": []any{
				map[string]any{
					"intent":  "start_journey",
					"display": "start_journey",
					"primary": true,
				},
			},
			"blocked": []any{},
		},
	}
	expr.PopulateMenuHelpers(&env)
	out, err = expr.Render(tmpl, env)
	require.NoError(t, err)
	require.Equal(t, "- start the journey", out)
}

// TestRender_EmptyMenu pins behaviour when env.Menu is nil — helpers must
// answer "unknown / not available" without panicking, and {{ range }} over
// nil yields the empty string.
func TestRender_EmptyMenu(t *testing.T) {
	env := expr.Env{Slots: map[string]any{}, World: map[string]any{}}
	expr.PopulateMenuHelpers(&env) // bind helpers against nil menu

	out, err := expr.Render(`{{ available("x") }} / {{ blocked("x") }} / {{ intent_status("x") }}`, env)
	require.NoError(t, err)
	require.Equal(t, "false / false / unknown", out)

	out, err = expr.Render(`{{ range menu.primary }}{{ .display }}{{ end }}`, env)
	require.NoError(t, err)
	require.Equal(t, "", out)
}

// TestRender_RangeNoIdentifierLeak verifies the dot-rewriter only touches
// leading-dot tokens — `a.foo` stays as `a.foo` inside a range body.
func TestRender_RangeNoIdentifierLeak(t *testing.T) {
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{"banner": "HELLO"},
		Menu: map[string]any{
			"primary": []any{
				map[string]any{"intent": "a", "display": "x"},
				map[string]any{"intent": "b", "display": "y"},
			},
		},
	}
	expr.PopulateMenuHelpers(&env)
	tmpl := "{{ range menu.primary }}{{ world.banner }}={{ .display }};{{ end }}"
	out, err := expr.Render(tmpl, env)
	require.NoError(t, err)
	require.Equal(t, "HELLO=x;HELLO=y;", out)
}
