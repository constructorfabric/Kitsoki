# Epic: Review edits where you actually read them

**Status:** In progress. **Slice #2 (tui-md-links) shipped** — OSC 8 `.md`
links + `/open`, migrated to [`docs/tui/README.md`](../tui/README.md) and its
file deleted. **Slice #1 (diff-open-fallback) Phase A shipped + adopted** —
`host.diff.open` with IDE verdict capture + difftool fallback, documented in
[`docs/architecture/hosts.md`](../architecture/hosts.md#hostdiffopen--review-a-change-in-the-best-surface),
adopted in the bugfix `reviewing` room with four flow fixtures. Epic stays open
only for slice #1's **Phase B turn-suspend gate** + the live-socket verdict
capture (ide-integration #1).
**Kind:**   epic
**Slices:** 2 (1 shipped, 1 in progress)

## Why

When kitsoki edits a file or proposes a change, the operator's best review
surface is the one they already have open — VS Code, or failing that the
system diff viewer / `$EDITOR` — not a cramped terminal pane. The web UI
already knows this: it renders a `.md` artifact path in a `kv` block as a
clickable button that opens the file in a modal (`tools/runstatus/src/components/ViewElement.vue:165-177`
+ `MarkdownModal.vue`). The TUI has **no** equivalent: the same kv value is
inert plain text (`internal/render/elements/kv.go:82-97`), and there is no
way at all to push a *diff* out to an external reviewer — `bugfix` and
`dev-story` review changes by printing summaries into the terminal or
opening a file read-only via `host.ide.open_file`.

This epic gives the TUI two "review where you actually read" affordances:
terminal-friendly clickable links to markdown artifacts, and a "we edited
*X* — want to review the diff?" room pattern that opens the change in the
connected IDE (capturing the operator's accept/reject as a recorded
decision) or falls back to the system diff viewer when no IDE is attached.

## What changes

Once both slices ship:

- A new **`host.diff.open`** host call: given a change (a path + proposed
  content, or already-applied edits), it opens a diff in the connected IDE
  link — reusing the `host.ide.open_diff` plumbing (`internal/host/ide_handlers.go:298`)
  — and returns the operator's **accept/reject verdict** as a recorded gate
  decision. When no IDE is connected it shells a **system difftool**
  (`git difftool` / `$KITSOKI_DIFFTOOL` / `code --wait -d`), which is
  view-only: it returns `verdict: null, reviewed: true`, and the room falls
  back to asking accept/refine as a normal TUI intent.
- A reusable **"review-diff" room pattern**, documented and adopted in one
  real story, that branches on whether a verdict came back.
- The TUI renders `.md` artifact paths in `kv` values as **OSC 8 terminal
  hyperlinks** (mirroring the web's `isMarkdownPath` detection), with a
  **`/open <path>`** slash command as the universal fallback for terminals
  without OSC 8 — also the verb the review-diff room points at for "open the
  changed files."

## Impact

- **Spans:** runtime (the host call + verdict capture + difftool fallback),
  tui (OSC 8 links + `/open`).
- **Net surface:** one new host call, one verdict-capture seam in the turn
  loop (the deferred `ide-integration.md` follow-up #2), one OSC 8 hyperlink
  helper + one slash command.
- **Docs on ship:** `docs/architecture/hosts.md` (`host.diff.open`),
  `docs/stories/state-machine.md` (the verdict gate, if the turn-suspend
  variant lands), `docs/tui/README.md` (OSC 8 links + `/open`), and a
  "review pattern" note in `docs/stories/story-style.md`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | diff-open-fallback | runtime | `host.diff.open`: connected-IDE verdict capture + system-difftool fallback + the review-diff room pattern | `ide-integration.md` #1/#2 | Phase A shipped + adopted; Phase B remains | [`diff-open-fallback.md`](diff-open-fallback.md) |
| 2 | tui-md-links | tui | OSC 8 hyperlinks for `.md` kv values + `/open` command | — | Shipped → [`docs/tui/README.md`](../tui/README.md) | (deleted) |

## Sequencing

```
#1 (runtime) ──┐ independent; both can ship in parallel
#2 (tui)    ───┘ #1's room uses #2's /open verb (soft dependency, not hard)
```

The slices are independent. #2 touches only rendering and has no engine
dependency, so it can land first and alone. #1 is the headline and the
harder one (it picks up the verdict-capture seam `ide-integration.md` #2
flagged as missing). The review-diff room in #1 *points at* the `/open`
verb from #2 to surface "here are the changed files," so #2-before-#1 is a
nice-to-have, not a blocker.

## Shared decisions

1. **Degradation is explicit, never silent.** Both slices degrade by
   capability and always tell the operator which surface they're getting:
   links render as OSC 8 where the terminal supports it and as a plain,
   `/open`-able path otherwise; a diff opens in the IDE *with a real verdict*
   where a link is attached and in a view-only system tool *without* one
   otherwise. We never fabricate a verdict, and never silently no-op
   (CLAUDE.md: principle of least surprise; the operator-ask doc's
   no-replacement-tool stance is the model).
2. **Detection mirrors the web's `isMarkdownPath`** — `/\S+\.md$/` on the
   rendered kv value (`ViewElement.vue:127`) — so web and TUI agree on what
   a "linkable artifact" is without inventing a new schema field. Whether to
   promote this to an explicit element field is a per-slice open question
   in #2.

## Cross-cutting open questions

1. **Verdict-bearing difftool?** The connected IDE's `openDiff` returns
   accept/reject; a plain system difftool cannot. Should we ever wrap a
   difftool to synthesize a verdict (e.g. an exit-code convention), or
   always degrade the fallback to view-only? *Lean: always view-only — the
   room captures the decision as the operator's next intent, which is what
   it already does today.*

## Non-goals

- A TUI-internal diff viewer or markdown pager. We push **out** to external
  tools the operator already trusts; we do not build one.
- JetBrains diff parity (`ide-integration.md` #4) and IDE auto-connect
  (`ide-integration.md` #5) — both inherited as-is.

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's own plan, then delete the child file.
  When both slices have shipped, delete this epic too.
-->
