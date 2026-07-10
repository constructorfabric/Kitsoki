package app

// Tests for the AgentDecl.TokenBudget loader wiring (dispatch-context-floor
// task 1.4): YAML parses into TokenBudgetDecl, and an invalid override
// (refuse_tokens < warn_tokens, or warn_tokens <= 0) is a load-time error —
// the same fail-closed posture internal/host's runtime budget gate enforces
// again as a safety net.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAgentDecl_TokenBudget_Valid verifies a well-formed token_budget:
// declaration parses onto AgentDecl.TokenBudget.
func TestAgentDecl_TokenBudget_Valid(t *testing.T) {
	yaml := `app:
  id: tb-valid
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  reviewer:
    system_prompt: "review"
    token_budget:
      warn_tokens: 50000
      refuse_tokens: 120000
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	a := def.Agents["reviewer"]
	require.NotNil(t, a.TokenBudget)
	require.Equal(t, int64(50000), a.TokenBudget.WarnTokens)
	require.Equal(t, int64(120000), a.TokenBudget.RefuseTokens)
}

// TestAgentDecl_TokenBudget_RefuseBelowWarn_LoadTimeError verifies the
// loader rejects a token_budget whose refuse_tokens is below warn_tokens —
// the same invariant the runtime gate enforces at dispatch time.
func TestAgentDecl_TokenBudget_RefuseBelowWarn_LoadTimeError(t *testing.T) {
	yaml := `app:
  id: tb-invalid
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  reviewer:
    system_prompt: "review"
    token_budget:
      warn_tokens: 100000
      refuse_tokens: 10000
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "token_budget")
}

// TestAgentDecl_TokenBudget_ZeroWarn_LoadTimeError verifies warn_tokens must
// be positive.
func TestAgentDecl_TokenBudget_ZeroWarn_LoadTimeError(t *testing.T) {
	yaml := `app:
  id: tb-zero-warn
  version: 0.1.0
root: foyer
states:
  foyer:
    view: "hi"
agents:
  reviewer:
    system_prompt: "review"
    token_budget:
      warn_tokens: 0
      refuse_tokens: 10000
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "token_budget")
}
