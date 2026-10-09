// Package viz renders an [app.AppDef] state machine as Mermaid and Graphviz
// diagrams. It is a leaf, read-only package: the `kitsoki viz` CLI command
// (see cmd/kitsoki) and the export-status tooling call into it after the
// loader has produced an AppDef, and it never mutates the app or touches the
// runtime. It offers three diagram families — a Graphviz DOT state graph, a
// Mermaid stateDiagram-v2 (flat or split per room), and a Mermaid flowchart
// that foregrounds data flow — plus the room-grouping primitive the other
// modes share.
//
// # Diagram modes
//
// Three independent emitters share one room model:
//
//   - DOT (state graph) — [Export] / [DOTBytes], via github.com/emicklei/dot.
//     Compound states become cluster subgraphs, transitions become labelled
//     edges. This is the only mode that uses an external graph library.
//   - Mermaid stateDiagram-v2 (topology) — [ExportMermaid] / [MermaidBytes]
//     for a single flat diagram, or [ExportMermaidRooms] to split a large app
//     into an `_overview.mmd` plus one file per room. Hand-rolled string
//     templating, no library.
//   - Mermaid flowchart LR (data flow) — [ExportFlowchart] / [FlowchartBytes],
//     scoped by [DetailLevel] and [FlowchartFilter]. Rooms are subgraphs,
//     on_enter chains are hexagon nodes, world writes are cylinder nodes.
//     [FlowchartWithMap] additionally returns a node-ID → [NodeRef] sidecar
//     so a UI can map a clicked node back to the AppDef element it represents.
//
// # Algorithm
//
// Two heuristics drive every mode:
//
// Room detection ([GroupRooms]). A "room" is the coarse grouping a reader
// thinks in. For each leaf state path, [roomOf] assigns a room:
//
//  1. If the first dotted segment names a top-level compound state, the room
//     is that compound state (cloak's `bar` owns `bar.dark`, `bar.lit`).
//  2. Otherwise the room is the prefix before the first `_` in the first
//     segment (`bugfix_idle` and `bugfix_repro_executing` both land in room
//     `bugfix`).
//
// Detail-level filtering ([DetailLevel]). The flowchart emits progressively
// more internal structure as the level rises: DetailRooms (one node per room)
// → DetailStates (leaf states inside room subgraphs) → DetailSteps (on_enter
// invoke chains as hexagons) → DetailFull (world-write and on_error nodes).
// Each level is a superset of the one below, so a single walk gated on the
// level produces all four.
//
// Transition targets are resolved relative to the owning state path
// ([resolveMermaidTarget] / [resolveTransitionTarget]): "." is self, "../"
// segments pop levels, a bare or dotted name is absolute. Targets containing
// a template expression ("{{ ... }}") are unresolvable at diagram time and are
// dropped — diagrams show only statically known edges.
//
// # Invariants
//
// [Rooms] keeps three views of one partition in sync: every path in
// Members[room] maps back to room in RoomOf, and Order lists exactly the keys
// of Members. All three are sorted for deterministic output, so re-running any
// emitter on the same AppDef yields byte-identical bytes — the export tooling
// and golden tests depend on this.
//
// Mermaid node IDs are derived purely from state paths ([mermaidStateID],
// [fcid]): dots, slashes, dashes and spaces become underscores, and a
// leading digit is prefixed with "N". The same helpers are used by the
// emitter and by [buildNodeMap], so the sidecar IDs in a [FlowchartResult]
// match the IDs in its Source exactly.
//
// # Worked example
//
// Given a two-room tavern app — a compound `bar` (initial child `dark`,
// transition `light_lamp` → `lit`, then `leave` → `street`) and a terminal
// `street` — [GroupRooms] partitions it into:
//
//	order:  [bar street]
//	bar:    [bar bar.dark bar.lit]   (the compound plus its two leaves)
//	street: [street]
//
// [FlowchartBytes] at [DetailRooms] collapses each room to one node and keeps
// only the cross-room edge:
//
//	flowchart LR
//	  Start(["<b>Start</b>"]):::input
//	  RI_bar[/"phase 0 · bar (3 states)"/]:::room
//	  RI_street[/"phase 1 · street (1 state)"/]:::room
//	  Start --> RI_bar
//	  RI_bar -- "leave" --> RI_street
//	  …classDef block…
//
// while [MermaidBytes] keeps the full topology, nesting the two leaves inside
// the `bar` compound block and drawing every intent edge. Runnable forms of
// both traces live in [ExampleFlowchartBytes] and [ExampleMermaidBytes].
//
// # Contracts
//
// All exported functions are pure and read-only over their [app.AppDef]
// argument; the package holds no shared mutable state, so every entry point is
// safe for concurrent use. Diagram functions accept a possibly-nil or empty
// AppDef and emit a well-formed but contentless diagram rather than panicking.
//
// The zero value of [DetailLevel] is [DetailRooms]; callers that want the
// common "states inside rooms" view must pass [DetailStates] explicitly (the
// CLI default). The zero value of [FlowchartFilter] selects every room.
//
// Errors are narrow. [ParseDetailLevel] errors on an unknown level name.
// [FlowchartFilter.Validate] errors when --room is combined with --from/--to
// or only one end of a range is set. [ResolveFilterRooms] errors when a
// filter names a room that does not exist. [ExportMermaidRooms] surfaces the
// errors from its injected mkdir/write callbacks. The diagram emitters
// themselves return only the error from the underlying [io.Writer].
//
// # Non-goals
//
//   - No real-time animation or current-state highlight overlay. A live
//     "you are here" view is the TUI's job; viz emits static documents.
//   - No 3D or interactive layout. Output is text (DOT / Mermaid) handed to an
//     external renderer (Graphviz, mmdc); viz never positions nodes itself.
//   - No external graph library beyond github.com/emicklei/dot, and that one
//     only for the DOT mode. Both Mermaid modes are hand-rolled string
//     templating so the exact emitted syntax stays under this package's
//     control (mermaid-cli is fussy about edge/text-size limits).
//   - No Kind="transition" entries in a [FlowchartResult] NodeMap. Mermaid
//     flowchart edges have no clickable node ID to key a mapping against, so
//     the sidecar covers nodes (states, effects, world writes) only.
//
// # Reference
//
// The user-facing `kitsoki viz` invocation — DOT vs Mermaid vs flowchart,
// `--detail`, `--rooms`, and rendering with Graphviz/mmdc — is documented in
// docs/guide/development/developer-guide.md. The state-machine concepts the
// diagrams depict (rooms, compound/parallel states, transitions, on_enter
// effects) are defined in docs/stories/state-machine.md.
package viz
