package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// offramp_capture_test.go — stateless unit coverage for the
// agent_off_ramp.capture_free_text loader desugaring pass
// (offramp_capture.go): the synthesized <room>_discuss intent, the no-op
// self-arc, the default_intent wiring, and the load-time invariants.

const captureBaseYAML = `app:
  id: capture-test
  version: 0.1.0
agents:
  helper:
    system_prompt: "You are the room helper."
intents:
  look:
    title: Look
    description: "Look around."
root: hub
states:
  hub:
    description: "The hub room."
    view: "Welcome to the hub."
    relevant_world: [status]
    agent_off_ramp: { agent: helper, capture_free_text: true }
    on:
      look: [{ target: hub }]
world:
  status: { type: string, default: "idle" }
`

func TestOffRampCapture_DesugarsExpectedShape(t *testing.T) {
	def, err := LoadBytes([]byte(captureBaseYAML))
	require.NoError(t, err)

	st := def.States["hub"]
	require.NotNil(t, st)
	require.NotNil(t, st.AgentOffRamp)
	require.True(t, st.AgentOffRamp.CaptureFreeText)

	// Synthesized top-level intent with the one-required-string-slot contract
	// routeViaDefaultIntent demands.
	capture, ok := def.Intents["hub_discuss"]
	require.True(t, ok, "hub_discuss must be registered top-level")
	slot, ok := capture.Slots["message"]
	require.True(t, ok)
	require.True(t, slot.Required)
	require.Equal(t, "string", slot.Type)

	// default_intent points at the synthesized name.
	require.Equal(t, "hub_discuss", st.DefaultIntent)

	// No-op self-arc keeps the intent legal for validation/menus/flows.
	arcs, ok := st.On["hub_discuss"]
	require.True(t, ok)
	require.Len(t, arcs, 1)
	require.Equal(t, "hub", arcs[0].Target)
	require.Empty(t, arcs[0].Effects, "the arc must be a pure no-op — live surfaces divert before it fires")
}

func TestOffRampCapture_PlainOffRampUntouched(t *testing.T) {
	yaml := strings.Replace(captureBaseYAML,
		"agent_off_ramp: { agent: helper, capture_free_text: true }",
		"agent_off_ramp: { agent: helper }", 1)
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)

	st := def.States["hub"]
	require.NotNil(t, st.AgentOffRamp)
	require.False(t, st.AgentOffRamp.CaptureFreeText)
	require.Empty(t, st.DefaultIntent)
	_, exists := def.Intents["hub_discuss"]
	require.False(t, exists, "no capture, no synthesized intent")
}

func TestOffRampCapture_ConflictsWithHandAuthoredDefaultIntent(t *testing.T) {
	yaml := strings.Replace(captureBaseYAML,
		"agent_off_ramp: { agent: helper, capture_free_text: true }",
		"agent_off_ramp: { agent: helper, capture_free_text: true }\n    default_intent: look", 1)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot combine with hand-authored default_intent")
}

func TestOffRampCapture_CollidesWithDeclaredIntent(t *testing.T) {
	yaml := strings.Replace(captureBaseYAML,
		"intents:\n  look:",
		"intents:\n  hub_discuss:\n    title: Mine\n    description: \"An author-declared name that collides.\"\n    slots:\n      message: { type: string, required: true }\n  look:", 1)
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "collides with a declared intent")
}
