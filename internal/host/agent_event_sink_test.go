package host

import (
	"context"
	"testing"
)

// TestRecordAgentUsageSumsAcrossRetries pins the fix for the last-write-wins
// usage bug: internal/host/agent_event_sink.go's agentUsageBox previously
// overwrote b.usage on every recordAgentUsage call, so a call that retried or
// (like host.agent.codeact) issued several provider requests under one box
// only ever reported its LAST request's tokens/cost — silently discarding
// every earlier request's spend. This is the root cause behind the "$0.006"
// headline in .context/2026-07-11-gx10-small-model-study-report.md actually
// being one reviewer resume out of seven retained provider results (~$0.034
// recomputed), per
// .context/2026-07-11-gx10-small-model-study-adversarial-review.md.
func TestRecordAgentUsageSumsAcrossRetries(t *testing.T) {
	ctx := WithAgentUsageBox(context.Background())

	recordAgentUsage(ctx, map[string]any{
		"input_tokens":  float64(1000),
		"output_tokens": float64(200),
	}, 0.01)
	recordAgentUsage(ctx, map[string]any{
		"input_tokens":  float64(2500),
		"output_tokens": float64(300),
	}, 0.02)
	recordAgentUsage(ctx, map[string]any{
		"input_tokens":  float64(500),
		"output_tokens": float64(50),
	}, 0.003)

	usage := AgentUsageFrom(ctx)
	if usage == nil {
		t.Fatal("expected usage to be recorded")
	}
	if got := usageInt64(usage, "input_tokens"); got != 4000 {
		t.Fatalf("summed input_tokens = %d, want 4000 (1000+2500+500 across 3 retained calls, not last-write-wins 500)", got)
	}
	if got := usageInt64(usage, "output_tokens"); got != 550 {
		t.Fatalf("summed output_tokens = %d, want 550", got)
	}
	if cost := AgentCostFrom(ctx); cost < 0.0329 || cost > 0.0331 {
		t.Fatalf("summed cost = %v, want ~0.033 (0.01+0.02+0.003 across 3 retained calls, not last-write-wins 0.003)", cost)
	}
}

// TestRecordAgentUsageNoBoxIsNoop mirrors the historical no-op contract: a
// call with no box installed in ctx (e.g. a direct unit-test call to a
// handler with a bare context.Background()) must not panic and must leave
// AgentUsageFrom/AgentCostFrom at their zero values.
func TestRecordAgentUsageNoBoxIsNoop(t *testing.T) {
	ctx := context.Background()
	recordAgentUsage(ctx, map[string]any{"input_tokens": float64(100)}, 1.0)
	if usage := AgentUsageFrom(ctx); usage != nil {
		t.Fatalf("expected nil usage with no box, got %v", usage)
	}
	if cost := AgentCostFrom(ctx); cost != 0 {
		t.Fatalf("expected zero cost with no box, got %v", cost)
	}
}

// TestSumUsageMergesNumericBucketsAndOverwritesOther pins sumUsage's exact
// contract: known numeric spend buckets sum, everything else last-write-wins
// (matching the historical behavior for non-spend fields).
func TestSumUsageMergesNumericBucketsAndOverwritesOther(t *testing.T) {
	prev := map[string]any{
		"input_tokens":  float64(10),
		"output_tokens": float64(5),
		"model":         "gpt-oss-120b",
	}
	next := map[string]any{
		"input_tokens":  float64(20),
		"output_tokens": float64(7),
		"model":         "gpt-oss-120b-resume",
	}
	out := sumUsage(prev, next)
	if got := usageInt64(out, "input_tokens"); got != 30 {
		t.Fatalf("input_tokens = %d, want 30", got)
	}
	if got := usageInt64(out, "output_tokens"); got != 12 {
		t.Fatalf("output_tokens = %d, want 12", got)
	}
	if out["model"] != "gpt-oss-120b-resume" {
		t.Fatalf("model = %v, want last-write-wins for non-numeric field", out["model"])
	}
}
