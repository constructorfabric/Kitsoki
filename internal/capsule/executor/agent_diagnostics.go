package executor

import (
	"fmt"
	"strings"
	"time"
)

const AgentDiagnosticsSchema = "capsule-agent-diagnostics/v1"
const WorkerCleanupDiagnosticsSchema = "capsule-worker-cleanup-status/v1"

// AgentDiagnostics is a provider-safe projection of the worker-local story
// trace. It intentionally contains no prompt/response text, filesystem paths,
// command lines, environment values, provider errors, or raw identifiers.
type AgentDiagnostics struct {
	Schema           string            `json:"schema"`
	TraceState       string            `json:"trace_state"`
	ObservedAt       time.Time         `json:"observed_at"`
	LastActivityAt   time.Time         `json:"last_activity_at,omitempty"`
	StallHint        string            `json:"stall_hint,omitempty"`
	PendingHostCalls int               `json:"pending_host_calls,omitempty"`
	ActiveCalls      []AgentCallStatus `json:"active_calls,omitempty"`
	Breadcrumbs      []AgentBreadcrumb `json:"breadcrumbs,omitempty"`
	Truncated        bool              `json:"truncated,omitempty"`
	InvalidLines     int               `json:"invalid_lines,omitempty"`
}

type AgentCallStatus struct {
	CallRef        string    `json:"call_ref"`
	Verb           string    `json:"verb,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	ModelRef       string    `json:"model_ref,omitempty"`
	ProfileRef     string    `json:"profile_ref,omitempty"`
	Phase          string    `json:"phase"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
}

type AgentBreadcrumb struct {
	At              time.Time `json:"at"`
	Kind            string    `json:"kind"`
	CallRef         string    `json:"call_ref,omitempty"`
	Verb            string    `json:"verb,omitempty"`
	Backend         string    `json:"backend,omitempty"`
	Outcome         string    `json:"outcome,omitempty"`
	Severity        string    `json:"severity,omitempty"`
	DurationMS      int64     `json:"duration_ms,omitempty"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	RawEventCount   int       `json:"raw_event_count,omitempty"`
	EstimatedTokens int64     `json:"estimated_tokens,omitempty"`
}

// WorkerCleanupDiagnostics is the path-free subset of the worker's durable
// cleanup summary returned alongside execution status.
type WorkerCleanupDiagnostics struct {
	Schema               string    `json:"schema"`
	Outcome              string    `json:"outcome"`
	CompletedAt          time.Time `json:"completed_at"`
	RunsRemoved          int       `json:"runs_removed"`
	SourcesRemoved       int       `json:"sources_removed"`
	ReclaimedBytes       int64     `json:"reclaimed_bytes"`
	SourceCleanupBlocked bool      `json:"source_cleanup_blocked,omitempty"`
}

func ValidateAgentDiagnostics(d AgentDiagnostics) error {
	if d.Schema != AgentDiagnosticsSchema {
		return fmt.Errorf("capsule executor: invalid agent diagnostics schema")
	}
	if !oneOf(d.TraceState, "missing", "ok", "partial", "unreadable", "unsafe") {
		return fmt.Errorf("capsule executor: invalid agent trace state")
	}
	if d.StallHint != "" && !oneOf(d.StallHint, "host_call_running", "awaiting_process", "process_running", "process_no_output", "provider_active", "awaiting_completion") {
		return fmt.Errorf("capsule executor: invalid agent stall hint")
	}
	for _, call := range d.ActiveCalls {
		if !safeDiagnosticRef(call.CallRef) || !oneOf(call.Phase, "call_started", "process_started", "process_no_output", "process_finished", "provider_active") {
			return fmt.Errorf("capsule executor: invalid active agent call")
		}
		if !oneOf(call.Verb, "", "ask", "extract", "decide", "task", "converse", "codeact", "ask_with_mcp", "ask_structured", "other") || !oneOf(call.Backend, "", "claude", "codex", "agy", "copilot", "local", "inprocess", "cassette", "other") {
			return fmt.Errorf("capsule executor: invalid active agent call classification")
		}
		if call.ModelRef != "" && !safeDiagnosticRef(call.ModelRef) {
			return fmt.Errorf("capsule executor: invalid agent model reference")
		}
		if call.ProfileRef != "" && !safeDiagnosticRef(call.ProfileRef) {
			return fmt.Errorf("capsule executor: invalid agent profile reference")
		}
	}
	for _, breadcrumb := range d.Breadcrumbs {
		if !oneOf(breadcrumb.Kind, "host_dispatched", "host_returned", "call_started", "call_completed", "call_failed", "process_started", "process_no_output", "process_finished", "provider_activity", "budget_checked", "launch_policy") {
			return fmt.Errorf("capsule executor: invalid agent breadcrumb kind")
		}
		if breadcrumb.CallRef != "" && !safeDiagnosticRef(breadcrumb.CallRef) {
			return fmt.Errorf("capsule executor: invalid agent breadcrumb reference")
		}
		if !oneOf(breadcrumb.Verb, "", "ask", "extract", "decide", "task", "converse", "codeact", "ask_with_mcp", "ask_structured", "other") || !oneOf(breadcrumb.Backend, "", "claude", "codex", "agy", "copilot", "local", "inprocess", "cassette", "other") {
			return fmt.Errorf("capsule executor: invalid agent breadcrumb classification")
		}
		if !oneOf(breadcrumb.Outcome, "", "running", "passed", "failed", "allowed", "denied", "proceed", "escalate", "refuse", "unknown") || !oneOf(breadcrumb.Severity, "", "info", "warn", "error") {
			return fmt.Errorf("capsule executor: invalid agent breadcrumb outcome")
		}
		if breadcrumb.DurationMS < 0 || breadcrumb.RawEventCount < 0 || breadcrumb.EstimatedTokens < 0 {
			return fmt.Errorf("capsule executor: invalid agent breadcrumb counters")
		}
	}
	return nil
}

func ValidateWorkerCleanupDiagnostics(d WorkerCleanupDiagnostics) error {
	if d.Schema != WorkerCleanupDiagnosticsSchema || d.CompletedAt.IsZero() || d.RunsRemoved < 0 || d.SourcesRemoved < 0 || d.ReclaimedBytes < 0 {
		return fmt.Errorf("capsule executor: invalid worker cleanup diagnostics")
	}
	if !oneOf(d.Outcome, "planned", "completed", "partial", "cancelled", "failed") {
		return fmt.Errorf("capsule executor: invalid worker cleanup outcome")
	}
	return nil
}

func safeDiagnosticRef(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+24 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
