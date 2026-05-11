package store_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// mkPayload is a helper to create a JSON payload for test events.
func mkPayload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// cloakInitialWorld returns the starting world for Cloak of Darkness.
func cloakInitialWorld() world.World {
	w := world.New()
	w.Vars["wearing_cloak"] = true
	w.Vars["disturbance"] = int64(0)
	w.Vars["message_rumpled"] = false
	return w
}

// cloakWinningHistory returns a hand-crafted event sequence for the Cloak
// winning path: foyer → cloakroom → hang cloak → foyer → bar.lit → read_message → ended.
// This mirrors what Machine.Turn would emit for each step.
func cloakWinningHistory() store.History {
	return store.History{
		// Turn 1: go west → cloakroom
		{Turn: 1, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "foyer", "to": "cloakroom", "intent": "go",
		})},
		{Turn: 1, Seq: 1, Kind: store.StateExited, Payload: mkPayload(map[string]any{"state": "foyer"})},
		{Turn: 1, Seq: 2, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "cloakroom"})},

		// Turn 2: hang_cloak → sets wearing_cloak=false
		{Turn: 2, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "cloakroom", "to": "cloakroom", "intent": "hang_cloak",
		})},
		{Turn: 2, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"set": map[string]any{"wearing_cloak": false},
		})},
		// Say effect (no world mutation, but part of the sequence).
		{Turn: 2, Seq: 2, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"say": "You hang the cloak on the hook.",
		})},

		// Turn 3: go east → foyer
		{Turn: 3, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "cloakroom", "to": "foyer", "intent": "go",
		})},
		{Turn: 3, Seq: 1, Kind: store.StateExited, Payload: mkPayload(map[string]any{"state": "cloakroom"})},
		{Turn: 3, Seq: 2, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "foyer"})},

		// Turn 4: go south → bar.lit (wearing_cloak==false so lit)
		{Turn: 4, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "foyer", "to": "bar.lit", "intent": "go",
		})},
		{Turn: 4, Seq: 1, Kind: store.StateExited, Payload: mkPayload(map[string]any{"state": "foyer"})},
		{Turn: 4, Seq: 2, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "bar"})},
		{Turn: 4, Seq: 3, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "bar.lit"})},

		// Turn 5: read_message → ended, sets message_rumpled=false (disturbance==0 not > 2)
		{Turn: 5, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.lit", "to": "ended", "intent": "read_message",
		})},
		{Turn: 5, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"set": map[string]any{"message_rumpled": false},
		})},
		{Turn: 5, Seq: 2, Kind: store.StateExited, Payload: mkPayload(map[string]any{"state": "bar.lit"})},
		{Turn: 5, Seq: 3, Kind: store.StateExited, Payload: mkPayload(map[string]any{"state": "bar"})},
		{Turn: 5, Seq: 4, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "ended"})},
	}
}

// ─── BuildJourney tests ───────────────────────────────────────────────────────

func TestBuildJourney_WinningPath(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"},
	}

	history := cloakWinningHistory()
	js, err := store.BuildJourney(def, "foyer", cloakInitialWorld(), history)
	require.NoError(t, err)
	require.NotNil(t, js)

	// Final state after winning path must be "ended".
	require.Equal(t, app.StatePath("ended"), js.State, "final state should be 'ended'")

	// World: wearing_cloak=false, message_rumpled=false, disturbance=0.
	require.Equal(t, false, js.World.Vars["wearing_cloak"], "wearing_cloak should be false after hang_cloak")
	require.Equal(t, false, js.World.Vars["message_rumpled"], "message_rumpled should be false (disturbance was 0)")
	require.Equal(t, int64(0), toI64(js.World.Vars["disturbance"]), "disturbance should remain 0")

	// Turn counter should be 5.
	require.Equal(t, app.TurnNumber(5), js.Turn)
}

func TestBuildJourney_LosingPath(t *testing.T) {
	// Losing path: foyer → bar.dark (wearing cloak) → blunder 3 times (disturbance=3)
	//              → go north → foyer → hang cloak → bar.lit → read_message → ended (lost)
	def := &app.AppDef{App: app.AppMeta{ID: "cloak-of-darkness", Version: "0.1.0"}}
	initial := world.New()
	initial.Vars["wearing_cloak"] = true
	initial.Vars["disturbance"] = int64(0)
	initial.Vars["message_rumpled"] = false

	history := store.History{
		// Turn 1: go south → bar.dark
		{Turn: 1, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "foyer", "to": "bar.dark",
		})},
		{Turn: 1, Seq: 1, Kind: store.StateEntered, Payload: mkPayload(map[string]any{"state": "bar.dark"})},

		// Turn 2: fumble in dark → disturbance=1
		{Turn: 2, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.dark", "to": "bar.dark",
		})},
		{Turn: 2, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"increment": map[string]int{"disturbance": 1},
		})},

		// Turn 3: fumble → disturbance=2
		{Turn: 3, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.dark", "to": "bar.dark",
		})},
		{Turn: 3, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"increment": map[string]int{"disturbance": 1},
		})},

		// Turn 4: fumble → disturbance=3
		{Turn: 4, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.dark", "to": "bar.dark",
		})},
		{Turn: 4, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"increment": map[string]int{"disturbance": 1},
		})},

		// Turn 5: go north → foyer
		{Turn: 5, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.dark", "to": "foyer",
		})},

		// Turn 6: hang_cloak in foyer (pretend) → wearing_cloak=false
		// (in real cloak app this happens in cloakroom, but for replay test we test the world mutation)
		{Turn: 6, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "foyer", "to": "bar.lit",
		})},
		{Turn: 6, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"set": map[string]any{"wearing_cloak": false},
		})},

		// Turn 7: read_message with disturbance=3 → message_rumpled=true
		{Turn: 7, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "bar.lit", "to": "ended",
		})},
		{Turn: 7, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"set": map[string]any{"message_rumpled": true},
		})},
	}

	js, err := store.BuildJourney(def, "foyer", initial, history)
	require.NoError(t, err)

	require.Equal(t, app.StatePath("ended"), js.State)
	require.Equal(t, int64(3), toI64(js.World.Vars["disturbance"]), "disturbance should be 3")
	require.Equal(t, true, js.World.Vars["message_rumpled"], "should have lost")
	require.Equal(t, false, js.World.Vars["wearing_cloak"])
}

func TestBuildJourney_EmptyHistory(t *testing.T) {
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()
	initial.Vars["x"] = int64(5)

	js, err := store.BuildJourney(def, "start", initial, nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("start"), js.State)
	require.Equal(t, int64(5), js.World.Vars["x"])
	require.Equal(t, app.TurnNumber(0), js.Turn)
}

func TestBuildJourney_ValidationFailedDoesNotChangeState(t *testing.T) {
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	history := store.History{
		// Turn 1: valid transition.
		{Turn: 1, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "state_a", "to": "state_b",
		})},
		// Turn 2: validation failed — should not change state.
		{Turn: 2, Seq: 0, Kind: store.ValidationFailed, Payload: mkPayload(map[string]any{
			"code":    "INTENT_NOT_ALLOWED_IN_STATE",
			"intent":  "foo",
			"state":   "state_b",
			"message": "intent not allowed",
		})},
	}

	js, err := store.BuildJourney(def, "state_a", initial, history)
	require.NoError(t, err)

	// State should still be state_b (from turn 1's successful transition).
	require.Equal(t, app.StatePath("state_b"), js.State)
}

func TestBuildJourney_TurnStartedAndEndedIgnored(t *testing.T) {
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()
	initial.Vars["count"] = int64(0)

	history := store.History{
		{Turn: 1, Seq: 0, Kind: store.TurnStarted, Payload: mkPayload(map[string]any{})},
		{Turn: 1, Seq: 1, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "a", "to": "b",
		})},
		{Turn: 1, Seq: 2, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"increment": map[string]int{"count": 1},
		})},
		{Turn: 1, Seq: 3, Kind: store.TurnEnded, Payload: mkPayload(map[string]any{})},
	}

	js, err := store.BuildJourney(def, "a", initial, history)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("b"), js.State)
	require.Equal(t, int64(1), toI64(js.World.Vars["count"]))
}

func TestBuildJourney_UnknownKindIgnored(t *testing.T) {
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	// An unknown/future event kind should be silently ignored (forward-compat).
	history := store.History{
		{Turn: 1, Seq: 0, Kind: store.EventKind("FutureEvent"), Payload: mkPayload(map[string]any{"data": "x"})},
		{Turn: 1, Seq: 1, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{
			"from": "a", "to": "b",
		})},
	}

	js, err := store.BuildJourney(def, "a", initial, history)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("b"), js.State)
}

func TestBuildJourney_EffectApplied_SetAndIncrement(t *testing.T) {
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()
	initial.Vars["x"] = int64(10)
	initial.Vars["y"] = "hello"

	history := store.History{
		{Turn: 1, Seq: 0, Kind: store.TransitionApplied, Payload: mkPayload(map[string]any{"from": "a", "to": "b"})},
		// Set y to "world".
		{Turn: 1, Seq: 1, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"set": map[string]any{"y": "world"},
		})},
		// Increment x by 5 → 15.
		{Turn: 1, Seq: 2, Kind: store.EffectApplied, Payload: mkPayload(map[string]any{
			"increment": map[string]int{"x": 5},
		})},
	}

	js, err := store.BuildJourney(def, "a", initial, history)
	require.NoError(t, err)
	require.Equal(t, "world", js.World.Vars["y"])
	require.Equal(t, int64(15), toI64(js.World.Vars["x"]))
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// toI64 normalises numeric types to int64 for comparison.
func toI64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	}
	return 0
}
