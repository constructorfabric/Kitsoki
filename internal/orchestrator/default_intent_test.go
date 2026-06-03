// Tests for the deterministic free-text tier (default_intent): when an
// utterance matches no intent deterministically or semantically, a state that
// declares default_intent sinks the whole input into that intent's single
// required string slot — without calling the main-turn LLM. A command the
// operator does name still wins in the earlier semantic tier, and a state
// without default_intent falls through to the harness exactly as before.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func newDefaultIntentApp(t *testing.T, withDefault bool) (*orchestrator.Orchestrator, *countingHarness, store.Store, app.SessionID) {
	t.Helper()
	defaultLine := ""
	if withDefault {
		defaultLine = "    default_intent: discuss\n"
	}
	appYAML := `
app:
  id: default-intent-test
  version: 0.1.0
world:
  last_message: { type: string, default: "" }
routing:
  enabled: true
intents:
  discuss:
    title: "Discuss"
    slots:
      message: { type: string, required: true }
  quit:
    title: "Quit"
    synonyms: ["quit"]
root: chat
states:
  chat:
    mode: conversational
` + defaultLine + `    view: "chat msg={{ world.last_message }}"
    on:
      discuss:
        - target: .
          effects:
            - set:
                last_message: "{{ slots.message }}"
      quit:
        - target: ended
  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	// Fallback routes to quit so a harness-handled turn has a sane outcome.
	h := &countingHarness{fall: staticHarness{intentName: "quit"}}
	orch := orchestrator.New(def, m, s, h)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, h, s, sid
}

// TestDefaultIntent_UnmatchedFreeTextRoutesToDefault is the core fix: prose that
// matches no command routes to `discuss` with the whole utterance as
// slots.message, deterministically, without the harness.
func TestDefaultIntent_UnmatchedFreeTextRoutesToDefault(t *testing.T) {
	t.Parallel()
	orch, h, s, sid := newDefaultIntentApp(t, true)
	ctx := context.Background()

	const msg = "this doc — what about the open file?"
	out, err := orch.Turn(ctx, sid, msg)
	require.NoError(t, err)
	require.EqualValues(t, 0, h.calls.Load(),
		"default tier must resolve free text without the main-turn LLM")
	require.Equal(t, app.StatePath("chat"), out.NewState, "discuss is a self-loop (target: .)")
	require.Contains(t, out.View, "msg="+msg,
		"the whole utterance must fill slots.message and reach the effect")

	// Provenance: the turn must record that the default tier routed it.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	assertRoutedBy(t, history, "default")
}

// TestDefaultIntent_NamedCommandStillWins — a command the operator names ("quit")
// resolves in the semantic tier before the default tier is reached.
func TestDefaultIntent_NamedCommandStillWins(t *testing.T) {
	t.Parallel()
	orch, h, _, sid := newDefaultIntentApp(t, true)
	ctx := context.Background()

	out, err := orch.Turn(ctx, sid, "quit")
	require.NoError(t, err)
	require.EqualValues(t, 0, h.calls.Load(), "synonym 'quit' resolves in the semantic tier")
	require.Equal(t, app.StatePath("ended"), out.NewState,
		"named command must win over the free-text default")
}

// TestDefaultIntent_AbsentFallsThroughToHarness — without default_intent the
// state behaves as before: unmatched prose falls through to the main-turn LLM.
func TestDefaultIntent_AbsentFallsThroughToHarness(t *testing.T) {
	t.Parallel()
	orch, h, _, sid := newDefaultIntentApp(t, false)
	ctx := context.Background()

	_, err := orch.Turn(ctx, sid, "this doc — what about the open file?")
	require.NoError(t, err)
	require.Positive(t, h.calls.Load(),
		"without default_intent, unmatched prose must fall through to the harness")
}

func assertRoutedBy(t *testing.T, history []store.Event, want string) {
	t.Helper()
	var found bool
	for _, ev := range history {
		if ev.Kind != store.TurnStarted {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		if p["routed_by"] == nil {
			continue
		}
		found = true
		require.Equal(t, want, p["routed_by"], "TurnStarted must record the resolving tier")
	}
	require.True(t, found, "a TurnStarted event carrying routing provenance must appear")
}
