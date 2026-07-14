package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/trace"
)

// retryFakeHarness is a scripted Harness whose RunTurn returns the next
// mcp.CallToolParams (or error) from calls on each invocation, and records
// every TurnInput it was handed (so a test can assert the corrective
// system-prompt fragment was actually threaded through on retry). Distinct
// from offramp_test.go's fixed-behavior fakes: this one is stateful across
// calls, which retryMalformedTransition's bounded-retry loop needs.
type retryFakeHarness struct {
	calls []mcp.CallToolParams
	errs  []error
	seen  []harness.TurnInput
}

func (h *retryFakeHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	h.seen = append(h.seen, in)
	i := len(h.seen) - 1
	var err error
	if i < len(h.errs) {
		err = h.errs[i]
	}
	if i < len(h.calls) {
		return h.calls[i], err
	}
	return mcp.CallToolParams{}, err
}
func (h *retryFakeHarness) Close() error { return nil }

func transitionParams(args map[string]any) mcp.CallToolParams {
	return mcp.CallToolParams{Name: "transition", Arguments: args}
}

// TestRetryMalformedTransitionRescuesOnSecondAttempt pins the F4 fix: a first
// malformed transition call (missing 'intent', the shape observed from
// Qwen3.6-27B in the GX10 study) is not immediately fatal — the harness gets
// one bounded retry with the parse error fed back, and a valid second attempt
// succeeds.
func TestRetryMalformedTransitionRescuesOnSecondAttempt(t *testing.T) {
	// firstParams/firstErr model the turn's ORIGINAL (already-happened)
	// harness.RunTurn call — the one orchestrator.go made before deciding to
	// retry. fake.calls holds only the responses retryMalformedTransition's
	// OWN RunTurn invocations will see: one rescue attempt that succeeds.
	firstParams := transitionParams(map[string]any{"confidence": 0.5}) // missing intent
	fake := &retryFakeHarness{
		calls: []mcp.CallToolParams{
			transitionParams(map[string]any{"intent": "start", "confidence": 0.9}),
		},
	}
	o := &Orchestrator{harness: fake}
	tl := trace.NewTurnLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), app.SessionID("s1"), 1, "root")
	in := harness.TurnInput{UserText: "start the thing"}

	_, firstErr := parseIntentCall(firstParams)
	if firstErr == nil {
		t.Fatal("fixture setup bug: first attempt should be malformed")
	}

	gotParams, call, err := o.retryMalformedTransition(context.Background(), tl, in, firstParams, firstErr)
	if err != nil {
		t.Fatalf("expected the retry to rescue the turn, got error: %v", err)
	}
	if call.Intent != "start" {
		t.Fatalf("call.Intent = %q, want %q", call.Intent, "start")
	}
	if gotParams.Name != "transition" {
		t.Fatalf("gotParams.Name = %q", gotParams.Name)
	}
	if len(fake.seen) != 1 {
		t.Fatalf("expected exactly 1 extra RunTurn call (bounded retry), got %d", len(fake.seen))
	}
	if !strings.Contains(fake.seen[0].SystemPrompt, "could not be parsed") {
		t.Fatalf("retry TurnInput.SystemPrompt missing corrective fragment: %q", fake.seen[0].SystemPrompt)
	}
	if !strings.Contains(fake.seen[0].SystemPrompt, firstErr.Error()) {
		t.Fatalf("retry TurnInput.SystemPrompt should cite the exact parse error %q, got %q", firstErr.Error(), fake.seen[0].SystemPrompt)
	}
}

// TestRetryMalformedTransitionBoundedGivesUpAfterMaxAttempts pins the bound:
// a persistently broken model/backend must fail the turn, not loop forever.
func TestRetryMalformedTransitionBoundedGivesUpAfterMaxAttempts(t *testing.T) {
	// The original (already-happened) attempt and every retry return a
	// differently-shaped malformed call, mirroring the real bug report's "a
	// different malformed shape every attempt".
	firstParams := transitionParams(map[string]any{"slots": `{"a":1}`}) // double-encoded slots, still missing intent
	fake := &retryFakeHarness{
		calls: []mcp.CallToolParams{
			transitionParams(map[string]any{"intent": "", "slots": nil}), // empty intent
			transitionParams(map[string]any{"confidence": 0.1}),          // still missing intent
			transitionParams(map[string]any{"intent": "start"}),          // would have rescued a 3rd attempt — never reached
		},
	}
	o := &Orchestrator{harness: fake}
	tl := trace.NewTurnLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), app.SessionID("s1"), 1, "root")
	in := harness.TurnInput{UserText: "start the thing"}

	_, firstErr := parseIntentCall(firstParams)

	_, _, err := o.retryMalformedTransition(context.Background(), tl, in, firstParams, firstErr)
	if err == nil {
		t.Fatal("expected retry-exhausted error, got nil")
	}
	if len(fake.seen) != maxMalformedTransitionRetries {
		t.Fatalf("expected exactly %d retry RunTurn calls (bounded), got %d", maxMalformedTransitionRetries, len(fake.seen))
	}
}

// TestRetryMalformedTransitionStopsOnHarnessError pins that a harness-level
// failure DURING a retry (timeout, refusal, ClarifyResponse) is not itself
// retried — retryMalformedTransition returns the original parse error so the
// caller's existing harness-error branches (which retryMalformedTransition
// does not have access to) handle it on the original params.
func TestRetryMalformedTransitionStopsOnHarnessError(t *testing.T) {
	fake := &retryFakeHarness{
		calls: []mcp.CallToolParams{{}},
		errs:  []error{&harness.ClarifyResponse{Message: "not sure"}},
	}
	o := &Orchestrator{harness: fake}
	tl := trace.NewTurnLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), app.SessionID("s1"), 1, "root")
	in := harness.TurnInput{UserText: "start the thing"}

	firstParams := transitionParams(map[string]any{"confidence": 0.5})
	_, firstErr := parseIntentCall(firstParams)

	gotParams, _, err := o.retryMalformedTransition(context.Background(), tl, in, firstParams, firstErr)
	if err != firstErr {
		t.Fatalf("expected the ORIGINAL parse error back so the caller's harness-error handling applies, got %v", err)
	}
	if gotParams.Name != firstParams.Name {
		t.Fatalf("expected the original params back on harness-error bailout")
	}
	if len(fake.seen) != 1 {
		t.Fatalf("expected exactly 1 retry attempt before bailing on harness error, got %d", len(fake.seen))
	}
}
