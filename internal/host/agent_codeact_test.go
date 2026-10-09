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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// fakeCodeactRunner returns a ClaudeRunner that replies with the discriminated-
// union JSON payloads in turns, in order — one per Next() call — and, like
// makeFakeRunner, simulates the mcp-validator submit by writing the payload to
// the --mcp-config's declared --output path so RealCodeactAgent's normal
// "read the validator's captured payload" path is exercised (not the
// code-block-recovery fallback).
func fakeCodeactRunner(t *testing.T, turns []map[string]any) host.ClaudeRunner {
	t.Helper()
	call := 0
	return func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		if call >= len(turns) {
			t.Fatalf("fakeCodeactRunner: unexpected extra call %d (stdin=%q)", call, stdin)
		}
		turn := turns[call]
		call++
		b, err := json.Marshal(turn)
		if err != nil {
			t.Fatalf("marshal turn: %v", err)
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			if werr := os.WriteFile(outputPath, b, 0o600); werr != nil {
				t.Fatalf("write validator output: %v", werr)
			}
		}
		return host.ClaudeRun{Stdout: string(b)}, nil
	}
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
	runner := fakeCodeactRunner(t, turns)

	ctx := host.WithClaudeRunner(context.Background(), runner)
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
	runner := fakeCodeactRunner(t, turns)

	ctx := host.WithClaudeRunner(context.Background(), runner)
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
	ctx := host.WithClaudeRunner(context.Background(), fakeCodeactRunner(t, nil))
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
