package host_test

// agent_dispatch_test.go — tests for the B-2 agent dispatcher.
//
// Coverage:
//   - Dispatcher calls agent.Ask and writes AgentCalled + AgentReturned events.
//   - Schema validation failure writes AgentError, not AgentReturned.
//   - errNoRegistry fallthrough when no registry is wired.
//   - Default plugin resolution (agent.claude) when PluginName is empty.
//   - SubEvents appended between AgentCalled and AgentReturned.
//
// All tests use agent.New(AskFunc) (builtin.inprocess) with a stub AskFunc.
// No real LLM calls; no real subprocesses.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// captureSink is a simple store.EventSink that records appended events.
type captureSink struct {
	events []store.Event
}

func (s *captureSink) Append(e store.Event) error {
	s.events = append(s.events, e)
	return nil
}

func (s *captureSink) History() store.History { return store.History(s.events) }

func (s *captureSink) kindsInOrder() []store.EventKind {
	var kinds []store.EventKind
	for _, e := range s.events {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// buildDispatchCtx injects an agent.Registry with the given agent under
// "agent.claude" and an event sink for capturing events.
func buildDispatchCtx(t *testing.T, o agent.Agent) (context.Context, *captureSink) {
	t.Helper()
	reg := agent.NewRegistry()
	reg.Register("agent.claude", o)

	sink := &captureSink{}
	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("sess-dispatch-test"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("room.state"),
	})
	return ctx, sink
}

// sampleDispatchRequest builds a minimal AgentDispatchRequest.
func sampleDispatchRequest() host.AgentDispatchRequest {
	return host.AgentDispatchRequest{
		Req: agent.AskRequest{
			SessionID:  app.SessionID("sess-dispatch-test"),
			TurnNumber: app.TurnNumber(1),
			StatePath:  app.StatePath("room.state"),
			Verb:       "ask",
			PromptText: "what should I do?",
			WithArgs:   map[string]any{"repo": "test/repo"},
			World:      world.World{Vars: map[string]any{}},
			Deadline:   time.Now().Add(30 * time.Second),
			CallID:     "call-dispatch-001",
		},
		PluginName:   "agent.claude",
		Verb:         "ask",
		Agent:        "test-agent",
		Model:        "haiku",
		PromptText:   "what should I do?",
		SystemPrompt: "you are a helpful assistant",
		InputDesc:    map[string]any{},
	}
}

// TestDispatch_HappyPath verifies that Dispatch writes AgentCalled +
// AgentReturned events when the agent succeeds.
func TestDispatch_HappyPath(t *testing.T) {
	t.Parallel()
	want := json.RawMessage(`{"choice":"a","score":0.9}`)
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: want}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	result, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}
	if string(result.Submission) != string(want) {
		t.Errorf("Submission: got %s, want %s", result.Submission, want)
	}

	kinds := sink.kindsInOrder()
	if len(kinds) < 2 {
		t.Fatalf("expected at least 2 events (AgentCalled, AgentReturned), got %d: %v", len(kinds), kinds)
	}
	if kinds[0] != store.AgentCalled {
		t.Errorf("events[0]: got %q, want AgentCalled", kinds[0])
	}
	if kinds[len(kinds)-1] != store.AgentReturned {
		t.Errorf("events[last]: got %q, want AgentReturned", kinds[len(kinds)-1])
	}
}

// TestDispatch_NoRegistry verifies that Dispatch returns the errNoRegistry
// sentinel when no registry is wired.
func TestDispatch_NoRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background() // no registry injected
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err == nil {
		t.Fatal("expected errNoRegistry, got nil")
	}
	if !host.IsNoRegistryError(err) {
		t.Errorf("expected IsNoRegistryError(err)==true, got false; err=%v", err)
	}
}

// TestDispatch_AgentError verifies that an agent.Ask error writes AgentError
// and returns the error.
func TestDispatch_AgentError(t *testing.T) {
	t.Parallel()
	askErr := &agent.AskError{Kind: "plugin_crash", Detail: "intentional test error"}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{}, askErr
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", err, err)
	}
	if ae.Kind != "plugin_crash" {
		t.Errorf("AskError.Kind: got %q, want plugin_crash", ae.Kind)
	}

	kinds := sink.kindsInOrder()
	if len(kinds) < 2 {
		t.Fatalf("expected at least 2 events (AgentCalled, AgentError), got %d: %v", len(kinds), kinds)
	}
	if kinds[0] != store.AgentCalled {
		t.Errorf("events[0]: got %q, want AgentCalled", kinds[0])
	}
	if kinds[len(kinds)-1] != store.AgentError {
		t.Errorf("events[last]: got %q, want AgentError", kinds[len(kinds)-1])
	}
}

// TestDispatch_SchemaValidationFailure verifies that when the agent returns a
// submission that fails schema validation, AgentError is written and no
// AgentReturned is written.
func TestDispatch_SchemaValidationFailure(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"choice": {"type": "string"}},
		"required": ["choice"],
		"additionalProperties": false
	}`)

	// Agent returns a submission that fails the schema (extra field).
	badSubmission := json.RawMessage(`{"choice":"a","extra":"not allowed"}`)
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: badSubmission}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()
	dr.Req.SchemaJSON = schema

	_, err := host.Dispatch(ctx, dr)
	if err == nil {
		t.Fatal("expected schema validation error, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", err, err)
	}
	if ae.Kind != "schema_invalid" {
		t.Errorf("AskError.Kind: got %q, want schema_invalid", ae.Kind)
	}

	// AgentCalled should be first, AgentError should be last.
	kinds := sink.kindsInOrder()
	if len(kinds) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(kinds), kinds)
	}
	if kinds[0] != store.AgentCalled {
		t.Errorf("events[0]: got %q, want AgentCalled", kinds[0])
	}
	if kinds[len(kinds)-1] != store.AgentError {
		t.Errorf("events[last]: got %q, want AgentError", kinds[len(kinds)-1])
	}
	// AgentReturned must NOT appear.
	for _, k := range kinds {
		if k == store.AgentReturned {
			t.Error("AgentReturned should not be written on schema validation failure")
		}
	}
}

// TestDispatch_SchemaValid verifies that a valid schema submission writes
// AgentReturned (not AgentError).
func TestDispatch_SchemaValid(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"],"additionalProperties":false}`)
	goodSubmission := json.RawMessage(`{"choice":"a"}`)
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: goodSubmission}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()
	dr.Req.SchemaJSON = schema

	result, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result.Submission) != string(goodSubmission) {
		t.Errorf("Submission: got %s, want %s", result.Submission, goodSubmission)
	}

	kinds := sink.kindsInOrder()
	last := kinds[len(kinds)-1]
	if last != store.AgentReturned {
		t.Errorf("events[last]: got %q, want AgentReturned", last)
	}
}

// TestDispatch_SubEvents verifies that SubEvents are appended between
// AgentCalled and AgentReturned when they pass namespace + call_id + size validation.
//
// B-4: sub-event Kind must start with the dispatching plugin name + "." and
// sub-event CallID must match the parent AgentCalled.CallID. The plugin is
// registered as "agent.claude" so the required prefix is "agent.claude.".
func TestDispatch_SubEvents(t *testing.T) {
	t.Parallel()

	const parentCallID = "call-dispatch-001" // matches sampleDispatchRequest().Req.CallID

	// Sub-event Kind must use the plugin namespace prefix "agent.claude.".
	// CallID must match the parent AgentCalled.
	subEvent := store.Event{
		Kind:    store.EventKind("agent.claude.internal_step"),
		Payload: json.RawMessage(`{"step":1}`),
		CallID:  parentCallID,
	}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{subEvent},
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kinds := sink.kindsInOrder()
	// Expected order: AgentCalled, agent.claude.internal_step, AgentReturned
	if len(kinds) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(kinds), kinds)
	}
	if kinds[0] != store.AgentCalled {
		t.Errorf("events[0]: got %q, want AgentCalled", kinds[0])
	}
	if kinds[1] != store.EventKind("agent.claude.internal_step") {
		t.Errorf("events[1]: got %q, want agent.claude.internal_step", kinds[1])
	}
	if kinds[len(kinds)-1] != store.AgentReturned {
		t.Errorf("events[last]: got %q, want AgentReturned", kinds[len(kinds)-1])
	}
}

// TestDispatch_DefaultPluginResolution verifies that empty PluginName resolves
// to agent.claude.
func TestDispatch_DefaultPluginResolution(t *testing.T) {
	t.Parallel()
	called := false
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		called = true
		return agent.AskResponse{Submission: json.RawMessage(`{"ok":true}`)}, nil
	}))

	ctx, _ := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()
	dr.PluginName = "" // empty → should resolve to agent.claude

	_, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("agent.Ask was not called when PluginName was empty")
	}
}

// TestDispatch_CallIDPreserved verifies that the CallID in the request is
// written to the event payload.
func TestDispatch_CallIDPreserved(t *testing.T) {
	t.Parallel()
	o := agent.New(agent.AskFunc(func(_ context.Context, req agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: json.RawMessage(`{}`)}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()
	dr.Req.CallID = "my-stable-call-id"

	_, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check AgentCalled event carries the CallID.
	var foundCallID bool
	for _, e := range sink.events {
		if e.Kind == store.AgentCalled && e.CallID == "my-stable-call-id" {
			foundCallID = true
		}
	}
	if !foundCallID {
		t.Error("AgentCalled event does not carry the expected CallID")
	}
}

// ─── B-4: SubEvents validation tests ─────────────────────────────────────────

// TestDispatch_SubEvents_NamespaceViolation verifies that a sub-event whose Kind
// does not start with the dispatching plugin name + "." causes AgentError and
// no sub-events land in the trace.
func TestDispatch_SubEvents_NamespaceViolation(t *testing.T) {
	t.Parallel()
	const parentCallID = "call-dispatch-001"

	// "agent.other.step" does not start with "agent.claude." — must be rejected.
	badSub := store.Event{
		Kind:   store.EventKind("agent.other.step"),
		CallID: parentCallID,
	}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{badSub},
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err == nil {
		t.Fatal("expected error for namespace violation, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T", err)
	}
	if ae.Kind != "sub_event_namespace_violation" {
		t.Errorf("Kind: got %q, want sub_event_namespace_violation", ae.Kind)
	}

	// AgentError written, no sub-events, no AgentReturned.
	kinds := sink.kindsInOrder()
	for _, k := range kinds {
		if k == store.AgentReturned {
			t.Error("AgentReturned must not be written on namespace violation")
		}
		if k == store.EventKind("agent.other.step") {
			t.Error("namespace-violating sub-event must not appear in trace")
		}
	}
	// AgentCalled and AgentError must be present.
	if !containsKind(kinds, store.AgentCalled) {
		t.Error("AgentCalled must be present even on violation")
	}
	if !containsKind(kinds, store.AgentError) {
		t.Error("AgentError must be written on namespace violation")
	}
}

// TestDispatch_SubEvents_CallIDMismatch verifies that a sub-event with a
// mismatched CallID causes AgentError.
func TestDispatch_SubEvents_CallIDMismatch(t *testing.T) {
	t.Parallel()

	badSub := store.Event{
		Kind:   store.EventKind("agent.claude.step"),
		CallID: "wrong-call-id", // must equal the parent call_id
	}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{badSub},
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err == nil {
		t.Fatal("expected error for call_id mismatch, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T", err)
	}
	if ae.Kind != "sub_event_call_id_mismatch" {
		t.Errorf("Kind: got %q, want sub_event_call_id_mismatch", ae.Kind)
	}
	kinds := sink.kindsInOrder()
	if !containsKind(kinds, store.AgentError) {
		t.Error("AgentError must be written on call_id mismatch")
	}
}

// TestDispatch_SubEvents_Oversize verifies that a sub-event larger than 4096 bytes
// is now accepted (PIPE_BUF limit was removed).
func TestDispatch_SubEvents_Oversize(t *testing.T) {
	if testing.Short() {
		t.Skip("large sub-event test allocates >4KB payload; skipped under -short")
	}
	t.Parallel()
	const parentCallID = "call-dispatch-001"

	// Build a payload that would previously have exceeded 4096 bytes.
	bigPayload, _ := json.Marshal(map[string]any{"data": string(make([]byte, 5000))})
	largeSub := store.Event{
		Kind:    store.EventKind("agent.claude.big_step"),
		CallID:  parentCallID,
		Payload: json.RawMessage(bigPayload),
	}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{largeSub},
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	dr := sampleDispatchRequest()

	_, err := host.Dispatch(ctx, dr)
	if err != nil {
		t.Fatalf("expected no error for large sub-event, got %v", err)
	}
	kinds := sink.kindsInOrder()
	if !containsKind(kinds, store.AgentCalled) ||
		!containsKind(kinds, store.AgentReturned) {
		t.Error("AgentCalled and AgentReturned must be written for successful dispatch")
	}
}

// TestDispatch_SubEvents_NilSlice verifies that a nil SubEvents slice writes
// zero sub-event lines (only AgentCalled + AgentReturned).
func TestDispatch_SubEvents_NilSlice(t *testing.T) {
	t.Parallel()
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  nil, // explicitly nil
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	_, err := host.Dispatch(ctx, sampleDispatchRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kinds := sink.kindsInOrder()
	if len(kinds) != 2 {
		t.Errorf("nil SubEvents: expected 2 events (AgentCalled+AgentReturned), got %d: %v", len(kinds), kinds)
	}
}

// TestDispatch_SubEvents_EmptySlice verifies that an empty SubEvents slice
// (non-nil) produces the same result as nil — zero sub-event lines.
func TestDispatch_SubEvents_EmptySlice(t *testing.T) {
	t.Parallel()
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{}, // empty non-nil
		}, nil
	}))

	ctx, sink := buildDispatchCtx(t, o)
	_, err := host.Dispatch(ctx, sampleDispatchRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kinds := sink.kindsInOrder()
	if len(kinds) != 2 {
		t.Errorf("empty SubEvents: expected 2 events (AgentCalled+AgentReturned), got %d: %v", len(kinds), kinds)
	}
}

// TestDispatch_SubEvents_TsRestamped verifies that kitsoki re-stamps each
// sub-event's ts at append time; the plugin's claimed ts is ignored.
func TestDispatch_SubEvents_TsRestamped(t *testing.T) {
	t.Parallel()
	const parentCallID = "call-dispatch-001"

	// Plugin claims a ts far in the past.
	pastTS := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	subEvent := store.Event{
		Kind:    store.EventKind("agent.claude.step"),
		CallID:  parentCallID,
		Payload: json.RawMessage(`{"ok":true}`),
		Ts:      pastTS,
	}
	o := agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
			SubEvents:  []store.Event{subEvent},
		}, nil
	}))

	before := time.Now()
	ctx, sink := buildDispatchCtx(t, o)
	_, err := host.Dispatch(ctx, sampleDispatchRequest())
	after := time.Now()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the sub-event in sink and verify ts was re-stamped.
	for _, e := range sink.events {
		if e.Kind == store.EventKind("agent.claude.step") {
			if !e.Ts.After(before.Add(-time.Second)) || !e.Ts.Before(after.Add(time.Second)) {
				t.Errorf("sub-event ts %v was not re-stamped (before=%v after=%v)", e.Ts, before, after)
			}
			if e.Ts.Equal(pastTS) {
				t.Error("plugin ts was not discarded — kitsoki must re-stamp")
			}
			return
		}
	}
	t.Error("sub-event not found in trace")
}

// containsKind returns true when kinds contains k.
func containsKind(kinds []store.EventKind, k store.EventKind) bool {
	for _, kind := range kinds {
		if kind == k {
			return true
		}
	}
	return false
}
