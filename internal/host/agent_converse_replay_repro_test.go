package host_test

// Reproduction for bug 2026-06-25T064437Z-web-replay-converse-chat-not-found
//
// "host.agent.converse fails 'chat not found' under kitsoki web --harness replay
//  — prior-art room shows a bogus 'scan failed' banner"
//
// Root cause: under the web replay harness, host.chat.resolve is satisfied from the
// host cassette (returns chat_id: "chat-1") WITHOUT running the real resolve handler,
// so the chat is never created in the live SQLite store. When host.agent.converse
// then runs its REAL handler and calls cs.Get(ctx, chatID), it fails with
// "chat not found" because the chat doesn't exist.
//
// This is asymmetric with the flow-test path:
//   - kitsoki test flows: host.agent.converse is FULLY replaced by a stub → no real
//     cs.Get → no chat needed → passes.
//   - kitsoki web --harness replay: host.agent.converse runs the REAL handler with
//     only the LLM dispatch mocked → real cs.Get → needs chat to exist → fails.
//
// The test simulates the web replay path: an empty ChatStore (no chats), a stubbed
// ClaudeRunner, and a call to AgentConverseHandler with a chat_id that was returned
// by a cassette-stubbed host.chat.resolve. CORRECT behaviour: the call succeeds (no
// "chat not found" error). CURRENT behaviour: fails with "chat not found".

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestAgentConverse_ReplayHarness_CassetteChatID reproduces the web-replay bug:
// when host.chat.resolve is backed by a cassette (returns chat_id without
// creating the chat in the live store), the subsequent host.agent.converse call
// with that chat_id must NOT fail with "chat not found".
//
// This test asserts CORRECT behaviour and is RED on the unfixed code.
func TestAgentConverse_ReplayHarness_CassetteChatID(t *testing.T) {
	t.Parallel()

	// Simulate the web replay harness state:
	// - host.chat.resolve was cassette-stubbed → returned "chat-1" but never
	//   created the chat in the store.
	// - ChatStore is wired (as it would be in a real kitsoki web session) but
	//   has NO chat with id "chat-1".
	cs := newFakeChatStore() // empty — no chats pre-created

	// Wire the ChatStore and a fake ClaudeRunner so no real LLM is needed.
	ctx := host.WithClaudeRunner(
		host.WithChatStore(context.Background(), cs),
		host.FakeConverse("A printable per-scene handout exported straight from the deck spec."),
	)

	// Drive host.agent.converse with the cassette-supplied chat_id.
	// The chat "chat-1" does not exist in the store — this is the buggy path.
	res, err := host.AgentConverseHandler(ctx, map[string]any{
		"question": "What is the scope of this feature?",
		"chat_id":  "chat-1", // cassette returned this; the real chat was never created
	})
	if err != nil {
		t.Fatalf("unexpected Go error from AgentConverseHandler: %v", err)
	}

	// CORRECT behaviour: converse should succeed even when the chat_id came from a
	// cassette-stubbed host.chat.resolve. The handler should NOT propagate a
	// "chat not found" error to the caller (which would set world.last_error and
	// cause downstream rooms to show a bogus "scan failed" banner).
	//
	// BUG: on the unfixed code, res.Error contains
	// "host.agent.converse: get chat chat-1: chat not found: chat-1"
	// and this test FAILs (which is the reproduction).
	if strings.Contains(res.Error, "chat not found") {
		t.Fatalf(
			"BUG 2026-06-25T064437Z-web-replay-converse-chat-not-found reproduced:\n"+
				"host.agent.converse returned 'chat not found' for a cassette-supplied chat_id.\n"+
				"Under kitsoki web --harness replay, host.chat.resolve is cassette-stubbed and\n"+
				"never creates the chat in the live store, so the real cs.Get fails.\n"+
				"Result.Error: %q",
			res.Error,
		)
	}
}
