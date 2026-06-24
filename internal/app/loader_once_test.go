package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOnceEffectValidation covers the load-time invariant for `once:` on an
// invoke effect: it requires a non-empty bind: (the bind target is the cache
// that arms the skip — once: with nothing to cache is meaningless). Mirrors
// the symmetric `background: true requires invoke:` invariant.
func TestOnceEffectValidation(t *testing.T) {
	t.Run("once_without_bind_rejected", func(t *testing.T) {
		yaml := `
app:
  id: once-test
  version: 0.1.0
root: start
hosts:
  - host.foo
intents:
  go:
    title: "Go"
states:
  start:
    on_enter:
      - invoke: host.foo
        once: true
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.Error(t, err, "once: true without bind: should be rejected")
		require.True(t, strings.Contains(err.Error(), "once: true requires a non-empty bind:"),
			"error should mention 'once: true requires a non-empty bind:', got: %s", err.Error())
	})

	t.Run("once_with_bind_loads", func(t *testing.T) {
		yaml := `
app:
  id: once-test
  version: 0.1.0
root: start
hosts:
  - host.foo
intents:
  go:
    title: "Go"
world:
  result: { type: object, default: {} }
states:
  start:
    on_enter:
      - invoke: host.foo
        once: true
        bind:
          result: submitted
    on:
      go:
        - target: start
`
		_, err := LoadBytes([]byte(yaml))
		require.NoError(t, err, "once: true with a non-empty bind: should load clean")
	})
}
