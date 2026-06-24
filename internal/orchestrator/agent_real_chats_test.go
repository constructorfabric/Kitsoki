package orchestrator_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestAgent_E2E_RealChatsStore drives the dev-story Agent flow through the
// orchestrator with a *real* chats.Store backing the host.chat.* handlers (no
// stubs). It also stubs ONLY host.agent.converse so claude isn't invoked. This
// closes the gap left by flow4 / flow8 which use host_handlers: stubs and
// short-circuit handler logic.
//
// Sequence:
//  1. main → go_agent  → agent (on_enter loads chat list — empty)
//  2. agent → new_chat → agent_active_new (on_enter: host.chat.create)
//  3. Verify a chat row was created in the DB.
//  4. agent_active_new → ask_question → agent_asking (on_enter: agent.talk stub + suggest_title stub)
//  5. Verify two chat_messages rows (user + assistant) landed in the DB.
//  6. go_back → agent → list now reports 1 chat.
func TestAgent_E2E_RealChatsStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX paths and the dev-story testdata fixtures")
	}

	// Locate the dev-story app.yaml relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	appPath := filepath.Join(repoRoot, "testdata", "apps", "dev-story", "app.yaml")

	def, err := app.Load(appPath)
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)
	chatStore := chathost.NewAdapter(rawChatStore)

	// Build the host registry with the REAL host.chat.* handlers; stub only
	// host.agent.converse and host.chat.suggest_title so claude isn't invoked.
	reg := host.NewRegistry()
	reg.Register("host.chat.resolve", host.ChatResolveHandler)
	reg.Register("host.chat.list", host.ChatListHandler)
	reg.Register("host.chat.transcript", host.ChatTranscriptHandler)
	reg.Register("host.chat.fork", host.ChatForkHandler)
	reg.Register("host.chat.archive", host.ChatArchiveHandler)
	reg.Register("host.chat.create", host.ChatCreateHandler)
	reg.Register("host.chat.rename", host.ChatRenameHandler)
	reg.Register("host.chat.suggest_title", func(ctx context.Context, args map[string]any) (host.Result, error) {
		chatID, _ := args["chat_id"].(string)
		return host.Result{Data: map[string]any{
			"chat_id":        chatID,
			"title":          "stub suggested title",
			"previous_title": "",
			"renamed":        true,
			"skipped":        false,
		}}, nil
	})
	reg.Register("host.agent.converse", func(ctx context.Context, args map[string]any) (host.Result, error) {
		// Mimic the chat-aware path: append messages so the transcript reflects
		// the turn just like the real handler would.
		chatID, _ := args["chat_id"].(string)
		question, _ := args["question"].(string)
		cs := host.ChatStoreFromContext(ctx)
		if cs == nil || chatID == "" {
			return host.Result{Error: "stub-agent: chat store / chat_id missing"}, nil
		}
		// Acquire the lock just like the real handler so we exercise lock paths.
		var data map[string]any
		lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
			if _, err := cs.AppendMessage(ctx, chatID, "user", question, nil); err != nil {
				return err
			}
			if _, err := cs.AppendMessage(ctx, chatID, "assistant", "stub-answer to: "+question, map[string]any{"exit_code": 0}); err != nil {
				return err
			}
			seq, _ := cs.LatestSeq(ctx, chatID)
			data = map[string]any{
				"answer":            "stub-answer to: " + question,
				"chat_id":           chatID,
				"session_id":        "stub-session",
				"claude_session_id": "stub-session",
				"transcript_seq":    seq,
			}
			return nil
		})
		if lockErr != nil {
			return host.Result{Error: lockErr.Error()}, nil
		}
		return host.Result{Data: data}, nil
	})
	// Other hosts the dev-story app declares but that aren't exercised in
	// this flow can be left unregistered — the orchestrator only invokes
	// what on_enter / effects fire.
	reg.Register("host.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{}}, nil
	})
	reg.Register("host.workspace_manager.get", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{}}, nil
	})
	reg.Register("host.agent.ask", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"answer": ""}}, nil
	})

	orch := orchestrator.New(def, m, s, &noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithChatStore(chatStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Step 1: navigate main → agent.
	out, err := orch.SubmitDirect(ctx, sid, "go_agent", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("agent"), out.NewState)

	// At this point the agent list view fired host.chat.list — should
	// observe count == 0 so far.
	var preCount int
	require.NoError(t, s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&preCount))
	assert.Equal(t, 0, preCount, "no chats should exist before new_chat fires")

	// Step 2: new_chat → agent_active_new (on_enter creates a chat via host.chat.create).
	out, err = orch.SubmitDirect(ctx, sid, "new_chat", map[string]any{
		"title": "End-to-end Test Chat",
	})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("agent_active_new"), out.NewState)

	// Step 3: a chat row exists.
	allChats, err := rawChatStore.List(ctx, "dev-story", "agent", "")
	require.NoError(t, err)
	require.Len(t, allChats, 1, "expected exactly one chat row after new_chat")
	chatID := allChats[0].ID

	// Step 4: ask_question → agent_asking → host.agent.converse stub appends user+assistant.
	out, err = orch.SubmitDirect(ctx, sid, "ask_question", map[string]any{
		"question": "what is the meaning of life?",
	})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("agent_asking"), out.NewState)

	// Step 5: verify chat_messages rows exist (user + assistant).
	msgs, err := rawChatStore.Transcript(ctx, chatID, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "expected user + assistant messages")
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "what is the meaning of life?", msgs[0].Content)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Contains(t, msgs[1].Content, "stub-answer")

	// Step 6: go_back → agent (list should now report 1 chat).
	out, err = orch.SubmitDirect(ctx, sid, "go_back", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("agent"), out.NewState)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	count, _ := journey.World.Vars["agent_chat_count"].(int)
	if count == 0 {
		// world coercion: the binding may have come back as int64 / float64.
		if c64, ok := journey.World.Vars["agent_chat_count"].(int64); ok {
			count = int(c64)
		}
		if cf, ok := journey.World.Vars["agent_chat_count"].(float64); ok {
			count = int(cf)
		}
	}
	assert.Equal(t, 1, count, "after going back to agent, list should report 1 chat")
}
