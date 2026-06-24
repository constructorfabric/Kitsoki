// Slice-2 gate for contextual-room-routing: the room-chat substrate.
// A room chat lane can be created/keyed and read back from the SAME chat store
// meta-mode uses, under a room-scoped key (app, "room:<lane>", state_path);
// posture maps to the right agent verb; start-new / resume / switch active-lane
// works (one active lane per room).
package roomchat_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/roomchat"
	"kitsoki/internal/store"
)

func newResolver(t *testing.T) (roomchat.Resolver, *chats.Store) {
	t.Helper()
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	return roomchat.Resolver{Store: chathost.NewAdapter(cs)}, cs
}

// 2.1: a room lane is keyed (app, "room:<lane>", state_path) in the same store,
// created on first Active, and resumed (same chat id) on the next Active.
func TestRoomLane_KeyedAndResolvedFromStore(t *testing.T) {
	r, cs := newResolver(t)
	ctx := context.Background()

	chat, created, err := r.Active(ctx, "app-1", roomchat.LaneHelp, "start", "help lane")
	require.NoError(t, err)
	require.True(t, created, "first Active must create the lane chat")
	require.Equal(t, "room:help", chat.Room, "lane key must be room:<lane>")
	require.Equal(t, "start", chat.ScopeKey, "scope_key must be the state path")
	require.Equal(t, "app-1", chat.AppID)

	// Read back via the underlying store under the room-scoped key.
	got, err := cs.List(ctx, "app-1", "room:help", "start")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, chat.ID, got[0].ID)

	// Re-resolving the same (app, kind, scope) resumes — no new row.
	again, created2, err := r.Active(ctx, "app-1", roomchat.LaneHelp, "start", "help lane")
	require.NoError(t, err)
	require.False(t, created2, "re-Active must resume the active lane, not create")
	require.Equal(t, chat.ID, again.ID)
}

// 2.1: appending an utterance to a lane is readable back via the store.
func TestRoomLane_AppendUtteranceReadBack(t *testing.T) {
	r, cs := newResolver(t)
	ctx := context.Background()

	chat, _, err := r.Active(ctx, "app-1", roomchat.LaneHelp, "start", "help lane")
	require.NoError(t, err)
	require.NoError(t, r.Append(ctx, chat.ID, "user", "how do I use this room?"))

	msgs, err := cs.Transcript(ctx, chat.ID, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "user", msgs[0].Role)
	require.Equal(t, "how do I use this room?", msgs[0].Content)
}

// 2.2: posture → verb. help lane and a read_only work lane select "ask"
// (read-only). An edit (open) work lane selects a write-capable verb (task or
// converse) — the one the write-mode gate guards.
func TestVerbForLane_PostureSelectsVerb(t *testing.T) {
	require.Equal(t, "ask",
		roomchat.VerbForLane(roomchat.LaneHelp, app.WriteModeOpen),
		"help lane is always read-only ask")
	require.Equal(t, "ask",
		roomchat.VerbForLane(roomchat.LaneWork, app.WriteModeReadOnly),
		"a read_only work lane must use the read-only ask verb")

	editVerb := roomchat.VerbForLane(roomchat.LaneWork, app.WriteModeOpen)
	require.Contains(t, []string{"task", "converse"}, editVerb,
		"an edit work lane must select a write-capable verb (gated by write-mode)")
	require.NotEqual(t, "ask", editVerb)
}

// 2.3: start-new archives the active lane and mints a fresh one; resume returns
// to a prior lane chat; one active lane per (app, kind, scope) at a time.
func TestRoomLane_StartNewResumeSwitch(t *testing.T) {
	r, cs := newResolver(t)
	ctx := context.Background()

	first, _, err := r.Active(ctx, "app-1", roomchat.LaneWork, "start", "work")
	require.NoError(t, err)
	require.NoError(t, r.Append(ctx, first.ID, "user", "first session"))

	// start new: archive current active, mint fresh.
	second, err := r.StartNew(ctx, "app-1", roomchat.LaneWork, "start", "work")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID, "start-new must mint a distinct lane chat")

	// the active lane is now the new one (Resolve skips archived).
	active, created, err := r.Active(ctx, "app-1", roomchat.LaneWork, "start", "work")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, second.ID, active.ID, "one active lane per room: newest wins")

	// resume previous: the older chat is still resumable by id, transcript intact.
	prior, err := r.Resume(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, prior.ID)
	msgs, err := cs.Transcript(ctx, first.ID, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "resumed lane keeps its transcript")

	// switch lane: a different kind has its own active chat under its own key.
	help, _, err := r.Active(ctx, "app-1", roomchat.LaneHelp, "start", "help")
	require.NoError(t, err)
	require.Equal(t, "room:help", help.Room)
	require.NotEqual(t, active.ID, help.ID, "lanes of different kind are distinct")
}
