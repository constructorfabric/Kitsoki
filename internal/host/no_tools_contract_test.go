package host

import (
	"strings"
	"testing"
)

// An empty enforced toolset must fold the no-tools contract into the persona
// (instruction-level enforcement for backends whose intrinsic shell survives
// the sandbox bypass); any non-empty toolset must leave the persona untouched.
func TestApplyNoToolsContract(t *testing.T) {
	base := Agent{SystemPrompt: "You are a quality gate."}

	got := applyNoToolsContract(base, ToolboxEnforcement{AllowedTools: nil})
	if !strings.Contains(got.SystemPrompt, "NO workspace tools") {
		t.Fatalf("empty toolset: contract missing from persona: %q", got.SystemPrompt)
	}
	if !strings.HasPrefix(got.SystemPrompt, "You are a quality gate.") {
		t.Fatalf("persona must stay the stable prefix, contract appended: %q", got.SystemPrompt)
	}

	withTools := applyNoToolsContract(base, ToolboxEnforcement{AllowedTools: []string{"Read"}})
	if strings.Contains(withTools.SystemPrompt, "NO workspace tools") {
		t.Fatalf("non-empty toolset must not carry the contract: %q", withTools.SystemPrompt)
	}

	empty := applyNoToolsContract(Agent{}, ToolboxEnforcement{})
	if !strings.Contains(empty.SystemPrompt, "NO workspace tools") || strings.HasPrefix(empty.SystemPrompt, "\n") {
		t.Fatalf("agent with no persona gets the bare contract: %q", empty.SystemPrompt)
	}
}
