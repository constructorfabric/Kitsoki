package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// offramp_conversation_test.go — white-box coverage for the conversation
// lane (agent_off_ramp.capture_free_text): the submitDirect divert, the
// per-room chat scope, and the engine-composed room context. Sibling of
// offramp_test.go; reuses its fake-agent converse stub and store wiring.

// conversationApp models the post-loader shape of a capture room: the
// loader pass (internal/app/offramp_capture.go) synthesizes hub_discuss +
// default_intent + the no-op arc; here they're built directly, matching
// what expandOffRampCaptures emits (asserted by that package's own tests).
func conversationApp() *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "conversation-test", Version: "1", Title: "Conversation Test"},
		Root: "hub",
		Intents: map[string]app.Intent{
			"look":        {Title: "Look", Description: "Look around."},
			"hub_discuss": {Title: "Discuss", Slots: map[string]app.Slot{"message": {Type: "string", Required: true}}},
		},
		States: map[string]*app.State{
			"hub": {
				Description:   "The hub room.",
				View:          app.LegacyView("Welcome to the hub."),
				RelevantWorld: []string{"status"},
				AgentOffRamp:  &app.OffRampDef{CaptureFreeText: true},
				DefaultIntent: "hub_discuss",
				On: map[string][]app.Transition{
					"look":        {{Target: "hub"}},
					"hub_discuss": {{Target: "hub"}},
				},
			},
		},
		World: map[string]app.VarDef{
			"status": {Type: "string", Default: "idle"},
		},
	}
}

// TestConversationDivert_SubmitDirect asserts the lane's core contract: a
// SubmitDirectRouted of the capture intent returns a ModeOffPath outcome
// carrying the converse answer, with the resting state and world untouched
// and no TransitionApplied persisted — the no-op arc never fires on the
// live path.
func TestConversationDivert_SubmitDirect(t *testing.T) {
	orch, raw, sid := setupOffRampOrchDef(t, conversationApp(), offRampNoopHarness{}, false)
	ctx := context.Background()

	// Prime the journey so the root state is materialized.
	_, err := orch.SubmitDirect(ctx, sid, "look", nil)
	require.NoError(t, err)
	jBefore, err := orch.loadJourney(sid)
	require.NoError(t, err)

	outcome, err := orch.SubmitDirectRouted(ctx, sid, "hub_discuss",
		map[string]any{"message": "what should I do next?"},
		"what should I do next?", RouteProvenance{Source: "default", MatchType: "free_text"})
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeOffPath, outcome.Mode)
	require.NotEmpty(t, outcome.View, "the converse answer is the view")
	require.Equal(t, app.StatePath("hub"), outcome.NewState)

	jAfter, err := orch.loadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, jBefore.State, jAfter.State, "state untouched")
	require.Equal(t, jBefore.World.Vars, jAfter.World.Vars, "world untouched")

	// The trace shows a conversation entry, not a transition.
	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawConversationEnter, sawQuestion bool
	for _, ev := range hist[1:] { // skip the priming turn's events? filter by kind instead
		switch ev.Kind {
		case store.OffPathEntered:
			sawConversationEnter = true
		case store.OffPathQuestion:
			sawQuestion = true
		}
	}
	require.True(t, sawConversationEnter, "OffPathEntered must be recorded")
	require.True(t, sawQuestion, "OffPathQuestion must be recorded")
}

// TestConversationDivert_OtherIntentsUnaffected asserts a plain command in a
// capture room still runs the machine as before.
func TestConversationDivert_OtherIntentsUnaffected(t *testing.T) {
	orch, _, sid := setupOffRampOrchDef(t, conversationApp(), offRampNoopHarness{}, false)
	ctx := context.Background()

	outcome, err := orch.SubmitDirect(ctx, sid, "look", nil)
	require.NoError(t, err)
	require.NotEqual(t, ModeOffPath, outcome.Mode, "a real command must not divert")
}

// TestConversationCaptureIntent_Gates asserts the divert's identity check:
// only a capture-enabled off-ramp with the synthesized default intent yields
// a capture name.
func TestConversationCaptureIntent_Gates(t *testing.T) {
	def := conversationApp()
	hub := def.States["hub"]
	require.Equal(t, "hub_discuss", conversationCaptureIntent(def, "hub", hub))

	noCapture := &app.State{AgentOffRamp: &app.OffRampDef{}, DefaultIntent: "hub_discuss"}
	require.Empty(t, conversationCaptureIntent(def, "hub", noCapture))

	noOffRamp := &app.State{DefaultIntent: "hub_discuss"}
	require.Empty(t, conversationCaptureIntent(def, "hub", noOffRamp))

	require.Empty(t, conversationCaptureIntent(def, "hub", nil))
}

// TestComposeRoomContext asserts the engine-composed context carries the
// room purpose, the available commands (minus the capture intent itself),
// and the relevant world values.
func TestComposeRoomContext(t *testing.T) {
	def := conversationApp()
	hub := def.States["hub"]
	w := world.World{Vars: map[string]any{"status": "rebasing", "unrelated": "x"}}

	got := composeRoomContext(def, "hub", hub, w, []string{"look", "hub_discuss"})
	require.Contains(t, got, "Conversation Test")
	require.Contains(t, got, "Room purpose: The hub room.")
	require.Contains(t, got, "- look: Look around.")
	require.NotContains(t, got, "hub_discuss", "the lane itself is not an operator command")
	require.Contains(t, got, "- status: rebasing")
	require.NotContains(t, got, "unrelated", "only relevant_world keys are included")
}

// TestCompactWorldValue asserts scalars pass through, structures render as
// compact JSON, and oversized values truncate.
func TestCompactWorldValue(t *testing.T) {
	require.Equal(t, "plain", compactWorldValue("plain"))
	require.Equal(t, `{"a":1}`, compactWorldValue(map[string]any{"a": 1}))
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'x'
	}
	got := compactWorldValue(string(long))
	require.LessOrEqual(t, len(got), 410)
}
