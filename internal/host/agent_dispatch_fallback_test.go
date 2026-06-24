package host_test

// agent_dispatch_fallback_test.go — step 4: local-model validation-reject
// fallback to agent.claude, recorded on the existing agent.call.* trace.
//
// When a builtin.local_llm backend returns a Submission that fails schema
// validation (Kind=="schema_invalid") AND the call was marked eligible via
// host.WithLocalLLMFallback, Dispatch re-dispatches the SAME call exactly once
// to agent.claude under the SAME call_id. On success the closing
// AgentReturned carries a substitution provenance map; without the flag, the
// schema_invalid error is returned and no fallback happens.
//
// All tests use agent.New(AskFunc) (builtin.inprocess) with stub AskFuncs.
// No real LLM calls, no subprocesses, no network — budgeted in ms.

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

// fallbackSchema requires a single string field "choice" and forbids extras.
var fallbackSchema = json.RawMessage(`{
	"type": "object",
	"properties": {"choice": {"type": "string"}},
	"required": ["choice"],
	"additionalProperties": false
}`)

// buildFallbackCtx wires a registry with a "local" backend (the originally
// resolved plugin) and a valid "agent.claude" backend (the fallback target),
// plus a capture sink and call ctx.
func buildFallbackCtx(t *testing.T, local, claude agent.Agent) (context.Context, *captureSink) {
	t.Helper()
	reg := agent.NewRegistry()
	reg.Register("local", local)
	reg.Register("agent.claude", claude)

	sink := &captureSink{}
	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("sess-fallback-test"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("room.state"),
	})
	return ctx, sink
}

// fallbackDispatchRequest builds a decide-shaped request against the "local"
// plugin with the schema attached.
func fallbackDispatchRequest() host.AgentDispatchRequest {
	return host.AgentDispatchRequest{
		Req: agent.AskRequest{
			SessionID:  app.SessionID("sess-fallback-test"),
			TurnNumber: app.TurnNumber(1),
			StatePath:  app.StatePath("room.state"),
			Verb:       "decide",
			PromptText: "decide the verdict",
			SchemaJSON: fallbackSchema,
			World:      world.World{Vars: map[string]any{}},
			Deadline:   time.Now().Add(30 * time.Second),
			CallID:     "call-fallback-001",
		},
		PluginName: "local",
		Verb:       "decide",
		Agent:      "test-agent",
		Model:      "qwen2.5-1.5b",
		PromptText: "decide the verdict",
		InputDesc:  map[string]any{},
	}
}

// schemaInvalidAgent returns a Submission that violates fallbackSchema.
func schemaInvalidAgent() agent.Agent {
	bad := json.RawMessage(`{"choice":"a","extra":"not allowed"}`)
	return agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: bad, Meta: map[string]any{"model": "qwen2.5-1.5b", "grammar": false}}, nil
	}))
}

// validClaudeAgent returns a Submission that satisfies fallbackSchema.
func validClaudeAgent(called *bool) agent.Agent {
	good := json.RawMessage(`{"choice":"a"}`)
	return agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		if called != nil {
			*called = true
		}
		return agent.AskResponse{Submission: good, Meta: map[string]any{"model": "claude"}}, nil
	}))
}

// TestDispatch_LocalLLMFallback_Success verifies that with the fallback flag
// set, a schema_invalid local-model response is rescued by re-dispatching to
// agent.claude under the same call_id, and the closing AgentReturned carries
// the substitution provenance.
//
// Test rigor: WITHOUT the fallback wiring (the WithLocalLLMFallback branch in
// Dispatch), this returns the schema_invalid AskError and writes AgentError,
// so the result.Submission check and the AgentReturned/substitution checks all
// FAIL. Confirmed by TestDispatch_LocalLLMFallback_DisabledHardFails below,
// which exercises the identical setup minus the flag and asserts the failure.
func TestDispatch_LocalLLMFallback_Success(t *testing.T) {
	t.Parallel()

	claudeCalled := false
	ctx, sink := buildFallbackCtx(t, schemaInvalidAgent(), validClaudeAgent(&claudeCalled))
	ctx = host.WithLocalLLMFallback(ctx, "local")

	result, err := host.Dispatch(ctx, fallbackDispatchRequest())
	if err != nil {
		t.Fatalf("Dispatch: unexpected error after fallback: %v", err)
	}
	if !claudeCalled {
		t.Error("agent.claude fallback was not invoked")
	}
	if string(result.Submission) != `{"choice":"a"}` {
		t.Errorf("Submission: got %s, want the valid claude submission", result.Submission)
	}
	if got := result.Meta["fallback_of"]; got != "local" {
		t.Errorf("Meta[fallback_of]: got %v, want \"local\"", got)
	}

	// One AgentCalled and exactly one closing AgentReturned (single call_id pair).
	kinds := sink.kindsInOrder()
	if kinds[0] != store.AgentCalled {
		t.Errorf("events[0]: got %q, want AgentCalled", kinds[0])
	}
	if kinds[len(kinds)-1] != store.AgentReturned {
		t.Errorf("events[last]: got %q, want AgentReturned", kinds[len(kinds)-1])
	}
	if n := countKind(kinds, store.AgentReturned); n != 1 {
		t.Errorf("expected exactly 1 AgentReturned (one closing event), got %d", n)
	}
	if countKind(kinds, store.AgentError) != 0 {
		t.Error("AgentError must NOT be written when the fallback succeeds")
	}

	// All events share the original call_id (one agent.call.* pair).
	for _, e := range sink.events {
		if e.CallID != "call-fallback-001" {
			t.Errorf("event %q carries call_id %q, want call-fallback-001", e.Kind, e.CallID)
		}
	}

	// The closing AgentReturned carries the substitution provenance.
	sub := substitutionOf(t, sink, store.AgentReturned)
	if sub["reason"] != "schema_invalid" {
		t.Errorf("substitution.reason: got %v, want schema_invalid", sub["reason"])
	}
	if sub["original_plugin"] != "local" {
		t.Errorf("substitution.original_plugin: got %v, want local", sub["original_plugin"])
	}
	if sub["fallback_plugin"] != "agent.claude" {
		t.Errorf("substitution.fallback_plugin: got %v, want agent.claude", sub["fallback_plugin"])
	}
}

// TestDispatch_LocalLLMFallback_DisabledHardFails verifies that WITHOUT the
// fallback flag, the identical schema_invalid local-model response fails hard:
// the schema_invalid AskError is returned and AgentError (not AgentReturned)
// is written. This is the negative control proving the success test exercises
// the new path.
func TestDispatch_LocalLLMFallback_DisabledHardFails(t *testing.T) {
	t.Parallel()

	claudeCalled := false
	ctx, sink := buildFallbackCtx(t, schemaInvalidAgent(), validClaudeAgent(&claudeCalled))
	// NOTE: no host.WithLocalLLMFallback — fallback disabled.

	_, err := host.Dispatch(ctx, fallbackDispatchRequest())
	if err == nil {
		t.Fatal("expected schema_invalid error with fallback disabled, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) || ae.Kind != "schema_invalid" {
		t.Fatalf("expected *agent.AskError schema_invalid, got %T: %v", err, err)
	}
	if claudeCalled {
		t.Error("agent.claude must NOT be invoked when fallback is disabled")
	}
	kinds := sink.kindsInOrder()
	if countKind(kinds, store.AgentReturned) != 0 {
		t.Error("AgentReturned must NOT be written when fallback is disabled and validation fails")
	}
	if kinds[len(kinds)-1] != store.AgentError {
		t.Errorf("events[last]: got %q, want AgentError", kinds[len(kinds)-1])
	}
}

// TestDispatch_LocalLLMFallback_FallbackAlsoFails verifies that when the
// agent.claude fallback ALSO returns a schema_invalid Submission, the call
// fails hard with a single closing AgentError that carries the substitution
// provenance (showing a substitution was attempted).
func TestDispatch_LocalLLMFallback_FallbackAlsoFails(t *testing.T) {
	t.Parallel()

	// Both backends return schema-invalid submissions.
	ctx, sink := buildFallbackCtx(t, schemaInvalidAgent(), schemaInvalidAgent())
	ctx = host.WithLocalLLMFallback(ctx, "local")

	_, err := host.Dispatch(ctx, fallbackDispatchRequest())
	if err == nil {
		t.Fatal("expected schema_invalid error when fallback also fails, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) || ae.Kind != "schema_invalid" {
		t.Fatalf("expected *agent.AskError schema_invalid, got %T: %v", err, err)
	}

	kinds := sink.kindsInOrder()
	if kinds[len(kinds)-1] != store.AgentError {
		t.Errorf("events[last]: got %q, want AgentError", kinds[len(kinds)-1])
	}
	if countKind(kinds, store.AgentError) != 1 {
		t.Errorf("expected exactly 1 closing AgentError, got %d", countKind(kinds, store.AgentError))
	}
	if countKind(kinds, store.AgentReturned) != 0 {
		t.Error("AgentReturned must NOT be written when the fallback also fails")
	}

	sub := substitutionOf(t, sink, store.AgentError)
	if sub["reason"] != "schema_invalid" || sub["original_plugin"] != "local" {
		t.Errorf("AgentError substitution provenance missing/wrong: %v", sub)
	}
}

// TestRegistry_IsLocalLLM verifies the registry transport introspection that
// gates the fallback at TryDispatchVerb: a *LocalLLMAgent alias reports true,
// an in-process stub and the default agent report false.
//
// Test rigor: without registry.IsLocalLLM this does not compile; the truth
// values are asserted against a real *LocalLLMAgent vs an inprocess stub.
func TestRegistry_IsLocalLLM(t *testing.T) {
	t.Parallel()
	reg := agent.NewRegistry()
	reg.Register("agent.claude", agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{}, nil
	})))
	reg.Register("agent.local", agent.NewLocalLLM("qwen2.5-1.5b", 0, "", false, "http://127.0.0.1:9", nil))

	if !reg.IsLocalLLM("agent.local") {
		t.Error("IsLocalLLM(agent.local): got false, want true for *LocalLLMAgent")
	}
	if reg.IsLocalLLM("agent.claude") {
		t.Error("IsLocalLLM(agent.claude): got true, want false for inprocess stub")
	}
	// Empty alias resolves to the default (claude), which is not a local_llm.
	if reg.IsLocalLLM("") {
		t.Error("IsLocalLLM(\"\"): got true, want false (resolves to default agent.claude)")
	}
}

// countKind returns how many events in kinds equal k.
func countKind(kinds []store.EventKind, k store.EventKind) int {
	n := 0
	for _, kind := range kinds {
		if kind == k {
			n++
		}
	}
	return n
}

// substitutionOf returns the substitution map from the last event of the given
// kind. Fails the test when the event or field is absent.
func substitutionOf(t *testing.T, sink *captureSink, kind store.EventKind) map[string]any {
	t.Helper()
	for i := len(sink.events) - 1; i >= 0; i-- {
		e := sink.events[i]
		if e.Kind != kind {
			continue
		}
		var payload struct {
			Substitution map[string]any `json:"substitution"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal %q payload: %v", kind, err)
		}
		if payload.Substitution == nil {
			t.Fatalf("%q event carries no substitution provenance; payload=%s", kind, e.Payload)
		}
		return payload.Substitution
	}
	t.Fatalf("no %q event found in trace", kind)
	return nil
}
