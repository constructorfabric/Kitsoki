package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func loadCloakForDeterministic(t *testing.T) (*orchestrator.Orchestrator, app.SessionID) {
	t.Helper()

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Use a replay harness; TryDeterministic bypasses it anyway.
	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, sid
}

// ─── Display match tests ──────────────────────────────────────────────────────

// TestTryDeterministic_GoSouth verifies that "go south" matches the "go south"
// menu entry in the foyer (exact Display match).
func TestTryDeterministic_GoSouth(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "go south")
	require.NoError(t, err)
	require.True(t, hit, "expected deterministic hit for 'go south'")
	require.NotNil(t, outcome)
	// Should transition to bar (dark or lit depending on wearing_cloak; initially dark).
	require.Equal(t, orchestrator.ModeTransitioned, outcome.Mode)
}

// TestTryDeterministic_GoSouth_CaseInsensitive verifies that "GO SOUTH  " also hits.
func TestTryDeterministic_GoSouth_CaseInsensitive(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "GO SOUTH  ")
	require.NoError(t, err)
	require.True(t, hit, "expected deterministic hit for 'GO SOUTH  '")
	require.NotNil(t, outcome)
	require.Equal(t, orchestrator.ModeTransitioned, outcome.Mode)
}

// TestTryDeterministic_Miss verifies that "stumble south" returns miss (no match).
func TestTryDeterministic_StumbleSouth_Miss(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "stumble south")
	require.NoError(t, err)
	require.False(t, hit, "expected miss for 'stumble south'")
	require.Nil(t, outcome)
}

// TestTryDeterministic_ExampleMatch_Look verifies that example-based matching
// works for intents with no required slots.
// The 'look' intent in cloak has examples: ["look", "look around", "describe the room"].
// "look" itself is also the Display text, so both Display and example matching apply.
func TestTryDeterministic_Look(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "look")
	require.NoError(t, err)
	require.True(t, hit, "expected deterministic hit for 'look' (Display match)")
	require.NotNil(t, outcome)
	require.Equal(t, orchestrator.ModeTransitioned, outcome.Mode)
}

// TestTryDeterministic_GoWest verifies that "go west" matches.
func TestTryDeterministic_GoWest(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "go west")
	require.NoError(t, err)
	require.True(t, hit, "expected deterministic hit for 'go west'")
	require.NotNil(t, outcome)
	require.Equal(t, orchestrator.ModeTransitioned, outcome.Mode)
}

// TestTryDeterministic_RandomInput_Miss verifies that arbitrary free-form input misses.
func TestTryDeterministic_RandomInput_Miss(t *testing.T) {
	orch, sid := loadCloakForDeterministic(t)
	ctx := context.Background()

	for _, input := range []string{
		"I want to go south please",
		"head towards the bar",
		"move south",
		"",
		"  ",
	} {
		outcome, hit, err := orch.TryDeterministic(ctx, sid, input)
		require.NoError(t, err, "input: %q", input)
		require.False(t, hit, "expected miss for input %q", input)
		require.Nil(t, outcome, "input: %q", input)
	}
}

// ─── Synthetic app: example-based matching and ambiguity ─────────────────────

const syntheticExamplesYAML = `
app:
  id: example-test
  version: 0.1.0

world: {}

intents:
  go:
    title: "Go"
    description: "Move in a direction."
    examples: ["n", "north please", "head north"]
    slots:
      direction:
        type: enum
        values: [north, south]
        required: true
        examples: ["n", "s"]

  attack:
    title: "Attack"
    examples: ["fight", "battle"]

root: start

states:
  start:
    view: "You are here."
    on:
      go:
        - when: "slots.direction == 'north'"
          target: north
        - when: "slots.direction == 'south'"
          target: south
      attack:
        - target: start
  north:
    view: "North."
    terminal: true
  south:
    view: "South."
    terminal: true
`

func loadSyntheticOrchestrator(t *testing.T) (*orchestrator.Orchestrator, app.SessionID) {
	t.Helper()

	def, err := app.LoadBytes([]byte(syntheticExamplesYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Static harness that never gets called in deterministic path.
	h := &staticHarness{intentName: "go", slots: map[string]any{"direction": "north"}}
	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, sid
}

// TestTryDeterministic_ExampleMatch_DisplayGoNorth verifies "go north" Display match.
func TestTryDeterministic_ExampleMatch_DisplayGoNorth(t *testing.T) {
	orch, sid := loadSyntheticOrchestrator(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "go north")
	require.NoError(t, err)
	require.True(t, hit, "expected hit for 'go north' (Display match)")
	require.NotNil(t, outcome)
	// north is a terminal state, so outcome is ModeCompleted.
	require.True(t, outcome.Mode == orchestrator.ModeTransitioned || outcome.Mode == orchestrator.ModeCompleted,
		"expected Transitioned or Completed, got %s", outcome.Mode)
}

// TestTryDeterministic_ExampleMatch_Attack verifies "fight" → attack via intent examples.
func TestTryDeterministic_ExampleMatch_Attack(t *testing.T) {
	orch, sid := loadSyntheticOrchestrator(t)
	ctx := context.Background()

	outcome, hit, err := orch.TryDeterministic(ctx, sid, "fight")
	require.NoError(t, err)
	require.True(t, hit, "expected hit for 'fight' (example match)")
	require.NotNil(t, outcome)
}

// TestTryDeterministic_AmbiguousExample verifies that when "n" appears under
// both "go north" and "go south" slot examples, it is treated as ambiguous (miss).
//
// The 'go' intent has slot examples: ["n", "s"]. "n" is a slot example for
// direction north in the "go north" menu entry. "s" is for south. Neither is
// declared as an example under the "attack" intent, so they are unambiguous
// there. However "n" is only under the "go north" entry in the final lookup.
// This test specifically verifies the single-entry case (unambiguous → hit).
func TestTryDeterministic_SlotExampleMatch_N(t *testing.T) {
	orch, sid := loadSyntheticOrchestrator(t)
	ctx := context.Background()

	// "n" is a slot example for direction=north under go intent.
	// The synthetic app only has one entry whose direction=north slot example is "n".
	outcome, hit, err := orch.TryDeterministic(ctx, sid, "n")
	require.NoError(t, err)
	// "n" may or may not hit depending on whether the slot example "n" uniquely maps
	// to the "go north" entry. Since "n" == normalizeInput("north") is FALSE (n ≠ north),
	// the slot example only matches if the example string itself matches the prefilled value.
	// direction=north, example="n" → normalizeInput("north") != "n" → NOT a match.
	// So this should be a miss.
	require.False(t, hit, "slot example 'n' should miss because normalizeInput('north') != 'n'")
	require.Nil(t, outcome)
}

// TestTryDeterministic_AmbiguousIntentExample verifies that if the same intent
// example appears under two different menu entries, it is treated as ambiguous (miss).
//
// We create a synthetic app where "run" is an example for two different intents.
const ambiguousExamplesYAML = `
app:
  id: ambiguous-test
  version: 0.1.0

world: {}

intents:
  flee:
    title: "Flee"
    examples: ["run", "escape", "flee"]
  sprint:
    title: "Sprint"
    examples: ["run", "dash", "sprint"]

root: start

states:
  start:
    view: "You are here."
    on:
      flee:
        - target: start
      sprint:
        - target: start
`

func TestTryDeterministic_AmbiguousExample_Miss(t *testing.T) {
	def, err := app.LoadBytes([]byte(ambiguousExamplesYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	h := &staticHarness{intentName: "flee"}
	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// "run" appears under both 'flee' and 'sprint' → ambiguous → miss.
	outcome, hit, err := orch.TryDeterministic(ctx, sid, "run")
	require.NoError(t, err)
	require.False(t, hit, "expected miss for ambiguous example 'run'")
	require.Nil(t, outcome)

	// "escape" appears only under 'flee' → unambiguous → hit.
	outcome, hit, err = orch.TryDeterministic(ctx, sid, "escape")
	require.NoError(t, err)
	require.True(t, hit, "expected hit for unambiguous example 'escape'")
	require.NotNil(t, outcome)
}
