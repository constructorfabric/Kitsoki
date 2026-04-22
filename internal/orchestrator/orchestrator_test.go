package orchestrator_test

import (
	"context"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/intent"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
	"hally/internal/world"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func loadCloakOrchestrator(t *testing.T) (*orchestrator.Orchestrator, *app.AppDef) {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/oracle.yaml")
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, h)
	return orch, def
}

// ─── Cloak winning path via ReplayHarness ─────────────────────────────────────

func TestOrchestratorCloakWinningPath(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn 1: foyer → go west → cloakroom
	out1, err := orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out1.Mode)
	require.Equal(t, app.StatePath("cloakroom"), out1.NewState)
	require.NotEmpty(t, out1.View)

	// Turn 2: cloakroom → hang the cloak
	out2, err := orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out2.Mode)
	require.Equal(t, app.StatePath("cloakroom"), out2.NewState)

	// Turn 3: cloakroom → go east → foyer
	out3, err := orch.Turn(ctx, sid, "go east")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out3.Mode)
	require.Equal(t, app.StatePath("foyer"), out3.NewState)

	// Turn 4: foyer → go south → bar.lit (cloak is hung, so lit)
	out4, err := orch.Turn(ctx, sid, "go south")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out4.Mode)
	require.Equal(t, app.StatePath("bar.lit"), out4.NewState)

	// Turn 5: bar.lit → read the message → ended (won)
	out5, err := orch.Turn(ctx, sid, "read the message")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out5.Mode)
	require.Equal(t, app.StatePath("ended"), out5.NewState)
	require.Contains(t, out5.View, "You have won")

	// Verify events were persisted.
	require.NotEmpty(t, out5.Events)
}

// ─── Clarify path ─────────────────────────────────────────────────────────────

// staticHarness always returns the same call.
type staticHarness struct {
	intentName string
	slots      map[string]any
}

func (h *staticHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	args := map[string]any{"intent": h.intentName}
	if h.slots != nil {
		args["slots"] = h.slots
	}
	return mcp.CallToolParams{Name: "transition", Arguments: args}, nil
}

func (h *staticHarness) Close() error { return nil }

func TestOrchestratorClarifyPath(t *testing.T) {
	// Build a minimal app that has an intent with a required slot.
	const appYAML = `
app:
  id: clarify-test
  version: 0.1.0

world: {}

intents:
  move:
    title: "Move"
    slots:
      direction:
        type: enum
        values: [north, south, east, west]
        required: true
        prompt: "Which direction?"

root: start

states:
  start:
    view: "You are at the start."
    on:
      move:
        - when: "slots.direction == 'north'"
          target: finish
        - default: true
          target: start

  finish:
    terminal: true
    view: "You finished!"
`

	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Harness that returns 'move' intent WITHOUT the required slot.
	h := &staticHarness{intentName: "move", slots: nil}

	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn: harness returns move without direction → should be ModeClarify.
	out, err := orch.Turn(ctx, sid, "go somewhere")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeClarify, out.Mode, "expected Clarify outcome")
	require.Equal(t, app.StatePath("start"), out.NewState)
	require.Equal(t, "move", out.PendingIntent)
	require.Len(t, out.SlotsNeeded, 1)
	require.Equal(t, "direction", out.SlotsNeeded[0].Name)

	// Now ContinueTurn with the missing slot → should succeed.
	cont, err := orch.ContinueTurn(ctx, sid, map[string]any{"direction": "north"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, cont.Mode, "expected Completed after slot fill")
	require.Equal(t, app.StatePath("finish"), cont.NewState)
}

// ─── Rejected path ─────────────────────────────────────────────────────────────

func TestOrchestratorRejectedPath(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Harness that tries to hang_cloak from foyer (not allowed there).
	h := &staticHarness{intentName: "hang_cloak", slots: nil}

	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeRejected, out.Mode)
	require.Equal(t, intent.ErrIntentNotAllowed, out.ErrorCode)
	require.Equal(t, app.StatePath("foyer"), out.NewState, "state should not advance on rejection")
}

// ─── Turn number tracking ──────────────────────────────────────────────────────

func TestOrchestratorTurnNumbers(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out1, err := orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.Equal(t, app.TurnNumber(1), out1.TurnNumber)

	out2, err := orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)
	require.Equal(t, app.TurnNumber(2), out2.TurnNumber)
}

// ─── Initial state/world helpers ──────────────────────────────────────────────

func TestOrchestratorInitialState(t *testing.T) {
	orch, def := loadCloakOrchestrator(t)

	require.Equal(t, app.StatePath("foyer"), orch.InitialState())

	w := orch.InitialWorld()
	require.Equal(t, true, w.Vars["wearing_cloak"])
	require.Equal(t, int64(0), toInt64(w.Vars["disturbance"]))
	_ = def
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

// ─── Event persistence verification ───────────────────────────────────────────

func TestOrchestratorEventPersistence(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.NotEmpty(t, out.Events)

	// Load history and verify events were persisted.
	// We can verify indirectly by doing another turn — the journey must be at cloakroom.
	out2, err := orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out2.Mode)
	require.Equal(t, app.StatePath("cloakroom"), out2.NewState)
}

// ─── Feedback: ComputeLocation and ComputeMenu ────────────────────────────────

func TestComputeLocation(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))
	w.Vars["wearing_cloak"] = true

	loc := orchestrator.ComputeLocation(def, app.StatePath("foyer"), w, 1)
	require.Equal(t, "foyer", loc.Breadcrumb)
	require.NotEmpty(t, loc.StateDescription)
	require.Equal(t, app.TurnNumber(1), loc.TurnNumber)
	require.True(t, loc.OnPath)
	// foyer has relevant_world: [wearing_cloak]
	require.Contains(t, loc.RelevantWorld, "wearing_cloak")
}

func TestComputeMenu(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := orchestrator.ComputeMenu(def, m, app.StatePath("foyer"), w)
	// foyer should have expanded go rows and look in primary.
	// "go" is expanded into enum values; bare "go" should NOT appear.
	displays := make([]string, len(menu.Primary))
	for i, item := range menu.Primary {
		displays[i] = item.Display
	}
	require.Contains(t, displays, "go south")
	require.Contains(t, displays, "go west")
	require.Contains(t, displays, "look")
	// Bare "go" should NOT appear (it's replaced by expanded rows).
	require.NotContains(t, displays, "go")
}

// ─── Clarify with already-provided slots ──────────────────────────────────────

func TestOrchestratorClarifyThenContinue(t *testing.T) {
	const appYAML = `
app:
  id: multi-slot-test
  version: 0.1.0

world: {}

intents:
  greet:
    slots:
      name: { type: string, required: true, prompt: "Your name?" }
      greeting: { type: enum, values: [hello, hi, hey], required: true, prompt: "Which greeting?" }

root: start

states:
  start:
    view: "Start."
    on:
      greet:
        - target: done
  done:
    terminal: true
    view: "Done."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// Harness returns greet with only 'name' slot, missing 'greeting'.
	h := &staticHarness{intentName: "greet", slots: map[string]any{"name": "Alice"}}

	orch := orchestrator.New(def, m, s, h)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "greet")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeClarify, out.Mode)
	require.Len(t, out.SlotsNeeded, 1)
	require.Equal(t, "greeting", out.SlotsNeeded[0].Name)
	// 'greeting' is an enum → UseForm should be in the clarification
	clarification := orchestrator.ComputeClarification(def, out.NewState, "greet", []string{"greeting"})
	require.True(t, clarification.UseForm, "enum slot should trigger form mode")

	// ContinueTurn with the missing greeting slot.
	cont, err := orch.ContinueTurn(ctx, sid, map[string]any{"greeting": "hello"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, cont.Mode)
	require.Equal(t, app.StatePath("done"), cont.NewState)
}

// ─── World integrity after orchestrator turns ─────────────────────────────────

func TestOrchestratorWorldIntegrity(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// go west → cloakroom
	_, err = orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)

	// hang_cloak → wearing_cloak should become false
	out, err := orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	// go east → foyer
	_, err = orch.Turn(ctx, sid, "go east")
	require.NoError(t, err)

	// go south → should reach bar.lit (not bar.dark) because cloak is hung
	out4, err := orch.Turn(ctx, sid, "go south")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("bar.lit"), out4.NewState,
		"should be bar.lit after hanging cloak; got %s", out4.NewState)
}

// ─── SlotNeed UseForm detection ───────────────────────────────────────────────

func TestComputeClarification(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	// go intent has an enum slot 'direction' → UseForm = true
	c := orchestrator.ComputeClarification(def, app.StatePath("foyer"), "go", []string{"direction"})
	require.Len(t, c.Slots, 1)
	require.Equal(t, "direction", c.Slots[0].Name)
	require.Equal(t, "enum", c.Slots[0].Type)
	require.True(t, c.UseForm, "enum slot should trigger form mode")
}

// ─── No pending clarification error ───────────────────────────────────────────

func TestOrchestratorContinueTurnNoPending(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// ContinueTurn without a prior clarify → error
	_, err = orch.ContinueTurn(ctx, sid, map[string]any{"direction": "north"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no pending clarification")
}

// ─── InitialView ──────────────────────────────────────────────────────────────

func TestOrchestratorInitialView(t *testing.T) {
	orch, _ := loadCloakOrchestrator(t)
	w := world.World{Vars: map[string]any{
		"wearing_cloak":    true,
		"disturbance":      int64(0),
		"message_rumpled":  false,
	}}

	view, err := orch.InitialView(w)
	require.NoError(t, err)
	require.NotEmpty(t, view)
	require.Contains(t, view, "hall")
}
