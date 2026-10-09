package host_test

// RED gate for the real (LLM-backed) host.agent.codeact Agent: proves
// AgentCodeactHandler drives a full codeact.Run to TerminatedDone using a
// scripted ClaudeRunner — zero real subprocess/LLM call. Against the old
// codeactStubAgent this test would have failed: the stub never calls
// claude at all (no ClaudeRunner invocation to script), so it could not
// have produced the per-step FakeCodeactStep assertions below, and — more
// fundamentally — a fixture asserting "the runner was invoked twice, once
// per step" is meaningless against a stub that terminates on step 0
// without ever touching the ClaudeRunner seam.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/host/agentruntime"
	"kitsoki/internal/store"
)

// scriptedCodeactRuntime is an injected no-subprocess runtime for CLI CodeAct
// tests. Its fenced JSON exercises the normal recovery path when the validator
// output file is absent, while still proving every generator step crossed the
// shared agentruntime boundary.
type scriptedCodeactRuntime struct {
	results []agentruntime.Result
	seen    []agentruntime.LaunchSpec
}

func (r *scriptedCodeactRuntime) Name() string { return "scripted-codeact" }

func (r *scriptedCodeactRuntime) Probe(context.Context) agentruntime.Capabilities {
	return agentruntime.Capabilities{Backend: r.Name(), Strength: agentruntime.StrengthSupervised}
}

func (r *scriptedCodeactRuntime) Launch(_ context.Context, spec agentruntime.LaunchSpec) (*agentruntime.Running, agentruntime.AppliedPolicy, error) {
	r.seen = append(r.seen, spec)
	index := len(r.seen) - 1
	if index >= len(r.results) {
		return nil, agentruntime.AppliedPolicy{}, fmt.Errorf("unexpected CodeAct runtime launch %d", index)
	}
	policy := agentruntime.AppliedPolicy{
		Backend: r.Name(), Strength: agentruntime.StrengthSupervised,
		MinStrength: spec.EffectiveMin(), Repo: spec.EffectiveRepo(),
		Network: spec.EffectiveNetwork(), Degrade: spec.EffectiveDegrade(),
	}
	return agentruntime.NewRunning(policy, func(context.Context) (agentruntime.Result, error) {
		return r.results[index], nil
	}), policy, nil
}

func fakeCodeactRuntime(t *testing.T, turns []map[string]any) *scriptedCodeactRuntime {
	t.Helper()
	results := make([]agentruntime.Result, 0, len(turns))
	for _, turn := range turns {
		b, err := json.Marshal(turn)
		if err != nil {
			t.Fatalf("marshal turn: %v", err)
		}
		results = append(results, agentruntime.Result{Stdout: "```json\n" + string(b) + "\n```\n"})
	}
	return &scriptedCodeactRuntime{results: results}
}

// TestAgentCodeactHandler_RealAgentDrivesRunToDone scripts two turns — a
// snippet turn (a trivial Starlark script returning a dict) followed by a
// done turn — and asserts the handler's codeact.Run reaches TerminatedDone
// with the expected payload, having called the (fake) LLM exactly twice: once
// per step, zero real subprocess/LLM calls.
func TestAgentCodeactHandler_RealAgentDrivesRunToDone(t *testing.T) {
	t.Parallel()

	turns := []map[string]any{
		{
			"action":  "snippet",
			"snippet": "def main(ctx):\n    return {\"seen\": True}\n",
		},
		{
			"action":  "done",
			"payload": map[string]any{"result": "ok"},
		},
	}
	sink := &memSink{}
	runtime := fakeCodeactRuntime(t, turns)
	ctx := host.WithAgentRuntimeRegistry(agentCtxForTest(sink), agentruntime.NewRegistry(runtime))
	ctx = host.WithAgents(ctx, map[string]host.Agent{
		"coder": {SystemPrompt: "You write small Starlark snippets."},
	})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":        "coder",
		"goal":         "produce a trivial result",
		"budget":       5,
		"capabilities": map[string]any{"world": "read"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	terminated, _ := res.Data["terminated"].(string)
	if terminated != "done" {
		t.Fatalf("expected terminated=done, got %q (Data=%#v)", terminated, res.Data)
	}

	payload, _ := res.Data["payload"].(map[string]any)
	if payload["result"] != "ok" {
		t.Fatalf("expected payload.result=ok, got %#v", payload)
	}

	steps, _ := res.Data["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("expected 2 journaled steps, got %d (%#v)", len(steps), steps)
	}
	step0, _ := steps[0].(map[string]any)
	snippet, _ := step0["snippet"].(string)
	if !strings.Contains(snippet, "seen") {
		t.Fatalf("expected step 0 to journal the scripted snippet; got %#v", step0)
	}
	if len(runtime.seen) != 2 {
		t.Fatalf("runtime launches = %d, want 2", len(runtime.seen))
	}
	for index, seen := range runtime.seen {
		if seen.EffectiveMin() != agentruntime.StrengthSupervised || seen.Resources.Timeout.String() != "15m0s" || seen.Resources.ActivityTimeout.String() != "1m30s" || seen.EffectiveDegrade() != agentruntime.DegradeFail {
			t.Fatalf("runtime spec %d = %#v, want shared CodeAct supervision baseline", index, seen)
		}
	}
	assertCodeactRuntimeReceipts(t, sink.events, 2)
}

// TestAgentCodeactHandler_SchemaRejectsThenAccepts proves the "schema" arg
// documented on AgentCodeactHandler is actually wired into codeact.Params.Schema:
// a first done() turn whose payload fails the declared JSON schema is rejected
// and fed back to the (fake) agent as an ErrorEnvelope, and a corrected second
// done() turn — matching the schema — terminates the loop successfully. Before
// this fix codeact.Run was always called with no Schema func, so the first,
// schema-invalid turn would have been silently accepted as TerminatedDone.
func TestAgentCodeactHandler_SchemaRejectsThenAccepts(t *testing.T) {
	t.Parallel()

	schemaPath := filepath.Join(t.TempDir(), "verdict.json")
	if err := os.WriteFile(schemaPath, []byte(`{
		"type": "object",
		"required": ["verdict"],
		"properties": {
			"verdict": {"type": "string", "enum": ["STILL-LIVE", "ALREADY-FIXED"]}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	turns := []map[string]any{
		{
			// Schema-invalid: verdict is missing entirely.
			"action":  "done",
			"payload": map[string]any{"summary": "looks broken"},
		},
		{
			// Corrected: matches the schema.
			"action":  "done",
			"payload": map[string]any{"verdict": "STILL-LIVE"},
		},
	}
	runtime := fakeCodeactRuntime(t, turns)
	ctx := host.WithAgentRuntimeRegistry(context.Background(), agentruntime.NewRegistry(runtime))
	ctx = host.WithAgents(ctx, map[string]host.Agent{
		"triager": {SystemPrompt: "You triage bugs."},
	})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":        "triager",
		"goal":         "assess whether the bug still exists",
		"budget":       5,
		"capabilities": map[string]any{"world": "read"},
		"schema":       schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	terminated, _ := res.Data["terminated"].(string)
	if terminated != "done" {
		t.Fatalf("expected terminated=done, got %q (Data=%#v)", terminated, res.Data)
	}
	payload, _ := res.Data["payload"].(map[string]any)
	if payload["verdict"] != "STILL-LIVE" {
		t.Fatalf("expected the CORRECTED payload to win, got %#v", payload)
	}

	steps, _ := res.Data["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("expected 2 journaled steps (reject + accept), got %d (%#v)", len(steps), steps)
	}
	step0, _ := steps[0].(map[string]any)
	errMsg, _ := step0["error"].(string)
	if !strings.Contains(errMsg, "rejected") {
		t.Fatalf("expected step 0 to journal a schema-rejection error, got %#v", step0)
	}
}

// TestAgentCodeactHandler_UnknownAgent verifies the pre-existing unknown-agent
// guard still fires as a Result.Error (not a Go error) before any claude call
// would happen — this exercises the same resolveAgent path RealCodeactAgent's
// constructor relies on.
func TestAgentCodeactHandler_UnknownAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent": "nonexistent",
		"goal":  "do something",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "unknown agent") {
		t.Fatalf("expected unknown-agent error; got %q", res.Error)
	}
}

func TestAgentCodeactHandler_CLIRuntimeFailsClosedWhenBaselineUnavailable(t *testing.T) {
	t.Parallel()
	ctx := host.WithAgentRuntimeRegistry(context.Background(), agentruntime.NewRegistry(agentruntime.NewFake(agentruntime.StrengthNone)))
	ctx = host.WithAgents(ctx, map[string]host.Agent{"coder": {SystemPrompt: "You write small Starlark snippets."}})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":        "coder",
		"goal":         "produce a trivial result",
		"capabilities": map[string]any{"world": "read"},
	})
	if err == nil || !strings.Contains(err.Error(), "no backend satisfies min_strength \"supervised\"") {
		t.Fatalf("error = %v, want fail-closed runtime policy denial", err)
	}
	if res.Error != "" {
		t.Fatalf("Result.Error = %q, want the handler's returned error path", res.Error)
	}
}

func assertCodeactRuntimeReceipts(t *testing.T, events []store.Event, wantSteps int) {
	t.Helper()
	starts := map[int]store.Event{}
	ends := map[int]store.Event{}
	for _, event := range events {
		if event.Kind != store.EventKind("agent.runtime.start") && event.Kind != store.EventKind("agent.runtime.end") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("runtime receipt payload: %v", err)
		}
		step, ok := payload["codeact_step"].(float64)
		if !ok {
			t.Fatalf("runtime receipt missing codeact_step: %#v", payload)
		}
		if payload["backend"] != "scripted-codeact" || payload["strength"] != "supervised" {
			t.Fatalf("runtime receipt policy = %#v", payload)
		}
		if event.Kind == store.EventKind("agent.runtime.start") {
			if payload["min_strength"] != "supervised" {
				t.Fatalf("runtime start policy = %#v", payload)
			}
			starts[int(step)] = event
		} else {
			ends[int(step)] = event
		}
	}
	if len(starts) != wantSteps || len(ends) != wantSteps {
		t.Fatalf("runtime receipts starts=%d ends=%d want=%d; events=%#v", len(starts), len(ends), wantSteps, events)
	}
	for step := 0; step < wantSteps; step++ {
		if starts[step].CallID == "" || starts[step].CallID != ends[step].CallID {
			t.Fatalf("runtime step %d call correlation start=%q end=%q", step, starts[step].CallID, ends[step].CallID)
		}
	}
}
