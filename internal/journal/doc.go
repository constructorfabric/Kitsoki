// Package journal implements the kitsoki session journal — a durable, ordered,
// append-only event log that is the canonical source of truth for resumable
// sessions. It sits beneath the orchestrator and the store: the orchestrator
// emits an [Entry] per turn-level fact, and resume reconstructs world/state/
// chat/job documents purely by replaying those entries — there is no separate
// "resume database" the way many engines keep a snapshot table alongside a log.
//
// # One log, two consumers
//
// The journal is built on a "one log, two consumers" principle: the same JSONL
// stream that --trace emits for human debugging is also the feed that
// --continue reads to reconstruct session state. Because both consumers read
// the identical log, the transcript an operator debugs is byte-for-byte the
// transcript resume rebuilds — a property the engine relies on for its replay
// guarantees (see docs/architecture/overview.md, "Persistence, replay, and
// auditability").
//
// # Algorithm
//
// Entries come in three shapes, distinguished by [Entry.Kind] and recognised by
// [IsPatchKind] / [IsCheckpointKind] / [IsTypedKind]:
//
//   - Patch entries — atomic RFC 6902 JSON-Patch ops against one of the four
//     physical documents: "world", "state", "chats/<id>", "jobs/<id>". Kind
//     values: [KindWorldPatch], [KindStateTransition], [KindChatsAppend],
//     [KindJobsUpdate]. These reconstruct document state when applied in order.
//
//   - Checkpoint entries — a full document snapshot ([KindWorldCheckpoint] and
//     siblings) carried in body.full. A checkpoint bounds replay cost: a reader
//     starts from the latest checkpoint and applies only the patches written
//     after it, rather than replaying from version 1.
//
//   - Typed entries — semantic events whose payloads cannot be reconstructed
//     from a patch sequence: host invocations, clarification schemas, timeout
//     arms, inbox-item lifecycle, off-path question/answer pairs, rendered
//     views, oracle/task traces. Kind values: [KindHostInvoked] and siblings.
//     Resume reads these verbatim; it never tries to derive them.
//
// To rebuild a document, [Reader.LoadDocument] finds the latest checkpoint for
// (session, doc), then [Reader.ReplayFrom] yields the patch entries after it,
// ordered by (Turn, Seq); the [Applier] folds each patch onto the running
// document. For the "world" document the applier is schema-aware: after every
// JSON-Patch apply it re-coerces numeric values declared "int" in
// [kitsoki/internal/app.WorldSchema] back to int64, undoing the float64 drift
// that encoding/json would otherwise introduce.
//
// # Invariants
//
//   - [Version] is monotonic per (session, doc): it starts at 1 and increments
//     on every patch or checkpoint write; 0 means "before any write."
//   - Patches for a document apply in (Turn, Seq) order; the writer assigns the
//     DocVersion, callers do not.
//   - Resume is pure data. [Reader.LoadDocument], [Reader.ReplayFrom], and
//     [Reader.ReplayTyped] touch no Harness, host.Registry, transport, LLM, or
//     time.Now() inside the apply loop — which is why the [Applier] and reader
//     constructors deliberately accept none of those. Replaying a log on a
//     fresh database must produce the same documents every time.
//   - A clean end of an iterator and a truncating DB error are NOT the same.
//     The replay iterators return an err() accessor precisely so the replay
//     path can refuse to treat a corrupt/short stream as a complete one.
//
// # Worked example
//
// Three world entries for one session (a checkpoint, then two patches), and the
// document [Reader.LoadDocument] reconstructs from them:
//
//	v1  world.checkpoint  body.full = {"vars":{"gold":100}}
//	v2  world.patch       [{"op":"replace","path":"/vars/gold","value":80}]
//	v3  world.patch       [{"op":"add","path":"/vars/oxen","value":2}]
//
//	LoadDocument("sess","world"):
//	  start from v1 snapshot          → {"vars":{"gold":100}}
//	  apply v2                        → {"vars":{"gold":80}}
//	  apply v3                        → {"vars":{"gold":80,"oxen":2}}
//	  result, version = 3
//
// gold is declared "int" in the world schema, so the schema-aware [Applier]
// keeps it int64 rather than letting it drift to a float64. A runnable form of
// this trace lives in [ExampleApplier_Apply].
//
// # Lifecycle
//
// A session's writer ([NewSQLiteWriter], or [NewMemWriter] for tests) appends
// entries as turns produce them, periodically emitting checkpoints per a
// [Policy] ([DefaultPolicy]). A reader ([NewSQLiteReader] / [NewMemReader])
// replays them on resume or for trace export. Writer and reader share one
// backing store; the SQLite pair persists to sessions.db, the in-memory pair
// lives only for a test's lifetime.
//
// # Non-goals
//
//   - No compression or pruning of entries. The log is append-only and kept in
//     full; auditability and byte-exact transcript reconstruction depend on
//     every entry surviving, so the journal never rewrites or garbage-collects
//     history. Checkpoints bound replay cost; they do not replace old entries.
//
//   - No streaming replication or distributed log. A journal is single-writer,
//     local to one sessions.db. Multi-host fan-out is a transport/store concern,
//     not the journal's — keeping it local is what makes replay deterministic.
//
//   - No interpretation of typed-entry bodies. The journal stores typed events
//     as opaque json.RawMessage and replays them verbatim; deciding what a
//     host.invoked or clarify.requested body means is the orchestrator's job,
//     not the log's. This keeps the schema additive: new event kinds need no
//     applier changes.
//
//   - No re-execution on replay. Resume never re-calls a host, re-runs the LLM,
//     or re-renders a view; recorded outcomes are read back as data (see the
//     "Resume is pure data" invariant). The journal is a record of what
//     happened, not a script to re-run.
//
// # Reference
//
// The user-facing persistence and replay model — why the world snapshot is a
// cache derived from the log, and what determinism buys — is documented in
// docs/architecture/overview.md ("Persistence, replay, and auditability", and
// the persistence-schema and task-span sections). The world-var coercion this
// package mirrors lives in internal/store/replay.go.
package journal
