package agentbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerKeepsFailedRetryAndRejectsDuplicateTerminal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	lines := []map[string]any{
		{"ts": "2026-07-13T00:00:00Z", "kind": "agent.call.start", "call_id": "a1", "state_path": "generate", "payload": map[string]any{"provider": "openai", "model": "gpt-test", "effort": "low"}},
		{"ts": "2026-07-13T00:00:01Z", "kind": "agent.call.error", "call_id": "a1", "payload": map[string]any{"error": "429"}},
		{"ts": "2026-07-13T00:00:02Z", "kind": "agent.call.start", "call_id": "a2", "state_path": "generate", "payload": map[string]any{"provider": "openai", "model": "gpt-test", "effort": "low"}},
		{"ts": "2026-07-13T00:00:03Z", "kind": "agent.call.complete", "call_id": "a2", "payload": map[string]any{"meta": map[string]any{"usage": map[string]any{"input_tokens": float64(7), "output_tokens": float64(2)}, "cost_decimal": "0.000123", "monetary_basis": "provider_estimate"}}},
	}
	var b []byte
	for _, l := range lines {
		x, _ := json.Marshal(l)
		b = append(b, append(x, '\n')...)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := BuildProviderRequestLedger(p, RequestIdentity{RunID: "run", AttemptID: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Status != "failed" || rows[1].Money.Amount != "0.000123" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestFrictionUnknownIsNotZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	data := "{\"ts\":\"2026-07-13T00:00:00Z\",\"kind\":\"agent.stream\",\"payload\":{\"tool\":\"read_file\",\"text\":\"ok\"}}\n"
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := AnalyzeFriction(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.CrawlTokens.Available || r.CrawlTokens.Reason == "" {
		t.Fatalf("crawl metric=%+v; want unavailable explanation", r.CrawlTokens)
	}
}

func TestProviderResultReceiptsPreserveRetryResults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	lines := []map[string]any{
		{"ts": "2026-07-13T00:00:00Z", "kind": "agent.call.start", "call_id": "retry-1", "state_path": "generate", "payload": map[string]any{"provider": "openai", "model": "gpt-test"}},
		{"ts": "2026-07-13T00:00:01Z", "kind": "agent.call.error", "call_id": "retry-1", "payload": map[string]any{"error": "429"}},
		{"ts": "2026-07-13T00:00:02Z", "kind": "agent.call.start", "call_id": "retry-2", "state_path": "generate", "payload": map[string]any{"provider": "openai", "model": "gpt-test"}},
		{"ts": "2026-07-13T00:00:04Z", "kind": "agent.call.complete", "call_id": "retry-2", "payload": map[string]any{"meta": map[string]any{"usage": map[string]any{"input_tokens": float64(7), "output_tokens": float64(2)}}}},
	}
	var b []byte
	for _, l := range lines {
		x, _ := json.Marshal(l)
		b = append(b, append(x, '\n')...)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	receipts, err := BuildProviderResultReceipts(p, RequestIdentity{RunID: "run", AttemptID: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 || receipts[0].AttemptOrdinal != 1 || receipts[1].AttemptOrdinal != 2 {
		t.Fatalf("receipts=%+v", receipts)
	}
	if receipts[0].Status != "failed" || receipts[1].Status != "completed" || receipts[1].APIDurationMS != 2000 {
		t.Fatalf("receipt lifecycle=%+v", receipts)
	}
	if receipts[1].RawUsageHash == "" {
		t.Fatal("successful provider result must carry raw usage hash")
	}
}

func TestValidateRuntimeLifecycleRejectsMalformedPairing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	lines := []map[string]any{
		{"kind": "agent.runtime.start", "call_id": "unclosed"},
		{"kind": "agent.runtime.end", "call_id": "orphan"},
		{"kind": "agent.runtime.start", "call_id": "duplicate"},
		{"kind": "agent.runtime.start", "call_id": "duplicate"},
		{"kind": "agent.runtime.end", "call_id": "duplicate"},
		{"kind": "agent.runtime.end", "call_id": "duplicate"},
		{"kind": "agent.runtime.start"},
	}
	var b []byte
	for _, line := range lines {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, append(raw, '\n')...)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := ValidateRuntimeLifecycle(p)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Violations) != 5 {
		t.Fatalf("report=%+v; want five lifecycle violations", report)
	}
	if report.Violations[0].Reason != "runtime start has no matching end" {
		t.Fatalf("first violation=%+v; want unclosed start", report.Violations[0])
	}
}

func TestValidateRuntimeLifecycleAcceptsIndependentClosedRuntimes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trace.jsonl")
	lines := []map[string]any{
		{"kind": "agent.runtime.start", "call_id": "first"},
		{"kind": "agent.runtime.start", "call_id": "second"},
		{"kind": "agent.runtime.end", "call_id": "second"},
		{"kind": "agent.runtime.end", "call_id": "first"},
	}
	var b []byte
	for _, line := range lines {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, append(raw, '\n')...)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := ValidateRuntimeLifecycle(p)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || len(report.Violations) != 0 {
		t.Fatalf("report=%+v; want valid closed lifecycles", report)
	}
}
