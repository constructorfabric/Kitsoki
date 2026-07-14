package app

// Tests for the Phase 1 bash_profile / external_side_effect loader additions.
// These exercise:
//   - BashProfileDecl YAML parsing (three forms)
//   - external_side_effect inference and declaration parsing
//   - agentsForContext field propagation (tested via the types, not orchestrator)

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/effect"
)

// TestBashProfileDecl_ReadOnly verifies that "read-only" string form parses.
func TestBashProfileDecl_ReadOnly(t *testing.T) {
	yaml := `app:
  id: bp-test
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  inspector:
    system_prompt: "inspect"
    tools: [Read, Grep]
    bash_profile: read-only
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["inspector"]
	require.NotNil(t, a.BashProfile)
	require.Equal(t, BashProfileReadOnly, a.BashProfile.Kind)
	require.Nil(t, a.BashProfile.Commands)
}

// TestBashProfileDecl_Commands verifies the map form with a commands list.
func TestBashProfileDecl_Commands(t *testing.T) {
	yaml := `app:
  id: bp-cmds
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  ci-diag:
    system_prompt: "diagnose"
    tools: [Bash]
    bash_profile:
      commands: [git, jq, grep, kubectl]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["ci-diag"]
	require.NotNil(t, a.BashProfile)
	require.Equal(t, BashProfileCommands, a.BashProfile.Kind)
	require.Equal(t, []string{"git", "jq", "grep", "kubectl"}, a.BashProfile.Commands)
}

// TestBashProfileDecl_SandboxWrite verifies the sandboxed_write form.
func TestBashProfileDecl_SandboxWrite(t *testing.T) {
	yaml := `app:
  id: bp-sandbox
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  builder:
    system_prompt: "build"
    tools: [Bash]
    bash_profile:
      sandboxed_write: /tmp/scratch
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["builder"]
	require.NotNil(t, a.BashProfile)
	require.Equal(t, BashProfileSandboxWrite, a.BashProfile.Kind)
	require.Equal(t, "/tmp/scratch", a.BashProfile.ScratchDir)
}

// TestBashProfileDecl_SandboxWrite_NoDir verifies sandboxed_write without a dir.
func TestBashProfileDecl_SandboxWrite_NoDir(t *testing.T) {
	yaml := `app:
  id: bp-sandbox-nodir
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  builder:
    system_prompt: "build"
    tools: [Bash]
    bash_profile:
      sandboxed_write: ""
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["builder"]
	require.NotNil(t, a.BashProfile)
	require.Equal(t, BashProfileSandboxWrite, a.BashProfile.Kind)
	require.Equal(t, "", a.BashProfile.ScratchDir)
}

// TestExternalSideEffect_InferredFalse verifies that when external_side_effect
// is absent and tools have no network capability, it is inferred as false.
func TestExternalSideEffect_InferredFalse(t *testing.T) {
	yaml := `app:
  id: ese-false
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  file-only:
    system_prompt: "file ops"
    tools: [Read, Edit, Write, Bash]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["file-only"]
	require.NotNil(t, a.ExternalSideEffect)
	require.False(t, *a.ExternalSideEffect, "file-only tools should infer external_side_effect=false")
}

// TestExternalSideEffect_InferredTrue_WebFetch verifies that WebFetch in the
// tool list causes external_side_effect to be inferred as true.
func TestExternalSideEffect_InferredTrue_WebFetch(t *testing.T) {
	yaml := `app:
  id: ese-webfetch
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  web-agent:
    system_prompt: "web search"
    tools: [Read, WebFetch]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["web-agent"]
	require.NotNil(t, a.ExternalSideEffect)
	require.True(t, *a.ExternalSideEffect, "WebFetch should infer external_side_effect=true")
}

// TestExternalSideEffect_DeclaredMatches verifies that an explicit declaration
// that matches the inference loads without warning (no loader error).
func TestExternalSideEffect_DeclaredMatches(t *testing.T) {
	yaml := `app:
  id: ese-match
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  pr-pusher:
    system_prompt: "push pr"
    tools: [Read, Edit, WebFetch]
    external_side_effect: true
`
	_, err := LoadBytes([]byte(yaml))
	require.NoError(t, err, "matching declaration and inference should load cleanly")
}

// TestExternalSideEffect_DeclaredReadContradictsNetworkTool_HardFails
// verifies the effect-taxonomy.md load-time hard-fail: declaring
// external_side_effect: false (which maps to effect: read, since no mutator
// tool is present) while the tool surface includes WebFetch/WebSearch (a
// network tool, joining to effect: external) is a load ERROR, not a
// warn-line — the teeth the old boolean never had. This supersedes the
// pre-taxonomy behavior (a bare warn), which could not distinguish a
// genuinely read-only agent from one that just claimed to be.
func TestExternalSideEffect_DeclaredReadContradictsNetworkTool_HardFails(t *testing.T) {
	yaml := `app:
  id: ese-disagree
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  suspicious:
    system_prompt: "suspicious"
    tools: [Read, WebFetch]
    external_side_effect: false
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "declaring read-tier posture over a network-tool surface must hard-fail")
	require.Contains(t, err.Error(), "suspicious")
}

// TestEffect_DeclaredOverPrivileged_WarnsNotErrors verifies that declaring a
// MORE privileged effect than the tool surface justifies (external, but the
// surface only joins to write) is a warn-line, not a load error — an author
// over-claiming privilege is a safe, if noisy, mistake, unlike under-claiming
// it.
func TestEffect_DeclaredOverPrivileged_WarnsNotErrors(t *testing.T) {
	yaml := `app:
  id: ese-overclaim
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  cautious:
    system_prompt: "cautious"
    tools: [Read, Edit, Write]
    external_side_effect: true
`
	_, err := LoadBytes([]byte(yaml))
	require.NoError(t, err, "over-declaring privilege should warn, not error")
}

// TestEffect_ExplicitFieldTakesPrecedenceAndValidates verifies the new
// `effect:` field parses, resolves, and rejects an unrecognised value.
func TestEffect_ExplicitFieldTakesPrecedenceAndValidates(t *testing.T) {
	yaml := `app:
  id: ese-explicit
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  writer:
    system_prompt: "writer"
    tools: [Read, Edit, Write]
    effect: write
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["writer"]
	require.Equal(t, effect.Write, a.Effect)
	require.NotNil(t, a.ExternalSideEffect)
	require.False(t, *a.ExternalSideEffect, "write-tier mirrors to external_side_effect=false")

	yaml2 := `app:
  id: ese-badenum
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  writer:
    system_prompt: "writer"
    effect: sideways
`
	_, err = LoadBytes([]byte(yaml2))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sideways")
}

// TestEffect_BothFieldsDeclaredIsLoadError verifies that effect: and the
// deprecated external_side_effect: are mutually exclusive.
func TestEffect_BothFieldsDeclaredIsLoadError(t *testing.T) {
	yaml := `app:
  id: ese-both
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  confused:
    system_prompt: "confused"
    effect: read
    external_side_effect: false
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "confused")
}

// TestBashProfile_NilWhenAbsent verifies that an agent without bash_profile
// loads with a nil BashProfile.
func TestBashProfile_NilWhenAbsent(t *testing.T) {
	yaml := `app:
  id: no-bp
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  simple:
    system_prompt: "simple"
    tools: [Read]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["simple"]
	require.Nil(t, a.BashProfile, "no bash_profile in YAML should yield nil BashProfile")
}
