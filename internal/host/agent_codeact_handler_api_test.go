// agent_codeact_handler_api_test.go covers AgentCodeactHandler's routing: when
// the resolved agent plugin is a builtin.local_llm transport the handler drives
// the loop over HTTP (ApiCodeactAgent + LocalLLMAgent), and otherwise it stays
// on the CLI path (RealCodeactAgent). These are package host_test (external)
// so they exercise the real wiring (registry + plugin-name context) the
// orchestrator would set up. The LocalLLMAgent transport is pointed at a fake
// RoundTripper so no real HTTP or LLM is involved.

package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/host"
	"kitsoki/internal/host/agentruntime"
)

// fakeAPIChatHandler is a RoundTripper emulating an OpenAI-compatible
// /v1/chat/completions endpoint: it returns the scripted message contents in
// order (one per call) and captures the Authorization header. A call beyond the
// scripted set fails the test — the API path must not over-call.
type fakeAPIChatHandler struct {
	t     *testing.T
	turns []string // message contents, one per call
	mu    sync.Mutex
	call  int
	auth  string
}

func (h *fakeAPIChatHandler) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	idx := h.call
	h.call++
	h.auth = r.Header.Get("Authorization")
	h.mu.Unlock()
	if idx >= len(h.turns) {
		h.t.Fatalf("fakeAPIChatHandler: unexpected extra call %d (want <= %d)", idx, len(h.turns))
	}
	body, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": h.turns[idx]}}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
	})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    r,
	}, nil
}

func (h *fakeAPIChatHandler) authHeader() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.auth
}

// explodeHandler fails the test if its RoundTrip is ever called — used to prove
// the API path was NOT taken on the fallthrough case.
type explodeHandler struct{ t *testing.T }

func (h *explodeHandler) RoundTrip(r *http.Request) (*http.Response, error) {
	h.t.Fatalf("API path should not have been taken; unexpected HTTP call to %s", r.URL.String())
	return nil, nil
}

// glmRegistry builds a registry with a single builtin.local_llm plugin
// ("agent.glm") backed by rt, plus optional knobs.
func glmRegistry(rt http.RoundTripper, opts ...func(*agent.LocalLLMAgent)) *agent.Registry {
	reg := agent.NewRegistry()
	llm := agent.NewLocalLLM("glm-4.6", 0, "", false, "https://api.test", nil).WithHTTPClient(&http.Client{Transport: rt})
	for _, opt := range opts {
		opt(llm)
	}
	reg.Register("agent.glm", llm)
	return reg
}

// TestAgentCodeactHandler_APIPathDrivesRunToDone wires the registry + plugin
// name so the handler routes to ApiCodeactAgent, and asserts the loop reaches
// done over the HTTP transport (no claude subprocess).
func TestAgentCodeactHandler_APIPathDrivesRunToDone(t *testing.T) {
	t.Parallel()
	turn1, _ := json.Marshal(map[string]any{"action": "snippet", "snippet": "def main(ctx):\n    return {\"seen\": True}\n"})
	turn2, _ := json.Marshal(map[string]any{"action": "done", "payload": map[string]any{"result": "ok"}})
	rt := &fakeAPIChatHandler{t: t, turns: []string{string(turn1), string(turn2)}}
	reg := glmRegistry(rt, func(o *agent.LocalLLMAgent) { o.WithJSONSchema(true) })

	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentPluginName(ctx, "agent.glm")
	ctx = host.WithAgents(ctx, map[string]host.Agent{"agent.glm": {SystemPrompt: "You triage by emitting Starlark snippets."}})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":        "agent.glm",
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
	if terminated, _ := res.Data["terminated"].(string); terminated != "done" {
		t.Fatalf("expected terminated=done, got %q (Data=%#v)", terminated, res.Data)
	}
	payload, _ := res.Data["payload"].(map[string]any)
	if payload["result"] != "ok" {
		t.Fatalf("expected payload.result=ok, got %#v", payload)
	}
}

// TestAgentCodeactHandler_APIPathSendsAPIKey asserts the api_key_env on the
// plugin decl surfaces as an Authorization: Bearer header on the HTTP call.
// Uses t.Setenv so cannot be parallel.
func TestAgentCodeactHandler_APIPathSendsAPIKey(t *testing.T) {
	t.Setenv("KITSOKI_TEST_GLM_KEY", "secret-token")
	turn1, _ := json.Marshal(map[string]any{"action": "done", "payload": map[string]any{"ok": true}})
	rt := &fakeAPIChatHandler{t: t, turns: []string{string(turn1)}}
	reg := glmRegistry(rt, func(o *agent.LocalLLMAgent) { o.WithAPIKeyEnv("KITSOKI_TEST_GLM_KEY") })

	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentPluginName(ctx, "agent.glm")
	ctx = host.WithAgents(ctx, map[string]host.Agent{"agent.glm": {SystemPrompt: "You triage."}})

	res, err := host.AgentCodeactHandler(ctx, map[string]any{
		"agent":        "agent.glm",
		"goal":         "produce a result",
		"budget":       5,
		"capabilities": map[string]any{"world": "read"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if got := rt.authHeader(); got != "Bearer secret-token" {
		t.Errorf("Authorization header: got %q, want %q", got, "Bearer secret-token")
	}
}

// TestAgentCodeactHandler_FallsBackToCLIWhenNoPluginNamed proves wiring a
// local_llm registry does NOT divert default codeact to the API path: with no
// plugin name injected, IsLocalLLM("") is false and the handler uses the CLI
// path (RealCodeactAgent via the scripted runtime). The explodeHandler
// fails loudly if the API path is accidentally taken.
func TestAgentCodeactHandler_FallsBackToCLIWhenNoPluginNamed(t *testing.T) {
	t.Parallel()
	turns := []map[string]any{
		{"action": "snippet", "snippet": "def main(ctx):\n    return {\"seen\": True}\n"},
		{"action": "done", "payload": map[string]any{"result": "ok"}},
	}
	runtime := fakeCodeactRuntime(t, turns)
	reg := glmRegistry(&explodeHandler{t: t}) // API path must NOT be taken

	ctx := host.WithAgentRuntimeRegistry(context.Background(), agentruntime.NewRegistry(runtime))
	ctx = host.WithAgentRegistry(ctx, reg)
	// Deliberately NO WithAgentPluginName → plugin name "" → CLI path.
	ctx = host.WithAgents(ctx, map[string]host.Agent{"coder": {SystemPrompt: "You write small Starlark snippets."}})

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
	if terminated, _ := res.Data["terminated"].(string); terminated != "done" {
		t.Fatalf("expected terminated=done via CLI path, got %q (Data=%#v)", terminated, res.Data)
	}
}
