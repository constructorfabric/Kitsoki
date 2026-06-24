package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRewriteExpr_HelperIntentArgRewritten is a failing-first regression
// guard for the bug: rewriteExpr does not rewrite intent-name string-literal
// arguments inside the view-helper calls available(), blocked(),
// blocked_reason(), and intent_status().
//
// When a child room is imported under an alias (e.g. "core"), every child
// intent named "foo" is renamed to "core__foo" in the On: arcs and the menu.
// A view template that references available('foo') must therefore become
// available('core__foo') after fold — otherwise the helper always reads
// against the renamed menu key and returns the wrong value (false/unknown).
//
// Fix: add a regex pass in rewriteExpr that rewrites each helper's quoted
// intent arg via the existing rewriteIntentRef.
//
// This test is expected to FAIL until the fix lands.
func TestRewriteExpr_HelperIntentArgRewritten(t *testing.T) {
	rw := &childRewriter{
		alias: "core",
		// At least one world key is present so rewriteExpr does not bail
		// via the early-return guard ("len(rw.childWorldKey) == 0").
		childWorldKey: map[string]struct{}{
			"status": {},
		},
		childIntent: map[string]struct{}{
			"foo": {},
		},
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		// ── primary cases: helper arg is a child intent, single-quoted ──────
		{
			name:  "available single-quote",
			input: "available('foo')",
			want:  "available('core__foo')",
		},
		{
			name:  "blocked single-quote",
			input: "blocked('foo')",
			want:  "blocked('core__foo')",
		},
		{
			name:  "blocked_reason single-quote",
			input: "blocked_reason('foo')",
			want:  "blocked_reason('core__foo')",
		},
		{
			name:  "intent_status single-quote",
			input: "intent_status('foo')",
			want:  "intent_status('core__foo')",
		},
		// ── double-quoted variant ───────────────────────────────────────────
		{
			name:  "available double-quote",
			input: `available("foo")`,
			want:  `available("core__foo")`,
		},
		{
			name:  "blocked_reason double-quote",
			input: `blocked_reason("foo")`,
			want:  `blocked_reason("core__foo")`,
		},
		// ── embedded in a larger template expression ────────────────────────
		{
			name:  "available inside if guard",
			input: "available('foo') && world.status == 'ok'",
			want:  "available('core__foo') && world.core__status == 'ok'",
		},
		{
			name:  "negated available",
			input: "!available('foo')",
			want:  "!available('core__foo')",
		},
		{
			name:  "blocked_reason in template interpolation",
			input: "✗ foo — {{ blocked_reason('foo') }}",
			want:  "✗ foo — {{ blocked_reason('core__foo') }}",
		},
		// ── non-child intent arg must NOT be rewritten ──────────────────────
		{
			name:  "non-child intent arg left alone",
			input: "available('bar')",
			want:  "available('bar')",
		},
		// ── world key rewriting still works (regression guard) ──────────────
		{
			name:  "world key still rewritten",
			input: "world.status",
			want:  "world.core__status",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := rw.rewriteExpr(c.input)
			require.Equal(t, c.want, got,
				"rewriteExpr(%q) should rewrite child-intent args in helper calls", c.input)
		})
	}
}
