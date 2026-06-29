# `issues/` — kitsoki's self-tracker (DEPRECATED — see [DEPRECATED.md](./DEPRECATED.md))

> **⚠️ Deprecated, frozen archive.** kitsoki now tracks its own bugs and
> features as **GitHub Issues** on
> [`constructorfabric/Kitsoki`](https://github.com/constructorfabric/Kitsoki/issues).
> These files are kept for reference and git history; nothing should file new
> tickets here. New bugs come from `kitsoki bug create --github` / the web
> Report-bug modal, new features from the design pipeline, and the existing pile
> migrates with `kitsoki issues migrate`. See [DEPRECATED.md](./DEPRECATED.md).

This directory **was** kitsoki's own bug + feature backlog, on disk, in
plain Markdown. Each file is a YAML-frontmatter-headed `.md` per the
bug format documented inline below (and in
[`docs/stories/bugs.md`](../docs/stories/bugs.md)).

Historically the dogfood app (`.kitsoki/stories/kitsoki-dev/`) read this directory via
`host.local_files.ticket`; once the GitHub cutover lands it binds
`host.gh.ticket` instead.

## Layout

```
issues/
├── bugs/                 — open / resolved / wontfix bug reports
│   ├── <ISO-utc>-<slug>.md
│   └── <ISO-utc>-<slug>.artifacts/   — optional sibling, evidence for one ticket
│       ├── screenshot.png
│       └── har.json
└── features/             — PRD-track features (cypilot story consumes; Wave 3)
    └── <ISO-utc>-<slug>.md
```

The ISO-utc + slug filename convention buys two things:

1. **Sortable by filed-at** — `ls issues/bugs/` shows newest-last.
2. **Stable across renames** — once a bug is filed, its filename
   never changes; the `slug` is descriptive, not canonical.

### Per-ticket artifacts folder

A ticket stays a **flat `bugs/<id>.md`**. When it carries binary
evidence (a screenshot, a HAR), that evidence lives in a **sibling
`bugs/<id>.artifacts/` directory**, not a folder-form ticket, and the
body links it from a `## Artifacts` section. Web-filed bugs (the Meta
menu's *Report bug*, see [`docs/tui/web-ui.md`](../docs/tui/web-ui.md))
populate this folder automatically.

The sibling form is deliberate: the dogfood reader
(`host.local_files.ticket`, `internal/host/localfiles_ticket.go`) globs
`issues/bugs/*.md` by listing the directory and skipping entries that
are directories or don't end in `.md`
(`if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") { continue }`,
`localfiles_ticket.go:308`). A `<id>.artifacts/` directory is therefore
**doubly excluded** — it is a dir *and* lacks the `.md` suffix — so it
never appears as a ticket and discovery needs zero changes. (Verified
against the reader on 2026-06-12.)

## Frontmatter schema (see also `docs/stories/bugs.md`)

```yaml
---
id:        <same as filename, sans .md>
title:     "Short, present-tense summary"
target:    kitsoki | story         # which artifact has the bug
filed_at:  <RFC3339 UTC>
status:    open | in_progress | resolved | wontfix
severity:  P0 | P1 | P2 | P3       # P0 = blocks releases
component: <short tag, e.g. tui, runtime, loader>
kitsoki_rev: <short SHA at filing>
trace_ref: ""                       # may be empty for hand-filed seeds
external: {}                        # external tracker links (Jira id, GH issue)
repro_command: ""                   # RECOMMENDED: a deterministic command that
                                    # FAILS (non-zero exit) while the bug is
                                    # live. The bugfix story's `reproducing`
                                    # room runs it RED-first (see
                                    # stories/bugfix/README.md → "repro RED-gate")
                                    # — non-zero exit = bug reproduces → proceed;
                                    # zero exit = cannot reproduce → needs-human.
                                    # Absent/empty ⇒ the pipeline SYNTHESISES the
                                    # gate: the reproducer authors a RED test,
                                    # commits it as the discrete pre-fix
                                    # reproducer, and derives gate_command from it
                                    # — so a bare report still ships autonomously
                                    # (see stories/bugfix/README.md → "synthesised
                                    # gate"). Supplying repro_command is still
                                    # preferred: it proves the bug reproduces
                                    # BEFORE any LLM/maker budget is spent.
---
```

## Body

The body is free-form Markdown — repro steps, expected vs actual,
trace excerpts, design notes. The convention is one `## Body` heading
introducing the prose; the dogfood pipeline doesn't enforce it but
both the rendered TUI view and the LLM-judge prompts read the body
contents wholesale.

## Comment thread

The dogfood transport (`host.append_to_file`, bound at
`.kitsoki/stories/kitsoki-dev/app.yaml`) appends `## Comment <RFC3339> by
<author>` blocks at the bottom of the file when checkpoint artifacts
fire. **The bug file IS the conversation log** — proposals, judge
verdicts, operator acks, and the resolution decision all live in the
same Markdown.

A typical resolved-bug file:

```
---
id: 2026-05-14T103205Z-tui-hangs-on-esc
status: resolved
resolved_at: 2026-05-14T15:00:00Z
resolved_in_commit: abc123
…
---

## Body
Esc hangs the TUI…

## Comment 2026-05-14T14:00:00Z by kitsoki
**Reproduction artifact:** …confirmed; root cause is …

## Comment 2026-05-14T14:02:13Z by llm-judge
Verdict: pass (confidence 0.91); reason: …

## Comment 2026-05-14T14:05:01Z by user
accept

…
```

## Workflow

1. **File** — by hand for now (`cp issues/bugs/_template.md issues/bugs/<new>.md && $EDITOR …`).
   the `kitsoki bug create` CLI emits a properly-formed file from a
   TUI prompt, and `/meta kitsoki bug` and `/meta story bug` triggers
   compose a bug-reporter agent that calls `kitsoki bug create`
   itself.
2. **Search** — `kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml`, then
   `tickets` → `search open kitsoki bugs`. The local-files provider
   scans `issues/bugs/*.md` and matches on title + body substring.
3. **Work** — pick a ticket, type `bugfix`, walk the pipeline.
4. **Close** — on `merge`, the bugfix→pr handoff fires
   `iface.ticket.transition` with `to: resolved`, flipping the
   frontmatter in place.

See `.kitsoki/stories/kitsoki-dev/README.md` for the full operator walkthrough
(both supervised and `llm_then_human` autonomous variants).

## Status: open seeds today

Two real kitsoki bugs are filed here as the smoke-test seeds for the
Phase 3 dogfood acceptance:

| File | Bug | Status |
|---|---|---|
| `bugs/2026-05-14T103205Z-tui-view-render-before-bind.md` | Views render BEFORE binds; templates referencing post-bind world keys must default with `??` or the first frame shows pending. Documented in `docs/proposals/notes/dev-story-implementation-contract.md` §W2.8 but not yet codified as a lint. | open |
| `bugs/2026-05-14T120000Z-glamour-cap-prose-views.md` | Prose `view:` blocks are hand-wrapped at ~65 chars and don't expand past that on wide terminals because Glamour's `WithPreservedNewLines` caps reflow. `ideas.md` §"Tech debt — View rendering" has the full analysis. | open |

The story-bug variant of the same loop also has a seed under
`stories/oregon-trail/issues/bugs/` so the dogfood's multi-glob
`ticket_globs:` is exercised end-to-end.

## Out of scope here

- `features/` is empty (modulo a placeholder README). cypilot's
  story consumes feature files; that lands in Wave 3 / Phase 5.
- The `external:` frontmatter block is unused by the local-files
  provider; it's reserved for the GitHub + Jira providers that will
  ship in Waves 3+.
