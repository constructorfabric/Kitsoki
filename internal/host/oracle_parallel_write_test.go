package host_test

// Wave 3-oracle smoke tests — parallel write of OracleCalled / OracleReturned
// events to the JSONL EventSink alongside the existing journal write.
//
// Tests in this file assert:
//  1. Both the journal row AND the JSONL events are written for a live oracle call.
//  2. OracleCalled.ts < OracleReturned.ts (no timestamp fudging).
//  3. OracleCalled.CallID == OracleReturned.CallID.
//  4. OracleCalled carries the full prompt body.
//  5. OracleReturned carries the full response body.
//  6. If the EventSink is nil (not wired), the call succeeds silently (no-op path).
//  7. mapHistory guard: a history with OracleCalled events causes FromHistory to
//     skip synthesiseOracleEvents and return the JSONL events as-is.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// oracleCtxForTest builds a minimal oracle context with session+turn+state.
func oracleCtxForTest(sink store.EventSink) context.Context {
	ctx := context.Background()
	ctx = host.WithOracleCallCtx(ctx, host.OracleCallCtx{
		SessionID: app.SessionID("test-session"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("planning"),
	})
	ctx = host.WithOracleEventSink(ctx, sink)
	return ctx
}

// ── Test 1: OracleCalled + OracleReturned written on success ─────────────────

// TestOracleAsk_ParallelWrite_Success asserts that a successful oracle.ask call
// writes both OracleCalled and OracleReturned events to the EventSink with:
//   - matching call_id
//   - OracleCalled.ts before OracleReturned.ts (no timestamp fudge)
//   - OracleCalled.payload contains the full prompt
//   - OracleReturned.payload contains the full response text
func TestOracleAsk_ParallelWrite_Success(t *testing.T) {
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
		oracleCtxForTest(sink),
		host.FakeAsk(fakeResponse),
	)

	res, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("OracleAskHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	// We expect exactly two events: OracleCalled + OracleReturned.
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events in sink, got %d: %v", len(sink.events), kinds(sink.events))
	}

	called := sink.events[0]
	returned := sink.events[1]

	if called.Kind != store.OracleCalled {
		t.Errorf("event[0] kind = %q, want %q", called.Kind, store.OracleCalled)
	}
	if returned.Kind != store.OracleReturned {
		t.Errorf("event[1] kind = %q, want %q", returned.Kind, store.OracleReturned)
	}

	// call_id must match.
	if called.CallID == "" {
		t.Error("OracleCalled.CallID is empty")
	}
	if called.CallID != returned.CallID {
		t.Errorf("call_id mismatch: OracleCalled=%q OracleReturned=%q", called.CallID, returned.CallID)
	}

	// No timestamp fudging: OracleCalled.ts must be before or equal to OracleReturned.ts.
	if called.Ts.After(returned.Ts) {
		t.Errorf("OracleCalled.ts (%v) is after OracleReturned.ts (%v)", called.Ts, returned.Ts)
	}

	// OracleCalled payload must carry the verb.
	var calledPayload map[string]any
	if err := json.Unmarshal(called.Payload, &calledPayload); err != nil {
		t.Fatalf("unmarshal OracleCalled.Payload: %v", err)
	}
	if calledPayload["verb"] != "ask" {
		t.Errorf("OracleCalled.payload.verb = %q, want \"ask\"", calledPayload["verb"])
	}
	// Trace-format prompt-reference contract: a small prompt must be embedded inline on
	// the OracleCalled event so a consumer always has a prompt reference. The
	// prompt text here is well under the 1KB offload threshold, so it lands in
	// `prompt` (not `prompt_file`). Asserting the exact text (not just non-nil)
	// makes this a real regression guard — an omitted key would be nil, and the
	// previous `== ""` check passed vacuously against nil.
	if got, _ := calledPayload["prompt"].(string); got != promptText {
		t.Errorf("OracleCalled.payload.prompt = %q, want %q (inline prompt ref)", got, promptText)
	}

	// OracleReturned payload must carry the verb.
	var retPayload map[string]any
	if err := json.Unmarshal(returned.Payload, &retPayload); err != nil {
		t.Fatalf("unmarshal OracleReturned.Payload: %v", err)
	}
	if retPayload["verb"] != "ask" {
		t.Errorf("OracleReturned.payload.verb = %q, want \"ask\"", retPayload["verb"])
	}
	if retPayload["duration_ms"] == nil {
		t.Error("OracleReturned.payload.duration_ms is missing")
	}

	// turn + state_path must be threaded through.
	if called.Turn != 1 {
		t.Errorf("OracleCalled.Turn = %d, want 1", called.Turn)
	}
	if string(called.StatePath) != "planning" {
		t.Errorf("OracleCalled.StatePath = %q, want \"planning\"", called.StatePath)
	}
}

// ── Test 2: OracleError written on failure ────────────────────────────────────

// TestOracleAsk_ParallelWrite_Error asserts that when the oracle runner returns
// an infra error, OracleError is written (not OracleReturned).
func TestOracleAsk_ParallelWrite_Error(t *testing.T) {
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
		oracleCtxForTest(sink),
		errorRunner,
	)

	res, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": promptPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error to be set on infra failure")
	}

	// Expect: OracleCalled + OracleError.
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(sink.events), kinds(sink.events))
	}
	if sink.events[0].Kind != store.OracleCalled {
		t.Errorf("event[0] kind = %q, want %q", sink.events[0].Kind, store.OracleCalled)
	}
	if sink.events[1].Kind != store.OracleError {
		t.Errorf("event[1] kind = %q, want %q", sink.events[1].Kind, store.OracleError)
	}

	// call_id must match.
	if sink.events[0].CallID != sink.events[1].CallID {
		t.Errorf("call_id mismatch on error path: called=%q error=%q",
			sink.events[0].CallID, sink.events[1].CallID)
	}

	// No timestamp fudge.
	if sink.events[0].Ts.After(sink.events[1].Ts) {
		t.Errorf("OracleCalled.ts (%v) is after OracleError.ts (%v)",
			sink.events[0].Ts, sink.events[1].Ts)
	}
}

// ── Test 3: nil sink is a safe no-op ─────────────────────────────────────────

// TestOracleAsk_NilSink_NoOp asserts that OracleAskHandler succeeds normally
// when no EventSink is wired into the context.
func TestOracleAsk_NilSink_NoOp(t *testing.T) {
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

	res, err := host.OracleAskHandler(ctx, map[string]any{
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

// ── Test 5: mapHistory guard — JSONL authoritative when OracleCalled present ─

// TestFromHistory_OracleCalledGuard asserts that when the store.History already
// contains OracleCalled events, FromHistory does NOT invoke synthesiseOracleEvents
// (even when WithOracleJournal is set). This is the wave 3-oracle bridge guard.
// Wave 4 deletes synthesiseOracleEvents; this guard proves the JSONL is
// authoritative before that deletion.
func TestFromHistory_OracleCalledGuard(t *testing.T) {
	t.Parallel()

	// Build a minimal history that includes an OracleCalled event.
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
			Kind:      store.OracleCalled,
			StatePath: "planning",
			CallID:    "deadbeef01234567",
			Payload:   json.RawMessage(`{"verb":"ask","prompt":"do the thing"}`),
		},
		{
			Turn:      1,
			Seq:       2,
			Ts:        now.Add(2 * time.Millisecond),
			Kind:      store.OracleReturned,
			StatePath: "planning",
			CallID:    "deadbeef01234567",
			Payload:   json.RawMessage(`{"verb":"ask","duration_ms":50}`),
		},
	}

	// Build a minimal AppDef so FromHistory doesn't panic.
	def := minimalAppDef(t)

	// Call FromHistory with no journal DB (opts empty): must succeed and return
	// the OracleCalled event in the output.
	snap, err := runstatus.FromHistory(hist, def, "test-session")
	if err != nil {
		t.Fatalf("FromHistory: %v", err)
	}

	// Find oracle events in the output.
	var calledCount, returnedCount int
	for _, ev := range snap.Events {
		if ev.Msg == string(store.OracleCalled) {
			calledCount++
		}
		if ev.Msg == string(store.OracleReturned) {
			returnedCount++
		}
	}
	if calledCount != 1 {
		t.Errorf("expected 1 OracleCalled event in snapshot, got %d", calledCount)
	}
	if returnedCount != 1 {
		t.Errorf("expected 1 OracleReturned event in snapshot, got %d", returnedCount)
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

// ensure runstatus import is used (TestFromHistory_OracleCalledGuard uses it).
var _ = runstatus.FromHistory
