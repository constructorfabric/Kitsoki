package server

// meta_stream.go — the POST /rpc/meta-stream SSE handler.
//
// Unlike the JSON-RPC meta.send (which blocks until the LLM finishes), this
// endpoint streams agent events in real time as text/event-stream. The client
// sees one SSE frame per interesting agent event ("delta", "tool") and a
// terminal "done" frame carrying the full MetaSendResult when the turn ends.
//
// Protocol:
//
//	POST /rpc/meta-stream
//	  body: {"session_id":"…","mode":"story.ask","chat_id":"…","input":"…"}
//	  response: text/event-stream
//
//	Events:
//	  data: {"type":"think","text":"…"}          ← extended-thinking prose (never the reply)
//	  data: {"type":"delta","text":"…"}          ← assistant narration / reply text chunk
//	  data: {"type":"tool","tool":"Read","preview":"…"}  ← tool call
//	  data: {"type":"done","assistant":"…","chat_id":"…","reload_requested":bool,"changed_files":[…]}
//	  data: {"type":"error","message":"…"}
//
// "think" is a distinct frame type because thinking is ALWAYS intermediate
// reasoning, while a narration "delta" may turn out to be the final reply
// (the model's answer also arrives as plain assistant text). The client
// renders "think" into the activity feed immediately and defers narration —
// flushing it into the feed when later activity proves it intermediate, and
// dropping it on "done" (which carries the authoritative reply). This mirrors
// the TUI's metaStreamPending deferral; see tui.go handleMetaStreamEvent.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"kitsoki/internal/host"
)

// metaStreamFrame is one SSE data payload for the /rpc/meta-stream endpoint.
type metaStreamFrame struct {
	Type string `json:"type"` // "think" | "delta" | "tool" | "done" | "error"

	// think / delta
	Text string `json:"text,omitempty"`

	// tool
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`

	// done (mirrors MetaSendResult fields)
	Assistant       string   `json:"assistant,omitempty"`
	ChatID          string   `json:"chat_id,omitempty"`
	ReloadRequested bool     `json:"reload_requested,omitempty"`
	ChangedFiles    []string `json:"changed_files,omitempty"`

	// error
	Message string `json:"message,omitempty"`
}

// chanStreamSink is a host.StreamSink that forwards events to a buffered
// channel. OnStreamEvent is called on the agent subprocess goroutine and MUST
// NOT block; the select/default drop preserves that contract.
type chanStreamSink struct {
	ch chan host.StreamEvent
}

func (s *chanStreamSink) OnStreamEvent(_ context.Context, ev host.StreamEvent) {
	select {
	case s.ch <- ev:
	default: // full — drop; client won't miss text since done carries the full reply
	}
}

// toolBreadcrumbs returns every tool_use in an assistant StreamEvent, in
// order, so each parallel tool call renders on its own line. Prefers
// ev.Tools (all tool calls) and falls back to the scalar ev.Tool for any
// event that predates the Tools field. Shared by both SSE handlers.
func toolBreadcrumbs(ev host.StreamEvent) []host.StreamToolUse {
	if len(ev.Tools) > 0 {
		return ev.Tools
	}
	if ev.Tool != "" {
		return []host.StreamToolUse{{Name: ev.Tool, Preview: ev.Preview}}
	}
	return nil
}

func streamThinkingText(ev host.StreamEvent) string {
	if ev.Thinking != "" {
		return ev.Thinking
	}
	if ev.Type == "assistant.reasoning" {
		return ev.Text
	}
	return ""
}

func streamDeltaText(ev host.StreamEvent) string {
	if ev.Type == "assistant.reasoning" {
		return ""
	}
	return ev.Text
}

func streamActivityTools(ev host.StreamEvent) []host.StreamToolUse {
	switch ev.Type {
	case "assistant", "tool.execution_start":
		return toolBreadcrumbs(ev)
	default:
		return nil
	}
}

func isStreamActivity(ev host.StreamEvent) bool {
	if ev.IsResult {
		return false
	}
	switch ev.Type {
	case "assistant", "assistant.message", "assistant.reasoning", "tool.execution_start":
	default:
		return false
	}
	return streamThinkingText(ev) != "" || streamDeltaText(ev) != "" || len(streamActivityTools(ev)) > 0
}

func (s *Server) handleMetaStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID string `json:"session_id"`
		Mode      string `json:"mode"`
		ChatID    string `json:"chat_id"`
		Input     string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Mode == "" {
		http.Error(w, "missing mode", http.StatusBadRequest)
		return
	}

	md, rerr := s.resolveMeta(map[string]any{"session_id": body.SessionID})
	if rerr != nil {
		http.Error(w, rerr.Message, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ch := make(chan host.StreamEvent, 256)
	// Detach execution from the request lifetime — see handleTurnStream for the
	// full rationale: a dropped SSE connection (operator closed the surface) must
	// not cancel an in-flight agent send and poison the session with "context
	// canceled". WithoutCancel keeps request values but severs cancellation; the
	// streaming loop below still tears down on r.Context().Done().
	ctx := host.WithStreamSink(context.WithoutCancel(r.Context()), &chanStreamSink{ch: ch})

	type sendOutcome struct {
		res MetaSendResult
		err error
	}
	outcome := make(chan sendOutcome, 1)
	go func() {
		res, err := md.Send(ctx, body.Mode, body.ChatID, body.Input)
		close(ch)
		outcome <- sendOutcome{res: res, err: err}
	}()

	emit := func(f metaStreamFrame) {
		b, err := json.Marshal(f)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

loop:
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.IsResult {
				continue
			}
			if isStreamActivity(ev) {
				// Narration and tool calls can both ride in ONE assistant
				// event (a thought plus the tool calls it explains). Emit
				// the thought first, then one breadcrumb per tool — never
				// one-or-the-other, or the prose (e.g. a fenced JSON reply)
				// leaks away or the tools collapse into a single line.
				// Extended-thinking prose gets its own "think" frame so the
				// client can render it immediately (it is never the reply)
				// while deferring narration deltas.
				if text := streamThinkingText(ev); text != "" {
					emit(metaStreamFrame{Type: "think", Text: text})
				}
				if text := streamDeltaText(ev); text != "" {
					emit(metaStreamFrame{Type: "delta", Text: text})
				}
				for _, tc := range streamActivityTools(ev) {
					emit(metaStreamFrame{Type: "tool", Tool: tc.Name, Preview: tc.Preview})
				}
			}
		}
	}

	o := <-outcome
	if o.err != nil {
		emit(metaStreamFrame{Type: "error", Message: o.err.Error()})
	} else {
		emit(metaStreamFrame{
			Type:            "done",
			Assistant:       o.res.Assistant,
			ChatID:          o.res.ChatID,
			ReloadRequested: o.res.ReloadRequested,
			ChangedFiles:    o.res.ChangedFiles,
		})
	}
}
