// Package runstatus defines the canonical [Snapshot] type and the
// adapters that build it. It sits between the event store
// ([kitsoki/internal/store]) and the run-status UI export layers: the
// JSON-RPC methods (live mode), the self-contained HTML artifact
// (export-status), and the test fixtures all consume one Snapshot
// shape so the live and exported views cannot drift.
//
// A Snapshot is self-contained: it carries the session header, the
// full [app.AppDef], a rendered Mermaid diagram with its node map, and
// the typed event stream. Given a Snapshot, a viewer needs nothing
// else from the store to render the run.
//
// # Algorithm
//
// [FromHistory] is the constructor. It walks a [store.History] once,
// in order, and for each [store.Event]:
//
//  1. Decodes the event's JSON Payload into the event's Attrs map, and
//     merges the off-band CallID field into Attrs so the SPA sees it
//     alongside the payload.
//  2. Maps the event 1:1 to a [TraceEvent] — no synthesis, no
//     back-fill. Msg is the event Kind; Level is "ERROR" for the
//     failure kinds (HarnessError, ValidationFailed, GuardRejected)
//     and "INFO" otherwise.
//  3. Tracks running session-header state: the latest StatePath (or
//     the "state" attr of a StateEntered event) becomes CurrentState,
//     the high-water Turn becomes Turn, the first event's timestamp
//     becomes StartedAt.
//  4. Re-marshals the event with [store.MarshalEventLine] and appends
//     the bytes to RawLines (see the byte-equality contract below).
//
// After the walk it resolves Terminal by compiling def and looking up
// whether CurrentState is a terminal state, renders the diagram via
// [viz.FlowchartWithMap], and assembles the Snapshot.
//
// [FromSink] is the same construction with one substitution: it takes
// the bytes the sink actually wrote ([store.JSONLSink.Lines]) for
// RawLines instead of re-marshalling, giving a byte-copy-equal raw
// stream rather than an encoder-pair-equal one.
//
// # Invariants
//
//   - len(Snapshot.Events) == len(Snapshot.RawLines) == len(hist).
//     The walk appends to both slices on every iteration; a
//     marshal failure appends a nil RawLines entry rather than
//     skipping, so the index alignment Events[i]↔RawLines[i] holds.
//   - Events preserve History order; CurrentState/Turn/StartedAt are
//     derived from that same single pass, never re-sorted.
//   - RawLines is the only field not serialised to JSON (json:"-").
//
// # Worked example
//
// Given a two-event history for app "demo" — a StateEntered into
// "greet" on turn 1, then a HarnessError on turn 1 — FromHistory
// produces:
//
//	in:  History{
//	       {Turn:1, Kind:"state.entered", StatePath:"greet",
//	        Payload:`{"state":"greet"}`},
//	       {Turn:1, Kind:"harness.error", Payload:`{"detail":"boom"}`},
//	     }
//	out: Snapshot{
//	       Session: {SessionID:"s1", AppID:"demo",
//	                 CurrentState:"greet", Turn:1, Terminal:false},
//	       Events: [
//	         {Level:"INFO",  Msg:"state.entered", StatePath:"greet",
//	          Attrs:{state:"greet"}},
//	         {Level:"ERROR", Msg:"harness.error",
//	          Attrs:{detail:"boom"}},
//	       ],
//	     }
//
// A runnable form of this trace lives in [ExampleFromHistory].
//
// # Lifecycle
//
// There is no compiled, reusable index here: [FromHistory] /
// [FromSink] are called fresh on each access (each export, each
// session.get) and return a value Snapshot. [app.Compile] is invoked
// once per call to resolve the terminal-state lookup. The returned
// Snapshot is a plain value safe for concurrent reads, but its
// TraceEvent.Attrs and Mermaid.NodeMap maps are shared, not deep
// copied — treat a returned Snapshot as read-only.
//
// # Non-goals
//
//   - No event synthesis or back-fill. Every TraceEvent comes from a
//     real store.Event; the exporter never invents a state-entry or
//     fills a missing turn, because the snapshot must be a faithful
//     mirror of what the orchestrator actually recorded.
//   - No persistence or storage. This package only transforms an
//     in-memory History/sink into a Snapshot; reading and writing the
//     JSONL trace itself belongs to [kitsoki/internal/store].
//   - No diagram layout logic. The Mermaid source and node map are
//     produced by [kitsoki/internal/viz]; runstatus only carries them.
//   - No HTML/RPC rendering. Turning a Snapshot into an artifact or a
//     wire response is the export layer's job; the split keeps this
//     package free of presentation concerns.
//
// # Reference
//
// The on-disk JSONL trace format these snapshots mirror — the event
// schema, the EventKind vocabulary, the EventSink contract, and the
// byte-equality / exporter pass-through guarantees that motivate
// Snapshot.RawLines — is documented in docs/tracing/trace-format.md.
package runstatus
