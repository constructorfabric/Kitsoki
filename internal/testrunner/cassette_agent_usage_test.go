package testrunner

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// TestCassetteAgentEvents_UsageMeta is the regression for the host-cassette
// replay path dropping recorded token usage from the trace. An EpisodeAgent
// carrying prompt_tokens / response_tokens / cost_usd must surface them on the
// agent.call.complete event's Meta in the canonical opaque shape
// (meta.usage.{input,output}_tokens + meta.cost_usd) the live claude-CLI
// transport emits — otherwise the runstatus usage meter aggregates the call as
// $0 even though the agent genuinely ran. Mirrors cassetteAgent.Ask.
func TestCassetteAgentEvents_UsageMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	p := writeCassetteFile(t, dir, "usage_agent.yaml", `
kind: host_cassette
app_id: git-ops
episodes:
  - id: decide_with_usage
    match:
      handler: host.agent.decide
    response:
      data: {submitted: {message: "fix: x"}}
    agent:
      verb: decide
      agent: git-ops
      model: claude-sonnet-4-6
      duration_ms: 1700
      prompt_tokens: 1430
      response_tokens: 38
      cost_usd: 0.0121
      response: '{"message":"fix: x"}'
`)

	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	sink := newMemSink()
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
		SessionID: app.SessionID("usage-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: "commit",
	})
	clk := newFakeClock()
	stateOf := func() string { return "commit" }
	dispatch := BuildCassetteDispatcherWithSink(cas, "host.agent.decide", stateOf, nil, nil, clk, sink, nil)
	if _, derr := dispatch(ctx, nil); derr != nil {
		t.Fatalf("dispatch: %v", derr)
	}

	var pl *host.AgentReturnedPayload
	for _, ev := range sink.History() {
		if ev.Kind != store.AgentReturned {
			continue
		}
		var got host.AgentReturnedPayload
		if uErr := json.Unmarshal(ev.Payload, &got); uErr != nil {
			t.Fatalf("unmarshal AgentReturned: %v", uErr)
		}
		pl = &got
	}
	if pl == nil {
		t.Fatal("no agent.call.complete (AgentReturned) event emitted")
	}
	if pl.Meta == nil {
		t.Fatal("AgentReturned.Meta is nil — recorded usage/cost was dropped")
	}
	usage, ok := pl.Meta["usage"].(map[string]any)
	if !ok {
		t.Fatalf("Meta.usage missing or wrong type: %#v", pl.Meta["usage"])
	}
	if got := toInt(usage["input_tokens"]); got != 1430 {
		t.Errorf("usage.input_tokens: got %d want 1430", got)
	}
	if got := toInt(usage["output_tokens"]); got != 38 {
		t.Errorf("usage.output_tokens: got %d want 38", got)
	}
	if got := toFloat(pl.Meta["cost_usd"]); got != 0.0121 {
		t.Errorf("Meta.cost_usd: got %v want 0.0121", got)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return -1
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return -1
}
