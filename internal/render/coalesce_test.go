package render

import (
	"testing"

	"kitsoki/internal/expr"
)

// TestPongo_DoubleQuestionMark_NullCoalesce verifies that the kitsoki-
// authored `??` operator is rewritten to pongo2's `|default:` filter
// before pongo2 parses the template. Authors throughout the story
// library use `{{ world.x ?? "(empty)" }}`; without this rewrite every
// such template hits a pongo2 parser error.
func TestPongo_DoubleQuestionMark_NullCoalesce(t *testing.T) {
	cases := []struct {
		name, in, want string
		env            expr.Env
	}{
		{
			name: "empty falls through to literal",
			in:   `{{ world.x ?? "(none)" }}`,
			want: "(none)",
			env:  expr.Env{World: map[string]any{"x": ""}},
		},
		{
			name: "set value passes through",
			in:   `{{ world.x ?? "(none)" }}`,
			want: "alpha",
			env:  expr.Env{World: map[string]any{"x": "alpha"}},
		},
		{
			name: "chained ?? walks left to right",
			in:   `{{ world.a ?? world.b ?? "fallback" }}`,
			want: "fallback",
			env:  expr.Env{World: map[string]any{"a": "", "b": ""}},
		},
		{
			name: "?? inside string literal is preserved",
			in:   `prose: ok??`,
			want: "prose: ok??",
		},
		{
			name: "mixed prose + template",
			in:   `Hi {{ world.who ?? "stranger" }}!`,
			want: "Hi stranger!",
			env:  expr.Env{World: map[string]any{"who": ""}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Pongo(tc.in, tc.env)
			if err != nil {
				t.Fatalf("Pongo: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
