package orchestrator_test

import (
	"context"
	"sync"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// recordingHarness captures every TurnInput it sees so a test can inspect
// what the orchestrator handed to the LLM. It returns a fixed intent so the
// machine has something to transition on.
type recordingHarness struct {
	mu     sync.Mutex
	inputs []harness.TurnInput

	intentName string
	slots      map[string]any
}

func (h *recordingHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	h.mu.Lock()
	// Copy the slice header to avoid aliasing if the orchestrator reuses
	// the underlying array between turns.
	cp := in
	cp.RecentTurns = append([]harness.TurnSummary(nil), in.RecentTurns...)
	h.inputs = append(h.inputs, cp)
	h.mu.Unlock()

	args := map[string]any{"intent": h.intentName}
	if h.slots != nil {
		args["slots"] = h.slots
	}
	return mcp.CallToolParams{Name: "transition", Arguments: args}, nil
}

func (h *recordingHarness) Close() error { return nil }

func (h *recordingHarness) capturedInputs() []harness.TurnInput {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]harness.TurnInput(nil), h.inputs...)
}

// TestOrchestrator_PopulatesRecentTurnsForHarness asserts that the
// orchestrator threads prior-turn summaries through TurnInput.RecentTurns
// so the LLM can resolve back-references like "what I just said". Turn 1
// must see an empty slice; turn 2 must see exactly one prior summary
// carrying the (user_text, intent, post-turn state) of turn 1.
func TestOrchestrator_PopulatesRecentTurnsForHarness(t *testing.T) {
	const appYAML = `
app:
  id: recent-turns-test
  version: 0.1.0
world: {}
intents:
  step:
    title: "Step"
root: a
states:
  a:
    view: "A."
    on:
      step:
        - target: b
  b:
    view: "B."
    on:
      step:
        - target: c
  c:
    terminal: true
    view: "C."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	h := &recordingHarness{intentName: "step"}
	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn 1: a → b
	out1, err := orch.Turn(ctx, sid, "first step")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out1.Mode)
	require.Equal(t, app.StatePath("b"), out1.NewState)

	// Turn 2: b → c (terminal)
	out2, err := orch.Turn(ctx, sid, "second step")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out2.Mode)

	inputs := h.capturedInputs()
	require.Len(t, inputs, 2, "harness should have been invoked twice")

	// Turn 1's invocation: nothing prior, RecentTurns is empty.
	require.Empty(t, inputs[0].RecentTurns,
		"turn 1 harness invocation should carry an empty RecentTurns slice")

	// Turn 2's invocation: exactly one prior turn summary.
	require.Len(t, inputs[1].RecentTurns, 1,
		"turn 2 harness invocation should carry exactly one prior turn summary")
	prior := inputs[1].RecentTurns[0]
	require.Equal(t, app.TurnNumber(1), prior.Turn)
	require.Equal(t, "first step", prior.UserText)
	require.Equal(t, "step", prior.Intent)
	require.Equal(t, app.StatePath("b"), prior.State)
	require.False(t, prior.Rejected,
		"a successful transition should not flag the summary as Rejected")
}

// TestOrchestrator_RecentTurnsCapsAtLimit asserts the orchestrator never
// emits more than RecentTurnsLimit summaries even after a long session.
// The cap is a prompt-size guard; without it a 100-turn session would
// shovel 100 turn records into every LLM call.
func TestOrchestrator_RecentTurnsCapsAtLimit(t *testing.T) {
	const appYAML = `
app:
  id: recent-turns-cap
  version: 0.1.0
world: {}
intents:
  ping:
    title: "Ping"
root: loop
states:
  loop:
    view: "Loop."
    on:
      ping:
        - target: loop
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	h := &recordingHarness{intentName: "ping"}
	orch := orchestrator.New(def, m, s, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	const totalTurns = orchestrator.RecentTurnsLimit + 3
	for i := 0; i < totalTurns; i++ {
		_, err := orch.Turn(ctx, sid, "ping")
		require.NoError(t, err, "turn %d", i+1)
	}

	inputs := h.capturedInputs()
	require.Len(t, inputs, totalTurns)

	last := inputs[totalTurns-1]
	require.LessOrEqual(t, len(last.RecentTurns), orchestrator.RecentTurnsLimit,
		"final turn's RecentTurns must not exceed RecentTurnsLimit")
	require.Equal(t, orchestrator.RecentTurnsLimit, len(last.RecentTurns),
		"after enough turns we should see exactly RecentTurnsLimit summaries")

	// Ordering is oldest → newest; the newest entry should reference the
	// immediately-prior turn (totalTurns-1).
	require.Equal(t, app.TurnNumber(totalTurns-1), last.RecentTurns[len(last.RecentTurns)-1].Turn,
		"RecentTurns tail should be the most recent prior turn")
}

// loadHistoryCountingStore wraps a store.Store and counts LoadHistory calls,
// so a test can assert Turn() reads the event log exactly once per turn
// instead of once for loadJourney and a second time for RecentTurns.
type loadHistoryCountingStore struct {
	store.Store
	mu    sync.Mutex
	calls int
}

func (s *loadHistoryCountingStore) LoadHistory(sid app.SessionID) (store.History, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.Store.LoadHistory(sid)
}

func (s *loadHistoryCountingStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestOrchestrator_RecentTurnsSingleHistoryRead locks the fix for the
// dispatch-context-floor double-read: Turn() must derive RecentTurns from
// the same event-log pass loadJourney already ran (carried on
// store.JourneyState.History) rather than issuing a second
// store.LoadHistory call for the same rows. One store.LoadHistory call per
// Turn(), not two, while truncation semantics (RecentTurnsLimit, ordering)
// stay identical to the uncapped/capped assertions above.
func TestOrchestrator_RecentTurnsSingleHistoryRead(t *testing.T) {
	const appYAML = `
app:
  id: recent-turns-single-read
  version: 0.1.0
world: {}
intents:
  step:
    title: "Step"
root: a
states:
  a:
    view: "A."
    on:
      step:
        - target: b
  b:
    view: "B."
    on:
      step:
        - target: c
  c:
    terminal: true
    view: "C."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	raw, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()

	counting := &loadHistoryCountingStore{Store: raw}

	h := &recordingHarness{intentName: "step"}
	orch := orchestrator.New(def, m, counting, h)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn() itself makes two loadJourney passes per call: TryDeterministic's
	// own pre-LLM routing pass (deterministic.go, runs before the session
	// lock, unconditionally for apps with no matching routing config), and
	// Turn's own loadJourney once it falls through to the harness. That
	// two-call shape is pre-existing and out of scope for this fix (it is a
	// routing-tier concern, not the RecentTurns concern this test locks).
	//
	// What this test asserts is that Turn() does NOT add a *third*
	// store.LoadHistory call to derive RecentTurns — before the fix,
	// extractRecentTurns re-queried the store itself instead of reusing
	// journey.History, so each Turn() cost 3 store reads instead of 2.
	const loadHistoryCallsPerTurn = 2

	// Turn 1: a → b. No prior turns, so RecentTurns must be empty.
	_, err = orch.Turn(ctx, sid, "first step")
	require.NoError(t, err)
	require.Equal(t, loadHistoryCallsPerTurn, counting.callCount(),
		"Turn 1 should cost exactly 2 store.LoadHistory calls (TryDeterministic + loadJourney), not a 3rd for RecentTurns")

	// Turn 2: b → c. Exactly one prior turn summary, still no extra read.
	_, err = orch.Turn(ctx, sid, "second step")
	require.NoError(t, err)
	require.Equal(t, 2*loadHistoryCallsPerTurn, counting.callCount(),
		"Turn 2 should bring the cumulative store.LoadHistory count to 4 (2 more calls, not 3 more)")

	inputs := h.capturedInputs()
	require.Len(t, inputs, 2)
	require.Empty(t, inputs[0].RecentTurns, "turn 1 has no prior turns")
	require.Len(t, inputs[1].RecentTurns, 1, "turn 2 should see exactly one prior turn summary")
	require.Equal(t, "first step", inputs[1].RecentTurns[0].UserText)
}
