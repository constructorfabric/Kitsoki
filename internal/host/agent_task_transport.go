// Package host — event-emitting MCP transport shim for host.agent.task.
//
// The task handler needs to observe every tool call the agent makes so each
// Edit/Write/Bash/sub-agent call produces a task.tool journal event. This
// file implements the observation machinery without a real MCP transport:
// instead it wraps the ClaudeRunner seam and post-processes the stream-json
// output to extract tool-call events.
//
// Stream vs journal events (D17):
//
//	Stream: task.tool.start and task.tool.end are sent to the StreamSink as
//	        events happen, so the TUI sees live progress.
//	Journal: a single rolled-up "task.tool" event is written per tool call.
//
// Built-in sub-agent MCP tools:
//
//	The task agent gets three built-in tools scoped to the parent session:
//	kitsoki.agent.extract, kitsoki.agent.decide, kitsoki.agent.ask.
//	Their invocations produce child agent spans under the parent trace.
//	The implementation here is a hook point; full sub-agent routing lands
//	in Phase 5/6 when the agent CLI surface exists.
//
// KITSOKI_SESSION_ID propagation:
//
//	Every subprocess spawned by the agent (Bash tool, post_cmd acceptance
//	subprocess) inherits KITSOKI_SESSION_ID from the parent so child
//	kitsoki agent calls join the parent trace. The env var is injected by
//	the task handler before spawning; this file documents the contract.
package host

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

// taskToolEvent is the rolled-up journal payload for a task.tool event.
type taskToolEvent struct {
	Tool          string `json:"tool"`
	InputPreview  string `json:"input_preview,omitempty"`
	OutputPreview string `json:"output_preview,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	ParentTraceID string `json:"parent_trace_id,omitempty"`
	Seq           int    `json:"seq"`
}

// observeTaskToolCalls reads cr.RawEvents (populated by runClaudeStreamJSON)
// and emits a task.tool journal event for each tool_use block, plus a
// task.tool.end event for each corresponding tool_result block.
//
// Reads RawEvents directly rather than re-parsing Stdout so that the full
// stream — including tool_use and tool_result blocks that runClaudeStreamJSON
// strips from Stdout — is visible. When RawEvents is nil (buffered-text path
// or a stub that emits plain text) the function returns without error.
//
// Per D17: stream events (task.tool.start / task.tool.end) fire as they are
// seen in the event list; journal events (task.tool) are the rolled-up record.
func observeTaskToolCalls(ctx context.Context, cr ClaudeRun, parentTraceID string) []taskToolEvent {
	var events []taskToolEvent
	seq := 0

	// toolsByID tracks active tool_use blocks by id so we can match
	// the corresponding tool_result for emitTaskToolEnd.
	type pendingTool struct {
		name    string
		seq     int
		traceID string
	}
	pendingByID := map[string]pendingTool{}

	for _, raw := range cr.RawEvents {
		var ev map[string]any
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		evType, _ := ev["type"].(string)
		msg, _ := ev["message"].(map[string]any)
		content, _ := msg["content"].([]any)

		switch evType {
		case "assistant":
			for _, c := range content {
				block, _ := c.(map[string]any)
				btype, _ := block["type"].(string)
				if btype != "tool_use" {
					continue
				}
				toolName, _ := block["name"].(string)
				toolID, _ := block["id"].(string)
				input, _ := block["input"].(map[string]any)
				inputPreview := toolUseArgsPreview(toolName, input)
				seq++
				traceID := newUUID()

				te := taskToolEvent{
					Tool:          toolName,
					InputPreview:  onelinePreview(inputPreview, 200),
					ParentTraceID: parentTraceID,
					TraceID:       traceID,
					Seq:           seq,
				}
				events = append(events, te)

				if toolID != "" {
					pendingByID[toolID] = pendingTool{name: toolName, seq: seq, traceID: traceID}
				}

				if sink := StreamSinkFrom(ctx); sink != nil {
					sink.OnStreamEvent(ctx, StreamEvent{
						Type:    "task.tool.start",
						Tool:    toolName,
						Preview: te.InputPreview,
					})
				}
				slog.InfoContext(ctx, "task.tool",
					"tool", toolName,
					"preview", te.InputPreview,
					"trace_id", traceID,
					"parent_trace_id", parentTraceID,
					"seq", seq,
				)
			}

		case "user":
			for _, c := range content {
				block, _ := c.(map[string]any)
				btype, _ := block["type"].(string)
				if btype != "tool_result" {
					continue
				}
				toolUseID, _ := block["tool_use_id"].(string)
				// Extract raw content text (may be string or []blocks), apply
				// the 256 KiB cap so large Read/Grep/Glob results don't bloat
				// the journal. The stream preview is then derived from the
				// (already capped) text.
				var rawText string
				switch v := block["content"].(type) {
				case string:
					rawText = v
				case []any:
					for _, sub := range v {
						sb, _ := sub.(map[string]any)
						if t, _ := sb["text"].(string); t != "" {
							rawText = t
							break
						}
					}
				}
				capped := capReadToolOutput(rawText)
				outputPreview := onelinePreview(capped, 200)
				if pt, ok := pendingByID[toolUseID]; ok {
					// Backfill OutputPreview on the journal-side taskToolEvent
					// so a single rolled-up record carries both directions of
					// the tool call. events is ordered by emission; pt.seq is
					// the 1-based seq, so the index is seq - 1.
					if idx := pt.seq - 1; idx >= 0 && idx < len(events) {
						events[idx].OutputPreview = outputPreview
					}
					emitTaskToolEnd(ctx, pt.name, outputPreview, pt.seq)
					delete(pendingByID, toolUseID)
				}
			}
		}
	}
	return events
}

// emitTaskToolEnd emits a task.tool.end StreamSink event for a completed tool call.
// Called when the full tool round-trip is visible (tool_result event observed).
func emitTaskToolEnd(ctx context.Context, toolName, outputPreview string, seq int) {
	if sink := StreamSinkFrom(ctx); sink != nil {
		sink.OnStreamEvent(ctx, StreamEvent{
			Type:    "task.tool.end",
			Subtype: "",
			Tool:    toolName,
			Preview: outputPreview,
		})
	}
	slog.InfoContext(ctx, "task.tool.end",
		"tool", toolName,
		"output_preview", outputPreview,
		"seq", seq,
	)
}

// emitAcceptanceAttempt records one pass of the acceptance loop to the trace.
// The journal event (task.acceptance.attempt) is emitted by the task handler
// directly using slog; this helper surfaces it on the StreamSink too.
func emitAcceptanceAttempt(ctx context.Context, attempt, exitCode int, stdoutPreview, rejectedReason string) {
	if sink := StreamSinkFrom(ctx); sink != nil {
		preview := rejectedReason
		if preview == "" {
			preview = stdoutPreview
		}
		sink.OnStreamEvent(ctx, StreamEvent{
			Type:    "task.acceptance.attempt",
			Preview: onelinePreview(preview, 200),
		})
	}
	slog.InfoContext(ctx, "task.acceptance.attempt",
		"attempt", attempt,
		"exit_code", exitCode,
		"rejected_reason", rejectedReason,
	)
}

// emitTaskEnd records the terminal task.end event to the trace and StreamSink.
func emitTaskEnd(ctx context.Context, outcome string, filesChanged []string, replayMode ReplayMode) {
	if sink := StreamSinkFrom(ctx); sink != nil {
		sink.OnStreamEvent(ctx, StreamEvent{
			Type:    "task.end",
			Subtype: outcome,
			Preview: strings.Join(filesChanged, ", "),
		})
	}
	slog.InfoContext(ctx, "task.end",
		"outcome", outcome,
		"files_changed", filesChanged,
		"replay_mode", string(replayMode),
	)
}
