// Package host — codex JSONL event classifier.
//
// `codex exec --json` emits one JSON object per line. The wire protocol has two
// layers: a small set of top-level event types and a richer set of item types
// nested inside item.started / item.completed events:
//
//   - top-level types: thread.started, turn.started, item.started,
//     item.completed, turn.completed (defensive: turn.failed / error).
//   - item types (in ev["item"]["type"]): agent_message, command_execution,
//     mcp_tool_call, reasoning.
//
// classifyCodexEvent distills each line into the backend-neutral classifiedEvent
// the runner's scan loop consumes, so emitClassified / the reply+usage assembly
// / the transcript tee all work unchanged across backends.
//
// Reply assembly: codex's final reply is the LAST agent_message item's text. To
// get "latest wins" semantics (rather than concatenating every agent_message),
// agent_message items are classified with Type "assistant.message", which the
// runner's reply assembler treats as latest-non-empty-wins (the same special
// case copilot relies on). The submitted JSON payload comes from the validator
// submit tool's output file, NOT the agent_message — same as claude/copilot.
package host

import "strings"

// classifyCodexEvent maps one parsed codex JSONL event into classifiedEvent.
// Best-effort and defensive: unrecognized shapes return a value carrying only
// the type so a future codex release adding an event kind still appears in the
// trace.
func classifyCodexEvent(ev map[string]any) classifiedEvent {
	evType, _ := ev["type"].(string)
	ce := classifiedEvent{Type: evType}

	switch evType {
	case "thread.started":
		// The session id used for `exec resume <id>`.
		if tid, _ := ev["thread_id"].(string); tid != "" {
			ce.SessionID = tid
		}

	case "item.started", "item.completed":
		item, _ := ev["item"].(map[string]any)
		classifyCodexItem(evType, item, &ce)

	case "turn.completed":
		// The terminal event. Usage carries token counts (no dollar cost).
		ce.IsResult = true
		if u, _ := ev["usage"].(map[string]any); u != nil {
			ce.Usage = normalizeCodexUsage(u)
		}
		// Codex reports no per-call dollar cost; Cost stays 0.

	default:
		// turn.started, turn.failed, error, and any future top-level type —
		// type only, so it still reaches the transcript.
	}
	return ce
}

// classifyCodexItem fills ce from a codex item object (the payload of
// item.started / item.completed). Switches on item["type"].
func classifyCodexItem(eventType string, item map[string]any, ce *classifiedEvent) {
	if item == nil {
		return
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "agent_message":
		// Surface as assistant.message so the runner's reply assembler keeps
		// the LATEST non-empty text as the final reply (codex's last
		// agent_message), rather than concatenating every one.
		ce.Type = "assistant.message"
		if t, _ := item["text"].(string); t != "" {
			ce.Text = t
		}

	case "reasoning":
		// Reasoning prose is backend-neutral assistant activity. Surface it
		// with the same type Claude uses so TUI/web/VS Code consumers do not
		// need provider-specific branches to render thinking. Keep it in
		// Thinking, not Text: reasoning is never the final reply.
		ce.Type = "assistant"
		ce.Thinking = codexReasoningText(item)

	case "command_execution":
		// A shell command run by codex. Preview the command line.
		cmd, _ := item["command"].(string)
		preview := onelinePreview(cmd, 120)
		if eventType == "item.started" {
			ce.Type = "assistant"
		}
		ce.Tool = "shell"
		ce.ToolArgs = preview
		ce.Tools = []StreamToolUse{{Name: "shell", Preview: preview}}

	case "mcp_tool_call":
		// An MCP tool invocation (e.g. the validator submit). Mirror copilot's
		// tool.execution_start: name + a compact preview of the arguments.
		name := codexMCPToolName(item)
		if name == "" {
			return
		}
		args, _ := item["arguments"].(map[string]any)
		preview := onelinePreview(toolUseArgsPreview(name, args), 120)
		if eventType == "item.started" {
			ce.Type = "assistant"
		}
		ce.Tool = name
		ce.ToolArgs = preview
		ce.Tools = []StreamToolUse{{Name: name, Preview: preview}}
		ce.ActionID, _ = item["id"].(string)
		if eventType == "item.started" {
			ce.ActionState = "started"
		} else {
			ce.ActionState = "completed"
		}

	default:
		// Unknown item type — leave ce carrying only the top-level type.
	}
}

func codexReasoningText(item map[string]any) string {
	if t, _ := item["text"].(string); t != "" {
		return t
	}
	if s, _ := item["summary"].(string); s != "" {
		return s
	}
	if parts, _ := item["summary"].([]any); len(parts) > 0 {
		var out []string
		for _, part := range parts {
			switch v := part.(type) {
			case string:
				if v != "" {
					out = append(out, v)
				}
			case map[string]any:
				if t, _ := v["text"].(string); t != "" {
					out = append(out, t)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// codexMCPToolName extracts the tool name from an mcp_tool_call item,
// tolerating the plausible field names codex may use (tool / name; optionally
// qualified by a server field).
func codexMCPToolName(item map[string]any) string {
	if t, _ := item["tool"].(string); t != "" {
		return t
	}
	if n, _ := item["name"].(string); n != "" {
		return n
	}
	return ""
}

// normalizeCodexUsage maps codex's turn.completed usage object into a stable,
// snake_cased map recorded as the per-call usage (recordAgentUsage stores it
// verbatim). Codex already reports snake_case token counts; absent fields are
// simply omitted. Cost stays 0 (codex reports no dollar cost).
func normalizeCodexUsage(u map[string]any) map[string]any {
	out := map[string]any{}
	copyNum := func(key string) {
		if v, ok := u[key]; ok {
			out[key] = v
		}
	}
	copyNum("input_tokens")
	copyNum("cached_input_tokens")
	copyNum("output_tokens")
	copyNum("reasoning_output_tokens")
	return out
}
