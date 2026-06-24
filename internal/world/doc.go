// Package world defines the runtime world snapshot: the typed-by-convention
// collection of persistent context variables declared by the app author in
// the YAML world schema (docs/embedded/app-schema.md). It sits beneath the
// orchestrator and machine layers as the shared state carrier: guards read a
// [World] to decide transitions, views render from it, and effects produce
// the next [World] that the orchestrator threads into the following turn.
//
// # Algorithm
//
// There is no algorithm to speak of — the package is deliberately a thin,
// immutable value type. The one design rule it enforces is copy-on-write:
//
//   - A [World] is never mutated in place. [World.With] returns a new World
//     whose Vars map is a full copy of the receiver's plus the one changed
//     key. The receiver is left byte-for-byte unchanged.
//   - Because mutation always allocates a fresh map, any snapshot handed to a
//     guard, a view, or a logger is frozen for that snapshot's lifetime. A
//     later effect cannot retroactively change what an earlier reader saw.
//
// [Slots] is the sibling type: the ephemeral, per-turn values the LLM harness
// extracted for one intent call, kept separate from the persistent World so
// that promoting a slot into world state is always an explicit effect rather
// than an accidental merge.
//
// # Invariants
//
//   - After w2 := w.With(k, v): w2.Get(k) == v, and w is unchanged for every
//     key (including k).
//   - [World.With] never aliases the receiver's Vars map; the two snapshots
//     share no mutable structure at the map level.
//   - [World.Get] on an absent key and on the zero World both return nil; the
//     package does not distinguish "absent" from "present but nil."
//
// # Worked example
//
// Threading two writes through immutable snapshots:
//
//	w0 := world.New()              // Vars: {}
//	w1 := w0.With("miles", 0)      // w0 unchanged; w1.Vars: {"miles":0}
//	w2 := w1.With("miles", 18)     // w1 unchanged; w2.Vars: {"miles":18}
//
//	w0.Get("miles") -> nil         // never written into w0
//	w1.Get("miles") -> 0           // the snapshot a guard read earlier
//	w2.Get("miles") -> 18          // the snapshot the next turn sees
//
// A runnable form of this trace lives in [ExampleWorld_With].
//
// # Lifecycle
//
// [New] mints an empty World at the start of a run (or load applies declared
// defaults via effects on top of New). Each turn the orchestrator passes the
// current World to guard/view/effect evaluation and keeps the World that
// effect application returns; that World is persisted (JSON) and becomes the
// input to the next turn. The zero World is a valid read-only snapshot, so a
// caller that only needs Get need not call New.
//
// # Non-goals
//
//   - No schema validation or type enforcement. Vars is map[string]any;
//     whether a value matches the declared type is the app-load and effects
//     layers' concern, not this package's — keeping World a dumb carrier is
//     what lets it cross the MCP/SQLite boundary unchanged.
//   - No default-value application. New returns an empty World; seeding
//     declared defaults is an effect, so that all writes flow through the
//     same audited path.
//   - No transactions or in-place mutation. Snapshots are immutable by
//     design; "rolling back" is just keeping a reference to an earlier World,
//     which is cheaper and clearer than an undo log.
//   - No key namespacing or collision handling between World and [Slots]. The
//     two pools are separate types precisely so the caller, not this package,
//     decides when a slot becomes world state.
//
// # Reference
//
// The world-variable declaration and typing rules live in the app schema
// (docs/embedded/app-schema.md). How effects compute the next World from the
// current one is owned by [kitsoki/internal/orchestrator].
package world
