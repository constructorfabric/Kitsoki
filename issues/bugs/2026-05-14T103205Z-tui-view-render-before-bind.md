---
id: 2026-05-14T103205Z-tui-view-render-before-bind
title: "TUI view templates render BEFORE on_enter binds — first frame shows '(pending)' even when host call already returned"
target: kitsoki
filed_at: 2026-05-14T10:32:05Z
status: fixed
severity: P2
component: tui
kitsoki_rev: 75c4f11
trace_ref: "031c8bda — advisory lint; runtime fix predates in main"
external: {}
assignee: ""
url: "issues/bugs/2026-05-14T103205Z-tui-view-render-before-bind.md"
---

## Body

When a state's `on_enter:` chain includes a synchronous `invoke:` with
a `bind:` projection (e.g. `iface.vcs.diff` binding to
`world.feature_branch_diff`), the bound world key is populated AFTER
the first TUI view is rendered for that state. The render uses the
PRE-bind world snapshot, so any template referencing the bound key
shows the schema default (typically `""` or `0`) until the next intent
turn refreshes the view.

The contract notes in `docs/proposals/notes/dev-story-implementation-
contract.md` §W2.8 acknowledge this and recommend authoring
defensively with the `??` fallback operator:

    {{ world.pr_url ?? "(pending)" }}

This works but is easy to forget. The bugfix story's checkpoint rooms
all comply; the pr-refinement rooms mostly comply; new authors hit
this on day one.

### Steps to reproduce

1. Author a state with `on_enter: - invoke: iface.X.Y / bind: { foo: result }`.
2. Author its `view:` referencing `{{ world.foo }}` without `??`.
3. Run the app and transition into the state.
4. First frame: foo is the world schema default. Hit any intent;
   second frame shows the bound value.

### Expected vs actual

**Expected:** the TUI either (a) defers rendering until on_enter
binds complete, or (b) the loader flags `view:` references to
bind-targets that lack a `??` fallback and emits a warning.

**Actual:** silent first-frame stale render. No diagnostic.

### Proposed fix sketch

Option 1 — Runtime: re-render after `on_enter` chain completes
(orchestrator-side hook).

Option 2 — Loader lint: walk every state's `on_enter` binds, collect
the LHS keys, then walk the same state's view template and flag any
`{{ world.<key> }}` reference to a bind-target that doesn't appear
inside a `??` expression.

Option 1 is correct; option 2 is the cheap mitigation.

### Severity rationale

P2 because there's a documented workaround (`??` fallback) and no
state-machine correctness issue — only a one-frame visual artifact.
Upgrade to P1 if a future room's view-template default is misleading
enough to confuse the operator.

### Files involved

- `internal/tui/transcript.go` — view render entry point.
- `internal/orchestrator/orchestrator.go` — `runOnEnter` ordering vs
  TUI subscription notification.
- `docs/stories/authoring.md` — would document either the runtime fix or the
  `??` discipline.
