package host_test

// Session-scoped chat resolution: a brand-new kitsoki session must NEVER adopt
// a prior session's conversation. A /reload or resume of the SAME session
// (same session id) still reuses the chat. This is an absolute invariant —
// chats never persist beyond a session.
//
// Regression of record: the prd story's discovery chat was keyed only by
// (app, room, scope_key=workdir), with no session dimension. Every new `kitsoki
// run` from the same workdir resumed the SAME chat — so a persona that leaked
// into the transcript once (BMAD's "John") replayed forever via --resume. The
// fix folds the session id into the chat identity unconditionally.

import (
	"context"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

func resolveInSession(t *testing.T, cs host.ChatStore, sid string, args map[string]any) (string, bool) {
	t.Helper()
	ctx := host.WithChatStore(context.Background(), cs)
	if sid != "" {
		ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{SessionID: app.SessionID(sid)})
	}
	res, err := host.ChatResolveHandler(ctx, args)
	if err != nil {
		t.Fatalf("ChatResolveHandler(sid=%q): %v", sid, err)
	}
	if res.Error != "" {
		t.Fatalf("ChatResolveHandler(sid=%q) Result.Error: %s", sid, res.Error)
	}
	id, _ := res.Data["chat_id"].(string)
	isNew, _ := res.Data["is_new"].(bool)
	if id == "" {
		t.Fatalf("ChatResolveHandler(sid=%q): empty chat_id", sid)
	}
	return id, isNew
}

// Default behaviour: a new session gets a fresh chat; the same session reuses it.
func TestChatResolve_SessionScopedByDefault(t *testing.T) {
	cs := realChatStoreForTest(t)
	args := map[string]any{"app": "prd", "room": "idle", "scope_key": "."}

	idA1, newA1 := resolveInSession(t, cs, "sess-A", args)
	idA2, newA2 := resolveInSession(t, cs, "sess-A", args)
	idB1, newB1 := resolveInSession(t, cs, "sess-B", args)

	if !newA1 {
		t.Error("first resolve in session A should create a new chat")
	}
	if newA2 || idA2 != idA1 {
		t.Errorf("second resolve in session A must reuse the chat (is_new=%v, id=%q want %q)", newA2, idA2, idA1)
	}
	if !newB1 {
		t.Error("a NEW session (B) must NOT adopt session A's chat — expected a fresh chat")
	}
	if idB1 == idA1 {
		t.Errorf("session B leaked session A's chat %q — the exact cross-session bug", idA1)
	}
}

// No session in context (stateless `kitsoki turn`, tests): folds to a no-op so
// resolution stays keyed on the bare scope_key — backward compatible.
func TestChatResolve_NoSessionFallsBackToScopeKey(t *testing.T) {
	cs := realChatStoreForTest(t)
	args := map[string]any{"app": "prd", "room": "idle", "scope_key": "."}

	id1, new1 := resolveInSession(t, cs, "", args)
	id2, new2 := resolveInSession(t, cs, "", args)

	if !new1 {
		t.Error("first sessionless resolve should create a new chat")
	}
	if new2 || id2 != id1 {
		t.Errorf("sessionless resolve must reuse by bare scope_key (is_new=%v, id=%q want %q)", new2, id2, id1)
	}
}
