// Package agent — test suite for agent contract, in-process transport,
// schema validation, and harness adapter.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/world"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return json.RawMessage(b)
}

func sampleRequest() AskRequest {
	return AskRequest{
		SessionID:  app.SessionID("sess-1"),
		TurnNumber: app.TurnNumber(3),
		StatePath:  app.StatePath("planning/refine"),
		Verb:       "decide",
		PromptText: "pick the best option",
		SchemaJSON: nil,
		WithArgs:   map[string]any{"repo": "acme/infra"},
		World:      world.World{Vars: map[string]any{"branch": "fix/CLOUD-42"}},
		Deadline:   time.Now().Add(30 * time.Second),
		CallID:     "abc123def456",
	}
}

// ─── Type contract: JSON round-trip ──────────────────────────────────────────

// TestAskRequestRoundTrip verifies AskRequest round-trips through JSON with
// stable field names.
func TestAskRequestRoundTrip(t *testing.T) {
	req := sampleRequest()
	req.SchemaJSON = mustMarshal(t, map[string]any{"type": "object"})

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got AskRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check stable JSON field names exist in raw bytes.
	for _, field := range []string{
		`"session_id"`, `"turn"`, `"state_path"`, `"verb"`,
		`"prompt"`, `"schema"`, `"with"`, `"world"`, `"deadline"`, `"call_id"`,
	} {
		if !containsBytes(b, field) {
			t.Errorf("JSON output missing field %s; got: %s", field, b)
		}
	}

	if got.SessionID != req.SessionID {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, req.SessionID)
	}
	if got.TurnNumber != req.TurnNumber {
		t.Errorf("TurnNumber: got %d, want %d", got.TurnNumber, req.TurnNumber)
	}
	if got.StatePath != req.StatePath {
		t.Errorf("StatePath: got %q, want %q", got.StatePath, req.StatePath)
	}
	if got.Verb != req.Verb {
		t.Errorf("Verb: got %q, want %q", got.Verb, req.Verb)
	}
	if got.PromptText != req.PromptText {
		t.Errorf("PromptText: got %q, want %q", got.PromptText, req.PromptText)
	}
	if got.CallID != req.CallID {
		t.Errorf("CallID: got %q, want %q", got.CallID, req.CallID)
	}
}

// TestAskResponseRoundTrip verifies AskResponse round-trips through JSON.
func TestAskResponseRoundTrip(t *testing.T) {
	resp := AskResponse{
		Submission: mustMarshal(t, map[string]any{"choice": "a", "score": 0.9}),
		Meta:       map[string]any{"model": "haiku", "tokens": 42},
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got AskResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if string(got.Submission) != string(resp.Submission) {
		t.Errorf("Submission: got %s, want %s", got.Submission, resp.Submission)
	}
	if got.Meta["model"] != "haiku" {
		t.Errorf("Meta.model: got %v, want haiku", got.Meta["model"])
	}
}

// ─── In-process happy path ────────────────────────────────────────────────────

func TestInProcessHappyPath(t *testing.T) {
	want := json.RawMessage(`{"choice":"a"}`)
	fn := AskFunc(func(_ context.Context, _ AskRequest) (AskResponse, error) {
		return AskResponse{Submission: want}, nil
	})
	o := New(fn)
	defer o.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		resp, err := o.Ask(ctx, sampleRequest())
		if err != nil {
			t.Fatalf("Ask #%d: unexpected error: %v", i, err)
		}
		if string(resp.Submission) != string(want) {
			t.Errorf("Ask #%d: got %s, want %s", i, resp.Submission, want)
		}
	}
}

// ─── In-process error path ────────────────────────────────────────────────────

func TestInProcessErrorPath(t *testing.T) {
	fn := AskFunc(func(_ context.Context, _ AskRequest) (AskResponse, error) {
		return AskResponse{}, &AskError{Kind: "plugin_crash", Detail: "intentional"}
	})
	o := New(fn)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AskError, got %T: %v", err, err)
	}
	if ae.Kind != "plugin_crash" {
		t.Errorf("Kind: got %q, want %q", ae.Kind, "plugin_crash")
	}
}

// ─── In-process deadline ──────────────────────────────────────────────────────

func TestInProcessDeadline(t *testing.T) {
	// AskFunc that blocks until ctx is done, then returns an error.
	fn := AskFunc(func(ctx context.Context, _ AskRequest) (AskResponse, error) {
		<-ctx.Done()
		return AskResponse{}, ctx.Err()
	})
	o := New(fn)
	defer o.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := o.Ask(ctx, sampleRequest())
	if err == nil {
		t.Fatal("expected error after deadline, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// TestInProcessContextCancelledAfterReturn covers the safety-net in Ask that
// propagates ctx.Err() even when fn itself returns nil error.
func TestInProcessContextCancelledAfterReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	fn := AskFunc(func(_ context.Context, _ AskRequest) (AskResponse, error) {
		return AskResponse{Submission: json.RawMessage(`{}`)}, nil
	})
	o := New(fn)
	defer o.Close()

	_, err := o.Ask(ctx, sampleRequest())
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected Canceled, got %v", err)
	}
}

// ─── Schema validation ────────────────────────────────────────────────────────

var validSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"choice": {"type": "string"},
		"score":  {"type": "number"}
	},
	"required": ["choice"],
	"additionalProperties": false
}`)

func TestSchemaValidation_Valid(t *testing.T) {
	sub := json.RawMessage(`{"choice":"a","score":0.9}`)
	if err := ValidateSubmission(validSchema, sub); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestSchemaValidation_NilSchemaSkips(t *testing.T) {
	// Any submission passes when schema is nil — intentional bypass.
	sub := json.RawMessage(`this is not even json`)
	if err := ValidateSubmission(nil, sub); err != nil {
		t.Errorf("expected nil error with nil schema, got: %v", err)
	}
}

func TestSchemaValidation_MalformedJSON(t *testing.T) {
	sub := json.RawMessage(`{broken`)
	err := ValidateSubmission(validSchema, sub)
	assertAskError(t, err, "schema_invalid")
}

func TestSchemaValidation_MissingRequired(t *testing.T) {
	// "choice" is required but absent.
	sub := json.RawMessage(`{"score":0.5}`)
	err := ValidateSubmission(validSchema, sub)
	assertAskError(t, err, "schema_invalid")
	ae := mustAskError(t, err)
	if ae.Detail == "" {
		t.Error("expected non-empty Detail for missing required field")
	}
}

func TestSchemaValidation_AdditionalPropertiesDisallowed(t *testing.T) {
	// "extra" is not in properties, and additionalProperties: false.
	sub := json.RawMessage(`{"choice":"a","extra":"not allowed"}`)
	err := ValidateSubmission(validSchema, sub)
	assertAskError(t, err, "schema_invalid")
}

func TestSchemaValidation_AdditionalPropertiesAllowed(t *testing.T) {
	// Schema without additionalProperties constraint: extra fields are fine.
	openSchema := json.RawMessage(`{
		"type": "object",
		"properties": {"choice": {"type": "string"}},
		"required": ["choice"]
	}`)
	sub := json.RawMessage(`{"choice":"a","extra":"fine"}`)
	if err := ValidateSubmission(openSchema, sub); err != nil {
		t.Errorf("expected nil error for open schema with extra fields, got: %v", err)
	}
}

func TestSchemaValidation_NullForRequired(t *testing.T) {
	// "choice" is present but null; the type constraint (string) fails.
	sub := json.RawMessage(`{"choice":null}`)
	err := ValidateSubmission(validSchema, sub)
	assertAskError(t, err, "schema_invalid")
	ae := mustAskError(t, err)
	if ae.Detail == "" {
		t.Error("expected non-empty Detail for null required field")
	}
}

func TestSchemaValidation_AbsentVsNullDistinctDetail(t *testing.T) {
	// Both absent and null "choice" should fail, but with different Detail text.
	absentSub := json.RawMessage(`{"score":1}`)
	nullSub := json.RawMessage(`{"choice":null,"score":1}`)

	errAbsent := ValidateSubmission(validSchema, absentSub)
	errNull := ValidateSubmission(validSchema, nullSub)

	if errAbsent == nil {
		t.Fatal("absent required field: expected error, got nil")
	}
	if errNull == nil {
		t.Fatal("null required field: expected error, got nil")
	}

	aeAbsent := mustAskError(t, errAbsent)
	aeNull := mustAskError(t, errNull)

	if aeAbsent.Detail == aeNull.Detail {
		t.Errorf("expected distinct Detail strings for absent vs null required field:\n absent: %q\n null:   %q",
			aeAbsent.Detail, aeNull.Detail)
	}
}

// ─── Harness adapter happy path ───────────────────────────────────────────────

// stubHarness is a harness.Harness stub for adapter tests.
type stubHarness struct {
	params mcp.CallToolParams
	err    error
	closed bool
	// closedPtr allows test assertions via pointer.
	closedPtr *bool
}

type structuredStubHarness struct {
	stubHarness
	input  harness.StructuredInput
	called bool
}

func (s *structuredStubHarness) RunStructured(_ context.Context, in harness.StructuredInput) (json.RawMessage, error) {
	s.called = true
	s.input = in
	return json.RawMessage(`{"class":"intent","intent":"root__bugfix_report","confidence":0.95}`), nil
}

func (s *stubHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return s.params, s.err
}

func (s *stubHarness) Close() error {
	s.closed = true
	if s.closedPtr != nil {
		*s.closedPtr = true
	}
	return nil
}

func TestFromHarness_HappyPath(t *testing.T) {
	wantArgs := map[string]any{"intent": "accept", "confidence": 0.95}
	stub := &stubHarness{
		params: mcp.CallToolParams{
			Name:      "transition",
			Arguments: wantArgs,
		},
	}
	o := FromHarness(stub)
	defer o.Close()

	resp, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: unexpected error: %v", err)
	}

	// Meta should be nil (harness doesn't expose token info).
	if resp.Meta != nil {
		t.Errorf("Meta: expected nil, got %v", resp.Meta)
	}

	// Submission should be the JSON-marshalled Arguments.
	var got map[string]any
	if err := json.Unmarshal(resp.Submission, &got); err != nil {
		t.Fatalf("unmarshal Submission: %v", err)
	}
	if got["intent"] != "accept" {
		t.Errorf("intent: got %v, want accept", got["intent"])
	}
}

func TestFromHarness_PreservesArbitraryStructuredSchema(t *testing.T) {
	stub := &structuredStubHarness{}
	o := FromHarness(stub)
	defer o.Close()

	req := sampleRequest()
	req.PromptText = "classify this bug report"
	req.SchemaJSON = json.RawMessage(`{
		"type":"object",
		"required":["class","confidence"],
		"properties":{"class":{"enum":["intent","help","room_request","meta_edit"]}}
	}`)
	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: unexpected error: %v", err)
	}
	if !stub.called {
		t.Fatal("schema-bound agent call must use RunStructured, not RunTurn")
	}
	if string(stub.input.SchemaJSON) != string(req.SchemaJSON) {
		t.Fatalf("structured schema changed:\n got: %s\nwant: %s", stub.input.SchemaJSON, req.SchemaJSON)
	}
	if stub.input.Prompt != req.PromptText {
		t.Fatalf("structured prompt = %q, want %q", stub.input.Prompt, req.PromptText)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Submission, &got); err != nil {
		t.Fatalf("unmarshal submission: %v", err)
	}
	if got["class"] != "intent" {
		t.Fatalf("class = %v, want intent", got["class"])
	}
}

func TestFromHarness_RejectsStructuredCallWhenHarnessLacksCapability(t *testing.T) {
	o := FromHarness(&stubHarness{})
	defer o.Close()
	req := sampleRequest()
	req.SchemaJSON = json.RawMessage(`{"type":"object"}`)
	_, err := o.Ask(context.Background(), req)
	assertAskError(t, err, "plugin_unsupported")
}

func TestFromHarness_DeadlineExceeded(t *testing.T) {
	stub := &stubHarness{err: context.DeadlineExceeded}
	o := FromHarness(stub)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	assertAskError(t, err, "deadline_exceeded")
}

func TestFromHarness_ClarifyResponse(t *testing.T) {
	stub := &stubHarness{err: &harness.ClarifyResponse{
		Message:    "please provide a valid intent",
		Underlying: errors.New("LLM did not call the expected tool"),
	}}
	o := FromHarness(stub)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	assertAskError(t, err, "schema_invalid")
	ae := mustAskError(t, err)
	if ae.Detail != "please provide a valid intent" {
		t.Errorf("Detail: got %q, want %q", ae.Detail, "please provide a valid intent")
	}
}

func TestFromHarness_GenericError_PluginCrash(t *testing.T) {
	stub := &stubHarness{err: errors.New("subprocess exited with code 1")}
	o := FromHarness(stub)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	assertAskError(t, err, "plugin_crash")
}

func TestFromHarness_CloseCallsHarnessClose(t *testing.T) {
	var closed bool
	stub := &stubHarness{
		params:    mcp.CallToolParams{},
		closedPtr: &closed,
	}
	o := FromHarness(stub)
	if err := o.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if !closed {
		t.Error("Close: underlying harness.Close was not called")
	}
}

// ─── AskError correctness ─────────────────────────────────────────────────────

func TestAskError_Error(t *testing.T) {
	ae := &AskError{Kind: "plugin_crash", Detail: "subprocess exited with code 1"}
	want := "agent: plugin_crash: subprocess exited with code 1"
	if ae.Error() != want {
		t.Errorf("got %q, want %q", ae.Error(), want)
	}
}

func TestAskError_ErrorFallsBackToUnderlying(t *testing.T) {
	inner := errors.New("inner")
	ae := &AskError{Kind: "transport_error", Underlying: inner}
	if !errors.Is(ae, inner) {
		t.Error("Unwrap: expected errors.Is to reach inner error")
	}
}

func TestAskError_NilSafe(t *testing.T) {
	var ae *AskError
	if ae.Error() != "agent: nil AskError" {
		t.Errorf("nil AskError.Error(): got %q", ae.Error())
	}
	if ae.Unwrap() != nil {
		t.Error("nil AskError.Unwrap(): expected nil")
	}
}

// ─── Concurrent Ask ──────────────────────────────────────────────────────────

// TestInProcessConcurrent runs multiple Ask calls concurrently to validate that
// the in-process transport is race-free under -race.
func TestInProcessConcurrent(t *testing.T) {
	var callCount atomic.Int64
	fn := AskFunc(func(_ context.Context, _ AskRequest) (AskResponse, error) {
		callCount.Add(1)
		return AskResponse{Submission: json.RawMessage(`{"ok":true}`)}, nil
	})
	o := New(fn)
	defer o.Close()

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := o.Ask(context.Background(), sampleRequest())
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Ask: %v", err)
		}
	}
	if callCount.Load() != n {
		t.Errorf("expected %d calls, got %d", n, callCount.Load())
	}
}

// ─── test helpers ─────────────────────────────────────────────────────────────

func assertAskError(t *testing.T, err error, wantKind string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected *AskError{Kind:%q}, got nil", wantKind)
	}
	var ae *AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AskError, got %T: %v", err, err)
	}
	if ae.Kind != wantKind {
		t.Errorf("AskError.Kind: got %q, want %q", ae.Kind, wantKind)
	}
}

func mustAskError(t *testing.T, err error) *AskError {
	t.Helper()
	var ae *AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AskError, got %T: %v", err, err)
	}
	return ae
}

// containsBytes returns true if haystack contains the needle substring.
func containsBytes(haystack []byte, needle string) bool {
	n := []byte(needle)
	for i := 0; i <= len(haystack)-len(n); i++ {
		match := true
		for j := range n {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
