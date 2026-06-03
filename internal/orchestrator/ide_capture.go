package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// IDECaptureRecord is the editor context the TUI captured at turn-submit, handed
// to the orchestrator so it lands in the session trace as an `ide.context_captured`
// event. It is the audit record behind an editor-informed turn: what the `/ide`
// link surfaced, where it came from, and whether it rode the prompt — recorded
// even when nothing usable was found (Source "none"), so a "the link was
// connected but the model didn't see my doc" report is diagnosable from the
// trace alone. No selection / diagnostic TEXT is carried (privacy lean); only
// the file path, counts, and provenance.
type IDECaptureRecord struct {
	Connected bool   // the /ide link was connected at capture time
	Source    string // "selection" | "active_editor" | "none"
	File      string // file the context came from ("" when Source none)
	Lines     int    // selected line count (0 for active-file-only / none)
	Range     string // selection range label ("" when not a selection)
	Injected  bool   // did this context ride the prompt this turn (fresh + file)
	Reason    string // why nothing rode, when Source=="none" (e.g. "ambiguous_focus")
	Detail    string // diagnostic detail when Source=="none" — e.g. the raw
	// getOpenEditors envelope (file paths/labels, never selection or file body)
	// so an unexpected editor wire-shape is fixable from the trace.
}

// RecordIDEContext appends an `ide.context_captured` event describing the editor
// context the TUI captured at submit. The TUI is the one place with the live
// link, but it has no session sink — so it hands the record here, where the
// orchestrator owns the trace sink and the turn counter. Called before routing,
// so the event is attributed to the turn about to run (journey.Turn+1) and
// ordered just ahead of that turn's events.
//
// It writes ONLY to the JSONL eventSink (the trace the operator reads), like
// RecordEffectiveStory: the JSONL sink continues a dense per-turn seq across
// incremental appends, whereas the SQLite batch writer restarts seq at 0 per
// batch — so routing a separate one-event batch through the dual-write path
// would collide on the (turn, seq) PK with the turn's own first event. The
// SQLite store omits this event; that is fine — it is non-mutating, so journey
// reconstruction is unaffected, and SQLite is the backward-compat bridge while
// JSONL is the authoritative trace.
//
// Best-effort: no sink, a load failure, or an append error is logged, never
// fatal — IDE observability must not break a turn. Mirrors the per-verb
// emitIDEContextCaptured the host handlers write when a story explicitly invokes
// host.ide.*; this is the ambient (per-turn, TUI-driven) counterpart.
func (o *Orchestrator) RecordIDEContext(ctx context.Context, sid app.SessionID, rec IDECaptureRecord) {
	if o.eventSink == nil {
		return
	}
	journey, err := o.loadJourney(sid)
	if err != nil {
		slog.WarnContext(ctx, "ide.context_captured: load journey", "err", err)
		return
	}

	payload := map[string]any{
		"connected": rec.Connected,
		"source":    rec.Source,
		"injected":  rec.Injected,
	}
	if rec.File != "" {
		payload["file"] = rec.File
	}
	if rec.Lines > 0 {
		payload["lines"] = rec.Lines
	}
	if rec.Range != "" {
		payload["range"] = rec.Range
	}
	if rec.Reason != "" {
		payload["reason"] = rec.Reason
	}
	if rec.Detail != "" {
		payload["detail"] = rec.Detail
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      journey.Turn + 1,
		Ts:        time.Now(),
		Kind:      store.IDEContextCaptured,
		StatePath: journey.State,
		Payload:   json.RawMessage(raw),
	}
	if err := o.eventSink.Append(ev); err != nil {
		slog.WarnContext(ctx, "ide.context_captured: append", "err", err)
	}
}
