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
