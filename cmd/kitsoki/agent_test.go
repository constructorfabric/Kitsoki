// agent_test.go — tests for `kitsoki agent <verb>` subcommands (Phase 6).
//
// All tests use the host.WithClaudeRunner / host.FakeXxx seam so no real
// subprocess is forked. Tests run in milliseconds.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// ── agent extract ─────────────────────────────────────────────────────────

func TestAgentExtract_BuildsArgsMap(t *testing.T) {
	t.Parallel()
	m := buildExtractArgs("hello world", "schema.json", "", "my-agent", "")
	if m["input"] != "hello world" {
		t.Fatalf("input: want %q got %v", "hello world", m["input"])
	}
	if m["schema"] != "schema.json" {
		t.Fatalf("schema: want %q got %v", "schema.json", m["schema"])
	}
	if m["agent"] != "my-agent" {
		t.Fatalf("agent: want %q got %v", "my-agent", m["agent"])
	}
}

func TestAgentExtract_WithResolversYAML(t *testing.T) {
	t.Parallel()
	m := buildExtractArgs("test", "schema.json", "synonyms.yaml", "fallback-agent", "")
	resolvers, ok := m["resolvers"].([]any)
	if !ok || len(resolvers) != 2 {
		t.Fatalf("expected 2 resolvers with resolvers-yaml, got %v", m["resolvers"])
	}
	first, _ := resolvers[0].(map[string]any)
	if first["synonyms"] != "synonyms.yaml" {
		t.Fatalf("first resolver should be synonyms: got %v", first)
	}
}

func TestAgentExtract_NoResolversYAML_AgentInRoot(t *testing.T) {
	t.Parallel()
	m := buildExtractArgs("test", "schema.json", "", "my-agent", "")
	if m["agent"] != "my-agent" {
		t.Fatalf("agent should be in top-level when no resolvers-yaml: %v", m["agent"])
	}
	if _, present := m["resolvers"]; present {
		t.Fatal("resolvers should not be set when resolvers-yaml is empty")
	}
}

func TestAgentExtract_ParentSessionPropagated(t *testing.T) {
	t.Parallel()
	m := buildExtractArgs("test", "schema.json", "", "", "parent-123")
	if m["parent_session_id"] != "parent-123" {
		t.Fatalf("parent_session_id not propagated: %v", m["parent_session_id"])
	}
}

func TestAgentExtract_DispatchesHandler(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeExtractJSON(map[string]any{"direction": "north"}))
	// No real schema file — handler returns domain error (no panic).
	res, err := dispatchAgentRPC(ctx, "agent.extract", map[string]any{
		"input":  "go north",
		"schema": "nonexistent-schema.json",
	}, nil)
	if err != nil {
		t.Fatalf("dispatch should not return infra error: %v", err)
	}
	_ = res
}

// ── agent decide ─────────────────────────────────────────────────────────

func TestAgentDecide_BuildsArgsMap(t *testing.T) {
	t.Parallel()
	m, err := buildDecideArgs("prompt.md", "schema.json", "my-agent", `{"key":"val"}`, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["schema"] != "schema.json" {
		t.Fatalf("schema: want schema.json got %v", m["schema"])
	}
	if m["agent"] != "my-agent" {
		t.Fatalf("agent: want my-agent got %v", m["agent"])
	}
	argsMap, ok := m["args"].(map[string]any)
	if !ok {
		t.Fatalf("args not a map: %T", m["args"])
	}
	if argsMap["key"] != "val" {
		t.Fatalf("args.key: want val got %v", argsMap["key"])
	}
}

func TestAgentDecide_InvalidArgsJSON(t *testing.T) {
	t.Parallel()
	_, err := buildDecideArgs("prompt.md", "schema.json", "", "not-json", "", "")
	if err == nil {
		t.Fatal("expected error for invalid --args-json")
	}
	if !strings.Contains(err.Error(), "parse --args-json") {
		t.Fatalf("error should mention parse --args-json: %v", err)
	}
}

func TestAgentDecide_ValidatorCmd(t *testing.T) {
	t.Parallel()
	m, err := buildDecideArgs("prompt.md", "schema.json", "", "", "python3 verify.py", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	validator, ok := m["validator"].(map[string]any)
	if !ok {
		t.Fatalf("validator not a map: %T", m["validator"])
	}
	if validator["post_cmd"] != "python3 verify.py" {
		t.Fatalf("post_cmd: want %q got %v", "python3 verify.py", validator["post_cmd"])
	}
}

func TestAgentDecide_MissingSchemaReturnsError(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide(""))
	res, err := dispatchAgentRPC(ctx, "agent.decide", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("dispatch should not return infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing schema on decide")
	}
}

// ── agent ask ────────────────────────────────────────────────────────────

func TestAgentAsk_BuildsArgsMap(t *testing.T) {
	t.Parallel()
	m, err := buildAskArgs("prompt.md", "agent-name", "/tmp/dir", "schema.json", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["prompt"] != "prompt.md" {
		t.Fatalf("prompt: want prompt.md got %v", m["prompt"])
	}
	if m["working_dir"] != "/tmp/dir" {
		t.Fatalf("working_dir: want /tmp/dir got %v", m["working_dir"])
	}
	if m["schema"] != "schema.json" {
		t.Fatalf("schema: want schema.json got %v", m["schema"])
	}
}

func TestAgentAsk_NoSchemaOmitsSchemaKey(t *testing.T) {
	t.Parallel()
	m, err := buildAskArgs("prompt.md", "", "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := m["schema"]; present {
		t.Fatal("schema key should be absent when no schema flag is set")
	}
}

func TestAgentAsk_MissingPromptReturnsError(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeAsk(""))
	res, err := dispatchAgentRPC(ctx, "agent.ask", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("dispatch should not return infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing prompt on ask")
	}
}

// ── agent task ───────────────────────────────────────────────────────────

func TestAgentTask_BuildsArgsMap(t *testing.T) {
	t.Parallel()
	m := buildTaskArgs("my-agent", "/work", "acceptance.json", "python3 verify.py", "Do the thing.", "")
	if m["agent"] != "my-agent" {
		t.Fatalf("agent: want my-agent got %v", m["agent"])
	}
	if m["working_dir"] != "/work" {
		t.Fatalf("working_dir: want /work got %v", m["working_dir"])
	}
	acceptance, ok := m["acceptance"].(map[string]any)
	if !ok {
		t.Fatalf("acceptance not a map: %T", m["acceptance"])
	}
	if acceptance["schema"] != "acceptance.json" {
		t.Fatalf("acceptance.schema: want acceptance.json got %v", acceptance["schema"])
	}
	if acceptance["post_cmd"] != "python3 verify.py" {
		t.Fatalf("acceptance.post_cmd: want python3 verify.py got %v", acceptance["post_cmd"])
	}
	ctx, ok := m["context"].(map[string]any)
	if !ok {
		t.Fatalf("context not a map: %T", m["context"])
	}
	if ctx["prompt"] != "Do the thing." {
		t.Fatalf("context.prompt: want 'Do the thing.' got %v", ctx["prompt"])
	}
}

func TestAgentTask_NoAcceptanceCmdOmitsPostCmd(t *testing.T) {
	t.Parallel()
	m := buildTaskArgs("agent", "/work", "schema.json", "", "", "")
	acceptance, _ := m["acceptance"].(map[string]any)
	if _, present := acceptance["post_cmd"]; present {
		t.Fatal("post_cmd should be absent when acceptance-cmd is not set")
	}
}

func TestAgentTask_MissingAgentReturnsError(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeTask(""))
	res, err := dispatchAgentRPC(ctx, "agent.task", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("dispatch should not return infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing agent on task")
	}
}

// ── agent converse ───────────────────────────────────────────────────────

func TestAgentConverse_MissingQuestionReturnsError(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeConverse(""))
	res, err := dispatchAgentRPC(ctx, "agent.converse", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("dispatch should not return infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing question on converse")
	}
}

// ── dispatchAgentRPC ─────────────────────────────────────────────────────

func TestDispatchAgentRPC_UnknownMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := dispatchAgentRPC(ctx, "agent.nonexistent", nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	if !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("error should mention unknown method: %v", err)
	}
}

func TestDispatchAgentRPC_NilParamsOK(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide(""))
	// nil params should not panic — treated as empty map.
	res, err := dispatchAgentRPC(ctx, "agent.decide", nil, nil)
	if err != nil {
		t.Fatalf("nil params should not return infra error: %v", err)
	}
	if res.Error == "" {
		t.Log("domain error expected for missing schema (OK)")
	}
}

// ── writeAgentResult ────────────────────────────────────────────────────

func TestWriteAgentResult_JSON(t *testing.T) {
	t.Parallel()
	res := host.Result{
		Data: map[string]any{
			"submitted":   map[string]any{"ok": true},
			"resolved_by": "llm",
		},
	}
	var buf bytes.Buffer
	if err := writeAgentResult(&buf, res); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	line := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(line, "{") {
		t.Fatalf("expected JSON output, got: %q", line)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	if m["resolved_by"] != "llm" {
		t.Fatalf("resolved_by: want llm got %v", m["resolved_by"])
	}
}

func TestWriteAgentResult_ErrorSurfaces(t *testing.T) {
	t.Parallel()
	res := host.Result{Error: "something went wrong"}
	var buf bytes.Buffer
	err := writeAgentResult(&buf, res)
	if err == nil {
		t.Fatal("expected error for Result.Error non-empty")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Fatalf("error should contain the result error: %v", err)
	}
}

func TestWriteAgentResult_EmptyDataOK(t *testing.T) {
	t.Parallel()
	res := host.Result{Data: nil}
	var buf bytes.Buffer
	if err := writeAgentResult(&buf, res); err != nil {
		t.Fatalf("unexpected error for nil data: %v", err)
	}
}

// ── injectSessionID ────────────────────────────────────────────────────────

func TestInjectSessionID_ExplicitStored(t *testing.T) {
	t.Parallel()
	ctx := injectSessionID(context.Background(), "explicit-sid-xyz")
	// The session ID is now stored in context via WithKitsokiSessionID,
	// not in the process-global env. Verify via the host package context accessor.
	got := host.KitsokiSessionIDFromCtx(ctx)
	if got != "explicit-sid-xyz" {
		t.Fatalf("expected session ID in context, got %q", got)
	}
}

func TestInjectSessionID_InheritsFromEnv(t *testing.T) {
	// Not parallel: reads process env.
	t.Setenv(agentSessionIDEnv, "from-env-456")
	ctx := injectSessionID(context.Background(), "")
	// When explicitID is empty, the env value should be stored in context.
	got := host.KitsokiSessionIDFromCtx(ctx)
	if got != "from-env-456" {
		t.Fatalf("expected env value stored in context, got %q", got)
	}
}

func TestInjectSessionID_DoesNotMutateGlobalEnv(t *testing.T) {
	t.Parallel()
	prev := os.Getenv(agentSessionIDEnv)
	_ = injectSessionID(context.Background(), "new-sid-999")
	got := os.Getenv(agentSessionIDEnv)
	if got != prev {
		t.Fatalf("injectSessionID must not mutate process env; before=%q after=%q", prev, got)
	}
}
