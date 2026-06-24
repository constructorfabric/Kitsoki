package app

// Load-time static expression compile-check tests (validate_exprs.go).
//
// The validator compiles (never evaluates) every effect value and guard
// expression in a loaded app, so a malformed expr-lang expression — most
// notably a pongo-only filter like `{{ x|default:y }}` written into an
// effect value — fails the load with a precise diagnostic instead of
// blowing up mid-turn the first time its transition fires.
//
// Runtime evaluation of these same expressions lives in internal/machine;
// here we only assert the load-time gate.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateExprs_ValidAppLoadsClean: a app whose effects use a single
// typed set: expr, a when: guard, and a multi-interpolation say: template all
// compile cleanly and the app loads without error. Guards against the
// validator false-positiving on perfectly valid authoring.
func TestValidateExprs_ValidAppLoadsClean(t *testing.T) {
	const yamlSrc = `
app:
  id: exprs-valid
  version: 0.1.0
world:
  count: { type: int, default: 0 }
  label: { type: string, default: "" }
intents:
  go: {}
root: start
states:
  start:
    on:
      go:
        - target: end
          when: "world.count > 0"
          effects:
            - set:
                count: "{{ world.count + 1 }}"
                label: "{{ world.label }}-{{ world.count }}"
  end: {}
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err, "valid set/when/multi-interpolation app must load cleanly")
}

// TestValidateExprs_MultiInterpolationNoFalsePositive: a `{{ a }}X{{ b }}`
// set: value is a STRING TEMPLATE, not one typed expr. The validator must
// route it through the template parser (ValidateTemplate) — compiling its
// middle as a single expr would spuriously fail. This is the regression test
// for the RenderValue classification edge.
func TestValidateExprs_MultiInterpolationNoFalsePositive(t *testing.T) {
	const yamlSrc = `
app:
  id: exprs-multi-interp
  version: 0.1.0
world:
  x: { type: string, default: "" }
  out: { type: string, default: "" }
intents:
  go: {}
root: start
states:
  start:
    on:
      go:
        - target: end
          effects:
            - set:
                out: "{{ world.x }}Q{{ world.count }}: {{ world.x }}"
  end: {}
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err, "multi-interpolation template set value must NOT false-positive")
}

// TestValidateExprs_PongoFilterInEffectRejected: `{{ x|default:y }}` is a
// pongo2 filter — valid in a VIEW template but NOT in an effect value, which
// uses expr-lang. expr-lang chokes on the `:` operator. Before this validator
// the bad expression compiled fine as YAML and only failed at runtime, every
// time the transition fired. The load must now fail, naming the state and the
// offending effect key.
func TestValidateExprs_PongoFilterInEffectRejected(t *testing.T) {
	const yamlSrc = `
app:
  id: exprs-pongo-filter
  version: 0.1.0
world:
  answers: { type: string, default: "" }
  x: { type: string, default: "" }
  out: { type: string, default: "" }
intents:
  go: {}
root: start
states:
  start:
    on:
      go:
        - target: end
          effects:
            - set:
                out: "{{ world.answers|default:world.x }}"
  end: {}
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err, "pongo filter in effect value must fail load")
	msg := err.Error()
	require.Contains(t, msg, `state "start"`, "diagnostic should name the state: %v", err)
	require.Contains(t, msg, `set "out"`, "diagnostic should name the effect key: %v", err)
}

// TestValidateExprs_MalformedGuardRejected: a genuinely malformed guard
// expression (dangling operator) must fail the load. when: guards compile via
// CompileBool, matching the runtime.
func TestValidateExprs_MalformedGuardRejected(t *testing.T) {
	const yamlSrc = `
app:
  id: exprs-bad-guard
  version: 0.1.0
world:
  count: { type: int, default: 0 }
intents:
  go: {}
root: start
states:
  start:
    on:
      go:
        - target: end
          when: "world.count >"
  end: {}
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err, "malformed guard must fail load")
	require.Contains(t, err.Error(), `state "start"`, "diagnostic should name the state: %v", err)
	require.Contains(t, err.Error(), "guard when", "diagnostic should identify the guard: %v", err)
}

// TestValidateExprs_AggregatesAllFailures: two distinct broken expressions in
// one app surface in a single load error. Authors fix everything in one pass
// rather than chasing one error at a time.
func TestValidateExprs_AggregatesAllFailures(t *testing.T) {
	const yamlSrc = `
app:
  id: exprs-aggregate
  version: 0.1.0
world:
  a: { type: string, default: "" }
  b: { type: string, default: "" }
intents:
  go: {}
root: start
states:
  start:
    on:
      go:
        - target: end
          when: "world.a >"
          effects:
            - set:
                b: "{{ world.a|default:world.b }}"
  end: {}
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "guard when", "both failures should be reported: %v", err)
	require.Contains(t, msg, `set "b"`, "both failures should be reported: %v", err)
}

// ── view ↔ on_enter bind-target fallback diagnostic ──────────────────────────
//
// These exercise collectViewBindFallbackWarnings directly (the function backing
// the non-fatal validateViewBindFallbacks advisory pass): a view that reads an
// on_enter invoke/bind target world key without a `??`/`| default(...)`
// fallback warns; the same key guarded by a fallback does not; and a world key
// that is NOT a bind target never warns even without a fallback.

// TestViewBindFallback_TableDriven covers the three core cases from the
// proposal plus the external-template skip limitation.
func TestViewBindFallback_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		state     *State
		wantWarns []string // bind-target keys expected to warn (empty = none)
	}{
		{
			name: "bind target referenced without fallback warns",
			state: &State{
				OnEnter: []Effect{{
					Invoke: "host.diff",
					Bind:   map[string]string{"feature_branch_diff": "diff"},
				}},
				View: LegacyView("Diff:\n{{ world.feature_branch_diff }}"),
			},
			wantWarns: []string{"feature_branch_diff"},
		},
		{
			name: "bind target with ?? fallback does not warn",
			state: &State{
				OnEnter: []Effect{{
					Invoke: "host.diff",
					Bind:   map[string]string{"feature_branch_diff": "diff"},
				}},
				View: LegacyView(`Diff:
{{ world.feature_branch_diff ?? "(pending)" }}`),
			},
			wantWarns: nil,
		},
		{
			name: "bind target with default filter does not warn",
			state: &State{
				OnEnter: []Effect{{
					Invoke: "host.diff",
					Bind:   map[string]string{"feature_branch_diff": "diff"},
				}},
				View: LegacyView(`{{ world.feature_branch_diff | default("(pending)") }}`),
			},
			wantWarns: nil,
		},
		{
			name: "non-bind world key without fallback does not warn",
			state: &State{
				OnEnter: []Effect{{
					Invoke: "host.diff",
					Bind:   map[string]string{"feature_branch_diff": "diff"},
				}},
				// References a different (non-bind-target) key.
				View: LegacyView("{{ world.some_other_key }}"),
			},
			wantWarns: nil,
		},
		{
			name: "external template file is skipped (not inline-scannable)",
			state: &State{
				OnEnter: []Effect{{
					Invoke: "host.diff",
					Bind:   map[string]string{"feature_branch_diff": "diff"},
				}},
				View: View{TemplateFile: "diff.pongo"},
			},
			wantWarns: nil,
		},
		{
			name: "no invoke means no bind target collected",
			state: &State{
				// Bind without Invoke is not a host-call bind target.
				OnEnter: []Effect{{
					Bind: map[string]string{"feature_branch_diff": "diff"},
				}},
				View: LegacyView("{{ world.feature_branch_diff }}"),
			},
			wantWarns: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := map[string]*State{"s": tc.state}
			got := collectViewBindFallbackWarnings("", states)
			var gotKeys []string
			for _, w := range got {
				require.Equal(t, "s", w.StatePath)
				gotKeys = append(gotKeys, w.Key)
			}
			require.ElementsMatch(t, tc.wantWarns, gotKeys,
				"warnings mismatch: %#v", got)
		})
	}
}

// TestViewBindFallback_LoadsNonFatal proves the advisory never aborts the load:
// an app with a fallback-less view over a bind-target key still loads cleanly.
func TestViewBindFallback_LoadsNonFatal(t *testing.T) {
	const yamlSrc = `
app:
  id: view-bind-fallback
  version: 0.1.0
world:
  feature_branch_diff: { type: string, default: "(pending)" }
intents:
  go: {}
root: start
states:
  start:
    on_enter:
      - invoke: host.diff
        bind:
          feature_branch_diff: diff
    view: "{{ world.feature_branch_diff }}"
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err, "fallback-less view must load (warning is non-fatal)")
}
