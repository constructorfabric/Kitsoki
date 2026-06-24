# TUI: View proofing tooling

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   [view-rendering-readability.md](view-rendering-readability.md) (slice 4)

## Why

Nothing renders "what does room `proposing` look like at 40 / 80 / 120
cols, in the TUI and the web?" or lints "this prose has embedded `\n`;
this code line is 140 cols wide; this view won't reflow." `kitsoki
render` (`cmd/kitsoki/render.go`) renders *app documentation*, not room
views. So authors — especially AI authors driving the
`kitsoki-story-authoring` skill — hand-wrap prose by eye against a
phantom 80-col terminal, and the result is the corpus of damage the epic
catalogues (`stories/dev-story/rooms/proposal.yaml:36`,
`stories/bugfix/rooms/idle.yaml:32`, …).

This slice is the safety net for the whole epic: the lint and golden
corpus are what slices 1–3 verify against, and they're what makes an AI
author "produce excellent results consistently."

## What changes

**One sentence:** a `kitsoki view` command renders any room view across
widths and environments and runs a lint catalog with `file:line` + fix;
a shared element fixture corpus enforces the render contract for both
backends via golden + property tests; the authoring skill requires a
proof pass.

## Impact

- **Code:** new `cmd/kitsoki/view.go` (`kitsoki view`), new
  `internal/render/lint`, a shared fixture corpus under
  `internal/render/elements/testdata/` consumed by both Go golden tests
  and the Vue (`tools/runstatus/`) component tests.
- **Rendering:** drives the real `elements.RenderAll` (same code the TUI
  uses) — never a re-implementation, so the proof matches production
  (`tools/runstatus/CLAUDE.md`: no UI hacks, the output must be real).
- **Input:** new CLI command + flags; no TUI input change.
- **Docs on ship:** `docs/tui/`, `.agents/skills/kitsoki-story-authoring/SKILL.md`.

## Mental model

`kitsoki view` is to room views what `kitsoki render` is to app docs: a
read-only proof of a single artifact. `--lint` is the spell-checker an
author runs before declaring a view done.

## Rendering changes

`kitsoki view` renders through `elements.RenderAll` for `--env tui`
(ANSI) and `--env plain` (stripped), and for `--env web` snapshots the
shared fixture corpus through the Vue render path (headless optional;
default is the fixture-corpus comparison — Epic open question 3). No new
element kinds; this is a consumer of the existing dispatcher.

```
kitsoki view <app.yaml> <state-path> [flags]
  --width 40,80,120     render at each width (default 80)
  --env   tui|web|plain render as TUI (ANSI) / web (HTML) / plain text
  --world k=v,…         seed world vars (to drive when:/template branches)
  --lint                run the lint catalog; exit non-zero on error-level
  --all                 walk every reachable room; lint the whole story
```

### Lint catalog (`internal/render/lint`)

Each finding is `file:line · severity · rule · fix`.

| Rule | Severity | Catches | Fix |
|------|----------|---------|-----|
| `embedded-hardbreak` | warn | `prose:` Source contains `\n`/`\n\n` | split into multiple `prose:` elements |
| `hand-wrapped-prose` | warn | ≥3 prose lines ending within ±3 of a common column | use a folded scalar `>` or one logical line |
| `legacy-scalar-view` | info | view uses scalar `view: \|`, not typed elements | migrate to typed elements (see `--fix`) |
| `overflowing-code` | warn | a `code:` line exceeds the narrowest target width | shorten, or accept horizontal scroll |
| `markdown-in-prose` | warn | tables/blockquotes/nested lists/headings inside `prose:` | use the matching typed element |
| `wide-kv-key` / `wide-list-label` | warn | key/label squeezes value/hint column below the floor at 80 | shorten the key/label |
| `say-then-view` | info | transition `say:` prepended to a typed view | move into a leading `prose:` element |
| `wont-reflow` | info | `extends:`/`template_file:` view (no per-element reflow on web) | acknowledge, or migrate chrome to typed |

`--fix` (optional, later) applies the mechanical fixes: scalar→elements
via slice 1's adapter, `\n`-split prose, `say:`→prose.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| `kitsoki view <app> <state>` | render a room view | `--width`, `--env`, `--world` |
| `kitsoki view … --lint` | render + lint, non-zero exit on error | CI-friendly |
| `kitsoki view <app> --all --lint` | lint every reachable room | story-wide proof |

## Rendering tests

The contract enforcement — a shared corpus, both backends:

- **Cross-environment golden corpus.** A representative element corpus
  (one of each kind + the awkward cases: wide `kv` key, long `list`
  label, multi-paragraph prose, 90-col `code`) rendered at `{40,80,120}`
  × `{plain, tui}` against Go golden files, and `{web}` against a Vue
  snapshot — all from the **same** input fixtures. **Verified to fail**
  today: there's no typed render for legacy views on the web path.
- **Property tests** (invariants, no goldens):
  - *No overflow:* for `plain`/`tui`, no `prose`/`list`/`kv` line exceeds
    the target width.
  - *Reflow idempotence:* render@W then re-wrap@W is a fixed point.
  - *Code verbatim:* `code` bytes survive every width unchanged.
  - *Width monotonicity:* a wider target never yields *more* lines for a
    reflowing element.
- **Lint self-tests:** each rule has a positive fixture (fires) and a
  negative fixture (clean), so the catalog can't silently rot.
- **`--all` smoke:** `kitsoki view stories/<each>/app.yaml --all --lint`
  runs in CI and reports (does not yet fail the build on warn-level until
  the corpus is cleaned — log what's deferred, never silently pass; see
  memory `no-silent-caps`).

## Migration plan

`kitsoki view` and the lint can land against the **current** typed corpus
before slice 1 — they immediately flag the legacy-scalar / embedded-`\n`
views as the queue of work. As slice 1 lands, `--all --lint` goes from
"mostly `legacy-scalar-view`" to clean; the golden corpus widens to cover
the newly-typed shapes.

## Tasks

```
## 1. Render
- [ ] 1.1 cmd/kitsoki/view.go: render a room view via elements.RenderAll (--width/--env/--world)
- [ ] 1.2 --env web path (shared fixture corpus; optional headless)

## 2. Lint
- [ ] 2.1 internal/render/lint: the rule catalog above, each with file:line + fix
- [ ] 2.2 --lint (non-zero on error) and --all (story-wide)

## 3. Prove + document
- [ ] 3.1 Shared fixture corpus + cross-env golden tests (verified to fail without typed web render)
- [ ] 3.2 Property tests (no-overflow, idempotence, code-verbatim, monotonicity)
- [ ] 3.3 Lint self-tests (positive + negative per rule)
- [ ] 3.4 Add a "proof the view" step to kitsoki-story-authoring SKILL.md
- [ ] 3.5 Wire kitsoki view --all --lint into CI (report-only first)
- [ ] 3.6 Update docs/tui/; trim/delete this proposal; update the epic slice row
```

## What we lose, honestly

A new CLI surface and a corpus to maintain. The golden corpus is a
maintenance cost — it must be regenerated when the element contract
intentionally changes — but that cost is the point: the contract can't
drift between TUI and web without a golden diff.

## Open questions

1. **`--fix` in this slice or a follow-up?** *Lean: follow-up* — render +
   lint is the safety net; auto-fix is a convenience that depends on
   slice 1's adapter being solid.
2. **Warn-level findings fail CI, or report-only?** *Lean: report-only
   until the corpus is cleaned, then promote to error* (memory
   `no-silent-caps` — log the deferral explicitly).

## Non-goals

- Auto-fixing views (`--fix` deferred).
- Replacing the `rendering-tests` skill's TUI-internal apparatus
  (`NewRenderingAnalyzer`) — this complements it at the view/CLI level.
- Linting non-view story content (intents, guards) — view-only.
