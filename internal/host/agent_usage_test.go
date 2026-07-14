package host_test

// Token-usage capture tests — assert that the per-invocation token usage the
// claude CLI reports on its terminal stream-json `result` event is surfaced on
// the AgentReturned event's Meta, via the per-call usage box installed by the
// orchestrator's host dispatch (host.WithAgentUsageBox).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// fakeStreamRunner returns a ClaudeRunner whose stdout is a stream-json
// transcript ending in a result event that carries `usage` + `total_cost_usd`.
// runClaudeStreamJSON's stub branch parses these lines, so the handler sees the
// final reply text AND records the usage into the context usage box.
func fakeStreamRunner(reply string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-usage-1"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}}`,
			`{"type":"result","subtype":"success","result":` + mustJSON(reply) +
				`,"session_id":"sess-usage-1","total_cost_usd":0.0123,` +
				`"usage":{"input_tokens":1200,"output_tokens":345,` +
				`"cache_read_input_tokens":900,"cache_creation_input_tokens":50}}`,
		}
		out := ""
		for _, l := range lines {
			out += l + "\n"
		}
		return host.ClaudeRun{Stdout: out}, nil
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestAgentAsk_UsageMeta asserts the result event's token usage + cost reach
// AgentReturned.Meta when a usage box is installed (the production wiring).
func TestAgentAsk_UsageMeta(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("summarise"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	// Production installs a fresh usage box per host call in host_dispatch.go;
	// mirror that here so the transport has somewhere to record usage.
	ctx := host.WithAgentUsageBox(agentCtxForTest(sink))
	ctx = host.WithClaudeRunner(ctx, fakeStreamRunner("final answer"))

	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath})
	if err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	returned := findEvent(t, sink.events, store.AgentReturned)
	var payload struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(returned.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentReturned.Payload: %v", err)
	}
	if payload.Meta == nil {
		t.Fatal("AgentReturned.Meta is nil — token usage was not captured")
	}

	usage, ok := payload.Meta["usage"].(map[string]any)
	if !ok {
		t.Fatalf("Meta.usage missing or wrong type: %#v", payload.Meta["usage"])
	}
	if got := usage["input_tokens"]; got != float64(1200) {
		t.Errorf("Meta.usage.input_tokens = %v, want 1200", got)
	}
	if got := usage["output_tokens"]; got != float64(345) {
		t.Errorf("Meta.usage.output_tokens = %v, want 345", got)
	}
	if got := usage["cache_read_input_tokens"]; got != float64(900) {
		t.Errorf("Meta.usage.cache_read_input_tokens = %v, want 900", got)
	}
	if got := payload.Meta["cost_usd"]; got != 0.0123 {
		t.Errorf("Meta.cost_usd = %v, want 0.0123", got)
	}
}

// TestAgentAsk_UsageMeta_CacheFields asserts the claude CLI's
// cache_read_input_tokens / cache_creation_input_tokens reach a named,
// typed surface on AgentReturned.Meta.cache (host.CacheUsage) — not just the
// raw Meta.usage map — per dispatch-context-floor task 1.2. Mirrors
// internal/harness/live.go's UsageInfo cache fields.
func TestAgentAsk_UsageMeta_CacheFields(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("summarise"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithAgentUsageBox(agentCtxForTest(sink))
	ctx = host.WithClaudeRunner(ctx, fakeStreamRunner("final answer"))

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	returned := findEvent(t, sink.events, store.AgentReturned)
	var payload struct {
		Meta struct {
			Cache *host.CacheUsage `json:"cache"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(returned.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentReturned.Payload: %v", err)
	}
	if payload.Meta.Cache == nil {
		t.Fatal("Meta.cache is nil — cache usage was not surfaced")
	}
	if got := payload.Meta.Cache.ReadTokens; got != 900 {
		t.Errorf("Meta.cache.read_tokens = %d, want 900", got)
	}
	if got := payload.Meta.Cache.CreationTokens; got != 50 {
		t.Errorf("Meta.cache.creation_tokens = %d, want 50", got)
	}
	if !payload.Meta.Cache.Hit {
		t.Error("Meta.cache.hit = false, want true (cache_read_input_tokens > 0)")
	}
}

// fakeStreamRunnerNoCache mirrors fakeStreamRunner but reports a usage object
// with no cache fields at all — the shape a transport with no cache
// accounting (e.g. copilot) would produce.
func fakeStreamRunnerNoCache(reply string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-usage-2"}`,
			`{"type":"result","subtype":"success","result":` + mustJSON(reply) +
				`,"session_id":"sess-usage-2","total_cost_usd":0.01,` +
				`"usage":{"input_tokens":100,"output_tokens":20}}`,
		}
		out := ""
		for _, l := range lines {
			out += l + "\n"
		}
		return host.ClaudeRun{Stdout: out}, nil
	}
}

// TestAgentAsk_UsageMeta_CacheFields_Absent asserts Meta.cache stays omitted
// (not a false all-zero CacheUsage) when the transport's usage object carries
// no cache keys at all — the fallback case task 1.2 calls out for
// cache-less transports.
func TestAgentAsk_UsageMeta_CacheFields_Absent(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("summarise"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithAgentUsageBox(agentCtxForTest(sink))
	ctx = host.WithClaudeRunner(ctx, fakeStreamRunnerNoCache("final answer"))

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	returned := findEvent(t, sink.events, store.AgentReturned)
	var payload struct {
		Meta struct {
			Cache *host.CacheUsage `json:"cache"`
			Usage map[string]any   `json:"usage"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(returned.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentReturned.Payload: %v", err)
	}
	if payload.Meta.Usage == nil {
		t.Fatal("Meta.usage is nil — usage box wiring regressed")
	}
	if payload.Meta.Cache != nil {
		t.Errorf("Meta.cache = %#v, want nil (no cache keys reported)", payload.Meta.Cache)
	}
}

// TestAgentAsk_UsageMeta_NoBox asserts the call still succeeds (Meta omitted)
// when no usage box is installed — degrading gracefully for paths that haven't
// wired one.
func TestAgentAsk_UsageMeta_NoBox(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("summarise"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithClaudeRunner(agentCtxForTest(sink), fakeStreamRunner("final answer"))
	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath})
	if err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	returned := findEvent(t, sink.events, store.AgentReturned)
	var payload struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(returned.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentReturned.Payload: %v", err)
	}
	if payload.Meta != nil {
		t.Errorf("expected nil Meta without a usage box, got %#v", payload.Meta)
	}
}

func findEvent(t *testing.T, events []store.Event, kind store.EventKind) store.Event {
	t.Helper()
	for _, ev := range events {
		if ev.Kind == kind {
			return ev
		}
	}
	t.Fatalf("no %q event found among %v", kind, kinds(events))
	return store.Event{}
}
