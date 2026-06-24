// Package store is event-sourced session persistence: it sits between the
// orchestrator (the producer of [Event]s, one turn at a time) and the
// TUI / runstatus dashboard (the consumer of replayed history). It offers
// two interchangeable event sinks — an on-disk append-only JSONL trace
// ([JSONLSink], the trace-as-state path) and a SQLite-backed store
// ([Store], the indexed/queryable path) — plus the replay fold
// ([BuildJourney]) that reconstructs (state, world) from either.
//
// The orchestrator writes through the narrow [EventSink] interface, so a
// call site is agnostic about which backend it holds. SQLite additionally
// carries the relational surface the JSONL trace cannot: session rows,
// snapshots, the external-key index, and the cross-process writer lock.
//
// # Algorithm
//
// Persistence is append-only event sourcing. Each user turn produces an
// ordered batch of [Event]s; state is never overwritten, only appended.
//
//	write:   AppendEvents(session, batch)  →  one INSERT per event,
//	         seq reassigned 0..n-1 within the turn, last_turn bumped,
//	         all inside one BEGIN-IMMEDIATE transaction (atomic per turn).
//	read:    LoadHistory(session)           →  events since the latest
//	         snapshot turn, ORDER BY turn, seq (bounded replay window).
//	replay:  BuildJourney(def, init, world, history)  →  fold each event
//	         by [EventKind] into a [JourneyState]; structural kinds
//	         (TransitionApplied, EffectApplied) mutate, annotation kinds
//	         are no-ops, unknown kinds are ignored (forward-compatible).
//
// The JSONL sink runs the same shape against a file: one line per event,
// O_APPEND + fsync, validated on open ([OpenJSONL]) for header, schema
// version, and (turn, seq) monotonicity.
//
// # Invariants
//
//   - Append-only. No event is ever rewritten or deleted (except whole-session
//     [Store.DeleteSession], an operator/test escape hatch). A completed or
//     abandoned session rejects further appends with [ErrSessionClosed].
//   - seq is dense from 0 within a turn. The sink/store is the sole authority
//     for seq; a caller's [Event.Seq] is overwritten. SQLite enforces this with
//     a UNIQUE (session_id, turn, seq) primary key; the JSONL loader re-checks
//     it on read and rejects gaps, duplicates, and out-of-order rows.
//   - Turn numbers are monotonic across a session, including off-path
//     side-channel batches (allocated max+1 so they never collide).
//   - Replay determinism: feeding the events a live turn produced back through
//     [BuildJourney] yields the same (state, world) the machine produced live.
//   - JSONL on disk is canonical UTF-8: NUL bytes rejected, strings must be
//     NFC-normalised, timestamps RFC3339Nano in UTC with an explicit Z.
//
// # Worked example
//
// Create a session, append one turn of two events, then load it back:
//
//	def := &app.AppDef{App: app.AppMeta{ID: "demo", Version: "v0"}}
//	st, _ := store.OpenMemory()
//	sid, _ := st.CreateSession(ctx, def)
//
//	st.AppendEvents(sid, []store.Event{
//	    {Turn: 1, Kind: store.TurnStarted},
//	    {Turn: 1, Kind: store.TransitionApplied,
//	        Payload: json.RawMessage(`{"to":"river.scouting"}`)},
//	})
//
//	h, _ := st.LoadHistory(sid)
//	// h has 2 events; seq was assigned 0 and 1 by the store:
//	//   h[0] = {Turn:1, Seq:0, Kind:"turn.start"}
//	//   h[1] = {Turn:1, Seq:1, Kind:"machine.transition", ...}
//
//	js, _ := store.BuildJourney(def, "river.scouting", world.World{}, h)
//	// js.State == "river.scouting", js.Turn == 1
//
// A runnable form of this trace lives in [ExampleStore].
//
// # Lifecycle
//
// [Open] (or [OpenMemory] for tests) is called once at startup; it runs the
// embedded DDL idempotently and configures WAL, foreign keys, and a single
// pooled connection. The returned [Store] is used for the life of the process
// and closed at shutdown via [Store.Close], which checkpoints the WAL. The
// JSONL path is symmetric: [OpenJSONL] at session start (taking an advisory
// flock), [JSONLSink.Append] per event, [JSONLSink.Close] at session end.
// Snapshots ([Store.Snapshot]) are taken on a caller-chosen cadence so
// [Store.LoadHistory] only ever replays events since the last snapshot.
//
// # Non-goals
//
//   - No multi-writer safety within a process. The store pins a single SQLite
//     connection (SetMaxOpenConns(1)) and serializes turn writes behind
//     BEGIN IMMEDIATE; the single-process PoC design makes WAL sufficient and
//     a connection pool unnecessary. Cross-process contention is handled
//     separately by the session writer lock ([Store.WithWriterLock]), not by
//     the connection layer.
//   - No peer-to-peer or multi-node replication. A session lives in exactly
//     one SQLite file (or one JSONL trace); there is no sync protocol because
//     the deployment model is one host owning its sessions.
//   - No encryption at rest. Event payloads are stored verbatim; confidentiality
//     is the operator's filesystem concern, kept out of the event log so traces
//     stay diffable and replayable byte-for-byte.
//   - No automatic compression or compaction of old events. Snapshots already
//     bound the replay window; rewriting history would violate the append-only
//     invariant and break byte-equality of traces.
//
// # Reference
//
// Sessions keyed by transport and the external-key index are documented in
// docs/architecture/transports.md ("Sessions keyed by transport"); the broader
// session/transport model lives in docs/architecture/overview.md. The journal
// the store can write atomically alongside events is [kitsoki/internal/journal].
package store
