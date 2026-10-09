// Package host — copilot JSONL event classifier.
//
// `copilot --output-format json` emits one JSON object per line. classifyCopilotEvent
// distills each into the backend-neutral classifiedEvent the runner's scan loop
// consumes, so emitClassified / the reply+usage assembly / the transcript tee
// all work unchanged across backends.
//
// Event vocabulary (every event also carries id/parentId/timestamp/type):
//   - session.* (mcp_servers_loaded, skills_loaded, tools_updated, …): setup noise.
//   - user.message {data.content}: the prompt echo.
//   - assistant.turn_start/turn_end, assistant.message_start: lifecycle markers.
//   - assistant.message_delta {data.deltaContent}: streaming text deltas (ephemeral).
//   - assistant.reasoning / assistant.reasoning_delta: thinking.
//   - assistant.message {data.content, data.toolRequests[], data.outputTokens}:
//     a complete assistant message; the final reply is the last one with non-empty
//     content (turnId increments across tool rounds).
//   - tool.execution_start {data.toolName, data.arguments}: a tool invocation.
//   - tool.execution_complete {data.success, data.result.content}: its result.
//   - result {data.sessionId, data.usage:{premiumRequests,totalApiDurationMs,
//     sessionDurationMs,…}}: the terminal event. No token totals or dollar cost.
package host

// classifyCopilotEvent maps one parsed copilot JSONL event into classifiedEvent.
// Best-effort and defensive: unrecognized shapes return a value carrying only
// the type so a future copilot release adding an event kind still appears in
// the trace.
func classifyCopilotEvent(ev map[string]any) classifiedEvent {
	evType, _ := ev["type"].(string)
	data, _ := ev["data"].(map[string]any)
	ce := classifiedEvent{Type: evType}

	switch evType {
	case "assistant.message":
		if c, _ := data["content"].(string); c != "" {
			ce.Text = c
		}
		if t, _ := data["reasoningText"].(string); t != "" {
			ce.Thinking = t
		}
		if ot, ok := data["outputTokens"].(float64); ok {
			ce.OutputTokens = int(ot)
		}
		tools := copilotToolRequests(data)
		ce.Tools = tools
		if len(tools) > 0 {
			ce.Tool = tools[0].Name
			ce.ToolArgs = tools[0].Preview
		}

	case "tool.execution_start":
		name, _ := data["toolName"].(string)
		if name != "" {
			args, _ := data["arguments"].(map[string]any)
			preview := onelinePreview(toolUseArgsPreview(name, args), 120)
			ce.Tool = name
			ce.ToolArgs = preview
			ce.Tools = []StreamToolUse{{Name: name, Preview: preview}}
		}

	case "assistant.reasoning":
		// Surface reasoning prose for the trace when present.
		if t, _ := data["text"].(string); t != "" {
			ce.Thinking = t
		} else if c, _ := data["content"].(string); c != "" {
			ce.Thinking = c
		}

	case "result":
		// The terminal result event carries sessionId/usage at the TOP level
		// (unlike assistant.*/tool.*/session.* events, which wrap payload in
		// "data"); fall back to data for resilience to a future shape change.
		ce.IsResult = true
		if sid, _ := ev["sessionId"].(string); sid != "" {
			ce.SessionID = sid
		} else if sid, _ := data["sessionId"].(string); sid != "" {
			ce.SessionID = sid
		}
		u, _ := ev["usage"].(map[string]any)
		if u == nil {
			u, _ = data["usage"].(map[string]any)
		}
		if u != nil {
			ce.Usage = normalizeCopilotUsage(u)
		}
		// Copilot reports no per-call dollar cost; Cost stays 0.

	default:
		// session.*, user.message, lifecycle markers, deltas — type only.
		// (Deltas are intentionally not surfaced as Text to avoid double-
		// counting against the complete assistant.message that follows.)
	}
	return ce
}

// copilotToolRequests converts an assistant.message's data.toolRequests array
// into StreamToolUse entries with compact arg previews.
func copilotToolRequests(data map[string]any) []StreamToolUse {
	reqs, _ := data["toolRequests"].([]any)
	if len(reqs) == 0 {
		return nil
	}
	var out []StreamToolUse
	for _, r := range reqs {
		req, _ := r.(map[string]any)
		name, _ := req["name"].(string)
		if name == "" {
			continue
		}
		args, _ := req["arguments"].(map[string]any)
		out = append(out, StreamToolUse{
			Name:    name,
			Preview: onelinePreview(toolUseArgsPreview(name, args), 120),
		})
	}
	return out
}

// normalizeCopilotUsage maps copilot's result.usage object into a stable,
// snake_cased map recorded as the per-call usage (recordAgentUsage stores it
// verbatim). Copilot has no token counts at the result level, so this carries
// premium-request counts and durations. Absent fields are simply omitted.
func normalizeCopilotUsage(u map[string]any) map[string]any {
	out := map[string]any{}
	copyNum := func(src, dst string) {
		if v, ok := u[src]; ok {
			out[dst] = v
		}
	}
	copyNum("premiumRequests", "premium_requests")
	copyNum("totalApiDurationMs", "total_api_duration_ms")
	copyNum("sessionDurationMs", "session_duration_ms")
	return out
}
