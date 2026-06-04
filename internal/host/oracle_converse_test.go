package host_test

// host.oracle.converse tests.
//
// All tests use FakeConverse / ClaudeRunner stubs (no real LLM). Tests are
// independent and parallel where possible; each finishes in milliseconds.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// stubConverseRunner returns a ClaudeRunner that echoes the permission_mode
// flag it received so tests can verify it was plumbed through correctly.
// Reply format: "ANSWER permission=[<mode>] sid=<sid> system=[<sp>] model=[<m>] tools=[<t>]"
// Missing fields are omitted.
func stubConverseRunner() host.ClaudeRunner {
	return func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		var permMode, sessionID, resumeID, systemPrompt, model, tools, denied string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--permission-mode":
				if i+1 < len(args) {
					permMode = args[i+1]
					i++
				}
			case "--disallowedTools":
				if i+1 < len(args) {
					denied = args[i+1]
					i++
				}
			case "--session-id":
				if i+1 < len(args) {
					sessionID = args[i+1]
					i++
				}
			case "--resume":
				if i+1 < len(args) {
					resumeID = args[i+1]
					i++
				}
			case "--append-system-prompt":
				if i+1 < len(args) {
					systemPrompt = args[i+1]
					i++
				}
			case "--model":
				if i+1 < len(args) {
					model = args[i+1]
					i++
				}
			case "--allowedTools":
				if i+1 < len(args) {
					tools = args[i+1]
					i++
				}
			}
		}
		sid := sessionID
		if sid == "" {
			sid = resumeID
		}
		out := "ANSWER"
		if permMode != "" {
			out += " permission=[" + permMode + "]"
		}
		if sid != "" {
			out += " sid=" + sid
		}
		if systemPrompt != "" {
			out += " system=[" + systemPrompt + "]"
		}
		if model != "" {
			out += " model=[" + model + "]"
		}
		if tools != "" {
			out += " tools=[" + tools + "]"
		}
		if denied != "" {
			out += " denied=[" + denied + "]"
		}
		return host.ClaudeRun{Stdout: out}, nil
	}
}

// ── Mandatory args ────────────────────────────────────────────────────────────

// TestOracleConverse_MissingQuestion returns Result.Error, not a Go error.
func TestOracleConverse_MissingQuestion(t *testing.T) {
	t.Parallel()
	res, err := host.OracleConverseHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for missing question")
	}
	if !strings.Contains(res.Error, "question argument is required") {
		t.Fatalf("expected 'question argument is required', got: %q", res.Error)
	}
}

// TestOracleConverse_InvalidPermissionMode returns Result.Error.
func TestOracleConverse_InvalidPermissionMode(t *testing.T) {
	t.Parallel()
	res, err := host.OracleConverseHandler(context.Background(), map[string]any{
		"question":        "hello",
		"permission_mode": "superuser",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for unknown permission_mode")
	}
	if !strings.Contains(res.Error, "unknown permission_mode") {
		t.Fatalf("expected 'unknown permission_mode', got: %q", res.Error)
	}
}

// ── permission_mode plumbing ──────────────────────────────────────────────────

// TestOracleConverse_DefaultPermissionMode verifies that omitting permission_mode
// defaults to bypassPermissions — matches legacy talk behaviour.
func TestOracleConverse_DefaultPermissionMode(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "ping",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "permission=[bypassPermissions]") {
		t.Fatalf("expected default permission_mode bypassPermissions in answer; got %q", ans)
	}
}

// TestOracleConverse_PermissionModeAsk verifies permission_mode: ask is
// translated to the CLI-valid enforcing mode (claude rejects "ask" verbatim).
func TestOracleConverse_PermissionModeAsk(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question":        "what should I do?",
		"permission_mode": "ask",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "permission=[default]") {
		t.Fatalf("expected ask translated to permission_mode default; got %q", ans)
	}
	if strings.Contains(ans, "permission=[ask]") {
		t.Fatalf("permission_mode ask must not reach the CLI verbatim; got %q", ans)
	}
}

// TestOracleConverse_PermissionModeDenyAll verifies permission_mode: denyAll is
// translated to the enforcing mode plus a hard --disallowedTools deny of every
// mutating tool (claude rejects "denyAll" verbatim).
func TestOracleConverse_PermissionModeDenyAll(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question":        "list files",
		"permission_mode": "denyAll",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "permission=[default]") {
		t.Fatalf("expected denyAll translated to permission_mode default; got %q", ans)
	}
	for _, tool := range []string{"Write", "Edit", "Bash"} {
		if !strings.Contains(ans, tool) {
			t.Fatalf("expected denyAll to disallow %s; got %q", tool, ans)
		}
	}
}

// ── Session ID / answer ───────────────────────────────────────────────────────

// TestOracleConverse_GeneratesSessionID verifies that omitting session_id causes
// the handler to mint a v4 UUID and return it.
func TestOracleConverse_GeneratesSessionID(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	sid, _ := res.Data["session_id"].(string)
	if sid == "" {
		t.Fatal("expected session_id to be generated")
	}
	uuidRE := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRE.MatchString(sid) {
		t.Fatalf("session_id %q is not a v4 UUID", sid)
	}
}

// TestOracleConverse_AnswerPresent verifies the answer field is populated.
func TestOracleConverse_AnswerPresent(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if ans == "" {
		t.Fatal("expected non-empty answer")
	}
}

// ── Agent plumbing ────────────────────────────────────────────────────────────

// TestOracleConverse_AgentSystemPromptAndModel verifies agent: resolves and
// forwards SystemPrompt + Model to claude.
func TestOracleConverse_AgentSystemPromptAndModel(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"dev-pair": {SystemPrompt: "pair programmer", Model: "claude-sonnet-4-6"},
		}),
		stubConverseRunner(),
	)

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "help me refactor",
		"agent":    "dev-pair",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "system=[pair programmer]") {
		t.Fatalf("agent.SystemPrompt not forwarded; ans=%q", ans)
	}
	if !strings.Contains(ans, "model=[claude-sonnet-4-6]") {
		t.Fatalf("agent.Model not forwarded; ans=%q", ans)
	}
}

// TestOracleConverse_AgentTools_ForwardedAsAllowedTools verifies that agent.Tools
// is forwarded as --allowedTools.
func TestOracleConverse_AgentTools_ForwardedAsAllowedTools(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"full-agent": {SystemPrompt: "sp", Tools: []string{"Read", "Edit", "Bash"}},
		}),
		stubConverseRunner(),
	)

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "do the work",
		"agent":    "full-agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "tools=[Read,Edit,Bash]") {
		t.Fatalf("agent.Tools not forwarded as --allowedTools; ans=%q", ans)
	}
}

// ── ChatStore transcript persistence ─────────────────────────────────────────

// TestOracleConverse_ChatStore_FirstTurn verifies that on the first turn:
// - user message is appended to the transcript
// - a new claude_session_id is generated and stored
// - assistant message is appended
// - result includes chat_id, claude_session_id, transcript_seq
func TestOracleConverse_ChatStore_FirstTurn(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "conv-1", Title: "Converse", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "first question",
		"chat_id":  "conv-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	if res.Data["chat_id"] != "conv-1" {
		t.Fatalf("expected chat_id=conv-1, got %v", res.Data["chat_id"])
	}
	claudeSID, _ := res.Data["claude_session_id"].(string)
	if claudeSID == "" {
		t.Fatal("expected claude_session_id to be generated")
	}
	seq, _ := res.Data["transcript_seq"].(int)
	if seq < 0 {
		t.Fatalf("expected transcript_seq >= 0, got %d", seq)
	}

	msgs := cs.messages["conv-1"]
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (user+assistant), got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("unexpected message roles: %v %v", msgs[0].Role, msgs[1].Role)
	}

	stored, _ := cs.Get(context.Background(), "conv-1")
	if stored.ClaudeSessionID != claudeSID {
		t.Fatalf("stored ClaudeSessionID %q != result %q", stored.ClaudeSessionID, claudeSID)
	}
}

// TestOracleConverse_ChatStore_ReusesSessionID verifies that a second turn
// reuses the existing claude_session_id via --resume.
func TestOracleConverse_ChatStore_ReusesSessionID(t *testing.T) {
	t.Parallel()
	const existingClaudeID = "22222222-3333-4444-8555-666666666666"
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{
		ID:              "conv-2",
		Title:           "Converse",
		Status:          "active",
		ClaudeSessionID: existingClaudeID,
	})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question": "follow up",
		"chat_id":  "conv-2",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	// The answer should echo the session ID (stub echoes sid=<value>).
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, existingClaudeID) {
		t.Fatalf("expected existing claude session ID %q in answer; got %q", existingClaudeID, ans)
	}
}

// TestOracleConverse_ChatStore_NoChatStore returns a domain error when
// chat_id is set but no ChatStore is wired.
func TestOracleConverse_ChatStore_NoChatStore(t *testing.T) {
	t.Parallel()
	res, err := host.OracleConverseHandler(context.Background(), map[string]any{
		"question": "anything",
		"chat_id":  "some-chat",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "no chat store wired") {
		t.Fatalf("expected 'no chat store wired' error, got: %q", res.Error)
	}
}

// TestOracleConverse_ChatStore_PermissionModeForwarded verifies that
// permission_mode is forwarded on the chat-aware path too.
func TestOracleConverse_ChatStore_PermissionModeForwarded(t *testing.T) {
	t.Parallel()
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "conv-pm", Title: "PM test", Status: "active"})
	ctx := host.WithClaudeRunner(host.WithChatStore(context.Background(), cs), stubConverseRunner())

	res, err := host.OracleConverseHandler(ctx, map[string]any{
		"question":        "do something risky",
		"chat_id":         "conv-pm",
		"permission_mode": "ask",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	ans, _ := res.Data["answer"].(string)
	if !strings.Contains(ans, "permission=[default]") {
		t.Fatalf("expected ask translated to permission=[default] on the chat path; got %q", ans)
	}
}

// ── Registration ──────────────────────────────────────────────────────────────

// TestOracleConverse_RegisteredAsBuiltin verifies OracleConverseHandler is
// wired into the registry by RegisterBuiltins.
func TestOracleConverse_RegisteredAsBuiltin(t *testing.T) {
	t.Parallel()
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.oracle.converse"); !ok {
		t.Fatal("host.oracle.converse was not registered by RegisterBuiltins")
	}
}

// TestOracleTalk_RemovedAsBuiltin verifies the deprecated alias is no longer
// registered after Phase 9 alias removal.
func TestOracleTalk_RemovedAsBuiltin(t *testing.T) {
	t.Parallel()
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.oracle.talk"); ok {
		t.Fatal("host.oracle.talk should have been removed in Phase 9 but is still registered")
	}
}

// ── Binary missing ────────────────────────────────────────────────────────────

// TestOracleConverse_BinaryMissing returns Result.Error with a helpful message.
func TestOracleConverse_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")

	res, err := host.OracleConverseHandler(context.Background(), map[string]any{
		"question": "anything",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error when binary is missing")
	}
	if sid, _ := res.Data["session_id"].(string); sid == "" {
		t.Fatal("expected a session_id to be echoed even on failure so caller can retry")
	}
}

// ── Replay shape ─────────────────────────────────────────────────────────────

// TestRenderConverseSpan verifies that RenderConverseSpan produces the
// opaque block format (decision D10: converse spans render as opaque blocks).
func TestRenderConverseSpan(t *testing.T) {
	t.Parallel()
	out := host.RenderConverseSpan("chat-abc", 12, 18)
	if !strings.Contains(out, "converse(chat=chat-abc") {
		t.Fatalf("expected 'converse(chat=chat-abc' in output; got %q", out)
	}
	if !strings.Contains(out, "seq=[12..18]") {
		t.Fatalf("expected 'seq=[12..18]' in output; got %q", out)
	}
	if !strings.Contains(out, "6 turns") {
		t.Fatalf("expected '6 turns' in output; got %q", out)
	}
	if !strings.Contains(out, "ChatStore") {
		t.Fatalf("expected 'ChatStore' in output; got %q", out)
	}
}
