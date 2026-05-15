package main

import (
	"strings"
	"testing"
)

// TestRewriteTable walks every row in the proposal §3.1 translation table,
// plus the nested combinations the rewriter must handle (range inside if,
// if inside range, nested ternaries).
func TestRewriteTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// §3.1 row 1 — plain interpolation, identical syntax.
		{
			name: "plain_interpolation_identical",
			in:   "{{ world.foo }}",
			want: "{{ world.foo }}",
		},
		{
			name: "plain_dotted_path_identical",
			in:   "{{ world.foo.bar }}",
			want: "{{ world.foo.bar }}",
		},

		// §3.1 row 2 — ternary. Pongo2 has no inline ternary, so the
		// whole `{{ }}` becomes a `{% if %}/{% else %}/{% endif %}`
		// block. String-literal branches drop their quotes (they
		// become literal template text); expression branches wrap in
		// `{{ }}`; parenthesised nested ternaries recurse to nested
		// blocks.
		{
			name: "ternary_simple_string_branches",
			in:   "{{ world.x ? 'a' : 'b' }}",
			want: "{% if world.x %}a{% else %}b{% endif %}",
		},
		{
			name: "ternary_with_comparison",
			in:   "{{ world.disturbance > 2 ? 'lost' : 'won' }}",
			want: "{% if world.disturbance > 2 %}lost{% else %}won{% endif %}",
		},
		{
			name: "ternary_nested_in_branch",
			in:   "{{ a ? 1 : (b ? 2 : 3) }}",
			want: "{% if a %}{{ 1 }}{% else %}{% if b %}{{ 2 }}{% else %}{{ 3 }}{% endif %}{% endif %}",
		},
		{
			name: "ternary_expression_branches",
			in:   "{{ world.x ? world.a : world.b }}",
			want: "{% if world.x %}{{ world.a }}{% else %}{{ world.b }}{% endif %}",
		},

		// §3.1 row 3 — nullish coalesce. Django filter args use colon
		// syntax: `|default:"foo"` not `|default('foo')`.
		{
			name: "nullish_coalesce_simple",
			in:   "{{ slots.foo ?? '(unset)' }}",
			want: "{{ slots.foo|default:'(unset)' }}",
		},
		{
			name: "nullish_coalesce_with_int_call",
			in:   "{{ int(slots.oxen ?? 0) }}",
			want: "{{ int(slots.oxen|default:0) }}",
		},
		{
			name: "nullish_coalesce_chained",
			in:   "{{ a ?? b ?? c }}",
			want: "{{ a|default:b|default:c }}",
		},

		// §3.1 row 4 — {{ if … }} … {{ end }}.
		{
			name: "if_end_block",
			in:   `{{ if world.foo != "" }}HI{{ end }}`,
			want: `{% if world.foo != "" %}HI{% endif %}`,
		},
		{
			name: "if_else_end_block",
			in:   "{{ if x }}A{{ else }}B{{ end }}",
			want: "{% if x %}A{% else %}B{% endif %}",
		},

		// §3.1 row 5 — range.
		{
			name: "range_simple_no_dot",
			in:   "{{ range world.tags }}*{{ end }}",
			want: "{% for item in world.tags %}*{% endfor %}",
		},
		{
			name: "range_with_dot_field",
			in:   "{{ range world.members }}{{ .name }}{{ end }}",
			want: "{% for item in world.members %}{{ item.name }}{% endfor %}",
		},
		{
			name: "range_with_dotted_path",
			in:   "{{ range world.party }}{{ .profile.role }}{{ end }}",
			want: "{% for item in world.party %}{{ item.profile.role }}{% endfor %}",
		},

		// Helper calls — pass through verbatim.
		{
			name: "helper_available_passthrough",
			in:   `{{ available('start_journey') }}`,
			want: `{{ available('start_journey') }}`,
		},
		{
			name: "helper_blocked_reason_passthrough",
			in:   `{{ blocked_reason('start_journey') }}`,
			want: `{{ blocked_reason('start_journey') }}`,
		},

		// Mixed literal text + interpolation.
		{
			name: "mixed_literal_and_expr",
			in:   "Cash: ${{ world.money }}",
			want: "Cash: ${{ world.money }}",
		},

		// Nested blocks.
		{
			name: "range_inside_if",
			in:   "{{ if world.have_party }}{{ range world.members }}{{ .name }} {{ end }}{{ end }}",
			want: "{% if world.have_party %}{% for item in world.members %}{{ item.name }} {% endfor %}{% endif %}",
		},
		{
			name: "if_inside_range",
			in:   "{{ range world.members }}{{ if .alive }}{{ .name }}{{ end }}{{ end }}",
			want: "{% for item in world.members %}{% if item.alive %}{{ item.name }}{% endif %}{% endfor %}",
		},
		{
			name: "consecutive_blocks_if_then_range",
			in:   "{{ if x }}HI{{ end }}{{ range xs }}.{{ end }}",
			want: "{% if x %}HI{% endif %}{% for item in xs %}.{% endfor %}",
		},

		// The real oregon-trail roster shape.
		{
			name: "oregon_trail_roster_line",
			in:   `1. {{ if world.party_member_1 != "" }}{{ world.party_member_1 }} (leader){{ else }}(unnamed){{ end }}`,
			want: `1. {% if world.party_member_1 != "" %}{{ world.party_member_1 }} (leader){% else %}(unnamed){% endif %}`,
		},
		{
			name: "oregon_trail_choose_action_helper",
			in:   `{{ if available("start_journey") }}- start the journey{{ else }}- ✗ start_journey — {{ blocked_reason("start_journey") }}{{ end }}`,
			want: `{% if available("start_journey") %}- start the journey{% else %}- ✗ start_journey — {{ blocked_reason("start_journey") }}{% endif %}`,
		},

		// Empty / no-template inputs.
		{
			name: "no_template_at_all",
			in:   "Just plain text — nothing to rewrite.",
			want: "Just plain text — nothing to rewrite.",
		},
		{
			name: "empty_string",
			in:   "",
			want: "",
		},

		// Ternary doesn't fire when `?` lives inside a quoted literal.
		{
			name: "question_mark_inside_string_literal",
			in:   "{{ 'Are you sure?' }}",
			want: "{{ 'Are you sure?' }}",
		},

		// Identifier prefixes (`iface.foo`) must not look like the `if`
		// keyword — boundary detection is whitespace-only.
		{
			name: "identifier_starting_with_if_is_not_keyword",
			in:   "{{ ifoo.bar }}",
			want: "{{ ifoo.bar }}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Rewrite(tc.in)
			if err != nil {
				t.Fatalf("Rewrite(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Rewrite mismatch\nin:   %q\ngot:  %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRewriteIdempotent verifies that running Rewrite twice produces the
// same output as running it once. This is the load-bearing property the
// proposal calls out (Phase C / Important).
func TestRewriteIdempotent(t *testing.T) {
	inputs := []string{
		`{{ if world.x }}A{{ else }}B{{ end }}`,
		`{{ range world.members }}{{ .name }}{{ end }}`,
		`{{ slots.foo ?? '(unset)' }}`,
		`{{ world.x ? 'a' : 'b' }}`,
		`Cash: ${{ world.money }}`,
		// A whole oregon-trail view body chunk.
		`Party of {{ world.party_size }}:
  1. {{ if world.party_member_1 != "" }}{{ world.party_member_1 }} (leader){{ else }}(unnamed){{ end }}
Profession: {{ if world.profession != nil }}{{ world.profession }}{{ else }}(not yet chosen){{ end }}`,
	}
	for i, in := range inputs {
		first, err := Rewrite(in)
		if err != nil {
			t.Fatalf("input %d: first pass: %v", i, err)
		}
		second, err := Rewrite(first)
		if err != nil {
			t.Fatalf("input %d: second pass: %v", i, err)
		}
		if first != second {
			t.Errorf("input %d not idempotent\nfirst:  %q\nsecond: %q", i, first, second)
		}
		// Sanity: post-migration text must contain no legacy block
		// keywords, no `?...:` ternary inside any `{{ }}`, and no
		// parens-form `|default(...)` filter.
		for _, marker := range []string{"{{ if ", "{{ else ", "{{ else}}", "{{ end ", "{{ end}}", "{{ range ", "|default("} {
			if strings.Contains(first, marker) {
				t.Errorf("input %d: rewritten output still contains legacy marker %q: %s", i, marker, first)
			}
		}
	}
}

// TestRewriteErrors covers the error paths: unmatched end, unclosed block,
// and unclosed `{{`.
func TestRewriteErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"unclosed_brace", "{{ world.x "},
		{"orphan_end", "{{ end }}"},
		{"unclosed_if", "{{ if x }}A"},
		{"unclosed_range", "{{ range xs }}."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Rewrite(tc.in); err == nil {
				t.Errorf("Rewrite(%q) expected error, got nil", tc.in)
			}
		})
	}
}
