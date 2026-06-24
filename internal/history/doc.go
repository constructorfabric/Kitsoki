// Package history implements the bounded room-history stack for
// back-navigation. It sits beside the orchestrator's turn loop: the
// machine pushes a [Stack] entry whenever the user enters a room, and
// pops one whenever a `back` intent fires. Three features consume it —
// `back`-intent transitions, inbox-teleport returns, and agent-room
// exits — so the stack is the single source of truth for "where the
// user came from."
//
// # Algorithm
//
// The stack is a last-in-first-out list of [Entry] values, each a
// (state path, slots snapshot) pair. The machine drives it through
// four operations:
//
//   - Push on transition-in: entering a room records the destination
//     state and the slots bound on arrival, so a later back can restore
//     not just the room but the bindings the user saw there.
//   - Pop on back: a `back` intent pops the top entry and routes to it.
//     An empty stack pops the construction-time main room instead, so
//     back is always well-defined.
//   - Clear on reset: transitioning to the main room (the canonical
//     reset point) drops the whole stack — the user is "home," with no
//     meaningful history to unwind.
//   - Peek for introspection: rendering a back affordance reads the top
//     without consuming it.
//
// # Invariants
//
//   - Depth is bounded at maxDepth (10) entries. On overflow the OLDEST entry is
//     evicted, never the newest — the most recent navigation is always
//     recoverable, and a runaway loop cannot exhaust memory.
//   - Pop on an empty stack never fails: it yields the main room with
//     a false "found" flag, so callers distinguish "popped real
//     history" from "fell back home" without a separate emptiness check.
//   - Slots handed to Push are shallow-cloned on the way in; mutating
//     the caller's map afterwards does not corrupt stored history.
//   - Teleport transitions push the inbox PREDECESSOR, not the inbox
//     room itself, so a back from a teleport target returns to where the
//     user was before the notification, not to the inbox. Transitions
//     flagged push_history: false are stackless (used for agent and
//     inbox rooms, which must not appear as back targets).
//
// # Worked example
//
// A user starts in the main room, opens the general store, then opens
// the wagon-inventory sub-room; each entry carries the slots bound on
// arrival. A single back pops the top entry:
//
//	New("main_room")
//	Push("general_store",  {budget: 240})   stack: [general_store]
//	Push("wagon_inventory", {tab: "food"})  stack: [general_store, wagon_inventory]
//	Pop() → ("wagon_inventory", {tab:"food"}, true)
//	                                         stack: [general_store]
//	Pop() → ("general_store",  {budget:240}, true)
//	                                         stack: []
//	Pop() → ("main_room",      nil,          false)   // empty → fallback
//
// A runnable form of this trace lives in [ExampleStack].
//
// # Persistence
//
// The stack survives a turn as a JSON-serialisable slice in world state
// under the reserved key [WorldKey] ("$history"). The orchestrator
// rehydrates a [Stack] from the world at the start of a turn with
// [FromWorld] and writes the mutated stack back with [ToWorld]; the
// in-memory [Stack] itself is per-turn scratch, not a long-lived object.
//
// # Non-goals
//
//   - No cross-session persistence beyond the world snapshot. The stack
//     lives only as long as the world it is serialised into; it is not a
//     separate durable log. Durable navigation history would belong in
//     the journal, not here.
//   - No named bookmarks. The stack is positional only — entries have no
//     labels and back unwinds strictly LIFO. Authors who want a labelled
//     checkpoint declare an explicit transition to a named room rather
//     than reaching into history. See docs/stories/state-machine.md §10
//     ("Controlled navigation").
//   - No deduplication or cycle collapsing. Re-entering the same room
//     twice pushes two entries; back unwinds each visit independently,
//     because "I came here twice" is a real navigation the user can undo
//     step by step.
//   - No concurrency support — see the contract on [Stack].
//
// # Reference
//
// Back-navigation and the role of this stack are described in
// docs/stories/state-machine.md §10 ("Controlled navigation:
// back-jumps, restart, and feedback arcs"); the package's place in the
// runtime is listed in docs/architecture/overview.md. Inbox-teleport
// metadata lives in [kitsoki/internal/inbox].
package history
