// Package workspace defines the typed `$workspace` world variable and the
// flow that populates it. It sits between the orchestrator's host layer and
// [kitsoki/internal/world]: [Load] calls the `host.workspace_manager.get`
// host function, decodes the result into a [Workspace], and writes the
// snapshot into [world.World] under [WorldKey] so guards and view templates
// can read repos, branches, the linked issue, and PRs as ordinary world data.
//
// The `$workspace` variable holds structured information about the current
// workspace — its repositories and their checked-out branches, an optional
// linked issue, and any associated pull requests. It is loaded when a
// Workspace Room is entered and refreshed on explicit user action; nothing
// in this package polls or watches for changes.
//
// # Algorithm
//
// There is no decision logic here — the package is a typed shim around one
// host call plus a world<->struct codec. The two directions are:
//
//	Load (host -> world):
//	 1. Invoke host.workspace_manager.get (passing workspace_id only when
//	    a non-empty id is supplied; otherwise the handler picks the current
//	    workspace).
//	 2. JSON round-trip the handler's result data into a Workspace
//	    (parseWorkspaceFromData marshals the map and unmarshals it back
//	    through the json tags, so field mapping follows the struct tags
//	    rather than hand-written key lookups).
//	 3. Validate required fields; on success write ws.ToMap() under
//	    WorldKey and return the new world.
//
//	FromMap / ToMap (world <-> struct):
//	 ToMap projects a Workspace to a map[string]any with omitempty fields
//	 dropped; FromMap is its tolerant inverse, reading whatever keys are
//	 present and ignoring anything malformed (it never errors).
//
// # Invariants
//
//   - The world snapshot is a plain map[string]any (it must cross the MCP
//     boundary and round-trip through SQLite), so ToMap emits only strings,
//     bools, and []any — never the Go structs themselves.
//   - ToMap and FromMap are inverses for any Workspace that passed
//     [Workspace.Validate]: id, root_path, and at least one repo survive the
//     round-trip; the omitempty fields (issue_id, pr_ids) round-trip only
//     when set.
//   - Required fields are id, root_path, and repos[≥1] with each repo's path
//     non-empty. issue_id and pr_ids are optional and carry the omitempty
//     JSON tag, so an absent value and an empty value are indistinguishable
//     once stored.
//   - Load is the only path that validates. ToMap, FromMap, SetInWorld, and
//     ClearFromWorld trust their input and never validate.
//
// # Worked example
//
// A handler returns a single-repo workspace with a linked issue; Load
// validates it and writes the snapshot into world:
//
//	host result data:
//	  {id: "ws-1", root_path: "/home/u/app",
//	   repos: [{path: "/home/u/app", branch: "main", dirty: false}],
//	   issue_id: "PROJ-123"}
//
//	parse  -> Workspace{ID:"ws-1", RootPath:"/home/u/app",
//	                     Repos:[{Path:"/home/u/app", Branch:"main"}],
//	                     IssueID:"PROJ-123"}
//	validate -> ok (id, root_path, repos[0].path all present)
//	ToMap  -> {"id":"ws-1", "root_path":"/home/u/app",
//	           "repos":[{"path":"/home/u/app","branch":"main","dirty":false}],
//	           "issue_id":"PROJ-123"}        (pr_ids dropped: empty)
//	world  -> w.With("$workspace", <that map>)
//
// A runnable form of this round-trip lives in [Example].
//
// # Lifecycle
//
// A Workspace Room calls [Load] on entry to seed `$workspace`, then reads it
// back through [FromMap] (or directly via [world.World.Get]) whenever it
// renders. [SetInWorld] stores an already-built Workspace without a host
// round-trip — used by tests and by callers that constructed the value
// themselves. [ClearFromWorld] drops the key when leaving the room. The
// struct carries no resources and is not retained across turns: each turn
// reads the snapshot out of world, never a long-lived *Workspace.
//
// # Contracts
//
//   - The zero Workspace is NOT valid — id, root_path, and repos are all
//     empty, so [Workspace.Validate] fails. Always obtain one through [Load]
//     or construct it explicitly with the required fields set.
//   - [FromMap] returns nil for a nil or non-map argument and never panics;
//     callers must nil-check. [SetInWorld] is a no-op (returns the input
//     world) when given a nil Workspace.
//   - All exported functions are pure with respect to their inputs: [world.World]
//     is immutable and copy-on-write, so Load, SetInWorld, and ClearFromWorld
//     return a new world rather than mutating the one passed in. They hold no
//     package-level state and are safe for concurrent use provided callers do
//     not share a single *Workspace across goroutines while mutating it.
//   - [Load] returns the unchanged input world together with an error when the
//     host invoke fails, when the handler reports a non-empty Result.Error,
//     when the result data is empty/unparseable, or when validation fails.
//
// # Non-goals
//
//   - No schema-DSL validation. [Workspace.Validate] checks required-field
//     presence only; it does not enforce a JSON Schema, value formats, or
//     cross-field rules. The struct is deliberately provisional (see
//     Reference) and gains fields only when a concrete room needs them.
//   - No multi-workspace state. Exactly one workspace lives under [WorldKey]
//     at a time; switching workspaces is a fresh [Load], not a collection.
//   - No live reloading or watching. The snapshot reflects the moment of the
//     last [Load]; staleness (a branch checked out after load, say) is the
//     caller's concern and is resolved by loading again.
//   - No reconciliation of conflicting fields. [FromMap] is permissive and
//     last-write-wins on the map it is given; it is not a merge.
//
// # Reference
//
// Typed workspace context is the `internal/workspace` row of the package map
// in docs/architecture/overview.md (section 11.3), loaded by the
// `host.workspace_manager.get` host function. The Workspace Room and the
// background-work model that motivates a refreshable workspace snapshot are
// described in section 6 (Long-running work and notifications) of the same
// document. The struct is intentionally provisional: it carries only the
// fields current rooms consume and is expected to grow, so callers should
// treat unknown future fields as additive.
package workspace
