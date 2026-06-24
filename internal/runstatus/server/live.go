package server

import (
	"path/filepath"
	"strings"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

// LiveSession adapts a live, in-process event sink into a runstatus [Source],
// so `kitsoki web` can serve the same SPA/RPC/SSE surface as `kitsoki status
// serve` against a session that is still running rather than a recorded trace
// file.
//
// It plays two roles at once and a single mutex serialises them:
//
//   - As a [store.EventSink] it is installed on the orchestrator
//     (Orchestrator.SetEventSink); the orchestrator appends every event through
//     Append.
//   - As a [Source] it answers the HTTP server's Snapshot / Events reads.
//
// The underlying [store.JSONLSink] is NOT safe for concurrent Append + History
// reads — its slices are unguarded — so all access funnels through l.mu. The
// orchestrator's appends and the server's reads never touch the sink directly.
//
// initialState backfills SessionHeader.CurrentState before the first event that
// carries a state path is written: a fresh session whose initial room has no
// on_enter chain emits nothing until the first transition, and without this the
// header and diagram would show no active state on the very first frame.
type LiveSession struct {
	mu           sync.Mutex
	sink         *store.JSONLSink
	def          *app.AppDef
	sid          string
	initialState string
}

// NewLiveSession wraps sink as a concurrency-safe live Source for def. sessionID
// is stamped into the snapshot header; initialState is the app's initial state
// path, used to backfill the header before the first state-bearing event.
func NewLiveSession(sink *store.JSONLSink, def *app.AppDef, sessionID, initialState string) *LiveSession {
	return &LiveSession{sink: sink, def: def, sid: sessionID, initialState: initialState}
}

// Append implements [store.EventSink]: the orchestrator's write path.
func (l *LiveSession) Append(ev store.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sink.Append(ev)
}

// History implements [store.EventSink]. It returns a shallow copy of the
// history slice so a caller can iterate it without racing a concurrent Append
// that grows (and may reallocate) the sink's backing slice.
func (l *LiveSession) History() store.History {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.sink.History()
	out := make(store.History, len(h))
	copy(out, h)
	return out
}

// AppDef implements [Source].
func (l *LiveSession) AppDef() *app.AppDef { return l.def }

// AnnotationPath implements [AnnotationSource]: returns the sidecar path
// <sink-dir>/<sessionID>.annotations.jsonl, co-located with the trace JSONL.
func (l *LiveSession) AnnotationPath() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.annotationPathLocked()
}

// Snapshot implements [Source]: the full header + diagram + events view, built
// from the live sink under the lock via the same [runstatus.FromSink] path the
// export-status artifact uses, so the live and exported views cannot drift.
func (l *LiveSession) Snapshot() (runstatus.Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, err := runstatus.FromSink(l.sink, l.def, l.sid)
	if err != nil {
		return snap, err
	}
	if snap.Session.CurrentState == "" && l.initialState != "" {
		snap.Session.CurrentState = l.initialState
	}
	// Load annotations from the sidecar (silently ignored if absent).
	annPath := l.annotationPathLocked()
	if anns, aerr := runstatus.LoadAnnotations(annPath); aerr == nil && len(anns) > 0 {
		snap.Annotations = anns
	}
	return snap, nil
}

// annotationPathLocked computes the annotation sidecar path without acquiring
// the mutex (must be called with l.mu already held).
func (l *LiveSession) annotationPathLocked() string {
	tracePath := l.sink.Path
	dir := filepath.Dir(tracePath)
	sid := strings.ReplaceAll(l.sid, string(filepath.Separator), "_")
	return filepath.Join(dir, sid+".annotations.jsonl")
}

// TranscriptsDir implements the orchestrator's transcript-writer discovery seam
// (host_dispatch.go): the per-session directory <sink-dir>/transcripts where
// agent-action sidecars (<call_id>.jsonl + .timings) are written, co-located with
// the trace JSONL and the annotation sidecar. The orchestrator type-asserts this
// method (an anonymous interface, no import cycle) to install a file-backed
// TranscriptWriter here in the web/live posture, and runstatus.session.transcript
// reads the same sidecars back. See docs/tracing/run-status-ui.md (Agent actions drawer).
func (l *LiveSession) TranscriptsDir() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return filepath.Join(filepath.Dir(l.sink.Path), "transcripts")
}

// Events implements [Source]: the cheap per-poll path. It maps the live history
// to trace events without rendering the diagram, mirroring FromSink's per-event
// mapping (Kind→Msg, payload→Attrs, call_id merged) via [runstatus.ToTraceEvent].
func (l *LiveSession) Events() ([]runstatus.TraceEvent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.sink.History()
	evs := make([]runstatus.TraceEvent, len(h))
	for i := range h {
		evs[i] = runstatus.ToTraceEvent(h[i])
	}
	runstatus.AggregateTaskDetails(evs)
	return evs, nil
}
