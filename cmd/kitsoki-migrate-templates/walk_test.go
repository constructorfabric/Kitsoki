package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsTemplatedField walks the proposal §3.1/§3.2 path allowlist plus the
// pure-expression denylist. This is the load-bearing routing decision —
// the byte-surgery walker only rewrites paths where this returns true.
func TestIsTemplatedField(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// View bodies — templated.
		{"/states/foyer/view", true},
		{"/states/bar/states/lit/view", true},
		{"/states/parent/states/child/states/grandchild/view", true},

		// Transition target / view / guard_hint — templated.
		{"/states/foyer/on/go[0]/target", true},
		{"/states/foyer/on/go[0]/view", true},
		{"/states/foyer/on/go[0]/guard_hint", true},

		// Effect leaves — templated (string-output via pongo2 Render).
		{"/states/foyer/on/go[0]/effects[0]/say", true},
		{"/states/x/on_enter[0]/say", true},

		// Initial selector — templated when present (Rewrite skips
		// non-`{{` values regardless).
		{"/states/bar/initial", true},

		// on_complete recursion — `say:` rewritten, `set:` not.
		{"/states/x/on/intent[0]/effects[0]/on_complete[0]/say", true},

		// Pure-expression fields — NOT templated (expr-lang guards).
		{"/states/foyer/on/go[0]/when", false},
		{"/states/foyer/on/go[0]/effects[0]/when", false},
		{"/states/x/on_enter[0]/when", false},

		// Typed-value fields — NOT templated (expr.RenderValue keeps
		// these typed; the codemod must not rewrite them).
		{"/states/foyer/on/go[0]/effects[0]/set/wearing_cloak", false},
		{"/states/bar/states/lit/on/read_message[0]/effects[0]/set/message_rumpled", false},
		{"/states/x/on_enter[0]/set/inbox_unread", false},
		{"/states/x/on/intent[0]/effects[0]/with/prompt_path", false},
		{"/states/x/on/intent[0]/effects[0]/with/args/theme", false},
		{"/states/x/on_enter[0]/with/body", false},
		{"/states/x/on/intent[0]/effects[0]/on_complete[0]/set/foo", false},

		// Structural / metadata — NOT templated.
		{"/states/foyer/description", false},
		{"/states/foyer/type", false},
		{"/states/foyer/terminal", false},
		{"/states/foyer/mode", false},
		{"/states/foyer/relevant_world[0]", false},
		{"/states/foyer/relevant_slots[1]", false},
		{"/states/foyer/menu[0]", false},

		// Effect bind: holds host-result keys, not templates.
		{"/states/x/on/intent[0]/effects[0]/bind/world_key", false},
		// on_error: is a state path, not a template.
		{"/states/x/on/intent[0]/effects[0]/on_error", false},

		// Top-level non-states — NOT templated.
		{"/app/id", false},
		{"/world/foo/default", false},
		{"/intents/go/slots/direction/values[0]", false},
		{"/off_path/trigger", false},

		// Proposals.
		{"/proposals/buy_supplies/views/reviewing", true},
		// execute/with/* args are typed values (expr.RenderValue) —
		// NOT rewritten under the pragmatic split.
		{"/proposals/buy_supplies/execute/with/items", false},
		{"/proposals/buy_supplies/policy/auto_accept_if", false}, // expr-lang
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(strings.TrimPrefix(tc.path, "/"), "/", "."), func(t *testing.T) {
			got := isTemplatedField(tc.path)
			if got != tc.want {
				t.Errorf("isTemplatedField(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestMigrateFileFixtures runs MigrateFile against compact in-test YAML
// fixtures that exercise the field-allowlist boundaries explicitly: a
// when: that must be left alone, a view: that must be rewritten, a
// nested effects.*.set.<k>: that must be rewritten.
func TestMigrateFileFixtures(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantCount   int
		wantContain []string // substrings expected in the output
		wantAbsent  []string // substrings expected to NOT appear
	}{
		{
			name: "view_rewritten_when_left_alone",
			src: `states:
  foyer:
    view: |
      {{ if world.cloaked }}Dark.{{ else }}Lit.{{ end }}
    on:
      go:
        - when: "slots.direction == 'south'"
          target: bar
`,
			wantCount: 1,
			wantContain: []string{
				"{% if world.cloaked %}Dark.{% else %}Lit.{% endif %}",
				// `when:` survives BYTE-IDENTICAL — byte-surgery mode
				// preserves quoting on non-rewritten leaves.
				`when: "slots.direction == 'south'"`,
			},
			wantAbsent: []string{
				"{{ if world.cloaked }}",
			},
		},
		{
			name: "set_with_legacy_syntax_stays_on_expr_lang",
			src: `states:
  bar:
    on:
      read_message:
        - target: ended
          effects:
            - set:
                message_rumpled: "{{ world.disturbance > 2 }}"
                money: "{{ world.money - 5 }}"
            - say: "Cash: ${{ world.money }}"
              when: "world.money > 0"
`,
			// Pragmatic split: set: stays on expr-lang (typed values).
			// say: rewrites only if it has legacy syntax — `${{ world.money }}`
			// is a plain interpolation, identical in both engines, so
			// Rewrite is a no-op there too. All four leaves stay
			// byte-identical.
			wantCount: 0,
			wantContain: []string{
				`message_rumpled: "{{ world.disturbance > 2 }}"`,
				`money: "{{ world.money - 5 }}"`,
				`say: "Cash: ${{ world.money }}"`,
				`when: "world.money > 0"`,
			},
		},
		{
			// Under the pragmatic split: set: RHS stays on expr-lang
			// (typed values via expr.RenderValue). The codemod must
			// NOT rewrite these — every leaf survives byte-identical
			// even though it contains ternary / `??` syntax.
			name: "ternary_and_nullish_in_set_rhs_left_alone",
			src: `states:
  store:
    on:
      buy:
        - target: store
          effects:
            - set:
                money: "{{ int(slots.cost ?? 0) }}"
                tier: "{{ world.kind == 'gold' ? 'A' : 'B' }}"
              when: "slots.kind != ''"
`,
			wantCount: 0,
			wantContain: []string{
				`money: "{{ int(slots.cost ?? 0) }}"`,
				`tier: "{{ world.kind == 'gold' ? 'A' : 'B' }}"`,
				`when: "slots.kind != ''"`,
			},
		},
		{
			// `??` and ternary inside a view (string-output text)
			// DO get rewritten — same syntax, different field.
			name: "ternary_and_nullish_in_view_rewritten",
			src: `states:
  store:
    view: |
      Cash: {{ slots.foo ?? '(unset)' }} — Tier {{ world.kind == 'gold' ? 'A' : 'B' }}
`,
			wantCount: 1,
			wantContain: []string{
				`Cash: {{ slots.foo|default:'(unset)' }} — Tier {% if world.kind == 'gold' %}A{% else %}B{% endif %}`,
			},
		},
		{
			name: "initial_template_rewritten",
			src: `states:
  bar:
    type: compound
    initial: "{{ world.cloaked ? 'dark' : 'lit' }}"
    states:
      dark:
        description: "dark"
      lit:
        description: "lit"
`,
			wantCount: 1,
			wantContain: []string{
				// Pongo2 has no inline ternary — the whole {{ }} is
				// replaced by a {% if %} block. String branches drop
				// quotes; the block stays inside the YAML string scalar.
				`initial: "{% if world.cloaked %}dark{% else %}lit{% endif %}"`,
			},
		},
		{
			name: "initial_bare_expression_left_alone",
			// When initial: is a bare expression (no `{{ }}` delimiters),
			// it's a pure expr-lang selector — leave it. Rewrite's
			// fast-path (no `{{`) skips it.
			src: `states:
  bar:
    type: compound
    initial: idle
`,
			wantCount:   0,
			wantContain: []string{"initial: idle"},
		},
		{
			name: "literal_block_view_with_helpers",
			src: `states:
  intro:
    view: |
      Choose:
        {{ if available("start_journey") }}- start the journey{{ else }}- ✗ start_journey — {{ blocked_reason("start_journey") }}{{ end }}
`,
			wantCount: 1,
			wantContain: []string{
				`{% if available("start_journey") %}- start the journey{% else %}- ✗ start_journey — {{ blocked_reason("start_journey") }}{% endif %}`,
			},
			wantAbsent: []string{
				"{{ if available",
				"{{ end }}",
			},
		},
		{
			name: "comment_preservation",
			src: `# top-of-file comment
states:
  # state comment
  foyer:
    view: |
      {{ if x }}A{{ end }}  # not really a YAML comment — inside the block
    on:
      go:
        # transition comment
        - target: bar       # inline comment
          when: "true"      # expr-lang guard
`,
			wantCount: 1,
			wantContain: []string{
				"# top-of-file comment",
				"# state comment",
				"# transition comment",
				"# inline comment",
				"# expr-lang guard",
				"{% if x %}A{% endif %}",
			},
		},
		{
			name: "idempotent_after_first_pass",
			src: `states:
  foyer:
    view: |
      {% if world.cloaked %}Dark.{% else %}Lit.{% endif %}
`,
			wantCount: 0,
			wantContain: []string{
				"{% if world.cloaked %}Dark.{% else %}Lit.{% endif %}",
			},
		},
		{
			name: "literal_values_in_intents_left_alone",
			// intents/<name>/slots/<slot>/values are literal lists, not
			// templated. The top-level "intents" key is excluded
			// outright by isTemplatedField — this just confirms
			// nothing under it gets rewritten even when {{ appears
			// nowhere (no false-positive rewrites).
			src: `intents:
  go:
    slots:
      direction:
        values: [north, south, east, west]
`,
			wantCount:   0,
			wantContain: []string{"values: [north, south, east, west]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, n, err := MigrateFile("fixture.yaml", []byte(tc.src))
			if err != nil {
				t.Fatalf("MigrateFile error: %v", err)
			}
			if n != tc.wantCount {
				t.Errorf("count = %d, want %d\noutput:\n%s", n, tc.wantCount, string(out))
			}
			outStr := string(out)
			for _, want := range tc.wantContain {
				if !strings.Contains(outStr, want) {
					t.Errorf("expected output to contain %q\nfull output:\n%s", want, outStr)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(outStr, absent) {
					t.Errorf("expected output to NOT contain %q\nfull output:\n%s", absent, outStr)
				}
			}
		})
	}
}

// TestMigrateSampleFixture runs the migrator against testdata/sample.yaml
// and compares the output to the hand-curated testdata/sample.expected.yaml.
// The fixture is the canonical "everything works together" check: it
// exercises every row in the §3.1 translation table within realistic
// YAML field shapes (literal-block view, ternary in set RHS,
// nullish-coalesce in with: arg, range with `.field`, bare-expression
// initial: left alone, when: guards left alone, bind: keys left alone,
// inline comments + head comments preserved verbatim).
func TestMigrateSampleFixture(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "sample.yaml"))
	if err != nil {
		t.Fatalf("read sample.yaml: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "sample.expected.yaml"))
	if err != nil {
		t.Fatalf("read sample.expected.yaml: %v", err)
	}
	out, n, err := MigrateFile("sample.yaml", src)
	if err != nil {
		t.Fatalf("MigrateFile: %v", err)
	}
	if n == 0 {
		t.Fatalf("sample.yaml: expected at least one rewrite, got 0")
	}
	if string(out) != string(expected) {
		t.Errorf("output does not match sample.expected.yaml\n--- got ---\n%s\n--- want ---\n%s", out, expected)
	}
	// Idempotency at the file-fixture level.
	out2, n2, err := MigrateFile("sample.yaml", out)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second pass rewrote %d leaves — expected 0 (idempotent)", n2)
	}
	if string(out2) != string(out) {
		t.Errorf("second-pass output differs from first-pass output")
	}
}

// TestMigrateFileIdempotent confirms that running MigrateFile twice on the
// same source produces identical output on the second pass — the
// load-bearing property the proposal calls out for the codemod.
func TestMigrateFileIdempotent(t *testing.T) {
	src := []byte(`states:
  intro:
    view: |
      Party of {{ world.party_size }}:
        1. {{ if world.party_member_1 != "" }}{{ world.party_member_1 }} (leader){{ else }}(unnamed){{ end }}
      Profession: {{ if world.profession != nil }}{{ world.profession }}{{ else }}(not yet chosen){{ end }}
      Tier: {{ world.kind == 'gold' ? 'A' : 'B' }}
    on:
      go:
        - when: "slots.direction == 'east'"
          target: foyer
          effects:
            - say: "Off you go: {{ slots.direction }}."
        - default: true
          target: intro
          guard_hint: "Cash: ${{ world.money }} — {{ slots.note ?? '(no note)' }}."
          effects:
            - set:
                cost: "{{ int(slots.cost ?? 0) }}"
                tier: "{{ world.kind == 'gold' ? 'A' : 'B' }}"
`)
	first, n1, err := MigrateFile("a", src)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if n1 == 0 {
		t.Fatalf("first pass made no rewrites — expected at least one")
	}
	second, n2, err := MigrateFile("a", first)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second pass rewrote %d leaves — expected 0 (idempotent)", n2)
	}
	if string(first) != string(second) {
		t.Errorf("second-pass output differs from first-pass output:\nFIRST:\n%s\nSECOND:\n%s", first, second)
	}
}
