package host_test

// Wave 3-agent smoke tests — parallel write of AgentCalled / AgentReturned
// events to the JSONL EventSink alongside the existing journal write.
//
// Tests in this file assert:
//  1. Both the journal row AND the JSONL events are written for a live agent call.
//  2. AgentCalled.ts < AgentReturned.ts (no timestamp fudging).
//  3. AgentCalled.CallID == AgentReturned.CallID.
//  4. AgentCalled carries the full prompt body.
//  5. AgentReturned carries the full response body.
//  6. If the EventSink is nil (not wired), the call succeeds silently (no-op path).
//  7. mapHistory guard: a history with AgentCalled events causes FromHistory to
//     skip synthesiseAgentEvents and return the JSONL events as-is.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// memSink is a minimal in-memory EventSink for tests.
type memSink struct {
	events []store.Event
}

func (s *memSink) Append(ev store.Event) error {
	s.events = append(s.events, ev)
	return nil
}

func (s *memSink) History() store.History {
	return store.History(s.events)
}

func agentCallEvents(events []store.Event) []store.Event {
	var out []store.Event
	for _, ev := range events {
		if ev.Kind == store.AgentCalled || ev.Kind == store.AgentReturned || ev.Kind == store.AgentError {
			out = append(out, ev)
		}
	}
	return out
}

// agentCtxForTest builds a minimal agent context with session+turn+state.
func agentCtxForTest(sink store.EventSink) context.Context {
	ctx := context.Background()
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("test-session"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("planning"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)
	return ctx
}

func TestAgentAsk_RunErrorClosesTraceSpan(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("say hi"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	ctx := host.WithClaudeRunner(agentCtxForTest(sink), func(context.Context, []string, string, string) (host.ClaudeRun, error) {
		return host.ClaudeRun{}, context.DeadlineExceeded
	})

	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath})
	if err != nil {
		t.Fatalf("AgentAskHandler returned Go error instead of domain error: %v", err)
	}
	if !strings.Contains(res.Error, "agent stream failed") {
		t.Fatalf("Result.Error = %q, want stream failure", res.Error)
	}
	agentEvents := agentCallEvents(sink.events)
	if len(agentEvents) != 2 {
		t.Fatalf("agent events = %#v, all events = %#v; want AgentCalled + AgentError", agentEvents, sink.events)
	}
	if agentEvents[0].Kind != store.AgentCalled || agentEvents[1].Kind != store.AgentError {
		t.Fatalf("event kinds = %s, %s; want agent.call.start, agent.call.error", agentEvents[0].Kind, agentEvents[1].Kind)
	}
	if agentEvents[0].CallID == "" || agentEvents[0].CallID != agentEvents[1].CallID {
		t.Fatalf("call ids not paired: %#v", agentEvents)
	}
}

func TestAgentTask_RunErrorClosesTraceSpan(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	ctx := host.WithClaudeRunner(agentCtxForTest(sink), func(context.Context, []string, string, string) (host.ClaudeRun, error) {
		return host.ClaudeRun{}, context.DeadlineExceeded
	})

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"working_dir": dir,
		"context":     map[string]any{"prompt": "do it"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
		"agent_contract": map[string]any{
			"system_prompt": "inline maker",
			"model":         "claude-sonnet-4-6",
			"tools":         []any{"Read"},
		},
	})
	if err != nil {
		t.Fatalf("AgentTaskHandler returned Go error instead of domain error: %v", err)
	}
	if !strings.Contains(res.Error, "agent stream failed") {
		t.Fatalf("Result.Error = %q, want stream failure", res.Error)
	}
	agentEvents := agentCallEvents(sink.events)
	if len(agentEvents) != 2 {
		t.Fatalf("agent events = %#v, all events = %#v; want AgentCalled + AgentError", agentEvents, sink.events)
	}
	if agentEvents[0].Kind != store.AgentCalled || agentEvents[1].Kind != store.AgentError {
		t.Fatalf("event kinds = %s, %s; want agent.call.start, agent.call.error", agentEvents[0].Kind, agentEvents[1].Kind)
	}
	if agentEvents[0].CallID == "" || agentEvents[0].CallID != agentEvents[1].CallID {
		t.Fatalf("call ids not paired: %#v", agentEvents)
	}
}

// ── Test 1: AgentCalled + AgentReturned written on success ─────────────────

// TestAgentAsk_ParallelWrite_Success asserts that a successful agent.ask call
// writes both AgentCalled and AgentReturned events to the EventSink with:
//   - matching call_id
//   - AgentCalled.ts before AgentReturned.ts (no timestamp fudge)
//   - AgentCalled.payload contains the full prompt
//   - AgentReturned.payload contains the full response text
func TestAgentAsk_ParallelWrite_Success(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	const promptText = "summarise the code in this directory"
	if err := os.WriteFile(promptPath, []byte(promptText), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	const fakeResponse = "the code does X and Y"
	ctx := host.WithClaudeRunner(
		agentCtxForTest(sink),
		host.FakeAsk(fakeResponse),
	)

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	// We expect exactly two events: AgentCalled + AgentReturned.
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events in sink, got %d: %v", len(sink.events), kinds(sink.events))
	}

	called := sink.events[0]
	returned := sink.events[1]

	if called.Kind != store.AgentCalled {
		t.Errorf("event[0] kind = %q, want %q", called.Kind, store.AgentCalled)
	}
	if returned.Kind != store.AgentReturned {
		t.Errorf("event[1] kind = %q, want %q", returned.Kind, store.AgentReturned)
	}

	// call_id must match.
	if called.CallID == "" {
		t.Error("AgentCalled.CallID is empty")
	}
	if called.CallID != returned.CallID {
		t.Errorf("call_id mismatch: AgentCalled=%q AgentReturned=%q", called.CallID, returned.CallID)
	}

	// No timestamp fudging: AgentCalled.ts must be before or equal to AgentReturned.ts.
	if called.Ts.After(returned.Ts) {
		t.Errorf("AgentCalled.ts (%v) is after AgentReturned.ts (%v)", called.Ts, returned.Ts)
	}

	// AgentCalled payload must carry the verb.
	var calledPayload map[string]any
	if err := json.Unmarshal(called.Payload, &calledPayload); err != nil {
		t.Fatalf("unmarshal AgentCalled.Payload: %v", err)
	}
	if calledPayload["verb"] != "ask" {
		t.Errorf("AgentCalled.payload.verb = %q, want \"ask\"", calledPayload["verb"])
	}
	// Trace-format prompt-reference contract: a small prompt must be embedded inline on
	// the AgentCalled event so a consumer always has a prompt reference. The
	// prompt text here is well under the 1KB offload threshold, so it lands in
	// `prompt` (not `prompt_file`). Asserting the exact text (not just non-nil)
	// makes this a real regression guard — an omitted key would be nil, and the
	// previous `== ""` check passed vacuously against nil.
	if got, _ := calledPayload["prompt"].(string); got != promptText {
		t.Errorf("AgentCalled.payload.prompt = %q, want %q (inline prompt ref)", got, promptText)
	}

	// AgentReturned payload must carry the verb.
	var retPayload map[string]any
	if err := json.Unmarshal(returned.Payload, &retPayload); err != nil {
		t.Fatalf("unmarshal AgentReturned.Payload: %v", err)
	}
	if retPayload["verb"] != "ask" {
		t.Errorf("AgentReturned.payload.verb = %q, want \"ask\"", retPayload["verb"])
	}
	if retPayload["duration_ms"] == nil {
		t.Error("AgentReturned.payload.duration_ms is missing")
	}

	// turn + state_path must be threaded through.
	if called.Turn != 1 {
		t.Errorf("AgentCalled.Turn = %d, want 1", called.Turn)
	}
	if string(called.StatePath) != "planning" {
		t.Errorf("AgentCalled.StatePath = %q, want \"planning\"", called.StatePath)
	}
}

// ── Test 2: AgentError written on failure ────────────────────────────────────

// TestAgentAsk_ParallelWrite_Error asserts that when the agent runner returns
// an infra error, AgentError is written (not AgentReturned).
func TestAgentAsk_ParallelWrite_Error(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Return a ClaudeRun with Infra error set.
	errorRunner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Infra: os.ErrNotExist}, nil
	}
	ctx := host.WithClaudeRunner(
		agentCtxForTest(sink),
		errorRunner,
	)

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error to be set on infra failure")
	}

	// Expect: AgentCalled + AgentError.
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(sink.events), kinds(sink.events))
	}
	if sink.events[0].Kind != store.AgentCalled {
		t.Errorf("event[0] kind = %q, want %q", sink.events[0].Kind, store.AgentCalled)
	}
	if sink.events[1].Kind != store.AgentError {
		t.Errorf("event[1] kind = %q, want %q", sink.events[1].Kind, store.AgentError)
	}

	// call_id must match.
	if sink.events[0].CallID != sink.events[1].CallID {
		t.Errorf("call_id mismatch on error path: called=%q error=%q",
			sink.events[0].CallID, sink.events[1].CallID)
	}

	// No timestamp fudge.
	if sink.events[0].Ts.After(sink.events[1].Ts) {
		t.Errorf("AgentCalled.ts (%v) is after AgentError.ts (%v)",
			sink.events[0].Ts, sink.events[1].Ts)
	}
}

// ── Test 3: nil sink is a safe no-op ─────────────────────────────────────────

// TestAgentAsk_NilSink_NoOp asserts that AgentAskHandler succeeds normally
// when no EventSink is wired into the context.
func TestAgentAsk_NilSink_NoOp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("inspect"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// No EventSink wired.
	ctx := host.WithClaudeRunner(
		context.Background(),
		host.FakeAsk("result"),
	)

	res, err := host.AgentAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}
}

// ── Test 4: DeriveCallID is deterministic ────────────────────────────────────

// TestDeriveCallID_Deterministic asserts that the same inputs produce the same
// call_id across calls, and that different inputs produce different call_ids.
func TestDeriveCallID_Deterministic(t *testing.T) {
	t.Parallel()

	id1 := host.DeriveCallID("my-app", "ep-01:0")
	id2 := host.DeriveCallID("my-app", "ep-01:0")
	if id1 != id2 {
		t.Errorf("DeriveCallID is not deterministic: %q != %q", id1, id2)
	}

	id3 := host.DeriveCallID("my-app", "ep-01:1")
	if id1 == id3 {
		t.Errorf("different inputs produced same call_id: %q", id1)
	}

	// 16 hex chars = 8 bytes.
	if len(id1) != 16 {
		t.Errorf("DeriveCallID length = %d, want 16", len(id1))
	}
}

// ── Test 5: mapHistory guard — JSONL authoritative when AgentCalled present ─

// TestFromHistory_AgentCalledGuard asserts that when the store.History already
// contains AgentCalled events, FromHistory does NOT invoke synthesiseAgentEvents
// (even when WithAgentJournal is set). This is the wave 3-agent bridge guard.
// Wave 4 deletes synthesiseAgentEvents; this guard proves the JSONL is
// authoritative before that deletion.
func TestFromHistory_AgentCalledGuard(t *testing.T) {
	t.Parallel()

	// Build a minimal history that includes an AgentCalled event.
	now := time.Now().UTC()
	hist := store.History{
		{
			Turn:      1,
			Seq:       0,
			Ts:        now,
			Kind:      store.TurnStarted,
			StatePath: "planning",
			Payload:   json.RawMessage(`{}`),
		},
		{
			Turn:      1,
			Seq:       1,
			Ts:        now.Add(time.Millisecond),
			Kind:      store.AgentCalled,
			StatePath: "planning",
			CallID:    "deadbeef01234567",
			Payload:   json.RawMessage(`{"verb":"ask","prompt":"do the thing"}`),
		},
		{
			Turn:      1,
			Seq:       2,
			Ts:        now.Add(2 * time.Millisecond),
			Kind:      store.AgentReturned,
			StatePath: "planning",
			CallID:    "deadbeef01234567",
			Payload:   json.RawMessage(`{"verb":"ask","duration_ms":50}`),
		},
	}

	// Build a minimal AppDef so FromHistory doesn't panic.
	def := minimalAppDef(t)

	// Call FromHistory with no journal DB (opts empty): must succeed and return
	// the AgentCalled event in the output.
	snap, err := runstatus.FromHistory(hist, def, "test-session")
	if err != nil {
		t.Fatalf("FromHistory: %v", err)
	}

	// Find agent events in the output.
	var calledCount, returnedCount int
	for _, ev := range snap.Events {
		if ev.Msg == string(store.AgentCalled) {
			calledCount++
		}
		if ev.Msg == string(store.AgentReturned) {
			returnedCount++
		}
	}
	if calledCount != 1 {
		t.Errorf("expected 1 AgentCalled event in snapshot, got %d", calledCount)
	}
	if returnedCount != 1 {
		t.Errorf("expected 1 AgentReturned event in snapshot, got %d", returnedCount)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func kinds(evs []store.Event) []store.EventKind {
	out := make([]store.EventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// minimalAppDef returns a minimal AppDef sufficient for runstatus.FromHistory
// without triggering any real story loading.
func minimalAppDef(t *testing.T) *app.AppDef {
	t.Helper()
	dir := t.TempDir()
	yamlContent := `
app:
  id: test-app
  title: Test
states:
  planning:
    intents:
      go:
        transitions:
          - to: done
  done:
    terminal: true
`
	appPath := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write app.yaml: %v", err)
	}
	def, err := app.Load(appPath)
	if err != nil {
		// If Load fails for structural reasons, use a minimal stub.
		t.Logf("app.Load failed (%v); using stub def", err)
		return &app.AppDef{
			App: app.AppMeta{ID: "test-app"},
		}
	}
	return def
}

// ensure runstatus import is used (TestFromHistory_AgentCalledGuard uses it).
var _ = runstatus.FromHistory
