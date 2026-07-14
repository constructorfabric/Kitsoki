package workerserver

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/capsule/executor"
)

const (
	maxProjectedTraceBytes = int64(32 << 20)
	maxProjectedLineBytes  = 1 << 20
	maxAgentBreadcrumbs    = 32
)

func (s *Server) projectRun(record RunRecord) RunRecord {
	projection := ProjectAgentTrace(filepath.Join(s.runDir(record.ExecutionID), "story-trace.jsonl"), s.cfg.Now())
	record.Agent = &projection
	record.Cleanup = s.latestCleanupDiagnostics()
	return record
}

func (s *Server) latestCleanupDiagnostics() *executor.WorkerCleanupDiagnostics {
	path := filepath.Join(s.cfg.Root, "cleanup", "latest.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1<<20 {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var summary CleanupSummary
	if json.Unmarshal(raw, &summary) != nil || summary.Schema != CleanupSummarySchema || summary.CompletedAt.IsZero() || !cleanupOutcome(summary.Outcome) || summary.Runs.Removed < 0 || summary.Sources.Removed < 0 || summary.ReclaimedBytes < 0 {
		return nil
	}
	return &executor.WorkerCleanupDiagnostics{
		Schema:               executor.WorkerCleanupDiagnosticsSchema,
		Outcome:              summary.Outcome,
		CompletedAt:          summary.CompletedAt.UTC(),
		RunsRemoved:          summary.Runs.Removed,
		SourcesRemoved:       summary.Sources.Removed,
		ReclaimedBytes:       summary.ReclaimedBytes,
		SourceCleanupBlocked: summary.SourceCleanupBlocked,
	}
}

func cleanupOutcome(value string) bool {
	return value == "planned" || value == "completed" || value == "partial" || value == "cancelled" || value == "failed"
}

// ProjectAgentTrace turns the local, content-rich story trace into a compact
// provider-safe status object. Payload content is never copied wholesale; each
// supported event has a strict field allowlist.
func ProjectAgentTrace(path string, observedAt time.Time) executor.AgentDiagnostics {
	out := executor.AgentDiagnostics{Schema: executor.AgentDiagnosticsSchema, TraceState: "missing", ObservedAt: observedAt.UTC()}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return out
	}
	if err != nil {
		out.TraceState = "unreadable"
		return out
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		out.TraceState = "unsafe"
		return out
	}
	file, err := os.Open(path)
	if err != nil {
		out.TraceState = "unreadable"
		return out
	}
	defer file.Close()

	start := int64(0)
	if info.Size() > maxProjectedTraceBytes {
		start = info.Size() - maxProjectedTraceBytes
		out.Truncated = true
		out.TraceState = "partial"
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		out.TraceState = "unreadable"
		return out
	}
	reader := bufio.NewReaderSize(io.LimitReader(file, info.Size()-start), 64*1024)
	if start > 0 {
		_, _, _ = readBoundedTraceLine(reader, maxProjectedLineBytes)
	}
	active := map[string]executor.AgentCallStatus{}
	for {
		line, oversized, readErr := readBoundedTraceLine(reader, maxProjectedLineBytes)
		if oversized {
			out.InvalidLines++
			out.TraceState = "partial"
		}
		if len(line) > 0 && !oversized {
			projectTraceLine(line, &out, active)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			out.TraceState = "partial"
			break
		}
	}
	if out.TraceState == "missing" {
		out.TraceState = "ok"
	}
	for _, call := range active {
		out.ActiveCalls = append(out.ActiveCalls, call)
	}
	sort.Slice(out.ActiveCalls, func(i, j int) bool {
		if !out.ActiveCalls[i].StartedAt.Equal(out.ActiveCalls[j].StartedAt) {
			return out.ActiveCalls[i].StartedAt.Before(out.ActiveCalls[j].StartedAt)
		}
		return out.ActiveCalls[i].CallRef < out.ActiveCalls[j].CallRef
	})
	if len(out.ActiveCalls) > 0 {
		switch out.ActiveCalls[len(out.ActiveCalls)-1].Phase {
		case "process_no_output":
			out.StallHint = "process_no_output"
		case "process_started":
			out.StallHint = "process_running"
		case "provider_active":
			out.StallHint = "provider_active"
		case "process_finished":
			out.StallHint = "awaiting_completion"
		default:
			out.StallHint = "awaiting_process"
		}
	} else if out.PendingHostCalls > 0 {
		out.StallHint = "host_call_running"
	}
	if err := executor.ValidateAgentDiagnostics(out); err != nil {
		return executor.AgentDiagnostics{Schema: executor.AgentDiagnosticsSchema, TraceState: "unsafe", ObservedAt: observedAt.UTC()}
	}
	return out
}

func projectTraceLine(line []byte, out *executor.AgentDiagnostics, active map[string]executor.AgentCallStatus) {
	var event struct {
		Ts      time.Time       `json:"ts"`
		Kind    string          `json:"kind"`
		CallID  string          `json:"call_id"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &event) != nil {
		// The header is intentionally not an event. A partial final append is
		// likewise ignored while the last complete lifecycle row stays usable.
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(line, &header) != nil || header.Kind != "session.header" {
			out.InvalidLines++
			out.TraceState = "partial"
		}
		return
	}
	if event.Kind == "session.header" {
		return
	}
	if event.Ts.IsZero() {
		out.InvalidLines++
		out.TraceState = "partial"
		return
	}
	callRef := diagnosticRef("call", event.CallID)
	switch event.Kind {
	case "harness.dispatched", "harness.returned":
		var payload struct {
			Namespace string `json:"namespace"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || !strings.HasPrefix(payload.Namespace, "host.agent.") {
			return
		}
		kind := "host_dispatched"
		if event.Kind == "harness.returned" {
			kind = "host_returned"
			if out.PendingHostCalls > 0 {
				out.PendingHostCalls--
			}
		} else {
			out.PendingHostCalls++
		}
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: kind, Verb: safeAgentVerb(strings.TrimPrefix(payload.Namespace, "host.agent."))})
	case "agent.call.start":
		if callRef == "" {
			return
		}
		var payload struct {
			Verb    string `json:"verb"`
			Backend string `json:"backend"`
			Model   string `json:"model"`
			Profile string `json:"profile"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		call := executor.AgentCallStatus{CallRef: callRef, Verb: safeAgentVerb(payload.Verb), Backend: safeBackend(payload.Backend), ModelRef: diagnosticRef("model", payload.Model), ProfileRef: diagnosticRef("profile", payload.Profile), Phase: "call_started", StartedAt: event.Ts.UTC(), LastActivityAt: event.Ts.UTC()}
		active[callRef] = call
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: "call_started", CallRef: callRef, Verb: call.Verb, Backend: call.Backend, Outcome: "running"})
	case "agent.call.complete", "agent.call.error":
		if callRef == "" {
			return
		}
		var payload struct {
			Verb       string `json:"verb"`
			DurationMS int64  `json:"duration_ms"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		kind, outcome := "call_completed", "passed"
		if event.Kind == "agent.call.error" {
			kind, outcome = "call_failed", "failed"
		}
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: kind, CallRef: callRef, Verb: safeAgentVerb(payload.Verb), Outcome: outcome, DurationMS: nonnegative(payload.DurationMS)})
		delete(active, callRef)
	case "agent.stream":
		if callRef == "" {
			return
		}
		var payload struct {
			Type          string `json:"type"`
			Subtype       string `json:"subtype"`
			Severity      string `json:"severity"`
			Backend       string `json:"backend"`
			DurationMS    int64  `json:"duration_ms"`
			ExitCode      int    `json:"exit_code"`
			RawEventCount int    `json:"raw_event_count"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			return
		}
		call := active[callRef]
		if call.CallRef == "" {
			call = executor.AgentCallStatus{CallRef: callRef, Phase: "call_started", StartedAt: event.Ts.UTC()}
		}
		call.LastActivityAt = event.Ts.UTC()
		if backend := safeBackend(payload.Backend); backend != "" {
			call.Backend = backend
		}
		if payload.Type == "agent.process" {
			kind, phase := processProjection(payload.Subtype)
			if kind == "" {
				return
			}
			call.Phase = phase
			active[callRef] = call
			breadcrumb := executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: kind, CallRef: callRef, Backend: call.Backend, Severity: safeSeverity(payload.Severity), DurationMS: nonnegative(payload.DurationMS), RawEventCount: maxInt(payload.RawEventCount, 0)}
			if kind == "process_finished" {
				exit := payload.ExitCode
				breadcrumb.ExitCode = &exit
				if payload.Severity == "error" || exit != 0 {
					breadcrumb.Outcome = "failed"
				} else {
					breadcrumb.Outcome = "passed"
				}
			}
			appendAgentBreadcrumb(out, breadcrumb)
			return
		}
		call.Phase = "provider_active"
		active[callRef] = call
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: "provider_activity", CallRef: callRef, Backend: call.Backend, Outcome: "running"})
	case "agent.dispatch.budget_checked":
		var payload struct {
			Verb            string `json:"verb"`
			EstimatedTokens int64  `json:"estimated_tokens"`
			Decision        string `json:"decision"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: "budget_checked", CallRef: callRef, Verb: safeAgentVerb(payload.Verb), Outcome: safeBudgetOutcome(payload.Decision), EstimatedTokens: nonnegative(payload.EstimatedTokens)})
	case "agent.launch.policy":
		var payload struct {
			Allowed bool `json:"allowed"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		outcome := "denied"
		if payload.Allowed {
			outcome = "allowed"
		}
		appendAgentBreadcrumb(out, executor.AgentBreadcrumb{At: event.Ts.UTC(), Kind: "launch_policy", CallRef: callRef, Outcome: outcome})
	}
}

func appendAgentBreadcrumb(out *executor.AgentDiagnostics, breadcrumb executor.AgentBreadcrumb) {
	if breadcrumb.At.After(out.LastActivityAt) {
		out.LastActivityAt = breadcrumb.At
	}
	out.Breadcrumbs = append(out.Breadcrumbs, breadcrumb)
	if len(out.Breadcrumbs) > maxAgentBreadcrumbs {
		out.Breadcrumbs = append([]executor.AgentBreadcrumb(nil), out.Breadcrumbs[len(out.Breadcrumbs)-maxAgentBreadcrumbs:]...)
		out.Truncated = true
	}
}

func readBoundedTraceLine(reader *bufio.Reader, maximum int) ([]byte, bool, error) {
	var line []byte
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if !oversized {
			if len(line)+len(fragment) > maximum {
				line = nil
				oversized = true
			} else {
				line = append(line, fragment...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytesTrimLine(line), oversized, err
	}
}

func bytesTrimLine(value []byte) []byte {
	value = []byte(strings.TrimSuffix(string(value), "\n"))
	value = []byte(strings.TrimSuffix(string(value), "\r"))
	return value
}

func diagnosticRef(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return "sha256:" + hex.EncodeToString(sum[:12])
}

func safeAgentVerb(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "ask", "extract", "decide", "task", "converse", "codeact", "ask_with_mcp", "ask_structured":
		return value
	case "":
		return ""
	default:
		return "other"
	}
}

func safeBackend(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range []string{"claude", "codex", "agy", "copilot", "local", "inprocess", "cassette"} {
		if lower == candidate || lower == "agent."+candidate || strings.HasPrefix(lower, candidate+"-") {
			return candidate
		}
	}
	if lower != "" {
		return "other"
	}
	return ""
}

func processProjection(subtype string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(subtype)) {
	case "start":
		return "process_started", "process_started"
	case "no_output":
		return "process_no_output", "process_no_output"
	case "finish":
		return "process_finished", "process_finished"
	default:
		return "", ""
	}
}

func safeSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "info", "warn", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func safeBudgetOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "proceed", "escalate", "refuse":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}

func nonnegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
