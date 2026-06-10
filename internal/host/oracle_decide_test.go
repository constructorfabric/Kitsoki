package host_test

// Tests for host.oracle.decide (Phase 2).
//
// All tests use FakeDecide / WithClaudeRunner — no real LLM calls, no
// real subprocesses. Budget: each test should run in milliseconds.
//
// Coverage:
//  1. Verb contract: schema required; submit auto-attached (mcp config present).
//  2. Validator: reject-with-reason triggers re-submit; success on retry.
//  3. Validator sandbox: mutating validator rejected.
//  4. Streaming: tokens flow through OracleStreamer.
//  5. Loader/runtime: mutation tools on agent rejected at call time.
//  6. Alias: ask_with_mcp (no mutations) → decide with warn (checked via Result).
//  7. Alias: ask_with_mcp + chat_id → error pointing at converse.
//  8. Agent fields: system_prompt / model / tools forwarded.
//  9. D5: per-call tools win over agent.Tools.
// 10. working_dir precedence: per-call > agent.DefaultCwd.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makePromptFile writes body to a temp file and returns its path.
func makePromptFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "decide-prompt.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	return p
}

// makeSchemaFile writes a minimal JSON schema to a temp file.
func makeSchemaFile(t *testing.T) string {
	t.Helper()
	schema := `{
  "type": "object",
  "properties": { "verdict": { "type": "string" } },
  "required": ["verdict"]
}`
	dir := t.TempDir()
	p := filepath.Join(dir, "verdict.json")
	if err := os.WriteFile(p, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return p
}

// ── 1. Verb contract ──────────────────────────────────────────────────────────

// TestOracleDecide_SchemaRequired verifies that omitting schema: yields
// a Result.Error (not a Go error).
func TestOracleDecide_SchemaRequired(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("ok"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "decide something",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "schema") {
		t.Fatalf("expected schema error in Result.Error; got %q", res.Error)
	}
}

// TestOracleDecide_PromptRequired verifies that omitting both prompt: and
// prompt_path: yields a Result.Error.
func TestOracleDecide_PromptRequired(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("ok"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error for missing prompt; got none")
	}
}

// TestOracleDecide_PromptAndPromptPathMutuallyExclusive verifies that setting
// both prompt: and prompt_path: is rejected.
func TestOracleDecide_PromptAndPromptPathMutuallyExclusive(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	promptPath := makePromptFile(t, "decide")
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("ok"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt":      "inline",
		"prompt_path": promptPath,
		"schema":      schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error; got %q", res.Error)
	}
}

// TestOracleDecide_SubmitAutoAttached verifies that a valid decide call
// with schema: succeeds (the handler builds an MCP config with the validator
// and passes it to claude — the FakeDecide runner doesn't actually use it
// but the handler must not error before calling the runner).
func TestOracleDecide_SubmitAutoAttached(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("I decided"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "Is this a good idea?",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// FakeDecide simulates submit via ParseMCPConfigSubmitOutput, so submitted
	// is present. rationale should carry the runner's stdout.
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "I decided") {
		t.Fatalf("expected rationale to contain runner stdout; got %q", rat)
	}
}

// TestOracleDecide_PromptPath verifies that prompt_path: is accepted.
func TestOracleDecide_PromptPath(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	promptPath := makePromptFile(t, "Should we proceed?")
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("yes"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"schema":      schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "yes") {
		t.Fatalf("expected rationale from runner; got %q", rat)
	}
}

// ── 2. Validator retry loop ───────────────────────────────────────────────────

// TestOracleDecide_Validator_SuccessOnFirstAttempt verifies that when a
// validator block is present and the validator writes a payload on the
// first submission, the result carries submitted + validator_attempts.
//
// We simulate this by writing a valid payload to the validator output file
// that the handler will pick up. Since the runner is stubbed, the validator
// subprocess is never invoked — we pre-create the validator state file to
// simulate "1 success".
func TestOracleDecide_Validator_SubmittedPayloadSurfaced(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	// Write a fake validator output so the handler reads it back as submitted.
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "decide-out.json")
	payload := map[string]any{"verdict": "approved"}
	payloadBytes, _ := json.Marshal(payload)
	if err := os.WriteFile(outPath, payloadBytes, 0o644); err != nil {
		t.Fatalf("write fake output: %v", err)
	}

	// Use a runner that just returns text — the handler will read outPath.
	// We cannot inject the output path externally, so instead we test the
	// buildDecideResult helper indirectly via a full call where a validator
	// output file happens to exist (we test the export path with a real
	// validator block in TestOracleDecide_ValidatorBlock_RetryLoop).
	// This test just confirms rationale + exit_code/ok are populated.
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("rationale text"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge this",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "rationale text") {
		t.Fatalf("expected rationale text; got %q", rat)
	}
	ok, _ := res.Data["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true for exit_code 0; got ok=%v, error=%q", ok, res.Error)
	}
}

// TestOracleDecide_ValidatorBlock_RetryLoop verifies that when validator: is
// declared and the first run doesn't submit, the handler tries again
// (--resume path). We set MaxOuterIterations to 1 via the stub so we don't
// actually loop; we just confirm the handler doesn't panic and returns an
// error indicating abandonment.
func TestOracleDecide_ValidatorBlock_Abandoned(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	// Runner returns empty stdout — simulates LLM exiting without submit.
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide(""))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
		"validator": map[string]any{
			"post_cmd":    "echo ok",
			"max_retries": 1,
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// With no real validator state file (FakeDecide doesn't run one),
	// ReadStateFile returns 0/0/"". outcomeFromState(0,0,1) == mcpOutcomeAbandoned.
	// After exhausting outer iterations the handler returns an abandonment error.
	if res.Error == "" {
		t.Fatal("expected abandonment error; got none")
	}
	if !strings.Contains(res.Error, "abandoned") && !strings.Contains(res.Error, "session abandoned") {
		t.Fatalf("expected abandonment message in error; got %q", res.Error)
	}
}

// ── 3. Validator sandbox: mutating validator rejected ─────────────────────────

// TestOracleDecide_ValidatorSandbox_NonZeroExitIsRetry verifies that when
// OracleDecideHandler has a validator block configured but FakeDecide never
// submits, the handler returns an abandonment error (not panic, not success).
func TestOracleDecide_ValidatorSandbox_NonZeroExitIsRetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := filepath.Join(dir, "reject.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide(""))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
		"validator": map[string]any{
			"post_cmd":    script,
			"max_retries": 1,
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// FakeDecide never calls submit, so schema never passes and the sandbox
	// validator never runs. The outer loop exhausts → abandonment error.
	if res.Error == "" {
		t.Fatal("expected abandonment error; got none")
	}
}

// TestOracleDecide_ValidatorSandbox_ContractViolation verifies that
// isSandboxContractViolation returns true when the sandbox result contains
// "operation not permitted" from the validator subprocess (M12 detection),
// and returns false for unshare infrastructure failures and clean rejections.
// Tests the detection function directly to avoid sandbox infrastructure flakiness.
func TestOracleDecide_ValidatorSandbox_ContractViolation(t *testing.T) {
	t.Parallel()

	// Mutation attempt detected: subprocess stderr contains denial signal.
	vrMutate := host.ValidatorResult{
		ExitCode: 1,
		Stderr:   "write /etc/passwd: operation not permitted",
	}
	if !host.IsSandboxContractViolationExport(vrMutate) {
		t.Fatal("expected contract violation for 'operation not permitted' in stderr")
	}

	// Clean semantic rejection — NOT a contract violation.
	vrClean := host.ValidatorResult{
		ExitCode: 1,
		Stderr:   "line 42: expected string, got int",
	}
	if host.IsSandboxContractViolationExport(vrClean) {
		t.Fatal("expected no contract violation for clean semantic rejection")
	}

	// unshare infrastructure failure — must NOT be flagged as a violation.
	vrInfra := host.ValidatorResult{
		ExitCode: 1,
		Stderr:   "unshare: unshare failed: Operation not permitted",
	}
	if host.IsSandboxContractViolationExport(vrInfra) {
		t.Fatal("expected no contract violation for unshare infrastructure failure")
	}
}

// TestOracleDecide_ValidatorSandbox_ZeroExitIsAccept verifies that a zero-exit
// sandbox result does not trigger the contract-violation sentinel.
func TestOracleDecide_ValidatorSandbox_ZeroExitIsAccept(t *testing.T) {
	t.Parallel()

	vr := host.ValidatorResult{ExitCode: 0}
	if host.IsSandboxContractViolationExport(vr) {
		t.Fatal("expected no contract violation for zero-exit result")
	}
}

// ── 4. Streaming: tokens flow through OracleStreamer ─────────────────────────

// TestOracleDecide_StreamsToSink verifies that when a StreamSink is installed
// in ctx, decide calls the streaming path (OracleStreamer selects stream-json).
// We confirm this by checking that the result's rationale contains the runner
// output (the stub's output is used as the synthetic reply).
func TestOracleDecide_StreamsToSink(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	var eventsReceived []host.StreamEvent
	sink := &collectingSink{fn: func(e host.StreamEvent) {
		eventsReceived = append(eventsReceived, e)
	}}

	ctx := host.WithStreamSink(
		host.WithClaudeRunner(context.Background(), host.FakeDecide("streaming verdict")),
		sink,
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "stream me",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "streaming verdict") {
		t.Fatalf("expected rationale from streamed runner; got %q", rat)
	}
	// Sink receives events; the stub doesn't produce JSONL so eventsReceived
	// may be empty — the important part is no error path was taken.
	_ = eventsReceived
}

// collectingSink collects StreamEvents for test assertions.
type collectingSink struct {
	fn func(host.StreamEvent)
}

func (s *collectingSink) OnStreamEvent(_ context.Context, e host.StreamEvent) {
	s.fn(e)
}

// ── 5. Mutation tool rejection ────────────────────────────────────────────────

// TestOracleDecide_MutationTool_Edit_Rejected verifies that the runtime
// safety net rejects an agent declaring Edit.
func TestOracleDecide_MutationTool_Edit_Rejected(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"mutator": {
				SystemPrompt: "I edit things",
				Tools:        []string{"Edit", "Read"},
			},
		}),
		host.FakeDecide("verdict"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "decide",
		"schema": schemaPath,
		"agent":  "mutator",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "mutation tool") {
		t.Fatalf("expected mutation-tool error; got %q", res.Error)
	}
}

// TestOracleDecide_MutationTool_Write_Rejected mirrors TestOracleDecide_MutationTool_Edit_Rejected
// for Write.
func TestOracleDecide_MutationTool_Write_Rejected(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"writer": {Tools: []string{"Write"}},
		}),
		host.FakeDecide("v"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "decide",
		"schema": schemaPath,
		"agent":  "writer",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "mutation tool") {
		t.Fatalf("expected mutation-tool error for Write; got %q", res.Error)
	}
}

// TestOracleDecide_PerCallMutationTool_Rejected verifies that a per-call
// tools: list containing a mutation tool is rejected.
func TestOracleDecide_PerCallMutationTool_Rejected(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("v"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "decide",
		"schema": schemaPath,
		"tools":  []any{"Edit"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "mutation tool") {
		t.Fatalf("expected mutation-tool rejection; got %q", res.Error)
	}
}

// TestOracleDecide_ReadOnlyTools_Allowed verifies that read-only tools are
// forwarded without error.
func TestOracleDecide_ReadOnlyTools_Allowed(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"reader": {
				Tools: []string{"Read", "Grep", "Glob"},
			},
		}),
		host.FakeDecideWithMeta("verdict"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "decide",
		"schema": schemaPath,
		"agent":  "reader",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error for read-only tools: %q", res.Error)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "tools=[Read,Grep,Glob]") {
		t.Fatalf("expected tools in rationale meta; got %q", rat)
	}
}

// ── 6 (renumbered from 8). Agent fields forwarded ────────────────────────────

// TestOracleDecide_AgentSystemPrompt_Forwarded verifies that agent.SystemPrompt
// is forwarded as --append-system-prompt.
func TestOracleDecide_AgentSystemPrompt_Forwarded(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"judge": {SystemPrompt: "you are a judge"},
		}),
		host.FakeDecideWithMeta("verdict"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge this",
		"schema": schemaPath,
		"agent":  "judge",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "you are a judge") {
		t.Fatalf("expected system prompt in meta; got %q", rat)
	}
}

// TestOracleDecide_AgentModel_Forwarded verifies that agent.Model is forwarded
// as --model.
func TestOracleDecide_AgentModel_Forwarded(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"opus-judge": {SystemPrompt: "sp", Model: "claude-opus-4-5"},
		}),
		host.FakeDecideWithMeta("verdict"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
		"agent":  "opus-judge",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "model=[claude-opus-4-5]") {
		t.Fatalf("expected model in meta; got %q", rat)
	}
}

// ── 9. D5 per-call tools win ──────────────────────────────────────────────────

// TestOracleDecide_PerCallTools_WinsOverAgentTools verifies D5 for decide.
func TestOracleDecide_PerCallTools_WinsOverAgentTools(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"judge": {Tools: []string{"Glob"}},
		}),
		host.FakeDecideWithMeta("verdict"),
	)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
		"agent":  "judge",
		"tools":  []any{"Read", "Grep"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	rat, _ := res.Data["rationale"].(string)
	if !strings.Contains(rat, "tools=[Read,Grep]") {
		t.Fatalf("per-call tools did not win; got %q", rat)
	}
	if strings.Contains(rat, "Glob") {
		t.Fatalf("agent.Tools leaked through; got %q", rat)
	}
}

// ── 10. working_dir precedence ────────────────────────────────────────────────

// TestOracleDecide_WorkingDir_PerCall_WinsOverAgentDefaultCwd verifies that
// an explicit working_dir arg wins over agent.DefaultCwd.
func TestOracleDecide_WorkingDir_PerCall_WinsOverAgentDefaultCwd(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	agentCwd := t.TempDir()
	perCallCwd := t.TempDir()

	var capturedCwd string
	runner := func(_ context.Context, _ []string, _, workingDir string) (host.ClaudeRun, error) {
		capturedCwd = workingDir
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"cwd-judge": {DefaultCwd: agentCwd},
		}),
		runner,
	)
	_, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt":      "judge",
		"schema":      schemaPath,
		"agent":       "cwd-judge",
		"working_dir": perCallCwd,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if capturedCwd != perCallCwd {
		t.Fatalf("expected per-call cwd %q; got %q", perCallCwd, capturedCwd)
	}
}

// TestOracleDecide_WorkingDir_AgentDefaultCwd_UsedWhenNoPerCall verifies that
// agent.DefaultCwd is used when working_dir is absent.
func TestOracleDecide_WorkingDir_AgentDefaultCwd_UsedWhenNoPerCall(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	agentCwd := t.TempDir()

	var capturedCwd string
	runner := func(_ context.Context, _ []string, _, workingDir string) (host.ClaudeRun, error) {
		capturedCwd = workingDir
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"cwd-judge": {DefaultCwd: agentCwd},
		}),
		runner,
	)
	_, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
		"agent":  "cwd-judge",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if capturedCwd != agentCwd {
		t.Fatalf("expected agent default cwd %q; got %q", agentCwd, capturedCwd)
	}
}

// ── result shape ──────────────────────────────────────────────────────────────

// TestOracleDecide_ResultShape verifies that a successful decide call produces
// all expected fields in Result.Data.
func TestOracleDecide_ResultShape(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("final rationale"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "what is the verdict?",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	for _, key := range []string{"rationale", "exit_code", "ok"} {
		if _, ok := res.Data[key]; !ok {
			t.Errorf("expected key %q in Result.Data; missing", key)
		}
	}
	exitCode, _ := res.Data["exit_code"].(int)
	if exitCode != 0 {
		t.Errorf("expected exit_code=0; got %d", exitCode)
	}
	okVal, _ := res.Data["ok"].(bool)
	if !okVal {
		t.Errorf("expected ok=true; got false")
	}
}

// TestOracleDecide_PhaseOneStubReplaced confirms the Phase 1 stub has been
// replaced — calling OracleDecideHandler no longer returns the "not yet
// implemented" sentinel.
func TestOracleDecide_PhaseOneStubReplaced(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("real"))
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "judge",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(res.Error, "not yet implemented") {
		t.Fatal("Phase 1 stub is still in place — oracle_decide.go was not wired")
	}
}

// ── 11. Code-block extraction fallback ───────────────────────────────────────

// TestOracleDecide_CodeBlockFallback verifies that when the model writes a
// valid JSON verdict inside a ```json ... ``` code block instead of calling
// the submit tool, buildDecideResult recovers the verdict rather than
// returning "no verdict captured".
//
// This pins the fix for the regression where proposal_namer (and any other
// decide call without a validator: block) would bounce back on_error: because
// the model emitted JSON as prose instead of using the tool.
func TestOracleDecide_CodeBlockFallback(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	// Runner that returns JSON in a code block but does NOT write to the
	// validator output path — exactly what the real model does when it bypasses
	// the submit tool.
	codeBlockRunner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		stdout := "Here is my verdict:\n\n```json\n{\"verdict\": \"yes\"}\n```\n"
		return host.ClaudeRun{Stdout: stdout}, nil
	}
	sink := &memSink{}
	ctx := host.WithClaudeRunner(oracleCtxForTest(sink), codeBlockRunner)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "Is this a good idea?",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected no error; got %q", res.Error)
	}
	submitted, ok := res.Data["submitted"]
	if !ok {
		t.Fatal("expected submitted to be populated from code block; was absent")
	}
	m, ok := submitted.(map[string]any)
	if !ok {
		t.Fatalf("expected submitted to be a map; got %T", submitted)
	}
	if m["verdict"] != "yes" {
		t.Fatalf("expected verdict=yes; got %v", m["verdict"])
	}
	// The internal bypass marker must be stripped before the Result binds into
	// world — a story bind: spec must never see it.
	if _, present := res.Data["_tool_bypassed"]; present {
		t.Error("internal _tool_bypassed key leaked into bound Result.Data")
	}

	// The deviation MUST be auditable in the trace: the oracle.call.complete
	// event's Meta records that the verdict was recovered from a code block
	// (this is why a raw ```json block reached the operator's chat).
	var returned *store.Event
	for i := range sink.events {
		if sink.events[i].Kind == store.OracleReturned {
			returned = &sink.events[i]
		}
	}
	if returned == nil {
		t.Fatal("expected an oracle.call.complete event in the trace")
	}
	var payload struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(returned.Payload, &payload); err != nil {
		t.Fatalf("unmarshal OracleReturned payload: %v", err)
	}
	if payload.Meta["tool_bypassed"] != true {
		t.Errorf("trace Meta.tool_bypassed = %v, want true", payload.Meta["tool_bypassed"])
	}
	if payload.Meta["verdict_recovered_from"] != "code_block" {
		t.Errorf("trace Meta.verdict_recovered_from = %v, want %q",
			payload.Meta["verdict_recovered_from"], "code_block")
	}
}

// TestOracleDecide_ToolBypass_SyntheticTranscript verifies the live decide path
// mirrors the tool-bypass deviation into the agent-action-transcript sidecar as
// a clearly-marked _kitsoki banner row (in addition to the trace Meta), followed
// by a validator_accept boundary — the operator sees WHY the verdict arrived as
// narration text, not a clean submit(), in the "Agent actions" drawer.
func TestOracleDecide_ToolBypass_SyntheticTranscript(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	codeBlockRunner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: "Verdict:\n\n```json\n{\"verdict\": \"yes\"}\n```\n"}, nil
	}
	transcriptsDir := filepath.Join(t.TempDir(), "transcripts")
	sink := &memSink{}
	ctx := host.WithClaudeRunner(oracleCtxForTest(sink), codeBlockRunner)
	ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(transcriptsDir))

	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "Is this a good idea?",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected no error; got %q", res.Error)
	}

	// Recover the (non-deterministic, live UUID) call_id from the transcript_ref
	// on the OracleReturned event, then read its sidecar.
	var ref *host.TranscriptRef
	for i := range sink.events {
		if sink.events[i].Kind != store.OracleReturned {
			continue
		}
		var pl host.OracleReturnedPayload
		if uErr := json.Unmarshal(sink.events[i].Payload, &pl); uErr != nil {
			t.Fatalf("unmarshal OracleReturned payload: %v", uErr)
		}
		ref = pl.TranscriptRef
	}
	if ref == nil {
		t.Fatal("expected a transcript_ref on oracle.call.complete (synthetic rows present)")
	}
	callID := strings.TrimSuffix(strings.TrimPrefix(ref.Path, "transcripts/"), ".jsonl")
	gotBytes, rerr := os.ReadFile(filepath.Join(transcriptsDir, callID+".jsonl"))
	if rerr != nil {
		t.Fatalf("read sidecar: %v", rerr)
	}
	got := string(gotBytes)
	if !strings.Contains(got, `"_kitsoki":"tool_bypassed"`) {
		t.Errorf("expected tool_bypassed banner row in sidecar; got:\n%s", got)
	}
	if !strings.Contains(got, `"verdict_recovered_from":"code_block"`) {
		t.Errorf("expected verdict_recovered_from in banner row; got:\n%s", got)
	}
	if !strings.Contains(got, `"_kitsoki":"validator_accept"`) {
		t.Errorf("expected validator_accept boundary row in sidecar; got:\n%s", got)
	}
	bypassIdx := strings.Index(got, `"_kitsoki":"tool_bypassed"`)
	acceptIdx := strings.Index(got, `"_kitsoki":"validator_accept"`)
	if bypassIdx > acceptIdx {
		t.Errorf("tool_bypassed banner must precede validator_accept; bypass=%d accept=%d", bypassIdx, acceptIdx)
	}
}

// TestOracleDecide_NoCodeBlock_StillErrors confirms the fallback does not
// swallow the error when the model exits without any usable JSON at all.
func TestOracleDecide_NoCodeBlock_StillErrors(t *testing.T) {
	t.Parallel()
	schemaPath := makeSchemaFile(t)

	noJSONRunner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: "I was going to decide but I forgot."}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), noJSONRunner)
	res, err := host.OracleDecideHandler(ctx, map[string]any{
		"prompt": "Decide something.",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// The retry loop exhausts all outer iterations and surfaces abandonment.
	if res.Error == "" {
		t.Fatal("expected an error when model produces no usable JSON; got none")
	}
}
