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
