package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestRewindRoute_IntentClass exercises the class=intent rewind path: route a
// contextual intent-class turn, then RewindRoute under ClassIntent. The rewind
// must recover the accepted intent (name + slots) from the IntentAccepted event
// at the rewound turn and re-dispatch it against the restored pre-turn state,
// recording a turn.context_route_overridden event.
func TestRewindRoute_IntentClass(t *testing.T) {
	def, err := app.LoadBytes([]byte(ctxRouteAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubContextRouter{
		submission: `{"class":"intent","intent":"go_west","confidence":0.9,"reason":"first route"}`,
	}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	const utterance = "head that way please"
	out1, err := orch.Turn(ctx, sid, utterance) // deterministic miss -> context route -> go_west
	require.NoError(t, err)
	require.Equal(t, app.StatePath("west_end"), out1.NewState,
		"first route advanced the machine to west_end via the intent class")
	require.NotNil(t, out1.ContextRoute)
	decisionID := out1.ContextRoute.DecisionID
	require.NotEmpty(t, decisionID)

	// Operator rewinds and re-routes under the SAME (intent) class.
	out2, err := orch.RewindRoute(ctx, sid, decisionID, orchestrator.ClassIntent,
		"operator: keep it as an intent", "")
	require.NoError(t, err, "class=intent rewind must succeed")
	require.NotNil(t, out2, "rewind must return a valid outcome")

	// The recovered intent re-dispatched against the restored pre-turn (start)
	// state, advancing the machine to west_end again.
	require.Equal(t, app.StatePath("west_end"), out2.NewState,
		"the recovered intent must re-dispatch against the restored pre-turn state")

	// The outcome carries an intent-class receipt naming the recovered intent.
	require.NotNil(t, out2.ContextRoute)
	require.Equal(t, "intent", out2.ContextRoute.Class)
	require.Equal(t, "go_west", out2.ContextRoute.Intent,
		"the receipt must name the recovered intent")
	require.Equal(t, decisionID, out2.ContextRoute.DecisionID)

	// A turn.context_route_overridden event was recorded, naming old+new class.
	var sawOverride bool
	for _, e := range out2.Events {
		if string(e.Kind) != "turn.context_route_overridden" {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(e.Payload, &p))
		require.Equal(t, "intent", p["old_class"])
		require.Equal(t, "intent", p["new_class"])
		require.Equal(t, decisionID, p["from_decision_id"])
		sawOverride = true
	}
	require.True(t, sawOverride,
		"rewind must record a turn.context_route_overridden event for audit/replay")
}
