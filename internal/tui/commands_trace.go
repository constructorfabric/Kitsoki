package tui

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/tui/blocks"
)

// commands_trace.go — single-pane-tui §"/trace": print the last turn's
// routing trace as a chat block. The Ctrl+R full-screen overlay is
// being removed; the same data lands inline now so scrollback carries
// every prior turn's trace.

// renderTraceBlock builds the routing-trace transcript block for the
// most recent turn (or a specific turn if --turn is supplied later).
// Returns "" when there's no observer or no recorded events — caller
// is responsible for either suppressing or substituting a friendly
// "no trace" line.
func renderTraceBlock(m RootModel) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if m.routingObserver == nil {
		return r.SlashOutput("(trace: no routing observer wired — running headless)")
	}
	turn := m.routingObserver.LatestTurn()
	tr := m.routingObserver.Trace(turn)
	if tr == nil || len(tr.Events) == 0 {
		return r.SlashOutput(fmt.Sprintf("(trace: no routing events captured for turn %d yet)", turn))
	}
	events := traceEventsToBlocks(tr.Events)
	var sb strings.Builder
	sb.WriteString(r.SlashOutput(fmt.Sprintf("trace · turn %d", turn)))
	sb.WriteString("\n")
	sb.WriteString(r.RoutingTrace(events))
	return sb.String()
}

// traceEventsToBlocks maps the observer's chronological event list
// into blocks.TraceEvent rows. Best-effort attr extraction — the
// observer's attrs map is loosely typed so we coerce defensively.
func traceEventsToBlocks(events []RoutingTraceEvent) []blocks.TraceEvent {
	out := make([]blocks.TraceEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, blocks.TraceEvent{
			Tier:   tierFromMsg(ev.Msg),
			Result: resultFromMsg(ev.Msg),
			Detail: detailFromAttrs(ev.Attrs),
		})
	}
	return out
}

// tierFromMsg extracts the tier label from a routing slog message
// like "turn.deterministic_hit" → "deterministic".
func tierFromMsg(msg string) string {
	const prefix = "turn."
	if !strings.HasPrefix(msg, prefix) {
		return msg
	}
	rest := msg[len(prefix):]
	if i := strings.IndexByte(rest, '_'); i > 0 {
		return rest[:i]
	}
	return rest
}

// resultFromMsg picks "hit", "miss", "ambiguous", "cancelled" out of
// the slog message suffix.
func resultFromMsg(msg string) string {
	switch {
	case strings.HasSuffix(msg, "_hit") || strings.HasSuffix(msg, "_routed"):
		return "hit"
	case strings.HasSuffix(msg, "_miss"):
		return "miss"
	case strings.HasSuffix(msg, "_ambiguous"):
		return "ambiguous"
	case strings.HasSuffix(msg, "_cancelled"):
		return "cancelled"
	default:
		return "info"
	}
}

// detailFromAttrs serialises the small set of "interesting" attributes
// into a single trailing string. Stable key order so the trace block
// is byte-stable for any given input.
func detailFromAttrs(a map[string]any) string {
	if len(a) == 0 {
		return ""
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		switch k {
		// Drop session_id / turn from the inline detail — they're
		// implicit context already shown in the trace header.
		case "session_id", "turn":
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(fmt.Sprintf("%s=%v", k, a[k]))
	}
	return sb.String()
}
