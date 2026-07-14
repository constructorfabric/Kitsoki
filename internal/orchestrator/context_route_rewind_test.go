package orchestrator_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// 4.1: a contextually-routed turn carries a structured receipt on its outcome —
// class, reason, and the alternatives the router considered. Proves the receipt
// data is queryable from the turn outcome (not just slog).
func TestContextualRouter_OutcomeCarriesReceipt(t *testing.T) {
	def, err := app.LoadBytes([]byte(ctxRouteAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	stub := &stubContextRouter{
		submission: `{"class":"intent","intent":"go_west","confidence":0.9,` +
			`"reason":"explicit westward navigation",` +
			`"alternatives":[{"class":"help","confidence":0.3},` +
			`{"class":"meta_edit","confidence":0.2}]}`,
	}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithAgentRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "qqzzx wibble frob") // deterministic miss
	require.NoError(t, err)

	require.NotNil(t, out.ContextRoute, "a contextual-route turn must carry a receipt")
	require.Equal(t, "intent", out.ContextRoute.Class)
	require.Equal(t, "explicit westward navigation", out.ContextRoute.Reason)
	require.GreaterOrEqual(t, len(out.ContextRoute.Alternatives), 2,
		"receipt must record the alternatives the router considered")
	require.NotEmpty(t, out.ContextRoute.DecisionID,
		"receipt must expose a decision id usable as a rewind target")
}

// 4.2: the proposal's "Rewind flow". Route a turn one way, then RewindRoute it
// under a different class. Assert: (a) the replacement class is dispatched with
// the ORIGINAL utterance; (b) machine state+world are restored to pre-turn
// before re-dispatch; (c) a turn.context_route_overridden event is recorded.
func TestContextualRouter_RewindRouteOverridesAndRedispatches(t *testing.T) {
	def, err := app.LoadBytes([]byte(ctxRouteAppYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	stub := &stubContextRouter{
		submission: `{"class":"intent","intent":"go_west","confidence":0.9,"reason":"first route"}`,
	}
	reg := agent.NewRegistry()
	reg.Register("agent.local", stub)

	h := &countingHarness{fall: staticHarness{intentName: "go_south"}}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithAgentRegistry(reg),
		orchestrator.WithChatStore(chathost.NewAdapter(cs)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	const utterance = "head that way please"
	out1, err := orch.Turn(ctx, sid, utterance) // deterministic miss -> context route
	require.NoError(t, err)
	require.Equal(t, app.StatePath("west_end"), out1.NewState,
		"first route advanced the machine to west_end")
	require.NotNil(t, out1.ContextRoute)
	decisionID := out1.ContextRoute.DecisionID
	require.NotEmpty(t, decisionID)

	// Operator rewinds and re-routes the SAME utterance as help (read-only lane).
	out2, err := orch.RewindRoute(ctx, sid, decisionID, orchestrator.ClassHelp,
		"operator: this was a help question, not navigation", "")
	require.NoError(t, err)

	// (b) State restored to pre-turn (start) — help is read-only, never advances.
	require.Equal(t, app.StatePath("start"), out2.NewState,
		"rewind must restore the machine to the pre-dispatch state before re-dispatch; "+
			"a help re-route leaves the machine at start")

	// (a) The replacement class dispatched the ORIGINAL utterance to the help lane.
	require.NotNil(t, out2.ContextRoute)
	require.Equal(t, "help", out2.ContextRoute.Class)
	chatsForLane, err := cs.List(ctx, "crr-slice1-test", "room:help", "start")
	require.NoError(t, err)
	require.Len(t, chatsForLane, 1, "help re-route must create the room help lane")
	msgs, err := cs.Transcript(ctx, chatsForLane[0].ID, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(msgs), 1)
	require.Equal(t, utterance, msgs[0].Content,
		"the rewind must re-dispatch the ORIGINAL utterance under the new class")

	// (c) A turn.context_route_overridden event was recorded, naming old+new class.
	var sawOverride bool
	for _, e := range out2.Events {
		if string(e.Kind) != "turn.context_route_overridden" {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(e.Payload, &p))
		require.Equal(t, "intent", p["old_class"], "override must record the original class")
		require.Equal(t, "help", p["new_class"], "override must record the replacement class")
		require.Equal(t, decisionID, p["from_decision_id"])
		sawOverride = true
	}
	require.True(t, sawOverride,
		"rewind must record a turn.context_route_overridden event for audit/replay")

	// Sanity: the override path never touched the main-turn LLM.
	require.Equal(t, int64(0), h.calls.Load(),
		"rewind/override must not reach the main-turn harness")
	_ = atomic.LoadInt32(&stub.calls)
}
