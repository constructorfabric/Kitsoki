package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOnComplete_InvokeAllowList verifies P1-3: an on_complete: invoke: that
// references a host not in the app's hosts: allow-list must be rejected at
// load time.
func TestOnComplete_InvokeAllowList(t *testing.T) {
	yaml := `
app:
  id: allowlist-test
  version: 0.1.0
root: start
hosts:
  - host.run
states:
  start:
    on_enter:
      - invoke: host.run
        background: true
        on_complete:
          - invoke: host.undeclared
    on:
      go:
        - target: start
intents:
  go:
    title: Go
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "on_complete: invoke: referencing undeclared host should fail")
	require.True(t, strings.Contains(err.Error(), "host.undeclared"),
		"error should mention the undeclared host, got: %s", err.Error())
}

// TestProposalExecute_BackgroundValidation verifies P1-4: ProposalExecute with
// background: true but no invoke: should fail at load time.
func TestProposalExecute_BackgroundValidation(t *testing.T) {
	yaml := `
app:
  id: prop-bg-test
  version: 0.1.0
root: start
states:
  start:
    on:
      go:
        - target: start
intents:
  go:
    title: Go
proposals:
  my_prop:
    execute:
      background: true
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "proposal execute with background: true and no invoke: should fail")
	require.True(t, strings.Contains(err.Error(), "background: true requires invoke:"),
		"error should mention 'background: true requires invoke:', got: %s", err.Error())
}

// TestProposalExecute_OnCompleteBackgroundRejected verifies P1-4: a proposal's
// on_complete: must not contain background: true.
func TestProposalExecute_OnCompleteBackgroundRejected(t *testing.T) {
	yaml := `
app:
  id: prop-bg-test2
  version: 0.1.0
root: start
hosts:
  - host.foo
  - host.bar
states:
  start:
    on:
      go:
        - target: start
intents:
  go:
    title: Go
proposals:
  my_prop:
    execute:
      invoke: host.foo
      background: true
      on_complete:
        - invoke: host.bar
          background: true
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err, "proposal on_complete with background: true should fail")
	require.True(t, strings.Contains(err.Error(), "background: true is not allowed inside on_complete:"),
		"error should mention on_complete restriction, got: %s", err.Error())
}

// TestBackgroundEffectValidation covers the load-time rules for background:
// and on_complete: on Effect (and ProposalExecute.OnComplete).
//
//   - Reject: background: true with no invoke:.
//   - Reject: on_complete: chain containing background: true at top level.
//   - Reject: nested on_complete: chain containing background: true.
func TestBackgroundEffectValidation(t *testing.T) {
	t.Run("background_without_invoke", func(t *testing.T) {
		yaml := `
app:
  id: bg-test
  version: 0.1.0
root: start
states:
  start:
    on_enter:
      - background: true
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err, "background: true without invoke: should be rejected")
		require.True(t, strings.Contains(err.Error(), "background: true requires invoke:"),
			"error should mention 'background: true requires invoke:', got: %s", err.Error())
	})

	t.Run("on_complete_with_background_true", func(t *testing.T) {
		yaml := `
app:
  id: bg-test
  version: 0.1.0
root: start
hosts:
  - host.foo
  - host.bar
states:
  start:
    on_enter:
      - invoke: host.foo
        background: true
        on_complete:
          - invoke: host.bar
            background: true
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err, "background: true inside on_complete: should be rejected")
		require.True(t, strings.Contains(err.Error(), "background: true is not allowed inside on_complete:"),
			"error should mention 'background: true is not allowed inside on_complete:', got: %s", err.Error())
	})

	t.Run("nested_on_complete_with_background_true", func(t *testing.T) {
		yaml := `
app:
  id: bg-test
  version: 0.1.0
root: start
hosts:
  - host.foo
  - host.bar
states:
  start:
    on_enter:
      - invoke: host.foo
        background: true
        on_complete:
          - set:
              x: "1"
            on_complete:
              - invoke: host.bar
                background: true
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err, "background: true inside nested on_complete: should be rejected")
		require.True(t, strings.Contains(err.Error(), "background: true is not allowed inside on_complete:"),
			"error should mention 'background: true is not allowed inside on_complete:', got: %s", err.Error())
	})

	t.Run("valid_background_with_invoke", func(t *testing.T) {
		yaml := `
app:
  id: bg-test
  version: 0.1.0
root: start
hosts:
  - host.foo
intents:
  go:
    title: Go
states:
  start:
    on_enter:
      - invoke: host.foo
        background: true
        on_complete:
          - set:
              x: "done"
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.NoError(t, err, "valid background effect should load cleanly")
	})
}
