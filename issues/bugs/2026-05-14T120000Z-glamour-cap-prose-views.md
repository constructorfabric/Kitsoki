---
id: 2026-05-14T120000Z-glamour-cap-prose-views
title: "Prose `view:` blocks don't expand past their hand-wrapped width on wide terminals"
target: kitsoki
filed_at: 2026-05-14T12:00:00Z
status: open
severity: P3
component: tui
kitsoki_rev: 75c4f11
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-05-14T120000Z-glamour-cap-prose-views.md"
---

## Body

The TUI runs every state's `view:` through Glamour with
`glamour.WithPreservedNewLines()`. That's required for structured
views (menu-like bullet lists, the Terminal Room's `propose "…"`
indented examples) where each authored line must stay on its own
line. But the same setting CAPS pure-prose views at their authored
wrap width — `testdata/apps/cloak/`'s foyer is hand-wrapped at ~65
chars/line, so it sits in a narrow column even on a 150-col terminal.

Shrinking works (Glamour re-wraps longer-than-panel lines); growing
past the authored wrap is a no-op.

### Steps to reproduce

1. `kitsoki run testdata/apps/cloak/app.yaml` on a 150-col terminal.
2. Land in `foyer`.
3. The prose view sits at ~65 cols; the right half of the panel is
   blank.

### Expected vs actual

**Expected:** prose blocks reflow to the available panel width;
structured blocks (lists, code) preserve layout.

**Actual:** every authored line is preserved verbatim regardless of
panel width; prose can't grow.

### Proposed fix

Introduce a typed view-element system per `ideas.md`'s
"Tech debt — View rendering: unify structured + prose content"
section. Authoring becomes:

    view:
      - prose: |
          You are in a spacious hall…
      - list:
          title: "Available areas"
          items:
            - { key: "Start a task", value: "tickets" }
      - kv:
          Workspace: "{{ world.current_workspace }}"
      - template: |
          (escape hatch — legacy Glamour pipe)

Per-element renderers in `internal/tui/elements/`. The existing
`view: "<string>"` form keeps today's Glamour-rendered behaviour
(mapped to `- template: <string>` internally), so the migration is
opt-in.

### Severity rationale

P3 — purely cosmetic. cloak's narrow column is the only visible
hit today; dev-story's structured views already render correctly
because their list-like content matches what `WithPreservedNewLines`
expects.

### Files involved

- `internal/tui/transcript.go` — `renderMarkdown` and the Glamour
  config.
- `internal/tui/` — would gain an `elements/` subpackage.
- `docs/stories/authoring.md` — view-element authoring story.
- `ideas.md` — has the full design sketch under "Tech debt".

### Not blocking

The `ideas.md` analysis flags this as not blocking ("Pick this up
when either (a) a new app needs column-aligned lists that don't
break on resize, or (b) someone tries to add richer UI"). This bug
file exists so the dogfood pipeline has a real-but-low-stakes seed
to work against during the Phase 3 PoC.
