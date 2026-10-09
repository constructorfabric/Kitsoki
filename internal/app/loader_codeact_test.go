// RED gate for goal docs/goals/codeact/GOAL.md G2 ("Verb wiring") and
// decomposition.yaml's s2-verb-wiring slice: story authors declare a
// `host.agent.codeact` invoke with a structured `with.capabilities` grant,
// validated at LOAD time against a registered builtin capability set, and
// task/converse-only knobs (e.g. `sandbox:`) must be rejected on codeact.
//
// None of this exists yet: `codeact` is not a recognized shortVerb in
// checkAgentEffect/validateAgentVerbCrossChecks (internal/app/loader.go,
// ~line 2270-2300), so today `host.agent.codeact` effects sail through load
// with zero validation. These tests define the target contract for whoever
// implements the loader half of s2. Do not implement the validation here —
// this file only pins the RED tests.
package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// codeactBaseYAML returns a minimal, otherwise-valid app declaring a single
// host.agent.codeact effect. Callers splice in the field under test (an
// unknown capability, a sandbox block, etc). Mirrors sandboxBaseYAML's
// "single template, string.Replace" convention in loader_sandbox_test.go.
func codeactBaseYAML(withExtra string) string {
	return `app:
  id: codeact-load-test
  version: 0.1.0
hosts:
  - host.agent.codeact
root: start
agents:
  coder:
    system_prompt: "act via scoped starlark"
states:
  start:
    on_enter:
      - invoke: host.agent.codeact
        with:
          agent: coder
          goal: "compute something useful"
          budget: 5
          schema: schemas/codeact_out.json
` + withExtra
}

// TestLoad_CodeactUnknownCapability_Rejected asserts that an unrecognized
// entry in with.capabilities fails load, mirroring the existing host
// allow-list pattern (TestLoad_AgentRefUnknown) and the sandbox-block
// allow-list pattern (TestSandboxBlockRejectsUnknownStrength).
//
// Assumed error-string contract for the implementer: the message must name
// both the literal string "capability" (so the failure class is obvious in
// a multi-error join) and the offending value "not_a_real_capability" (so a
// typo is diagnosable without re-reading the YAML). Exact prefix/wording is
// not pinned — only that both substrings appear together in one message,
// analogous to `with.agent %q is not declared in agents`.
func TestLoad_CodeactUnknownCapability_Rejected(t *testing.T) {
	yaml := codeactBaseYAML(`          capabilities:
            not_a_real_capability: true
`)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "an unknown with.capabilities entry must fail load, not silently no-op at runtime")
	require.Contains(t, err.Error(), "capability")
	require.Contains(t, err.Error(), "not_a_real_capability")
}

func TestLoad_CodeactListCapabilities_Rejected(t *testing.T) {
	yaml := codeactBaseYAML(`          capabilities: ["world", "vcs"]
`)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "top-level capability lists are not part of the opt-in schema")
	require.Contains(t, err.Error(), "capabilities")
	require.Contains(t, err.Error(), "mapping")
}

// TestLoad_CodeactRejectsSandboxKnob asserts that a sandbox: block — valid
// only for host.agent.task/host.agent.converse today (checkAgentEffect's
// `if shortVerb == "task" || shortVerb == "converse"` guard at
// internal/app/loader.go ~line 2281) — is rejected when attached to a
// host.agent.codeact effect, because codeact's own bounded-Starlark-loop
// sandboxing (internal/host/codeact) is a different mechanism than the
// subprocess-agent sandbox: knob and mixing the two is a load-time author
// error, not a silently-ignored knob.
//
// Assumed error-string contract for the implementer: the message must
// mention "sandbox" and "codeact" together (e.g. something like
// `sandbox: is not valid for host.agent.codeact — sandbox is a task/converse
// -only knob`), so an author who copy-pastes a task-shaped with: block onto
// a codeact effect gets pointed at the right fix.
func TestLoad_CodeactRejectsSandboxKnob(t *testing.T) {
	yaml := codeactBaseYAML(`          capabilities:
            world: read
          sandbox:
            min_strength: supervised
            rw: [".artifacts/goal"]
`)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "sandbox: is a task/converse-only knob and must be rejected on host.agent.codeact")
	require.Contains(t, err.Error(), "sandbox")
	require.Contains(t, err.Error(), "codeact")
}

// TestLoad_CodeactValidCapabilities_Accepted is the positive control: a
// codeact effect whose with.capabilities grants the sanctioned v1 builtin set
// and carries no sandbox: block should load cleanly. The v1 set here is world,
// vcs, and http: ctx.world.get already exists per internal/host/codeact's
// executor doc comment, and vcs/http are the next agent-facing proxies.
//
// Investigated: does this currently pass vacuously? Yes — checkAgentEffect
// only special-cases shortVerb == "task"/"converse"/"ask"/"decide"/"extract"
// (internal/app/loader.go's switch), so a bare host.agent.codeact effect
// with no recognized cross-check today falls through every branch as a
// no-op and LoadBytes returns no error. That means this test is CURRENTLY
// GREEN, not RED — but it is not testing nothing: it pins (a) that the
// loader must keep accepting this exact shape once validation lands, and
// (b) via the AppDef assertions below, that the effect actually parsed with
// invoke == "host.agent.codeact" and preserved the structured capability grant,
// which is the concrete surface the implementer must not regress when they
// add the capability allow-list check for the unknown-capability case above.
func TestLoad_CodeactValidCapabilities_Accepted(t *testing.T) {
	yaml := codeactBaseYAML(`          capabilities:
            world: read
            vcs: read
            http:
              methods: [GET]
`)
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err, "a codeact effect using only the sanctioned v1 capability set must load cleanly")
	require.NotNil(t, def)

	start := def.States["start"]
	require.NotNil(t, start, "start state must parse")
	require.Len(t, start.OnEnter, 1)

	eff := start.OnEnter[0]
	require.Equal(t, "host.agent.codeact", eff.Invoke)
	caps, ok := eff.With["capabilities"].(map[string]any)
	require.True(t, ok, "with.capabilities must round-trip as a mapping")
	require.Equal(t, "read", caps["world"])
	require.Equal(t, "read", caps["vcs"])
	httpCaps, ok := caps["http"].(map[string]any)
	require.True(t, ok, "http grant must round-trip as a mapping")
	require.ElementsMatch(t, []any{"GET"}, httpCaps["methods"])
}
