// Package effect defines the effect taxonomy — the classification substrate
// every host-call operation and every agent is measured against, replacing
// the overloaded `external_side_effect: bool` that internal/app and
// internal/host used to read two incompatible ways (task replay mode vs.
// converse read-only posture). See docs/proposals/effect-taxonomy.md for the
// design rationale.
//
// The package sits between internal/app (the YAML loader, which resolves an
// agent's or a host_interfaces op's declared/inferred [Effect] at load time)
// and internal/host / internal/machine / internal/orchestrator (the runtime,
// which enforces converse read-only posture, task replay mode, and records
// the resolved class on host-call events). It has no dependency on any of
// them — a leaf package, like internal/bashprofile — so the classification
// table is defined exactly once and every layer reads the same values.
//
// # Two orthogonal axes
//
// [Effect] classifies WHAT a call touches: [Pure] | [Read] | [Write] |
// [External], an ordered ladder where each tier is a superset of the last's
// blast radius. Deterministic (a plain bool, not a type of its own) is
// orthogonal: whether RE-RUNNING the call reproduces its result. The two
// cannot merge into one enum — a tool-less `agent.extract` call touches
// nothing ([Pure]) yet is non-deterministic (it's an LLM call).
//
// # Agents derive, host calls declare
//
// A host-call operation declares its effect class (with a sensible builtin
// default per verb — see [ClassifyVerb]). An agent's effect class is instead
// the JOIN (least-upper-bound) over its tool surface: the most-privileged
// tool wins. [FromTools] computes this join; [ToolClass] classifies one tool
// name (accepting both the "host."-prefixed form internal/app's loader
// normalises tool names into, and the bare Claude-CLI tool name, so callers
// on either side of that normalisation boundary get the same answer).
//
// # Contracts
//
// [Join] and [ToolClass] fail closed: an effect value outside the four
// recognised tiers, or a tool name absent from the classification table (an
// external MCP server the table doesn't know about), is treated as
// [External] — the taxonomy assumes maximum privilege for anything it can't
// classify, rather than silently trusting it. [LessEqual] mirrors this: an
// unrecognised value on either side returns false, so a caller comparing
// against an invalid Effect never mistakes "unknown" for "safely read-only."
//
// [FromLegacyBool] maps the deprecated `external_side_effect: bool` onto this
// ladder for the one-release migration window: true -> [External];
// false -> [Write] when the tool surface includes a mutator (Write/Edit/
// MultiEdit/NotebookEdit/Bash), else [Read]. It deliberately ignores network
// tools (WebFetch/WebSearch) when mapping — a false declaration alongside a
// network tool is exactly the contradiction internal/app's load-time
// hard-fail exists to catch (declared <= [Read] but the real tool-surface
// join is [External]), not something this mapping should paper over.
//
// # Non-goals
//
//   - No enforcement. This package names the classes; internal/host applies
//     them (converse tool policy, task replay mode) and internal/app
//     validates declarations against them. Keeping enforcement out keeps the
//     enum importable by every layer without dragging in runtime machinery.
//   - No per-tool permission grading finer than the four-tier ladder.
//   - No toolbox vocabulary or unified enforcement. Those live in the app/host
//     layers and are documented in docs/architecture/hosts.md and
//     docs/stories/state-machine.md; this package only supplies the class they
//     read.
package effect
