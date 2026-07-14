package agentbench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	goyaml "github.com/goccy/go-yaml"
)

const Kind = "agent_bench/v1"

type Manifest struct {
	Version string `json:"version" yaml:"version"`
	Cases   []Case `json:"cases" yaml:"cases"`
	Path    string `json:"-" yaml:"-"`
}

type Case struct {
	ID           string       `json:"id" yaml:"id"`
	Description  string       `json:"description,omitempty" yaml:"description,omitempty"`
	Trace        string       `json:"trace,omitempty" yaml:"trace,omitempty"`
	Run          RunSpec      `json:"run,omitempty" yaml:"run,omitempty"`
	Budgets      Budgets      `json:"budgets,omitempty" yaml:"budgets,omitempty"`
	Expectations Expectations `json:"expectations,omitempty" yaml:"expectations,omitempty"`
}

type RunSpec struct {
	Command []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Env     map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Workdir string            `json:"workdir,omitempty" yaml:"workdir,omitempty"`
	Timeout string            `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type Budgets struct {
	MaxWallSeconds    float64 `json:"max_wall_seconds,omitempty" yaml:"max_wall_seconds,omitempty"`
	MaxToolCalls      int     `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxReadCalls      int     `json:"max_read_calls,omitempty" yaml:"max_read_calls,omitempty"`
	MaxFilesRead      int     `json:"max_files_read,omitempty" yaml:"max_files_read,omitempty"`
	MaxInputTokens    int64   `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`
	MaxOutputTokens   int64   `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"`
	MaxTotalTokens    int64   `json:"max_total_tokens,omitempty" yaml:"max_total_tokens,omitempty"`
	MaxCostUSD        float64 `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
	MaxThinkingEvents int     `json:"max_thinking_events,omitempty" yaml:"max_thinking_events,omitempty"`
}

type Expectations struct {
	RequireSubmit   bool     `json:"require_submit,omitempty" yaml:"require_submit,omitempty"`
	FinalState      string   `json:"final_state,omitempty" yaml:"final_state,omitempty"`
	ForbiddenTools  []string `json:"forbidden_tools,omitempty" yaml:"forbidden_tools,omitempty"`
	RequireArtifact string   `json:"require_artifact,omitempty" yaml:"require_artifact,omitempty"`
	// RequireSubmitVerb restricts which host.agent.* verb's submit call may
	// satisfy RequireSubmit (e.g. "task", "ask", "codeact"). When set, only a
	// submit tool call (or payload submitted:true signal) attributed to a call
	// with this exact verb counts. When empty (the common case), the default
	// per-role attribution rule applies instead: a submit attributed to the
	// "decide" verb (this harness's review/revise-verdict verb — see
	// docs/architecture/hosts.md#hostagentdecide) never counts toward
	// RequireSubmit, because "decide" is structurally the reviewer role, not
	// the maker/decomposer role under test. This is the fix for the false-pass
	// class where a reviewer's own submit/revise verdict satisfied a
	// decomposer's completion gate (see
	// .context/2026-07-11-gx10-small-model-study-adversarial-review.md).
	RequireSubmitVerb string `json:"require_submit_verb,omitempty" yaml:"require_submit_verb,omitempty"`
}

// Report is the scored outcome of one bench case against one trace.
//
// Passed remains the single composite gate existing callers (CLI exit code,
// artifacts, envelopes) key off: true iff there are zero Failures. Outcome and
// BudgetCompliance are the two independent axes the composite used to hide —
// a "partial" outcome (correctness gates all satisfied) with an
// "over_budget" compliance is a materially different result from a "failed"
// outcome, even though both fail the same Passed bool. See
// .context/2026-07-11-gx10-small-model-study-plan.md Stage 1.
type Report struct {
	CaseID   string   `json:"case_id"`
	Trace    string   `json:"trace"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
	Metrics  Metrics  `json:"metrics"`
	// Outcome is "solved" (every correctness gate — submit attribution,
	// final_state, required artifact, forbidden tools — passed),
	// "partial" (every correctness gate passed, but a budget was exceeded),
	// or "failed" (a correctness gate failed, regardless of budget).
	Outcome string `json:"outcome"`
	// BudgetCompliance is "within_budget" or "over_budget", independent of
	// Outcome — a case can be correctness-"failed" AND over budget at once.
	BudgetCompliance string `json:"budget_compliance"`
}

type Metrics struct {
	Events             int `json:"events"`
	AgentStreamEvents  int `json:"agent_stream_events"`
	AgentCallsStarted  int `json:"agent_calls_started,omitempty"`
	AgentCallsFinished int `json:"agent_calls_finished,omitempty"`
	AgentCallsErrored  int `json:"agent_calls_errored,omitempty"`
	AgentCallsInFlight int `json:"agent_calls_in_flight,omitempty"`
	RuntimeStarts      int `json:"runtime_starts,omitempty"`
	RuntimeEnds        int `json:"runtime_ends,omitempty"`
	RuntimeInFlight    int `json:"runtime_in_flight,omitempty"`
	// RuntimeAccountingStatus is complete only when every runtime start has a
	// matching end and every CLI CodeAct call has at least one paired
	// supervised-runtime receipt. direct_api is an explicit non-subprocess
	// CodeAct boundary, not an absent CLI receipt.
	RuntimeAccountingStatus  string         `json:"runtime_accounting_status,omitempty"`
	WallSeconds              float64        `json:"wall_seconds,omitempty"`
	ToolCallsTotal           int            `json:"tool_calls_total"`
	ToolCallsByName          map[string]int `json:"tool_calls_by_name,omitempty"`
	ReadCalls                int            `json:"read_calls,omitempty"`
	FilesRead                []string       `json:"files_read,omitempty"`
	ForbiddenToolCalls       map[string]int `json:"forbidden_tool_calls,omitempty"`
	ThinkingEvents           int            `json:"thinking_events,omitempty"`
	InputTokens              int64          `json:"input_tokens,omitempty"`
	OutputTokens             int64          `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int64          `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64          `json:"cache_read_input_tokens,omitempty"`
	TotalTokens              int64          `json:"total_tokens,omitempty"`
	CostUSD                  float64        `json:"cost_usd,omitempty"`
	// FinalState is the authoritative terminal state: the state_path of the
	// LAST machine.state_entered event in the trace (the leaf state the
	// machine actually settled in), falling back to the last event carrying a
	// non-empty top-level state_path only when the trace has no
	// machine.state_entered events at all (e.g. a non-story trace format).
	// The old behavior — last event with ANY non-empty state_path — is wrong
	// for kitsoki story traces: a machine.transition event's own top-level
	// state_path reflects the *compound parent* state active when the event
	// was written (e.g. "configure"), not the leaf destination the payload's
	// "to" field (or the paired machine.state_entered's state_path) names
	// (e.g. "__exit__needs-human"). See the false-pass root cause in
	// .context/2026-07-11-gx10-small-model-study-adversarial-review.md.
	FinalState string `json:"final_state,omitempty"`
	// Submitted is true if ANY validator submit signal was observed anywhere
	// in the trace, regardless of which call/verb it belongs to. Kept for
	// back-compat with existing consumers; RequireSubmit scoring uses
	// MakerSubmitted (or a verb-scoped check when RequireSubmitVerb is set),
	// not this field.
	Submitted bool `json:"submitted"`
	// MakerSubmitted is true if a submit signal was observed on a call whose
	// verb is NOT "decide" (i.e. not attributed to the reviewer role). This is
	// what RequireSubmit checks by default — see Expectations.RequireSubmitVerb.
	MakerSubmitted bool `json:"maker_submitted,omitempty"`
	// SubmittedByVerb records which host.agent.* verbs had at least one submit
	// signal attributed to them, for auditability (e.g. {"decide": true,
	// "codeact": false} shows a reviewer submitted but the maker never did).
	SubmittedByVerb map[string]bool `json:"submitted_by_verb,omitempty"`
	// AccountingStatus is "complete" when every started agent call reached a
	// terminal (finished or errored) event with usage recorded, or "partial"
	// when a call errored, is still in flight, or usage could not be
	// attributed to a specific call — i.e. the cost/token totals above are a
	// floor, not a reconciled total. Never silently reported as if it were
	// complete; consumers must check this field before treating CostUSD /
	// TotalTokens as exact.
	AccountingStatus string `json:"accounting_status"`
}

func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := goyaml.UnmarshalWithOptions(b, &m, goyaml.Strict()); err != nil {
		return Manifest{}, err
	}
	m.Path = path
	if m.Version != Kind {
		return Manifest{}, fmt.Errorf("unsupported agent bench version %q", m.Version)
	}
	seen := map[string]bool{}
	for i, c := range m.Cases {
		if c.ID == "" {
			return Manifest{}, fmt.Errorf("case %d missing id", i)
		}
		if seen[c.ID] {
			return Manifest{}, fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
	}
	return m, nil
}

func (m Manifest) Case(id string) (Case, error) {
	if id == "" {
		if len(m.Cases) == 1 {
			return m.Cases[0], nil
		}
		return Case{}, fmt.Errorf("--case is required when manifest has %d cases", len(m.Cases))
	}
	for _, c := range m.Cases {
		if c.ID == id {
			return c, nil
		}
	}
	return Case{}, fmt.Errorf("case %q not found", id)
}

func ScoreManifestCase(manifestPath, caseID, traceOverride string) (Report, error) {
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return Report{}, err
	}
	c, err := m.Case(caseID)
	if err != nil {
		return Report{}, err
	}
	trace, err := resolveCaseTrace(manifestPath, c, traceOverride)
	if err != nil {
		return Report{}, err
	}
	return ScoreTrace(trace, c)
}

func resolveCaseTrace(manifestPath string, c Case, traceOverride string) (string, error) {
	trace := traceOverride
	traceFromManifest := false
	if trace == "" {
		trace = c.Trace
		traceFromManifest = true
	}
	if trace == "" {
		return "", fmt.Errorf("case %q has no trace; pass --trace or set trace in manifest", c.ID)
	}
	if traceFromManifest && !filepath.IsAbs(trace) {
		trace = filepath.Join(filepath.Dir(manifestPath), trace)
	}
	return trace, nil
}

func ScoreTrace(tracePath string, c Case) (Report, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()

	metrics := Metrics{
		ToolCallsByName:    map[string]int{},
		ForbiddenToolCalls: map[string]int{},
		SubmittedByVerb:    map[string]bool{},
	}
	fileSet := map[string]bool{}
	forbidden := map[string]bool{}
	for _, tool := range c.Expectations.ForbiddenTools {
		forbidden[tool] = true
	}

	// callVerb maps a host.agent.* call_id to its dispatching verb
	// ("decide", "task", "ask", "codeact", ...), populated from each
	// agent.call.start event as it is seen. A single forward pass suffices:
	// agent.call.start always precedes the stream/complete/error events that
	// carry the same call_id. Used to attribute submit signals and usage
	// accounting-completeness per call, not just globally.
	callVerb := map[string]string{}
	// codeactRuntimeKind is stamped by host.agent.codeact on the outer call.
	// runtimeStarts/runtimeEnds are deliberately tracked independently from
	// agent calls: a runtime is a generator-process lifecycle, not another
	// provider request.
	codeactRuntimeKind := map[string]string{}
	runtimeStarts := map[string]int{}
	runtimeEnds := map[string]int{}
	// callTerminated tracks which call_ids reached a terminal (finished or
	// errored) event, for AccountingStatus.
	callTerminated := map[string]bool{}
	// fallbackFinalState is the legacy "last event with any non-empty
	// state_path" signal, used only when the trace has no
	// machine.state_entered events at all.
	var fallbackFinalState string
	var authoritativeFinalState string
	sawMachineState := false

	// callUsage buckets accumulated usage PER call_id (the "" key catches
	// events with no call_id, e.g. traces predating call_id propagation on
	// agent.stream events). Within one call_id, later usage-bearing events
	// legitimately repeat/refine the SAME call's own final number (a stream
	// delta and its paired agent.call.complete both describing one call), so
	// taking the max within a bucket is safe; but summing ACROSS distinct
	// call_id buckets is required — multiple call_ids are multiple distinct
	// provider requests (a validator retry loop's several `claude --resume`
	// calls, or host.agent.codeact's per-step calls), and treating them as
	// one running-max (the pre-fix behavior) silently reported only the
	// single most-expensive call instead of the call's total spend. This is
	// the review's "Agent-bench takes the maximum observed tokens/cost
	// instead of summing unique provider requests" finding, fixed here to
	// mirror the same call-summing internal/host/agent_event_sink.go now does
	// live — see
	// .context/2026-07-11-gx10-small-model-study-adversarial-review.md.
	callUsage := map[string]*Metrics{}
	usageBucket := func(callID string) *Metrics {
		b := callUsage[callID]
		if b == nil {
			b = &Metrics{}
			callUsage[callID] = b
		}
		return b
	}

	var first, last time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return Report{}, fmt.Errorf("%s:%d: %w", tracePath, line, err)
		}
		metrics.Events++
		if ev.StatePath != "" {
			fallbackFinalState = ev.StatePath
		}
		if ev.Kind == "machine.state_entered" {
			sawMachineState = true
			if ev.StatePath != "" {
				authoritativeFinalState = ev.StatePath
			} else if s := stringValue(ev.Payload, "state"); s != "" {
				authoritativeFinalState = s
			}
		}
		if !ev.TS.IsZero() {
			if first.IsZero() {
				first = ev.TS
			}
			last = ev.TS
		}
		verb := callVerb[ev.CallID]
		if ev.Kind == "agent.call.start" {
			if v := stringValue(ev.Payload, "verb"); v != "" {
				callVerb[ev.CallID] = v
				verb = v
			}
			if verb == "codeact" {
				codeactRuntimeKind[ev.CallID] = stringValue(ev.Payload, "runtime_kind")
			}
		}
		switch ev.Kind {
		case "agent.runtime.start":
			metrics.RuntimeStarts++
			runtimeStarts[ev.CallID]++
		case "agent.runtime.end":
			metrics.RuntimeEnds++
			runtimeEnds[ev.CallID]++
		}
		if ev.Kind == "agent.stream" {
			metrics.AgentStreamEvents++
			if isThinkingEvent(ev.Payload) {
				metrics.ThinkingEvents++
			}
			for _, call := range extractToolCalls(ev.Payload) {
				metrics.ToolCallsTotal++
				metrics.ToolCallsByName[call.Name]++
				if forbidden[call.Name] {
					metrics.ForbiddenToolCalls[call.Name]++
				}
				if strings.EqualFold(call.Name, "Read") {
					metrics.ReadCalls++
					if path := call.ReadPath(); path != "" && !fileSet[path] {
						fileSet[path] = true
						metrics.FilesRead = append(metrics.FilesRead, path)
					}
				}
				if isSubmitTool(call.Name) {
					recordSubmit(&metrics, verb)
				}
			}
			accumulateUsage(usageBucket(ev.CallID), ev.Payload)
		}
		switch ev.Kind {
		case "agent.call.start":
			metrics.AgentCallsStarted++
		case "agent.returned", "agent.call.returned", "agent.call.complete", "agent.call.end", "agent.task.complete":
			metrics.AgentCallsFinished++
			callTerminated[ev.CallID] = true
			accumulateUsage(usageBucket(ev.CallID), ev.Payload)
		case "agent.error", "agent.call.error":
			metrics.AgentCallsErrored++
			callTerminated[ev.CallID] = true
			accumulateUsage(usageBucket(ev.CallID), ev.Payload)
		}
		if payloadHasSubmit(ev.Payload) {
			recordSubmit(&metrics, verb)
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	if sawMachineState && authoritativeFinalState != "" {
		metrics.FinalState = authoritativeFinalState
	} else {
		metrics.FinalState = fallbackFinalState
	}
	// Sum every call's own (max-within-call) usage bucket into the trace
	// totals — see callUsage's doc comment above.
	for _, b := range callUsage {
		metrics.InputTokens += b.InputTokens
		metrics.OutputTokens += b.OutputTokens
		metrics.CacheCreationInputTokens += b.CacheCreationInputTokens
		metrics.CacheReadInputTokens += b.CacheReadInputTokens
		metrics.TotalTokens += b.TotalTokens
		metrics.CostUSD += b.CostUSD
	}
	if !first.IsZero() && !last.IsZero() && last.After(first) {
		metrics.WallSeconds = math.Round(last.Sub(first).Seconds()*1000) / 1000
	}
	if metrics.TotalTokens == 0 {
		metrics.TotalTokens = metrics.InputTokens + metrics.OutputTokens
	}
	sort.Strings(metrics.FilesRead)
	if len(metrics.ForbiddenToolCalls) == 0 {
		metrics.ForbiddenToolCalls = nil
	}
	if len(metrics.ToolCallsByName) == 0 {
		metrics.ToolCallsByName = nil
	}
	if len(metrics.SubmittedByVerb) == 0 {
		metrics.SubmittedByVerb = nil
	}
	terminalCalls := metrics.AgentCallsFinished + metrics.AgentCallsErrored
	if metrics.AgentCallsStarted > terminalCalls {
		metrics.AgentCallsInFlight = metrics.AgentCallsStarted - terminalCalls
	}
	metrics.AccountingStatus = "complete"
	if metrics.AgentCallsInFlight > 0 || metrics.AgentCallsErrored > 0 {
		metrics.AccountingStatus = "partial"
	}
	for id := range callVerb {
		if !callTerminated[id] {
			metrics.AccountingStatus = "partial"
		}
	}
	metrics.RuntimeAccountingStatus = "not_applicable"
	runtimeComplete := true
	runtimeIDs := map[string]bool{}
	for id := range runtimeStarts {
		runtimeIDs[id] = true
	}
	for id := range runtimeEnds {
		runtimeIDs[id] = true
	}
	for id := range runtimeIDs {
		starts, ends := runtimeStarts[id], runtimeEnds[id]
		if starts != ends {
			runtimeComplete = false
			if starts > ends {
				metrics.RuntimeInFlight += starts - ends
			} else {
				metrics.RuntimeInFlight += ends - starts
			}
		}
	}
	directAPIOnly := len(codeactRuntimeKind) > 0
	for id, kind := range codeactRuntimeKind {
		switch kind {
		case "cli":
			directAPIOnly = false
			if runtimeStarts[id] == 0 || runtimeStarts[id] != runtimeEnds[id] {
				runtimeComplete = false
			}
		case "direct_api":
			if runtimeStarts[id] != 0 || runtimeEnds[id] != 0 {
				runtimeComplete = false
			}
		default:
			directAPIOnly = false
			// A CodeAct trace without an explicit kind predates the receipt
			// contract and cannot be exact campaign evidence.
			runtimeComplete = false
		}
	}
	if len(codeactRuntimeKind) > 0 || metrics.RuntimeStarts > 0 || metrics.RuntimeEnds > 0 {
		metrics.RuntimeAccountingStatus = "complete"
		if directAPIOnly && metrics.RuntimeStarts == 0 && metrics.RuntimeEnds == 0 {
			metrics.RuntimeAccountingStatus = "direct_api"
		}
		if !runtimeComplete {
			metrics.RuntimeAccountingStatus = "partial"
			metrics.AccountingStatus = "partial"
		}
	}

	correctnessFailures, budgetFailures := scoreFailures(c, metrics, tracePath, first)
	failures := append(append([]string{}, correctnessFailures...), budgetFailures...)

	outcome := "solved"
	if len(correctnessFailures) > 0 {
		outcome = "failed"
	} else if len(budgetFailures) > 0 {
		outcome = "partial"
	}
	budgetCompliance := "within_budget"
	if len(budgetFailures) > 0 {
		budgetCompliance = "over_budget"
	}

	return Report{
		CaseID:           c.ID,
		Trace:            tracePath,
		Passed:           len(failures) == 0,
		Failures:         failures,
		Metrics:          metrics,
		Outcome:          outcome,
		BudgetCompliance: budgetCompliance,
	}, nil
}

// recordSubmit attributes a submit signal to verb (the call it was observed
// under; "" when the signal could not be tied to a specific call_id — treated
// as a maker submit for back-compat with traces/tests that predate call_id
// propagation on agent.stream events). Only a verb of exactly "decide" — this
// harness's reviewer/verdict verb — is excluded from MakerSubmitted.
func recordSubmit(m *Metrics, verb string) {
	m.Submitted = true
	if verb != "" {
		m.SubmittedByVerb[verb] = true
	}
	if verb != "decide" {
		m.MakerSubmitted = true
	}
}

type traceEvent struct {
	TS        time.Time      `json:"ts"`
	Kind      string         `json:"kind"`
	StatePath string         `json:"state_path"`
	CallID    string         `json:"call_id"`
	Payload   map[string]any `json:"payload"`
}

type toolCall struct {
	Name    string
	Preview string
	Input   any
}

func extractToolCalls(payload map[string]any) []toolCall {
	var out []toolCall
	if raw, ok := payload["tools"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name := stringValue(m, "name", "tool"); name != "" {
				out = append(out, toolCall{Name: name, Preview: stringValue(m, "preview", "text"), Input: m["input"]})
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if name := stringValue(payload, "tool", "name"); name != "" {
		out = append(out, toolCall{Name: name, Preview: stringValue(payload, "preview", "text"), Input: payload["input"]})
	}
	return out
}

func (c toolCall) ReadPath() string {
	if m, ok := c.Input.(map[string]any); ok {
		for _, key := range []string{"file_path", "path"} {
			if s, ok := m[key].(string); ok {
				return s
			}
		}
	}
	fields := strings.Fields(c.Preview)
	for _, f := range fields {
		if strings.Contains(f, "/") || strings.Contains(f, ".") {
			return strings.Trim(f, "`'\"")
		}
	}
	return ""
}

func isThinkingEvent(payload map[string]any) bool {
	if stringValue(payload, "thinking") != "" {
		return true
	}
	subtype := stringValue(payload, "subtype", "type")
	return strings.Contains(subtype, "thinking")
}

func isSubmitTool(name string) bool {
	lower := strings.ToLower(name)
	return lower == "submit" || strings.HasSuffix(lower, "__submit") || strings.Contains(lower, "validator__submit")
}

func payloadHasSubmit(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "submitted" {
				if b, ok := val.(bool); !ok || b {
					return true
				}
			}
			if payloadHasSubmit(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if payloadHasSubmit(val) {
				return true
			}
		}
	}
	return false
}

func accumulateUsage(metrics *Metrics, payload map[string]any) {
	metrics.InputTokens = maxInt64(metrics.InputTokens, int64Value(payload, "input_tokens"))
	metrics.OutputTokens = maxInt64(metrics.OutputTokens, int64Value(payload, "output_tokens"))
	metrics.CacheCreationInputTokens = maxInt64(metrics.CacheCreationInputTokens, int64Value(payload, "cache_creation_input_tokens"))
	metrics.CacheReadInputTokens = maxInt64(metrics.CacheReadInputTokens, int64Value(payload, "cache_read_input_tokens"))
	metrics.TotalTokens = maxInt64(metrics.TotalTokens, int64Value(payload, "total_tokens"))
	metrics.CostUSD = math.Max(metrics.CostUSD, floatValue(payload, "total_cost_usd", "cost_usd"))
	if meta, ok := payload["meta"].(map[string]any); ok {
		metrics.CostUSD = math.Max(metrics.CostUSD, floatValue(meta, "total_cost_usd", "cost_usd"))
		if usage, ok := meta["usage"].(map[string]any); ok {
			metrics.InputTokens = maxInt64(metrics.InputTokens, int64Value(usage, "input_tokens"))
			metrics.OutputTokens = maxInt64(metrics.OutputTokens, int64Value(usage, "output_tokens"))
			metrics.CacheCreationInputTokens = maxInt64(metrics.CacheCreationInputTokens, int64Value(usage, "cache_creation_input_tokens"))
			metrics.CacheReadInputTokens = maxInt64(metrics.CacheReadInputTokens, int64Value(usage, "cache_read_input_tokens"))
			metrics.TotalTokens = maxInt64(metrics.TotalTokens, int64Value(usage, "total_tokens"))
		}
	}
}

// isEscalationExitState reports whether state is one of the story engine's
// reserved "__exit__*" escalation leaves (e.g. "__exit__needs-human"). A run
// that lands there abandoned the task and asked for human intervention; it
// must never score as a completed/solved run unless the case explicitly
// expects that exact exit state.
func isEscalationExitState(state string) bool {
	return strings.HasPrefix(state, "__exit__")
}

// scoreFailures returns (correctnessFailures, budgetFailures) separately so
// the caller can derive an Outcome/BudgetCompliance pair instead of
// collapsing both axes into one bool. Correctness failures are gates about
// WHAT happened (submit attribution, final state, required artifact,
// forbidden tools, dangling in-flight calls); budget failures are gates about
// HOW MUCH it cost (wall time, tool calls, tokens, thinking events, dollars).
func scoreFailures(c Case, m Metrics, tracePath string, runStart time.Time) (correctness, budget []string) {
	addMaxFloat := func(label string, actual, max float64) {
		if max > 0 && actual > max {
			budget = append(budget, fmt.Sprintf("%s %.3f exceeds max %.3f", label, actual, max))
		}
	}
	addMaxInt := func(label string, actual, max int) {
		if max > 0 && actual > max {
			budget = append(budget, fmt.Sprintf("%s %d exceeds max %d", label, actual, max))
		}
	}
	addMaxInt64 := func(label string, actual, max int64) {
		if max > 0 && actual > max {
			budget = append(budget, fmt.Sprintf("%s %d exceeds max %d", label, actual, max))
		}
	}
	addMaxFloat("wall_seconds", m.WallSeconds, c.Budgets.MaxWallSeconds)
	addMaxInt("tool_calls_total", m.ToolCallsTotal, c.Budgets.MaxToolCalls)
	addMaxInt("read_calls", m.ReadCalls, c.Budgets.MaxReadCalls)
	addMaxInt("files_read", len(m.FilesRead), c.Budgets.MaxFilesRead)
	addMaxInt("thinking_events", m.ThinkingEvents, c.Budgets.MaxThinkingEvents)
	addMaxInt64("input_tokens", m.InputTokens, c.Budgets.MaxInputTokens)
	addMaxInt64("output_tokens", m.OutputTokens, c.Budgets.MaxOutputTokens)
	addMaxInt64("total_tokens", m.TotalTokens, c.Budgets.MaxTotalTokens)
	addMaxFloat("cost_usd", m.CostUSD, c.Budgets.MaxCostUSD)
	for _, tool := range sortedKeys(m.ForbiddenToolCalls) {
		correctness = append(correctness, fmt.Sprintf("forbidden tool %q called %d time(s)", tool, m.ForbiddenToolCalls[tool]))
	}
	if m.AgentCallsInFlight > 0 {
		correctness = append(correctness, fmt.Sprintf("agent_calls_in_flight %d: trace has start event(s) without returned/error terminal event", m.AgentCallsInFlight))
	}
	if m.RuntimeAccountingStatus == "partial" {
		correctness = append(correctness, "runtime receipt lifecycle is incomplete or mismatched")
	}
	if c.Expectations.RequireSubmit {
		switch {
		case c.Expectations.RequireSubmitVerb != "":
			if !m.SubmittedByVerb[c.Expectations.RequireSubmitVerb] {
				correctness = append(correctness, fmt.Sprintf("required submit was not observed on verb %q", c.Expectations.RequireSubmitVerb))
			}
		default:
			if !m.MakerSubmitted {
				if m.SubmittedByVerb["decide"] {
					correctness = append(correctness, "required submit was not observed on a maker/decomposer call (only the reviewer's \"decide\" submit was observed, which does not satisfy a maker completion gate)")
				} else {
					correctness = append(correctness, "required submit was not observed")
				}
			}
		}
	}
	if c.Expectations.FinalState != "" {
		if m.FinalState != c.Expectations.FinalState {
			correctness = append(correctness, fmt.Sprintf("final_state %q does not match expected %q", m.FinalState, c.Expectations.FinalState))
		}
	} else if isEscalationExitState(m.FinalState) {
		// No explicit final_state expectation was declared, but the run's
		// authoritative terminal state is a reserved escalation exit — this
		// is never a passing outcome regardless of what else was observed
		// (e.g. an earlier reviewer submit). See the V3 gpt-oss false-pass:
		// __exit__needs-human printed PASS because no case declared
		// final_state and RequireSubmit was satisfied by the reviewer's
		// revise submit.
		correctness = append(correctness, fmt.Sprintf("final_state %q is a reserved escalation exit (needs-human/error); the run did not complete", m.FinalState))
	}
	if c.Expectations.RequireArtifact != "" {
		path := c.Expectations.RequireArtifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(tracePath), path)
		}
		if info, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				correctness = append(correctness, fmt.Sprintf("required artifact %q does not exist", c.Expectations.RequireArtifact))
			} else {
				correctness = append(correctness, fmt.Sprintf("required artifact %q cannot be checked: %v", c.Expectations.RequireArtifact, err))
			}
		} else if !runStart.IsZero() && info.ModTime().Before(runStart) {
			// Stale-artifact rejection: a file that already existed (and was
			// not rewritten) before this trace's first event cannot be this
			// attempt's own output — it is a leftover from a prior attempt
			// that a cleaned-up run would not have produced. See the F2/stale
			// artifact class in the adversarial review's no-LLM regression
			// list.
			correctness = append(correctness, fmt.Sprintf("required artifact %q predates this run (mtime %s before trace start %s) — looks like a stale artifact from a prior attempt", c.Expectations.RequireArtifact, info.ModTime().UTC().Format(time.RFC3339), runStart.UTC().Format(time.RFC3339)))
		}
	}
	return correctness, budget
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func int64Value(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case json.Number:
			n, _ := v.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	return 0
}

func floatValue(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			n, _ := v.Float64()
			return n
		case string:
			n, _ := strconv.ParseFloat(v, 64)
			return n
		}
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
