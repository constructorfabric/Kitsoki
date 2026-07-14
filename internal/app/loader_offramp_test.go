package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOffRamp_BareScalar asserts the bare `agent_off_ramp: true` form loads
// onto an enabled, voiceless OffRampDef (the off-path voice is adopted at
// runtime).
func TestOffRamp_BareScalar(t *testing.T) {
	yaml := `app:
  id: offramp-bare
  version: 0.1.0
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "Tell me about the idea you want to explore."
    agent_off_ramp: true
    on:
      look: [{ target: idea }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	st := def.States["idea"]
	require.NotNil(t, st.AgentOffRamp, "bare true should yield an enabled off-ramp")
	require.True(t, st.AgentOffRamp.Enabled())
	require.Equal(t, "", st.AgentOffRamp.Agent)
	require.Equal(t, "", st.AgentOffRamp.Persona)
}

// TestOffRamp_StructForm asserts the {agent, persona, banner} mapping form
// loads its fields and that the named agent resolves cleanly.
func TestOffRamp_StructForm(t *testing.T) {
	yaml := `app:
  id: offramp-struct
  version: 0.1.0
intents:
  look:
    title: Look
root: discovery
states:
  discovery:
    view: "..."
    agent_off_ramp:
      agent: discovery-guide
      banner: "(thinking it through)"
    on:
      look: [{ target: discovery }]
agents:
  discovery-guide:
    system_prompt: "be a helpful guide"
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	st := def.States["discovery"]
	require.NotNil(t, st.AgentOffRamp)
	require.True(t, st.AgentOffRamp.Enabled())
	require.Equal(t, "discovery-guide", st.AgentOffRamp.Agent)
	require.Equal(t, "(thinking it through)", st.AgentOffRamp.Banner)
}

func TestOffRamp_StructFormAllowsCaptureFreeText(t *testing.T) {
	yaml := `app:
  id: offramp-capture-free-text
  version: 0.1.0
agents:
  discovery-guide:
    model: test
    system_prompt: "Answer questions about the room."
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "..."
    agent_off_ramp:
      agent: discovery-guide
      capture_free_text: true
    on:
      look: [{ target: idea }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	st := def.States["idea"]
	require.NotNil(t, st.AgentOffRamp)
	require.True(t, st.AgentOffRamp.CaptureFreeText)
}

// TestOffRamp_FalseScalarNormalizesToNil asserts that an explicit
// `agent_off_ramp: false` is normalized to a nil pointer so the runtime sees
// no off-ramp (byte-identical to omitting the key).
func TestOffRamp_FalseScalarNormalizesToNil(t *testing.T) {
	yaml := `app:
  id: offramp-false
  version: 0.1.0
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "..."
    agent_off_ramp: false
    on:
      look: [{ target: idea }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	require.Nil(t, def.States["idea"].AgentOffRamp,
		"explicit false must normalize to a nil pointer")
}

// TestOffRamp_AbsentIsNil asserts the default-off contract: no key → nil.
func TestOffRamp_AbsentIsNil(t *testing.T) {
	yaml := `app:
  id: offramp-absent
  version: 0.1.0
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "..."
    on:
      look: [{ target: idea }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	require.Nil(t, def.States["idea"].AgentOffRamp)
}

// TestOffRamp_RejectedOnTerminalState asserts the load-time invariant: an
// off-ramp on a terminal: true state fails to load (Task 2.3).
func TestOffRamp_RejectedOnTerminalState(t *testing.T) {
	yaml := `app:
  id: offramp-terminal
  version: 0.1.0
intents:
  finish:
    title: Finish
root: start
states:
  start:
    view: "go"
    on:
      finish: [{ target: done }]
  done:
    view: "the end"
    terminal: true
    agent_off_ramp: true
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "terminal",
		"off-ramp on a terminal state must be rejected with a clear error")
}

// TestOffRamp_RejectedOnConversationalState asserts the load-time invariant:
// an off-ramp on a mode: conversational state fails to load (Task 2.3).
func TestOffRamp_RejectedOnConversationalState(t *testing.T) {
	yaml := `app:
  id: offramp-conversational
  version: 0.1.0
intents:
  look:
    title: Look
root: chat
states:
  chat:
    view: "let's talk"
    mode: conversational
    agent_off_ramp: true
    on:
      look: [{ target: chat }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversational",
		"off-ramp on a conversational state must be rejected with a clear error")
}

// TestOffRamp_UnknownAgentRejected asserts that an off-ramp naming an agent
// not declared in the top-level agents: map fails to load — the same check
// off_path.agent gets.
func TestOffRamp_UnknownAgentRejected(t *testing.T) {
	yaml := `app:
  id: offramp-bad-agent
  version: 0.1.0
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "..."
    agent_off_ramp:
      agent: nonexistent-guide
    on:
      look: [{ target: idea }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent-guide")
	require.Contains(t, err.Error(), "agent_off_ramp.agent")
}

// TestOffRamp_UnknownKeyRejected asserts the struct form is strict: an
// off-path-only key (trigger:) on the off-ramp fails to load rather than being
// silently ignored.
func TestOffRamp_UnknownKeyRejected(t *testing.T) {
	yaml := `app:
  id: offramp-bad-key
  version: 0.1.0
intents:
  look:
    title: Look
root: idea
states:
  idea:
    view: "..."
    agent_off_ramp:
      trigger: help
    on:
      look: [{ target: idea }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "agent_off_ramp") ||
			strings.Contains(err.Error(), "trigger"),
		"a stray off-path-only key on the off-ramp must fail the load, got: %v", err)
}
