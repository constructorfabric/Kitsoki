package orchestrator_test

import (
	"context"
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

// 2.1+2.2: a {class:help} verdict on a deterministic miss appends the utterance
// to the readonly room lane (via the injected chat store) and DOES NOT advance
// the machine or mutate world — the help lane is read-only. Reuses the slice-1
// rig (stubContextRouter, countingHarness, staticHarness, ctxRouteAppYAML are
// defined in context_route_test.go).
func TestContextualRouter_HelpClassAppendsToReadonlyLane(t *testing.T) {
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
		submission: `{"class":"help","confidence":0.9,"reason":"how do I use this room"}`,
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

	out, err := orch.Turn(ctx, sid, "how do I use this room?") // deterministic miss
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&stub.calls),
		"contextual router dispatched once")
	require.Equal(t, int64(0), h.calls.Load(),
		"help class must NOT reach the main-turn LLM")

	// Help is read-only: the machine must NOT advance.
	require.Equal(t, app.StatePath("start"), out.NewState,
		"a help verdict must not advance the state machine")

	// The utterance was appended to the room help lane under the room-scoped key.
	chatsForLane, err := cs.List(ctx, "crr-slice1-test", "room:help", "start")
	require.NoError(t, err)
	require.Len(t, chatsForLane, 1, "help verdict must create/append the room help lane")
	msgs, err := cs.Transcript(ctx, chatsForLane[0].ID, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(msgs), 1, "the utterance must be appended to the lane")
	require.Equal(t, "how do I use this room?", msgs[0].Content)
}
