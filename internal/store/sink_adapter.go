package store

// sink_adapter.go bridges [Store] to [EventSink] via StoreSinkAdapter. See
// doc.go for the package overview.
//
// Wave 2a seam: every orchestrator call site that previously called
// store.AppendEventsAndJournal now routes event writes through StoreSinkAdapter.
// StoreSinkAdapter wraps a Store + SessionID so that the per-session EventSink
// interface is satisfied while the SQLite backend remains in place.  Later
// waves swap NewStoreSinkAdapter for OpenJSONL at construction sites, removing
// the SQLite dependency from write paths.
//
// Seq assignment: the SQLite events table has a UNIQUE constraint on
// (session_id, turn, seq).  appendEventsTx assigns seq starting at 0 for each
// batch call, so calling AppendEvents once per event would cause seq collisions
// on the second and subsequent events in the same turn.  StoreSinkAdapter
// therefore exposes AppendBatch for callers that have the full turn slice ready
// before writing.  The single-event Append path is provided for EventSink
// compatibility and buffers internally until Flush is called.

import (
	"sync"

	"kitsoki/internal/app"
)

// Compile-time assertion: *StoreSinkAdapter must satisfy EventSink.
var _ EventSink = (*StoreSinkAdapter)(nil)

// StoreSinkAdapter implements EventSink by delegating to an underlying Store
// for a fixed session.  It is session-scoped: one adapter per session.
//
// Use NewStoreSinkAdapter to construct.  Callers that build the entire event
// slice before writing should call AppendBatch; callers that stream one event
// at a time should call Append followed by Flush.
type StoreSinkAdapter struct {
	s   Store
	sid app.SessionID
	buf []Event // pending events not yet flushed via AppendBatch/Flush
}

// NewStoreSinkAdapter returns a *StoreSinkAdapter that routes writes to s for
// session sid.  The returned value satisfies EventSink and also exposes
// AppendBatch and Flush for batch callers.
func NewStoreSinkAdapter(s Store, sid app.SessionID) *StoreSinkAdapter {
	return &StoreSinkAdapter{s: s, sid: sid}
}

// Append buffers ev.  The event is not persisted until Flush is called.
// For orchestrator turn-write paths where the full slice is available,
// AppendBatch is more efficient and avoids the need to call Flush.
func (a *StoreSinkAdapter) Append(ev Event) error {
	a.buf = append(a.buf, ev)
	return nil
}

// Flush writes all buffered events to the store in a single AppendEvents call
// and clears the buffer.  Returns nil immediately if no events are buffered.
func (a *StoreSinkAdapter) Flush() error {
	if len(a.buf) == 0 {
		return nil
	}
	events := a.buf
	a.buf = nil
	return a.s.AppendEvents(a.sid, events)
}

// AppendBatch writes events to the store in a single AppendEvents call,
// bypassing the internal buffer.  This is the preferred method for
// orchestrator turn-write paths where the full event slice is assembled
// before the write.
func (a *StoreSinkAdapter) AppendBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return a.s.AppendEvents(a.sid, events)
}

// History returns the full event history for the session by delegating to
// Store.LoadHistory.  The returned slice is the live SQLite projection; callers
// that need a stable snapshot should copy it before mutating.
func (a *StoreSinkAdapter) History() History {
	h, _ := a.s.LoadHistory(a.sid)
	return h
}

// DeferredSink wraps an EventSink that may be nil at creation time but is set
// later. Used when the sink is not available at the time the EventSink
// parameter is captured (e.g., cassette dispatcher creation happens before
// session creation). Thread-safe for Append; History is read-only after first call.
type DeferredSink struct {
	mu   sync.Mutex
	sink EventSink
}

func NewDeferredSink() *DeferredSink {
	return &DeferredSink{}
}

// SetSink updates the underlying sink. Safe to call before/after Append calls.
func (d *DeferredSink) SetSink(sink EventSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sink = sink
}

// Append delegates to the underlying sink if set, otherwise buffers locally
// (though buffering is not supported — this is best-effort).
func (d *DeferredSink) Append(ev Event) error {
	d.mu.Lock()
	sink := d.sink
	d.mu.Unlock()
	if sink == nil {
		return nil // silently drop if no sink is set yet
	}
	return sink.Append(ev)
}

// History delegates to the underlying sink if set.
func (d *DeferredSink) History() History {
	d.mu.Lock()
	sink := d.sink
	d.mu.Unlock()
	if sink == nil {
		return History{}
	}
	return sink.History()
}
