package host_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// fakeOracleBin returns the path to testdata/fake-oracle.sh.
func fakeOracleBin(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-oracle.sh")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fake-oracle.sh not found at %s: %v", path, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("fake-oracle.sh is not executable")
	}
	return path
}

// fakeOneShotBin returns the path to testdata/fake-oneshot.sh.
func fakeOneShotBin(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-oneshot.sh")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("fake-oneshot.sh not found at %s: %v", path, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("fake-oneshot.sh is not executable")
	}
	return path
}

// ── host.oracle.talk (conversational) ─────────────────────────────────────────

// TestOracleTalk_GeneratesSessionID calls the handler with no session_id and
// verifies the handler creates a UUID, invokes the fake binary, and returns
// both the answer and the generated session_id.
func TestOracleTalk_GeneratesSessionID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	res, err := host.OracleTalkHandler(context.Background(), map[string]any{
		"question": "how does X work",
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

	answer, _ := res.Data["answer"].(string)
	if !strings.Contains(answer, "how does X work") {
		t.Fatalf("answer does not echo the question: %q", answer)
	}
	if !strings.Contains(answer, sid) {
		t.Fatalf("answer does not contain the generated session_id: %q (sid=%s)", answer, sid)
	}
}

// TestOracleTalk_PreservesSessionID verifies that when a session_id is passed
// in, it is forwarded to the binary unchanged and returned in the result.
func TestOracleTalk_PreservesSessionID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	const existingSID = "11111111-2222-4333-8444-555555555555"
	res, err := host.OracleTalkHandler(context.Background(), map[string]any{
		"question":   "second turn",
		"session_id": existingSID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if sid, _ := res.Data["session_id"].(string); sid != existingSID {
		t.Fatalf("session_id not preserved: got %q want %q", sid, existingSID)
	}
	if ans, _ := res.Data["answer"].(string); !strings.Contains(ans, existingSID) {
		t.Fatalf("fake binary did not receive existing session_id: %q", ans)
	}
}

// TestOracleTalk_MissingQuestion asserts that an empty question returns an
// application-level error (Result.Error), not a Go error.
func TestOracleTalk_MissingQuestion(t *testing.T) {
	res, err := host.OracleTalkHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for missing question")
	}
}

// TestOracleTalk_BinaryMissing asserts that when the claude binary is not
// available, the handler returns Result.Error with a helpful message and
// still echoes the (possibly generated) session_id so the caller can retry.
func TestOracleTalk_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")

	res, err := host.OracleTalkHandler(context.Background(), map[string]any{
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

// TestOracleTalk_RegisteredAsBuiltin verifies the handler is wired into the
// default Registry via RegisterBuiltins.
func TestOracleTalk_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.oracle.talk"); !ok {
		t.Fatal("host.oracle.talk was not registered by RegisterBuiltins")
	}
}

// ── host.oracle.ask (one-shot) ────────────────────────────────────────────────

// TestOracleAsk_RendersPromptWithArgs verifies that {{ args.X }} placeholders
// in the prompt file are substituted from the handler's args before the
// binary is invoked.
func TestOracleAsk_RendersPromptWithArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotBin(t))

	// Write a prompt file that references two args.
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "repair.md")
	body := "Command: {{ args.failed_cmd }}\nError: {{ args.last_error }}\n"
	if err := os.WriteFile(promptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	res, err := host.OracleAskHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
		"failed_cmd":  "ls /nope",
		"last_error":  "No such file or directory",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	out, _ := res.Data["stdout"].(string)
	if !strings.Contains(out, "ls /nope") {
		t.Fatalf("stdout does not reflect templated failed_cmd: %q", out)
	}
	if !strings.Contains(out, "No such file or directory") {
		t.Fatalf("stdout does not reflect templated last_error: %q", out)
	}

	if ok, _ := res.Data["ok"].(bool); !ok {
		t.Fatal("ok should be true on clean exit")
	}
	if code, _ := res.Data["exit_code"].(int); code != 0 {
		t.Fatalf("exit_code should be 0 on clean exit, got %d", code)
	}
}

// TestOracleAsk_ResolvesRelativePath verifies that a prompt_path is resolved
// relative to KITSOKI_APP_DIR when set.
func TestOracleAsk_ResolvesRelativePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotBin(t))

	dir := t.TempDir()
	promptsDir := filepath.Join(dir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "hi.md"), []byte("hello {{ args.name }}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(host.AppDirEnv, dir)

	res, err := host.OracleAskHandler(context.Background(), map[string]any{
		"prompt_path": "prompts/hi.md",
		"name":        "world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if out, _ := res.Data["stdout"].(string); !strings.Contains(out, "hello world") {
		t.Fatalf("relative prompt_path did not resolve: %q", out)
	}
}

// TestOracleAsk_MissingPromptPath returns an application-level error.
func TestOracleAsk_MissingPromptPath(t *testing.T) {
	res, err := host.OracleAskHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for missing prompt_path")
	}
}

// TestOracleAsk_PromptNotFound returns Result.Error, not a Go error.
func TestOracleAsk_PromptNotFound(t *testing.T) {
	res, err := host.OracleAskHandler(context.Background(), map[string]any{
		"prompt_path": "/definitely/does/not/exist.md",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for missing prompt file")
	}
}

// TestOracleAsk_BinaryMissing returns Result.Error with the install hint.
func TestOracleAsk_BinaryMissing(t *testing.T) {
	t.Setenv(host.OracleBinEnv, "/definitely/does/not/exist/claude")

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error when binary is missing")
	}
}

// TestOracleAsk_NonZeroExit populates exit_code, ok=false, and Result.Error.
func TestOracleAsk_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oneshot.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOneShotBin(t))

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	// Sentinel keyword that the fake binary treats as "exit non-zero".
	if err := os.WriteFile(promptPath, []byte("FAIL please"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := host.OracleAskHandler(context.Background(), map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if ok, _ := res.Data["ok"].(bool); ok {
		t.Fatal("ok should be false on non-zero exit")
	}
	if code, _ := res.Data["exit_code"].(int); code == 0 {
		t.Fatal("exit_code should be non-zero")
	}
	if res.Error == "" {
		t.Fatal("Result.Error should be set on non-zero exit")
	}
}

// TestOracleAsk_RegisteredAsBuiltin verifies the handler is wired into the
// default Registry via RegisterBuiltins.
func TestOracleAsk_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.oracle.ask"); !ok {
		t.Fatal("host.oracle.ask was not registered by RegisterBuiltins")
	}
}

// ── host.oracle.talk chat-aware path ──────────────────────────────────────────

// TestOracleTalk_ChatAwarePath_FirstTurn verifies that on the first turn with a
// chat_id and a ChatStore in context:
//   - user message is appended to the transcript
//   - a new claude_session_id is generated and stored on the chat
//   - assistant message is appended to the transcript
//   - result contains chat_id, claude_session_id, transcript_seq
func TestOracleTalk_ChatAwarePath_FirstTurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-1", Title: "My Chat", Status: "active"})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.OracleTalkHandler(ctx, map[string]any{
		"question": "What is X?",
		"chat_id":  "chat-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	// chat_id echoed back
	if res.Data["chat_id"] != "chat-1" {
		t.Fatalf("expected chat_id=chat-1 in result, got %v", res.Data["chat_id"])
	}

	// claude_session_id generated (non-empty UUID)
	claudeSID, _ := res.Data["claude_session_id"].(string)
	if claudeSID == "" {
		t.Fatal("expected claude_session_id to be generated")
	}

	// transcript_seq present
	seq, _ := res.Data["transcript_seq"].(int)
	if seq < 0 {
		t.Fatalf("expected transcript_seq >= 0, got %d", seq)
	}

	// Two messages in transcript: user + assistant
	msgs := cs.messages["chat-1"]
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages in transcript, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected first message role=user, got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("expected second message role=assistant, got %q", msgs[1].Role)
	}

	// claude session ID stored on chat
	stored, _ := cs.Get(context.Background(), "chat-1")
	if stored.ClaudeSessionID == "" {
		t.Fatal("expected ClaudeSessionID to be stored on chat after first turn")
	}
	if stored.ClaudeSessionID != claudeSID {
		t.Fatalf("stored ClaudeSessionID %q != result claude_session_id %q", stored.ClaudeSessionID, claudeSID)
	}
}

// TestOracleTalk_ChatAwarePath_ReusesSessionID verifies that on the second turn
// the existing claude_session_id is reused, not replaced.
func TestOracleTalk_ChatAwarePath_ReusesSessionID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	const existingClaudeID = "11111111-2222-4333-8444-555555555555"
	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{
		ID:              "chat-1",
		Title:           "My Chat",
		Status:          "active",
		ClaudeSessionID: existingClaudeID,
	})
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.OracleTalkHandler(ctx, map[string]any{
		"question": "Second question",
		"chat_id":  "chat-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	returnedSID, _ := res.Data["claude_session_id"].(string)
	if returnedSID != existingClaudeID {
		t.Fatalf("expected existing claude_session_id %q to be reused, got %q", existingClaudeID, returnedSID)
	}

	// The answer from the fake binary should echo the session_id
	answer, _ := res.Data["answer"].(string)
	if !strings.Contains(answer, existingClaudeID) {
		t.Fatalf("fake binary did not receive existing session_id in answer: %q", answer)
	}
}

// TestOracleTalk_ChatAwarePath_NoChatStore verifies that providing a chat_id
// but no store in context produces a domain-level error.
func TestOracleTalk_ChatAwarePath_NoChatStore(t *testing.T) {
	res, err := host.OracleTalkHandler(context.Background(), map[string]any{
		"question": "anything",
		"chat_id":  "chat-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "no chat store wired") {
		t.Fatalf("expected 'no chat store wired' error, got: %q", res.Error)
	}
}

// TestRunOracleTalkWithChat_AssistantAppendFails_SurfacesError verifies C2:
// when claude succeeded but persisting the assistant message fails, the
// handler returns Result.Error so on_error: routing fires.  The answer is
// still exposed under Result.Data["answer"] so the user sees the reply.
func TestRunOracleTalkWithChat_AssistantAppendFails_SurfacesError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	cs := newFakeChatStore()
	cs.addChat(host.ChatRecord{ID: "chat-c2", Title: "C2 chat", Status: "active"})
	cs.failAppendOnRole = "assistant" // user append succeeds; assistant fails
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.OracleTalkHandler(ctx, map[string]any{
		"question": "ping",
		"chat_id":  "chat-c2",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "persist assistant message") {
		t.Fatalf("expected persist-assistant error in Result.Error, got: %q", res.Error)
	}
	// The user did get the answer — keep it in Data for diagnostics.
	answer, _ := res.Data["answer"].(string)
	if answer == "" {
		t.Fatal("expected Result.Data[\"answer\"] to be present even when persistence failed")
	}
	// The user message must have been appended (only the assistant append fails).
	msgs := cs.messages["chat-c2"]
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("expected exactly one user message in transcript, got %+v", msgs)
	}
}

// TestRunOracleTalkWithChat_SetSessionFails_NoTranscriptPollution verifies
// I10: when SetClaudeSessionID fails, no user message has been appended yet.
// Pre-fix order was append-user → set-session, so a session-write failure
// stranded a user message in the chat with no Claude session to resume.
func TestRunOracleTalkWithChat_SetSessionFails_NoTranscriptPollution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-oracle.sh requires bash")
	}
	t.Setenv(host.OracleBinEnv, fakeOracleBin(t))

	cs := newFakeChatStore()
	// Chat starts with empty ClaudeSessionID so the handler will try to
	// allocate one and call SetClaudeSessionID.
	cs.addChat(host.ChatRecord{ID: "chat-i10", Title: "I10 chat", Status: "active"})
	cs.failSetSession = true
	ctx := host.WithChatStore(context.Background(), cs)

	res, err := host.OracleTalkHandler(ctx, map[string]any{
		"question": "ping",
		"chat_id":  "chat-i10",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "set claude session id") {
		t.Fatalf("expected set-session error in Result.Error, got: %q", res.Error)
	}
	// No transcript pollution: the user message must NOT have been appended.
	msgs := cs.messages["chat-i10"]
	if len(msgs) != 0 {
		t.Fatalf("expected empty transcript on SetClaudeSessionID failure, got %+v", msgs)
	}
}
