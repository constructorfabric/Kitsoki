package server

// turn_stream.go — the POST /rpc/turn-stream SSE handler.
//
// The normal turn RPCs (session.turn / session.submit / session.continue /
// session.offpath) block until the LLM finishes — 30–120s for a typical agent
// call. This endpoint streams agent events in real time as text/event-stream
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
//	  data: {"type":"cancelled"}                  ← operator hit Stop (session.cancel)
//	  data: {"type":"error","message":"…"}
//
// "think" and "delta" are distinct frame types because they mean different
// things to a client: thinking is ALWAYS intermediate reasoning, while a
// narration delta may turn out to be the final reply (the model's answer also
// arrives as plain assistant text). The meta-stream client relies on that
// distinction to defer narration the way the TUI does; this endpoint shares
// the protocol so the two streams stay interchangeable.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/world"
)

// turnStreamFrame is one SSE data payload for the /rpc/turn-stream endpoint.
type turnStreamFrame struct {
	Type string `json:"type"` // "think" | "delta" | "tool" | "done" | "cancelled" | "error"

	// think / delta
	Text string `json:"text,omitempty"`

	// tool
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`

	// done
	Result *turnResult `json:"result,omitempty"`

	// error
	Message string `json:"message,omitempty"`

	// routing
	Turn       int64   `json:"turn,omitempty"`
	Intent     string  `json:"intent,omitempty"`
	RoutedBy   string  `json:"routed_by,omitempty"`
	MatchType  string  `json:"match_type,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
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
		Visual    map[string]any `json:"visual"`
		Anchor    map[string]any `json:"anchor"`
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
	// Detach the turn's EXECUTION from the HTTP request lifetime. A streamed turn
	// can be a 30–120s agent call; if the operator closes the surface (the VS Code
	// chat panel, a browser tab) mid-turn, the SSE connection drops and r.Context()
	// is cancelled. Were the driver running on that context, the turn would fail
	// with "context canceled", the room's `on_error:` arc would land that string in
	// world.last_error, and it would be BAKED into the persisted view — so every
	// later reopen of the session shows "context canceled" instead of the workbench.
	// WithoutCancel keeps the request's values (stream sink, operator prompter,
	// actor) while severing cancellation, so the turn runs to a real outcome in the
	// background. The streaming loop below still tears down on r.Context().Done();
	// the detached turn's late events are simply dropped by chanStreamSink.
	//
	// Because the execution context is detached from the request, a client
	// disconnect can't stop the turn — and neither could anything else, which is
	// why "Stop" used to be impossible. We layer an explicit cancel on top and
	// register it so runstatus.session.cancel can fire it; that cancel propagates
	// all the way to the agent subprocess (exec.CommandContext), and the
	// orchestrator treats the resulting context.Canceled as a clean abort that
	// persists nothing. unregister() only frees the registry slot — it must NOT
	// cancel, because this handler also returns on a client disconnect and a
	// disconnected turn is meant to run to completion (the detach contract above).
	// cancelTurn is fired only by session.cancel or by the turn goroutine's defer
	// once the turn truly finishes.
	execCtx, cancelTurn := context.WithCancel(context.WithoutCancel(r.Context()))
	unregister := s.registerActiveTurn(body.SessionID, cancelTurn)
	defer unregister()
	ctx := host.WithStreamSink(execCtx, &chanStreamSink{ch: ch})
	// Lift an optional visual bundle / picked anchor (the media-annotation
	// composer dispatches its intent over the streaming path) so the agent edits
	// the pointed-at element. Build the params map with only the present keys so
	// liftVisualAmbient's nil-checks stay accurate.
	{
		p := map[string]any{}
		if body.Visual != nil {
			p["visual"] = body.Visual
		}
		if body.Anchor != nil {
			p["anchor"] = body.Anchor
		}
		if c2, ok := liftVisualAmbient(ctx, p); ok {
			ctx = c2
		}
	}
	// A free-text turn may carry lightweight ambient slots (e.g. `current_scene` —
	// the deck slide the operator is currently looking at) so the routed intent
	// targets it even with no annotation. Gap-fill only: the harness's own
	// classification wins (see orchestrator.WithSupplementSlots).
	if body.Method == "turn" && len(body.Slots) > 0 {
		ctx = WithTurnSupplements(ctx, world.Slots(body.Slots))
	}

	type outcome struct {
		tr  *turnResult
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		// Free the execution context once the turn has truly finished. Deferring
		// it here (not in the handler) is what lets a disconnected turn keep
		// running: the handler may return early on r.Context().Done(), but this
		// goroutine — and the context — lives until the turn completes. A no-op if
		// session.cancel already cancelled.
		defer cancelTurn()
		var tr *turnResult
		var err error
		switch body.Method {
		case "turn":
			out, e := entry.Driver.Turn(ctx, body.Input)
			if e == nil {
				var drive *orchestrator.OperationDriveOutcome
				drive, out, e = driveBackgroundOperationAfterTurn(ctx, entry.Driver, out)
				r := newTurnResultWithOperationDrive(out, entry.Driver, drive)
				tr = &r
			}
			err = e
		case "submit":
			out, e := entry.Driver.SubmitDirect(ctx, body.Intent, body.Slots)
			if e == nil {
				var drive *orchestrator.OperationDriveOutcome
				drive, out, e = driveBackgroundOperationAfterTurn(ctx, entry.Driver, out)
				r := newTurnResultWithOperationDrive(out, entry.Driver, drive)
				tr = &r
			}
			err = e
		case "continue":
			out, e := entry.Driver.ContinueTurn(ctx, body.Slots)
			if e == nil {
				var drive *orchestrator.OperationDriveOutcome
				drive, out, e = driveBackgroundOperationAfterTurn(ctx, entry.Driver, out)
				r := newTurnResultWithOperationDrive(out, entry.Driver, drive)
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

	// Heartbeat: a turn whose work emits no stream events (e.g. a
	// host.agent.task reviser that runs 50–70s with no streamed narration)
	// would leave the SSE connection idle the whole time. A dev proxy or the
	// browser can drop a silent stream before the terminal `done` frame arrives,
	// leaving the chat bubble stuck on "…". A periodic ping keeps the connection
	// alive; the client ignores the unknown frame type.
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

loop:
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			emit(turnStreamFrame{Type: "ping"})
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.IsResult {
				continue
			}
			if ev.Type == "routing" {
				emit(turnStreamFrame{
					Type:       "routing",
					Turn:       ev.Turn,
					Intent:     ev.Intent,
					RoutedBy:   ev.RoutedBy,
					MatchType:  ev.MatchType,
					Confidence: ev.Confidence,
				})
				continue
			}
			if isStreamActivity(ev) {
				// One assistant event can carry both a thought and the tool
				// calls it explains. Emit the thought first, then one
				// breadcrumb per tool — emitting one-or-the-other drops the
				// prose (e.g. a fenced JSON reply) or collapses parallel
				// tool calls into a single line. Extended-thinking prose
				// gets its own "think" frame type: it is never the reply,
				// and clients that defer narration (the meta overlay) need
				// to tell the two apart on the wire.
				if text := streamThinkingText(ev); text != "" {
					emit(turnStreamFrame{Type: "think", Text: text})
				}
				if text := streamDeltaText(ev); text != "" {
					emit(turnStreamFrame{Type: "delta", Text: text})
				}
				for _, tc := range streamActivityTools(ev) {
					emit(turnStreamFrame{Type: "tool", Tool: tc.Name, Preview: tc.Preview})
				}
			}
		}
	}

	o := <-result
	switch {
	case o.err == nil:
		emit(turnStreamFrame{Type: "done", Result: o.tr})
	case execCtx.Err() != nil:
		// The operator hit Stop: our explicit cancel fired, so the turn aborted
		// with context.Canceled and the orchestrator persisted nothing. Emit a
		// distinct "cancelled" terminal (not "error") so the client resets to idle
		// without surfacing a red "context canceled" toast.
		emit(turnStreamFrame{Type: "cancelled"})
	default:
		emit(turnStreamFrame{Type: "error", Message: o.err.Error()})
	}
}
