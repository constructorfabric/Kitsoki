package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationValidation(t *testing.T) {
	t.Run("commit_outside_operation_rejected", func(t *testing.T) {
		yaml := `
app: { id: op-test, version: 0.1.0 }
root: start
intents: { go: {} }
states:
  start:
    on:
      go:
        - target: start
          effects:
            - commit_operation:
                world: { result: "ok" }
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "commit_operation is only valid inside a state with operation:"), err.Error())
	})

	t.Run("persist_draft_requires_id", func(t *testing.T) {
		yaml := `
app: { id: op-test, version: 0.1.0 }
root: start
intents: { go: {} }
states:
  start:
    operation: { scope: demo }
    on:
      go:
        - target: start
          effects:
            - persist_draft:
                world: [result]
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "persist_draft requires id:"), err.Error())
	})
}

func TestOperationPolicyValidation(t *testing.T) {
	t.Run("valid_policy_and_transition_reference_load", func(t *testing.T) {
		yaml := `
app: { id: op-policy-test, version: 0.1.0 }
root: start
operations:
  demo_run:
    title: "Demo run"
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    stop_on: [needs-human, host-error]
    pause_on: [operator-ask]
    terminal_artifact: done_artifact
    phase_summary:
      from: [done_artifact]
intents: { go: {} }
states:
  start:
    on:
      go:
        - target: done
          operation: demo_run
  done:
    terminal: true
`
		def, err := LoadBytes([]byte(yaml))
		require.NoError(t, err)
		require.Equal(t, "Demo run", def.Operations["demo_run"].Title)
		require.Equal(t, "demo_run", def.States["start"].On["go"][0].Operation)
	})

	t.Run("transition_reference_must_exist", func(t *testing.T) {
		yaml := `
app: { id: op-policy-test, version: 0.1.0 }
root: start
intents: { go: {} }
states:
  start:
    on:
      go:
        - target: start
          operation: missing_run
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err)
		require.Contains(t, err.Error(), `operation "missing_run" is not declared`)
	})

	t.Run("mode_is_typed", func(t *testing.T) {
		yaml := `
app: { id: op-policy-test, version: 0.1.0 }
root: start
operations:
  bad:
    mode: daemon
intents: { go: {} }
states:
  start:
    on:
      go: [{ target: start }]
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err)
		require.Contains(t, err.Error(), `operations.bad.mode "daemon" is invalid`)
	})
}
