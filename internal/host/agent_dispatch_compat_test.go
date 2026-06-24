package host_test

// agent_dispatch_compat_test.go — backwards-compatibility regression guard for
// the B-7 production wiring.
//
// Production (cmd/kitsoki/main.go) always wires an agent.Registry into the
// orchestrator. Before this guard, TryDispatchVerb hijacked *every* agent call
// once a registry was present, returning the dispatch result shape
// (submission/submitted/ok/meta) and dropping the legacy `stdout` key that
// existing stories bind (e.g. stories/dev-story/rooms/agent.yaml binds
// `agent_answer: stdout`; stories/code-review/rooms/comment.yaml binds
// `draft_comment: stdout`). That silently broke those rooms in production.
//
// The contract: the plugin dispatch path is opt-in. It engages only when the
// story explicitly names a plugin via the effect's `agent:` field (the
// orchestrator injects the plugin name into context only in that case). With no
// explicit plugin, TryDispatchVerb falls through (handled=false) so the caller
// runs its legacy in-process handler unchanged.
//
// Also covers: schema path → content resolution (agent_decide.go). The plugin
// path must load the schema file and pass its content (not the path string) to
// TryDispatchVerb so that ValidateSubmission can compile and apply it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

func compatCtx(t *testing.T) context.Context {
	t.Helper()
	reg := agent.NewRegistry()
	reg.Register("agent.claude", agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: json.RawMessage(`{"answer":"42"}`)}, nil
	})))
	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentEventSink(ctx, &captureSink{})
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("s"), Turn: 1, StatePath: "r.s",
	})
	return ctx
}

// TestTryDispatchVerb_NoExplicitPlugin_FallsThrough is the regression guard:
// with a registry wired but no explicit `agent:` plugin name in context, the
// dispatch path must NOT engage, so the legacy handler runs and preserves the
// `stdout` result shape.
func TestTryDispatchVerb_NoExplicitPlugin_FallsThrough(t *testing.T) {
	t.Parallel()
	ctx := compatCtx(t) // no WithAgentPluginName

	_, handled, err := host.TryDispatchVerb(ctx, "ask", "what is the answer?", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatalf("default agent call (no explicit agent: field) must fall through to the legacy handler, but dispatch hijacked it — this drops the stdout bind key and breaks existing stories")
	}
}

// TestTryDispatchVerb_ExplicitPlugin_Dispatches confirms the opt-in path still
// works: when the story names a plugin via `agent:`, dispatch engages and
// returns the plugin result shape.
func TestTryDispatchVerb_ExplicitPlugin_Dispatches(t *testing.T) {
	t.Parallel()
	ctx := host.WithAgentPluginName(compatCtx(t), "agent.claude")

	res, handled, err := host.TryDispatchVerb(ctx, "ask", "what is the answer?", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatalf("explicit agent: plugin must route through dispatch, but it fell through")
	}
	if _, ok := res.Data["submission"]; !ok {
		t.Fatalf("dispatch result must carry submission key; got keys %v", res.Data)
	}
}

// TestAgentDecideHandler_PluginPath_SchemaFileResolved is the regression test
// for the bug where agent_decide passed the schema as a JSON path string to the
// plugin dispatch path instead of loading the schema content. This caused
// ValidateSubmission to fail with "schema compilation failed" (a JSON Schema must
// be an object or boolean, not a string) even when the agent returned a valid
// submission. The fix reads the schema file and passes its content.
//
// Test rigor: the fake local-LLM server returns a code-fenced submission (the
// exact failure mode from dogfood: Qwen2.5-1.5b wraps its JSON in ```json…```).
// Without the schema-resolution fix (and without stripCodeFence) both the
// primary agent AND the claude fallback fail at ValidateSubmission, so the
// handler returns an error. With both fixes the handler returns the valid
// submission — proving the schema file is read and the fence is stripped.
func TestAgentDecideHandler_PluginPath_SchemaFileResolved(t *testing.T) {
	t.Parallel()

	// Minimal schema: requires a "slug" string field.
	schemaContent := `{"type":"object","properties":{"slug":{"type":"string"}},"required":["slug"]}`

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "slug.json")
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	promptPath := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Name this proposal."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	// Fake llama-server: returns the valid submission wrapped in a code fence —
	// the exact output shape that caused the dogfood regression.
	fenced := "```json\n{\"slug\":\"virtual-pets\"}\n```"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": fenced}}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 8},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    r,
		}, nil
	})}

	localLLM := agent.NewLocalLLM("qwen2.5-1.5b", 0, "", false, "https://llm.test", nil).WithHTTPClient(client)
	defer localLLM.Close()

	reg := agent.NewRegistry()
	reg.Register("agent.local_llm", localLLM)
	// agent.claude fallback is NOT needed here since stripCodeFence + schema
	// resolution means the primary call succeeds.
	reg.Register("agent.claude", agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		t.Error("agent.claude fallback must NOT be called when the primary local-LLM call succeeds after code-fence stripping")
		return agent.AskResponse{}, nil
	})))

	sink := &captureSink{}
	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("sess-schema-res"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("proposal.intake"),
	})
	ctx = host.WithAgentPluginName(ctx, "agent.local_llm")

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt_path": promptPath,
		"schema":      schemaPath,
		"agent":       "agent.local_llm",
	})
	if err != nil {
		t.Fatalf("AgentDecideHandler: unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("AgentDecideHandler returned error: %s", res.Error)
	}

	// The submission must be the stripped JSON, not the fenced version.
	sub, _ := res.Data["submission"].(map[string]any)
	if sub == nil {
		t.Fatalf("submission is nil; data: %v", res.Data)
	}
	if sub["slug"] != "virtual-pets" {
		t.Errorf("slug: got %v, want \"virtual-pets\"", sub["slug"])
	}

	// Exactly one AgentCalled + one AgentReturned; no AgentError.
	kinds := make([]store.EventKind, len(sink.events))
	for i, e := range sink.events {
		kinds[i] = e.Kind
	}
	for _, k := range kinds {
		if k == store.AgentError {
			t.Error("AgentError must NOT be written when the primary call succeeds")
		}
	}
}
