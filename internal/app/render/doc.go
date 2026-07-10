// Package render turns a loaded [app.AppDef] into a publishable Markdown
// document. It sits at the end of the app pipeline — downstream of
// [app.Load] (YAML parse + validate) and orthogonal to the orchestrator
// that executes the app: render reads the same in-memory AppDef the
// runtime uses and emits a human-readable artifact for review and version
// control. The output is one-way; YAML stays the source of truth and this
// package never parses Markdown back into an AppDef.
//
// # Algorithm
//
// [Markdown] drives a fixed, single-pass pipeline over the AppDef. Sections
// are emitted in a reader-first order — identity first, then the visual map,
// then the detailed reference tables — and each section is skipped when its
// backing data is empty:
//
//  1. Title — app title (falling back to ID) plus a version/author/license
//     meta line.
//  2. Overview — app ID, entry room link, and counts of rooms, intents, and
//     world variables, plus the host allow-list when present.
//  3. State diagram — a Mermaid flowchart: one node per state path (flat and
//     nested), one labelled edge per transition.
//  4. World variables — a table of name, type, default, and allowed values.
//  5. Intents — per intent: title, description, priority/hidden/examples
//     bullets, and a slot table.
//  6. Rooms — per state (depth-first via [app.AppDef] nesting): flags
//     (root/terminal/compound/parallel/mode), description, relevant
//     world/slots, menu, view source, on-enter effects, a transition table,
//     and any timeout.
//  7. Off-path — the escape-hatch trigger/banner/return, when configured.
//  8. Footer — a "generated, do not edit" notice.
//
// # Invariants
//
// The output is deterministic: every map (states, intents, world vars,
// transitions, effect key/value sets) is walked in sorted-key order, so the
// same AppDef always renders byte-for-byte the same document. This is what
// lets the rendered file be committed as a work product and diffed
// meaningfully — see [render_test.TestMarkdown_CloakGolden], which locks the
// cloak example against a golden file.
//
// Cross-references resolve within the document: transition targets that name
// a declared room link to that room's anchor, and intent cells link to the
// intent's anchor. Template targets (e.g. "{{ world.prev_state }}") and
// relative path targets are rendered as plain code spans, not links, because
// they have no single static anchor.
//
// # Worked example
//
// Given a two-room app (root "start" → "end" on the "open" intent, one int
// world var "score"), [Markdown] emits, in order:
//
//	# Two Doors
//
//	**Version** 0.1.0
//
//	## Overview
//
//	- App ID: `doors`
//	- Entry room: [`start`](#room-start)
//	- Rooms: 2
//	- Intents: 1
//	- World variables: 1
//
//	## State Diagram
//	...mermaid flowchart, then World Variables, Intents, Rooms, footer.
//
// A runnable form of this trace lives in [render_test.ExampleMarkdown].
//
// # Non-goals
//
//   - No round-trip Markdown↔YAML parsing. YAML is the source of truth; the
//     document is a derived, read-only artifact, so there is nothing to parse
//     back.
//   - No incremental or streaming rendering. The document is a single
//     committed work product rendered in one pass; there is no partial
//     output to stream.
//   - No output formats beyond Markdown. The format is fixed (GitHub-flavored
//     Markdown with embedded Mermaid); callers wanting other formats render
//     the AppDef themselves.
//   - No template-driven output customization. The section set and order are
//     deliberately fixed so that every rendered app reads the same way and
//     diffs stay stable.
//
// # Reference
//
// The app pipeline this package terminates — load, validate, render — is
// described in docs/guide/development/developer-guide.md. The state-machine
// vocabulary the rendered tables surface (rooms, intents, transitions,
// effects, off-path) is documented under docs/stories.
package render
