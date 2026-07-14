// agent_codeact_api_test.go is the internal (package host) unit test for the
// direct-API codeact.Agent: it drives codeact.Run with an ApiCodeactAgent
// backed by a stub agent.Agent and asserts the loop reaches done, that a
// schema-invalid done() self-corrects via the ErrorEnvelope feedback, and that
// the per-step prompt carries the goal + granted capability names. The
// handler-level routing (registry → ApiCodeactAgent vs the CLI path) is covered
// separately in agent_codeact_handler_api_test.go (package host_test).

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/host/codeact"
)

// stubCodeactAgent is a minimal agent.Agent that returns scripted submissions
// (one per Ask call) and records each AskRequest's PromptText so tests can
// assert the per-step prompt carries the goal/capabilities. It makes no HTTP
// call — the LocalLLMAgent transport is not on this path; ApiCodeactAgent only
// depends on the agent.Agent Ask/Close contract.
type stubCodeactAgent struct {
	turns   []map[string]any
	prompts []string
	calls   int
}

func (s *stubCodeactAgent) Ask(_ context.Context, req agent.AskRequest) (agent.AskResponse, error) {
	if s.calls >= len(s.turns) {
		return agent.AskResponse{}, fmt.Errorf("stubCodeactAgent: no turn scripted for call %d", s.calls)
	}
	turn := s.turns[s.calls]
	s.prompts = append(s.prompts, req.PromptText)
	s.calls++
	body, err := json.Marshal(turn)
	if err != nil {
		return agent.AskResponse{}, fmt.Errorf("stubCodeactAgent marshal: %w", err)
	}
	return agent.AskResponse{Submission: body}, nil
}

func (s *stubCodeactAgent) Close() error { return nil }

// TestApiCodeactAgent_DrivesRunToDone scripts two turns — a snippet then a
// done — and asserts codeact.Run reaches TerminatedDone with the done
// payload, having called the (stub) agent exactly twice.
func TestApiCodeactAgent_DrivesRunToDone(t *testing.T) {
	stub := &stubCodeactAgent{turns: []map[string]any{
		{"action": "snippet", "snippet": "def main(ctx):\n    return {\"seen\": True}\n"},
		{"action": "done", "payload": map[string]any{"result": "ok"}},
	}}
	a := newApiCodeactAgent(context.Background(), stub, map[string]any{}, "produce a trivial result", 5, []string{"world"})

	res, err := codeact.Run(context.Background(), codeact.Params{Budget: 5, Agent: a})
	if err != nil {
		t.Fatalf("codeact.Run: %v", err)
	}
	if res.Terminated != codeact.TerminatedDone {
		t.Fatalf("terminated: got %q, want %q", res.Terminated, codeact.TerminatedDone)
	}
	if res.Payload["result"] != "ok" {
		t.Fatalf("payload: got %#v, want result=ok", res.Payload)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps: got %d, want 2 (snippet + done)", len(res.Steps))
	}
	if stub.calls != 2 {
		t.Fatalf("stub calls: got %d, want 2", stub.calls)
	}
}

// TestApiCodeactAgent_SchemaRejectsThenAccepts proves the done()-payload
// schema gate is exercised through the API path the same way it is through
// the CLI path: a first done() whose payload fails the schema is rejected and
// fed back as an ErrorEnvelope, and a corrected second done() terminates the
// loop. The schema gate is owned by codeact.Run, so ApiCodeactAgent gets this
// for free.
func TestApiCodeactAgent_SchemaRejectsThenAccepts(t *testing.T) {
	schemaFn := func(payload map[string]any) error {
		if _, ok := payload["verdict"]; !ok {
			return fmt.Errorf("done() payload rejected: missing required field verdict")
		}
		return nil
	}
	stub := &stubCodeactAgent{turns: []map[string]any{
		{"action": "done", "payload": map[string]any{"summary": "looks broken"}}, // rejected
		{"action": "done", "payload": map[string]any{"verdict": "STILL-LIVE"}},   // accepted
	}}
	a := newApiCodeactAgent(context.Background(), stub, map[string]any{}, "assess whether the bug still exists", 5, []string{"world"})

	res, err := codeact.Run(context.Background(), codeact.Params{Budget: 5, Agent: a, Schema: schemaFn})
	if err != nil {
		t.Fatalf("codeact.Run: %v", err)
	}
	if res.Terminated != codeact.TerminatedDone {
		t.Fatalf("terminated: got %q, want done", res.Terminated)
	}
	if res.Payload["verdict"] != "STILL-LIVE" {
		t.Fatalf("payload: got %#v, want verdict=STILL-LIVE", res.Payload)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps: got %d, want 2 (reject + accept)", len(res.Steps))
	}
	if res.Steps[0].Err == nil || !strings.Contains(res.Steps[0].Err.Message, "rejected") {
		t.Fatalf("expected step 0 to journal a schema-rejection error, got %#v", res.Steps[0].Err)
	}
	if stub.calls != 2 {
		t.Fatalf("stub calls: got %d, want 2", stub.calls)
	}
}

// TestApiCodeactAgent_StepPromptCarriesGoalAndCapabilities asserts the
// composed per-step prompt (system preamble + step context) carries the goal,
// the granted capability descriptions, and the API-path JSON instruction
// (not the CLI submit-tool instruction).
func TestApiCodeactAgent_StepPromptCarriesGoalAndCapabilities(t *testing.T) {
	stub := &stubCodeactAgent{turns: []map[string]any{
		{"action": "done", "payload": map[string]any{"ok": true}},
	}}
	a := newApiCodeactAgent(context.Background(), stub, map[string]any{}, "triage the reported bug", 5, []string{"world", "vcs"})

	if _, err := codeact.Run(context.Background(), codeact.Params{Budget: 5, Agent: a}); err != nil {
		t.Fatalf("codeact.Run: %v", err)
	}
	if len(stub.prompts) != 1 {
		t.Fatalf("expected 1 prompt captured, got %d", len(stub.prompts))
	}
	p := stub.prompts[0]
	for _, want := range []string{
		"triage the reported bug",
		"world — read-only access to ctx.world.get(key)",
		"vcs — read-only version-control probes",
		"Respond with a single JSON object",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("step prompt missing %q\n--- prompt ---\n%s", want, p)
		}
	}
	// The CLI-path submit-tool instruction must NOT appear on the API path.
	if strings.Contains(p, "validator's submit tool") {
		t.Errorf("API-path prompt should not carry the CLI submit-tool instruction\n--- prompt ---\n%s", p)
	}
}

// TestParseCodeactEmission covers the submission→Emission decoder directly:
// snippet, done, empty, malformed, and unknown-action cases. These are the
// same guards RealCodeactAgent enforces inline; the API path surfaces them as
// hard errors (codeact.Run aborts).
func TestParseCodeactEmission(t *testing.T) {
	t.Parallel()
	if _, err := parseCodeactEmission(nil, 0); err == nil {
		t.Fatal("expected error for nil submission")
	}
	if _, err := parseCodeactEmission([]byte("   "), 0); err == nil {
		t.Fatal("expected error for blank submission")
	}
	if _, err := parseCodeactEmission([]byte("{not json"), 0); err == nil {
		t.Fatal("expected error for malformed json")
	}
	if _, err := parseCodeactEmission([]byte(`{"action":"snippet","snippet":""}`), 0); err == nil {
		t.Fatal("expected error for snippet action with empty snippet")
	}
	if _, err := parseCodeactEmission([]byte(`{"action":"bogus"}`), 0); err == nil {
		t.Fatal("expected error for unknown action")
	}

	e, err := parseCodeactEmission([]byte(`{"action":"snippet","snippet":"def main(ctx):\n    return {}\n"}`), 1)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if e.Done || e.Snippet == "" {
		t.Fatalf("snippet emission: got %#v", e)
	}
	e, err = parseCodeactEmission([]byte(`{"action":"done","payload":{"verdict":"ALIVE"}}`), 2)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !e.Done || e.Payload["verdict"] != "ALIVE" {
		t.Fatalf("done emission: got %#v", e)
	}
}
