package app

// Load-time validation tests for State.DefaultIntent (the free-text sink).
//
// Runtime routing is covered by
// internal/orchestrator/default_intent_test.go.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultIntent_Valid: a default_intent that names a reachable intent with
// exactly one required string slot loads cleanly.
func TestDefaultIntent_Valid(t *testing.T) {
	const yamlSrc = `
app:
  id: default-intent-ok
  version: 0.1.0
world: {}
intents:
  discuss:
    slots:
      message: { type: string, required: true }
  quit: {}
root: chat
states:
  chat:
    mode: conversational
    default_intent: discuss
    on:
      discuss:
        - target: .
      quit:
        - target: ended
  ended:
    terminal: true
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)
}

// TestDefaultIntent_NoArcRejected: default_intent must be reachable from the
// state (have an on: arc).
func TestDefaultIntent_NoArcRejected(t *testing.T) {
	const yamlSrc = `
app:
  id: default-intent-noarc
  version: 0.1.0
world: {}
intents:
  discuss:
    slots:
      message: { type: string, required: true }
  quit: {}
root: chat
states:
  chat:
    default_intent: discuss
    on:
      quit:
        - target: ended
  ended:
    terminal: true
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err)
	require.Contains(t, err.Error(), "default_intent")
	require.Contains(t, err.Error(), "on: arc")
}

// TestDefaultIntent_WrongSlotShapeRejected: the named intent must declare
// exactly one required string slot (here it has none).
func TestDefaultIntent_WrongSlotShapeRejected(t *testing.T) {
	const yamlSrc = `
app:
  id: default-intent-slotless
  version: 0.1.0
world: {}
intents:
  discuss: {}
root: chat
states:
  chat:
    default_intent: discuss
    on:
      discuss:
        - target: .
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err)
	require.Contains(t, err.Error(), "default_intent")
	require.Contains(t, err.Error(), "required string slot")
}

// TestDefaultIntent_NonStringSlotRejected: a single required slot of the wrong
// type cannot receive the free-text utterance.
func TestDefaultIntent_NonStringSlotRejected(t *testing.T) {
	const yamlSrc = `
app:
  id: default-intent-intslot
  version: 0.1.0
world: {}
intents:
  discuss:
    slots:
      count: { type: number, required: true }
root: chat
states:
  chat:
    default_intent: discuss
    on:
      discuss:
        - target: .
`
	_, err := LoadBytes([]byte(yamlSrc))
	require.Error(t, err)
	require.Contains(t, err.Error(), "required string slot")
}
