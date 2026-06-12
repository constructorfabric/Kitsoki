package server

// turn_stream.go — the POST /rpc/turn-stream SSE handler.
//
// The normal turn RPCs (session.turn / session.submit / session.continue /
// session.offpath) block until the LLM finishes — 30–120s for a typical oracle
// call. This endpoint streams oracle events in real time as text/event-stream
// so the browser sees live progress instead of a frozen UI.
//
// Protocol:
//
//	POST /rpc/turn-stream
//	  body: {
//	    "session_id": "…",
//	    "method":     "turn"|"submit"|"continue"|"offpath",
//	    "input":      "…",           // turn / offpath
//	    "intent":     "…",           // submit
//	    "slots":      {…}            // submit / continue
//	  }
//	  response: text/event-stream
//
//	Events:
//	  data: {"type":"think","text":"…"}   ← extended-thinking prose (never the reply)
//	  data: {"type":"delta","text":"…"}   ← assistant narration / reply text
//	  data: {"type":"tool","tool":"Read","preview":"…"}
//	  data: {"type":"done","result":{<turnResult>}}
//	  data: {"type":"error","message":"…"}
//
// "think" and "delta" are distinct frame types because they mean different
// things to a client: thinking is ALWAYS intermediate reasoning, while a
// narration delta may turn out to be the final reply (the model's answer also
// arrives as plain assistant text). The meta-stream client relies on that
// distinction to defer narration the way the TUI does; this endpoint shares
// the protocol so the two streams stay interchangeable.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"kitsoki/internal/host"
)

// turnStreamFrame is one SSE data payload for the /rpc/turn-stream endpoint.
type turnStreamFrame struct {
	Type string `json:"type"` // "think" | "delta" | "tool" | "done" | "error"

	// think / delta
	Text string `json:"text,omitempty"`

	// tool
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`

	// done
	Result *turnResult `json:"result,omitempty"`

	// error
	Message string `json:"message,omitempty"`
}

func (s *Server) handleTurnStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID string         `json:"session_id"`
		Method    string         `json:"method"`
		Input     string         `json:"input"`
		Intent    string         `json:"intent"`
		Slots     map[string]any `json:"slots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	switch body.Method {
	case "turn", "submit", "continue", "offpath":
	default:
		http.Error(w, `missing or invalid "method" (want turn|submit|continue|offpath)`, http.StatusBadRequest)
		return
	}

	entry, rerr := s.resolve(map[string]any{"session_id": body.SessionID})
	if rerr != nil {
		http.Error(w, rerr.Message, http.StatusBadRequest)
		return
	}
	if entry.Driver == nil {
		http.Error(w, "turn-stream: this surface is read-only (no live session)", http.StatusForbidden)
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
	ctx := host.WithStreamSink(r.Context(), &chanStreamSink{ch: ch})

	type outcome struct {
		tr  *turnResult
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		var tr *turnResult
		var err error
		switch body.Method {
		case "turn":
			out, e := entry.Driver.Turn(ctx, body.Input)
			if e == nil {
				r := newTurnResult(out, entry.Driver)
				tr = &r
			}
			err = e
		case "submit":
			out, e := entry.Driver.SubmitDirect(ctx, body.Intent, body.Slots)
			if e == nil {
				r := newTurnResult(out, entry.Driver)
				tr = &r
			}
			err = e
		case "continue":
			out, e := entry.Driver.ContinueTurn(ctx, body.Slots)
			if e == nil {
				r := newTurnResult(out, entry.Driver)
				tr = &r
			}
			err = e
		case "offpath":
			answer, e := entry.Driver.AskOffPath(ctx, body.Input)
			if e == nil {
				r := turnResult{Mode: "offpath", View: answer}
				tr = &r
			}
			err = e
		}
		close(ch)
		result <- outcome{tr: tr, err: err}
	}()

	emit := func(f turnStreamFrame) {
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
			if ev.Type == "assistant" {
				// One assistant event can carry both a thought and the tool
				// calls it explains. Emit the thought first, then one
				// breadcrumb per tool — emitting one-or-the-other drops the
				// prose (e.g. a fenced JSON reply) or collapses parallel
				// tool calls into a single line. Extended-thinking prose
				// gets its own "think" frame type: it is never the reply,
				// and clients that defer narration (the meta overlay) need
				// to tell the two apart on the wire.
				if ev.Thinking != "" {
					emit(turnStreamFrame{Type: "think", Text: ev.Thinking})
				}
				if ev.Text != "" {
					emit(turnStreamFrame{Type: "delta", Text: ev.Text})
				}
				for _, tc := range toolBreadcrumbs(ev) {
					emit(turnStreamFrame{Type: "tool", Tool: tc.Name, Preview: tc.Preview})
				}
			}
		}
	}

	o := <-result
	if o.err != nil {
		emit(turnStreamFrame{Type: "error", Message: o.err.Error()})
	} else {
		emit(turnStreamFrame{Type: "done", Result: o.tr})
	}
}
