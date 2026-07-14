package agentbench

// Immutable request-ledger and friction sidecars are deliberately trace-only:
// they must be usable to audit a completed run without contacting a provider.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const ProviderRequestLedgerV1 = "provider-request-ledger/v1"
const FrictionV1 = "friction/v1"
const ProviderResultReceiptV1 = "provider-result/v1"
const RuntimeLifecycleV1 = "agent-runtime-lifecycle/v1"

type RequestIdentity struct {
	RunID     string `json:"run_id"`
	AttemptID string `json:"attempt_id"`
}

// ProviderRequest is append-only evidence for one provider invocation. Money
// remains a decimal string: float conversion is forbidden at this boundary.
type ProviderRequest struct {
	Schema       string       `json:"schema"`
	RequestID    string       `json:"request_id"`
	RunID        string       `json:"run_id"`
	AttemptID    string       `json:"attempt_id"`
	Stage        string       `json:"stage"`
	Requested    ModelRequest `json:"requested"`
	Resolved     ModelRequest `json:"resolved"`
	StartedNS    int64        `json:"started_monotonic_ns"`
	FinishedNS   int64        `json:"finished_monotonic_ns"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	Usage        TokenUsage   `json:"usage"`
	Money        Money        `json:"money"`
	EvidenceHash string       `json:"evidence_hash"`
}

// ProviderResultReceipt is the provider-result boundary for one terminal
// request. Unlike aggregate bench metrics, this record survives retries and
// resumes as one independently attributable result. Unknown usage remains
// unknown (zero values plus an empty raw hash), rather than being reported as
// a free provider call.
type ProviderResultReceipt struct {
	Schema         string       `json:"schema"`
	RequestID      string       `json:"request_id"`
	RunID          string       `json:"run_id"`
	AttemptID      string       `json:"attempt_id"`
	AttemptOrdinal int          `json:"attempt_ordinal"`
	Stage          string       `json:"stage"`
	Requested      ModelRequest `json:"requested"`
	Resolved       ModelRequest `json:"resolved"`
	StartedNS      int64        `json:"started_monotonic_ns"`
	FinishedNS     int64        `json:"finished_monotonic_ns"`
	APIDurationMS  int64        `json:"api_duration_ms"`
	Status         string       `json:"status"`
	Error          string       `json:"error,omitempty"`
	Usage          TokenUsage   `json:"usage"`
	Money          Money        `json:"money"`
	RawUsageHash   string       `json:"raw_usage_hash,omitempty"`
	EvidenceHash   string       `json:"evidence_hash"`
}

type ModelRequest struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
}
type TokenUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
}
type Money struct {
	Amount   string `json:"amount_decimal,omitempty"`
	Currency string `json:"currency"`
	Basis    string `json:"basis,omitempty"`
}

type ledgerEvent struct {
	TS        time.Time      `json:"ts"`
	Kind      string         `json:"kind"`
	StatePath string         `json:"state_path"`
	CallID    string         `json:"call_id"`
	Payload   map[string]any `json:"payload"`
}

// RuntimeLifecycleReport records the complete lifecycle verdict for supervised
// agent runtimes in an EventSink JSONL trace. A valid report proves that every
// start has exactly one terminal end with the same call ID.
type RuntimeLifecycleReport struct {
	Schema     string
	Trace      string
	Valid      bool
	Violations []RuntimeLifecycleViolation
}

// RuntimeLifecycleViolation identifies a trace line that cannot participate in
// a single well-formed runtime lifecycle.
type RuntimeLifecycleViolation struct {
	Line   int
	CallID string
	Kind   string
	Reason string
}

// ValidateRuntimeLifecycle verifies the terminal pairing contract independently
// of Arena scoring. Runtime events without a call ID, duplicate starts or ends,
// and starts with no end are all invalid. Malformed trace JSON is an error,
// never silently ignored.
func ValidateRuntimeLifecycle(trace string) (RuntimeLifecycleReport, error) {
	f, err := os.Open(trace)
	if err != nil {
		return RuntimeLifecycleReport{}, err
	}
	defer f.Close()

	report := RuntimeLifecycleReport{Schema: RuntimeLifecycleV1, Trace: trace, Valid: true}
	starts := map[string]int{}
	ends := map[string]int{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; s.Scan(); line++ {
		var ev ledgerEvent
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			return report, fmt.Errorf("%s:%d: %w", trace, line, err)
		}
		if ev.Kind != "agent.runtime.start" && ev.Kind != "agent.runtime.end" {
			continue
		}
		if strings.TrimSpace(ev.CallID) == "" {
			report.Violations = append(report.Violations, RuntimeLifecycleViolation{
				Line: line, Kind: ev.Kind, Reason: "runtime event is missing call_id",
			})
			continue
		}
		switch ev.Kind {
		case "agent.runtime.start":
			if first, exists := starts[ev.CallID]; exists {
				report.Violations = append(report.Violations, RuntimeLifecycleViolation{
					Line: line, CallID: ev.CallID, Kind: ev.Kind,
					Reason: fmt.Sprintf("duplicate runtime start; first start is line %d", first),
				})
				continue
			}
			starts[ev.CallID] = line
		case "agent.runtime.end":
			if first, exists := ends[ev.CallID]; exists {
				report.Violations = append(report.Violations, RuntimeLifecycleViolation{
					Line: line, CallID: ev.CallID, Kind: ev.Kind,
					Reason: fmt.Sprintf("duplicate runtime end; first end is line %d", first),
				})
				continue
			}
			ends[ev.CallID] = line
			if _, started := starts[ev.CallID]; !started {
				report.Violations = append(report.Violations, RuntimeLifecycleViolation{
					Line: line, CallID: ev.CallID, Kind: ev.Kind, Reason: "runtime end has no matching start",
				})
			}
		}
	}
	if err := s.Err(); err != nil {
		return report, err
	}
	for callID, line := range starts {
		if _, ended := ends[callID]; !ended {
			report.Violations = append(report.Violations, RuntimeLifecycleViolation{
				Line: line, CallID: callID, Kind: "agent.runtime.start", Reason: "runtime start has no matching end",
			})
		}
	}
	sort.Slice(report.Violations, func(i, j int) bool {
		return report.Violations[i].Line < report.Violations[j].Line
	})
	report.Valid = len(report.Violations) == 0
	return report, nil
}

// BuildProviderRequestLedger reconstructs one immutable row per terminal
// provider call. A duplicate terminal event is rejected rather than silently
// becoming a last-write-wins update. Failed calls remain rows with zero/unknown
// usage, which is materially different from a zero-cost success.
func BuildProviderRequestLedger(trace string, identity RequestIdentity) ([]ProviderRequest, error) {
	f, err := os.Open(trace)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	starts := map[string]ledgerEvent{}
	terminal := map[string]bool{}
	var rows []ProviderRequest
	var n int64
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		n++
		var ev ledgerEvent
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", trace, n, err)
		}
		if ev.CallID == "" {
			continue
		}
		switch ev.Kind {
		case "agent.call.start":
			starts[ev.CallID] = ev
		case "agent.call.complete", "agent.call.error":
			start, ok := starts[ev.CallID]
			if !ok {
				continue
			}
			if terminal[ev.CallID] {
				return nil, fmt.Errorf("%s:%d: duplicate terminal evidence for request %q", trace, n, ev.CallID)
			}
			terminal[ev.CallID] = true
			requested := modelFrom(start.Payload)
			resolved := modelFrom(ev.Payload)
			if resolved.Model == "" {
				resolved = requested
			}
			usage, money := usageFrom(ev.Payload)
			status := "completed"
			if ev.Kind == "agent.call.error" {
				status = "failed"
			}
			row := ProviderRequest{Schema: ProviderRequestLedgerV1, RequestID: ev.CallID, RunID: identity.RunID, AttemptID: identity.AttemptID, Stage: start.StatePath, Requested: requested, Resolved: resolved, StartedNS: start.TS.UnixNano(), FinishedNS: ev.TS.UnixNano(), Status: status, Usage: usage, Money: money}
			if status == "failed" {
				row.Error, _ = ev.Payload["error"].(string)
			}
			raw, _ := json.Marshal(struct {
				Start    ledgerEvent `json:"start"`
				Terminal ledgerEvent `json:"terminal"`
			}{start, ev})
			sum := sha256.Sum256(raw)
			row.EvidenceHash = hex.EncodeToString(sum[:])
			rows = append(rows, row)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RequestID < rows[j].RequestID })
	return rows, nil
}

// BuildProviderResultReceipts emits one receipt for every terminal provider
// call in trace order. The ordinal is deliberately based on terminal results,
// not call IDs, so a retry/resume can be compared without relying on provider
// naming conventions.
func BuildProviderResultReceipts(trace string, identity RequestIdentity) ([]ProviderResultReceipt, error) {
	f, err := os.Open(trace)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	starts := map[string]ledgerEvent{}
	terminal := map[string]bool{}
	rows := make([]ProviderResultReceipt, 0)
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; s.Scan(); line++ {
		var ev ledgerEvent
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", trace, line, err)
		}
		if ev.CallID == "" {
			continue
		}
		if ev.Kind == "agent.call.start" {
			starts[ev.CallID] = ev
			continue
		}
		if ev.Kind != "agent.call.complete" && ev.Kind != "agent.call.error" {
			continue
		}
		start, ok := starts[ev.CallID]
		if !ok {
			continue
		}
		if terminal[ev.CallID] {
			return nil, fmt.Errorf("%s:%d: duplicate terminal evidence for request %q", trace, line, ev.CallID)
		}
		terminal[ev.CallID] = true
		requested := modelFrom(start.Payload)
		resolved := modelFrom(ev.Payload)
		if resolved.Model == "" {
			resolved = requested
		}
		usage, money := usageFrom(ev.Payload)
		status := "completed"
		if ev.Kind == "agent.call.error" {
			status = "failed"
		}
		receipt := ProviderResultReceipt{
			Schema: ProviderResultReceiptV1, RequestID: ev.CallID, RunID: identity.RunID,
			AttemptID: identity.AttemptID, AttemptOrdinal: len(rows) + 1, Stage: start.StatePath,
			Requested: requested, Resolved: resolved, StartedNS: start.TS.UnixNano(),
			FinishedNS: ev.TS.UnixNano(), APIDurationMS: ev.TS.Sub(start.TS).Milliseconds(),
			Status: status, Usage: usage, Money: money,
		}
		if status == "failed" {
			receipt.Error, _ = ev.Payload["error"].(string)
		}
		if raw, ok := rawUsage(ev.Payload); ok {
			receipt.RawUsageHash = sha256JSON(raw)
		}
		raw, _ := json.Marshal(struct {
			Start    ledgerEvent `json:"start"`
			Terminal ledgerEvent `json:"terminal"`
		}{start, ev})
		receipt.EvidenceHash = sha256JSON(json.RawMessage(raw))
		rows = append(rows, receipt)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func rawUsage(p map[string]any) (any, bool) {
	if meta, ok := p["meta"].(map[string]any); ok {
		if usage, ok := meta["usage"]; ok {
			return usage, true
		}
	}
	usage, ok := p["usage"]
	return usage, ok
}

func sha256JSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func modelFrom(p map[string]any) ModelRequest {
	return ModelRequest{Provider: stringField(p, "provider", "backend"), Model: stringField(p, "model", "resolved_model"), Effort: stringField(p, "effort", "reasoning_effort")}
}
func stringField(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k].(string); ok {
			return v
		}
	}
	return ""
}
func usageFrom(p map[string]any) (TokenUsage, Money) {
	if m, ok := p["meta"].(map[string]any); ok {
		p = m
	}
	u, _ := p["usage"].(map[string]any)
	if u == nil {
		u = p
	}
	return TokenUsage{Input: int64Value(u, "input_tokens"), Output: int64Value(u, "output_tokens"), Reasoning: int64Value(u, "reasoning_tokens"), CacheRead: int64Value(u, "cache_read_input_tokens"), CacheWrite: int64Value(u, "cache_creation_input_tokens")}, Money{Amount: stringField(p, "cost_decimal", "cost_usd_decimal"), Currency: "USD", Basis: stringField(p, "monetary_basis")}
}

type FrictionReport struct {
	Schema                    string   `json:"schema"`
	Trace                     string   `json:"trace"`
	ToolErrors                Metric   `json:"tool_errors"`
	SchemaFailures            Metric   `json:"schema_failures"`
	Retries                   Metric   `json:"retries"`
	NoStateChangeCalls        Metric   `json:"no_state_change_calls"`
	WastedCallTokens          Metric   `json:"wasted_call_tokens"`
	CrawlTokens               Metric   `json:"crawl_tokens"`
	TimeToFirstSuccessfulCall Metric   `json:"time_to_first_successful_call_ms"`
	Evidence                  []string `json:"evidence"`
}
type Metric struct {
	Available bool   `json:"available"`
	Value     int64  `json:"value,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// FrictionRankingV1 is the deterministic, no-provider ranking boundary for a
// set of EventSink traces. It deliberately ranks only directly evidenced
// friction; unavailable telemetry has no invented zero contribution.
const FrictionRankingV1 = "friction-ranking/v1"

type FrictionRanking struct {
	Schema  string              `json:"schema"`
	Entries []FrictionRankEntry `json:"entries"`
}

type FrictionRankEntry struct {
	Rank   int            `json:"rank"`
	Trace  string         `json:"trace"`
	Score  int64          `json:"score"`
	Report FrictionReport `json:"report"`
}

// RankFriction analyzes and orders traces by their evidenced friction. The
// weights make hard failures dominate retries and no-op calls, while keeping
// token and latency contributions bounded. Ties use the trace path, so a
// fixture always has one stable order on every machine.
func RankFriction(traces []string) (FrictionRanking, error) {
	ranking := FrictionRanking{Schema: FrictionRankingV1}
	for _, trace := range traces {
		report, err := AnalyzeFriction(trace)
		if err != nil {
			return FrictionRanking{}, err
		}
		ranking.Entries = append(ranking.Entries, FrictionRankEntry{
			Trace: trace, Score: frictionScore(report), Report: report,
		})
	}
	sort.SliceStable(ranking.Entries, func(i, j int) bool {
		if ranking.Entries[i].Score != ranking.Entries[j].Score {
			return ranking.Entries[i].Score > ranking.Entries[j].Score
		}
		return ranking.Entries[i].Trace < ranking.Entries[j].Trace
	})
	for i := range ranking.Entries {
		ranking.Entries[i].Rank = i + 1
	}
	return ranking, nil
}

func frictionScore(r FrictionReport) int64 {
	value := func(m Metric) int64 {
		if !m.Available {
			return 0
		}
		return m.Value
	}
	return value(r.ToolErrors)*1000 + value(r.SchemaFailures)*1000 +
		value(r.Retries)*100 + value(r.NoStateChangeCalls)*10 +
		value(r.WastedCallTokens)/100 + value(r.CrawlTokens)/100 +
		value(r.TimeToFirstSuccessfulCall)/1000
}

// AnalyzeFriction reports only facts that trace evidence supports. In
// particular token-attribution metrics are unavailable when provider usage is
// absent; they are never fabricated as zero.
func AnalyzeFriction(trace string) (FrictionReport, error) {
	f, err := os.Open(trace)
	if err != nil {
		return FrictionReport{}, err
	}
	defer f.Close()
	r := FrictionReport{Schema: FrictionV1, Trace: trace}
	var first time.Time
	var success time.Time
	var toolErrors, schemaFailures, retries, noChange, wasted int64
	var crawlTokens int64
	usageSeen := false
	seen := map[string]bool{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; s.Scan(); line++ {
		var ev ledgerEvent
		if err := json.Unmarshal(s.Bytes(), &ev); err != nil {
			return r, fmt.Errorf("%s:%d: %w", trace, line, err)
		}
		if first.IsZero() {
			first = ev.TS
		}
		kind := strings.ToLower(ev.Kind)
		if ev.Kind == "agent.call.start" {
			continue
		}
		if strings.Contains(kind, "tool") && (strings.Contains(kind, "error") || strings.Contains(kind, "fail")) {
			toolErrors++
			r.Evidence = append(r.Evidence, fmt.Sprintf("%s:%d", trace, line))
		}
		if strings.Contains(kind, "schema") && (strings.Contains(kind, "error") || strings.Contains(kind, "fail") || strings.Contains(kind, "invalid")) {
			schemaFailures++
			r.Evidence = append(r.Evidence, fmt.Sprintf("%s:%d", trace, line))
		}
		if strings.Contains(kind, "retry") || boolValue(ev.Payload["retry"]) {
			retries++
			r.Evidence = append(r.Evidence, fmt.Sprintf("%s:%d", trace, line))
		}
		if ev.Kind != "agent.call.complete" && ev.Kind != "agent.call.error" || ev.CallID == "" || seen[ev.CallID] {
			continue
		}
		seen[ev.CallID] = true
		if ev.Kind == "agent.call.complete" && success.IsZero() {
			success = ev.TS
		}
		usage, _ := usageFrom(ev.Payload)
		if changed, ok := ev.Payload["state_changed"].(bool); ok {
			if !changed {
				noChange++
				wasted += usage.Input + usage.Output
				r.Evidence = append(r.Evidence, fmt.Sprintf("%s:%d", trace, line))
			}
		}
		tool := strings.ToLower(stringField(ev.Payload, "tool", "tool_name", "name"))
		if strings.Contains(tool, "read") || strings.Contains(tool, "search") || tool == "rg" || tool == "grep" || tool == "find" {
			crawlTokens += usage.Input + usage.Output
			if raw, ok := rawUsage(ev.Payload); ok && raw != nil {
				usageSeen = true
			}
		}
	}
	if err := s.Err(); err != nil {
		return r, err
	}
	if toolErrors > 0 {
		r.ToolErrors = Metric{Available: true, Value: toolErrors}
	} else {
		r.ToolErrors = Metric{Reason: "trace has no tool-error events"}
	}
	if schemaFailures > 0 {
		r.SchemaFailures = Metric{Available: true, Value: schemaFailures}
	} else {
		r.SchemaFailures = Metric{Reason: "trace has no schema-failure events"}
	}
	if retries > 0 {
		r.Retries = Metric{Available: true, Value: retries}
	} else {
		r.Retries = Metric{Reason: "trace has no retry evidence"}
	}
	if noChange > 0 {
		r.NoStateChangeCalls = Metric{Available: true, Value: noChange}
	} else {
		r.NoStateChangeCalls = Metric{Reason: "trace has no no-state-change evidence"}
	}
	if wasted > 0 {
		r.WastedCallTokens = Metric{Available: true, Value: wasted}
	} else {
		r.WastedCallTokens = Metric{Reason: "trace has no no-state-change usage"}
	}
	if usageSeen {
		r.CrawlTokens = Metric{Available: true, Value: crawlTokens}
	} else {
		r.CrawlTokens = Metric{Reason: "trace has no per-tool token attribution"}
	}
	if !first.IsZero() && !success.IsZero() {
		r.TimeToFirstSuccessfulCall = Metric{Available: true, Value: success.Sub(first).Milliseconds()}
	} else {
		r.TimeToFirstSuccessfulCall = Metric{Reason: "trace has no successful tool call"}
	}
	return r, nil
}

func boolValue(v any) bool { b, _ := v.(bool); return b }
