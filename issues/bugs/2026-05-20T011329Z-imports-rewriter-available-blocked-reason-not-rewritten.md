---
# triage-marathon: ALREADY-FIXED in main — f3030bdf (rewriteExpr helperIntentArgRE pass); live bugfix drive self-terminated (no fabrication)
id: 2026-05-20T011329Z-imports-rewriter-available-blocked-reason-not-rewritten
title: "imports rewriter: intent-name string args to available()/blocked()/blocked_reason()/intent_status() are not prefix-rewritten, so imported-room menu helpers always answer false/\"\""
target: kitsoki
filed_at: 2026-05-20T01:13:29Z
filed_by: cloud-user
status: fixed
severity: P1
component: app/imports
kitsoki_rev: c539d00
trace_ref: "/tmp/kitsoki-dogfood-trace.jsonl @ 2026-05-20T01:13:00Z"
external: {}
assignee: ""
url: "issues/bugs/2026-05-20T011329Z-imports-rewriter-available-blocked-reason-not-rewritten.md"
related:
  - 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency
---

## Body

When a story is imported under an alias (e.g. kitsoki-dev imports
dev-story as `core`), the loader rewrites every state's `on:` map
keys and every global intent name to the alias-prefixed form
(`continue` → `core__continue`). Computed menus carry the prefixed
names; the runtime intent dispatcher resolves the bare name through
`State.IntentAliases`, so typing `continue` at the prompt still
works.

But the **view-template helpers** `available('X')`, `blocked('X')`,
`blocked_reason('X')`, `intent_status('X')` take the intent name as
a **string literal argument**, and that literal is NOT rewritten by
the importer. The rewriter only touches `world.<ident>` patterns
(see `internal/app/imports_rewriter.go:349` —
`worldIdentRE = regexp.MustCompile(\`\bworld\.([A-Za-z_][A-Za-z0-9_]*)\`)`).

The net effect: every menu-helper call inside an imported sub-story
looks up the wrong key. `available('continue')` checks for `continue`
in the primary set, but the set only contains `core__continue`, so it
returns false. `blocked_reason('continue')` likewise returns `""`
because the blocked map is keyed by `core__continue`.

### Observed

Standing on `core.ticket_search` in kitsoki-dev with a ticket picked
(world.ticket_id non-empty, so the `continue` transition's first
when-arm matches and the action IS dispatchable). The rendered menu
shows:

```
- ✗ continue —
- pick_ticket id=<TKT>         pick a ticket from the list above
- search_tickets query=<text>  narrow the list
- go_back
- go_main
- look
```

The first row should be the green "continue" label whose `when:` is
`available('continue')` (lines 75-77 of
`stories/dev-story/rooms/ticket_search.yaml`); instead the fallback
`✗ continue — {{ blocked_reason('continue') }}` row (lines 78-79)
fires, with `blocked_reason('continue')` substituting to empty (so
the user sees `✗ continue —` with nothing after the em dash).

Typing `continue` at the prompt still transitions to `bf` correctly
— this is purely a view-helper miscount, not a runtime routing bug.

### Root cause

`stories/dev-story/rooms/ticket_search.yaml` lines 75-79:

```yaml
- label: "continue"
  hint:  "hand the picked ticket to the bugfix pipeline"
  when:  "available('continue')"
- label: "✗ continue — {{ blocked_reason('continue') }}"
  when:  "!available('continue')"
```

Fold-time rewrite path (`internal/app/imports.go` step 8 →
`imports_rewriter.go::rewriteState` → `rewriteView` →
`rewriteViewElement` → `rewriteExpr`):

- `on.continue` → `on.core__continue` ✓ (handled by
  `rewriteIntentRef` at imports_rewriter.go:307)
- `m.appDef.Intents["continue"]` → `m.appDef.Intents["core__continue"]`
  ✓ (handled by imports.go:404-418)
- `world.ticket_id` → `world.core__ticket_id` ✓ (handled by
  `worldIdentRE` at imports_rewriter.go:349)
- `available('continue')` → `available('continue')` ✗ — string
  literal arg is invisible to `worldIdentRE`.

The bare-name menu helpers therefore always return the "intent not
in menu" answer (false / "") inside any imported room. The longer
the import chain the worse it gets — a doubly-imported room would
need `available('core__bf__accept')` to render correctly.

### Repro

1. `kitsoki run stories/kitsoki-dev/app.yaml`
2. `tickets` → land on `core.ticket_search`.
3. `pick_ticket id=<any>` to set world.ticket_id.
4. Look at the Actions list. Expected: green `continue`. Actual:
   `✗ continue —`.

The same defect fires for `drive` and `go_pr_refinement` in
`stories/dev-story/rooms/main.yaml` lines 44-53 — every imported
room that uses these helpers is affected.

### Expected

`available('continue')` / `blocked_reason('continue')` inside an
imported sub-story should answer about the underlying intent
regardless of how many fold layers sit above it. The author writes
bare intent names; the loader is responsible for keeping helper
arguments in sync with the rewritten `on:` / intent table.

### Suggested fix sketch

In `internal/app/imports_rewriter.go::rewriteExpr` (or a sibling
helper called from the same call sites), also rewrite string
arguments to the four menu helpers. Cheap regex:

```go
var menuHelperRE = regexp.MustCompile(
    `\b(available|blocked|blocked_reason|intent_status)\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\)`)
```

Replace the captured intent name via `rewriteIntentRef` (so it picks
up the alias prefix iff the name is in `rw.childIntent`). Apply
inside `rewriteExpr` so every author-supplied template string
benefits — view sources, view-element `When`, list-item `Label` /
`Hint` / `When`, kv pair values, and transition `When` / `GuardHint`.

Alternative if a regex feels fragile: parse with pongo2 / expr-lang
and walk the call expressions. Probably overkill for v1 — every
existing call site uses the bare-string-literal form (no variable
intent names).

### Regression test

Add a test under `internal/app/` that imports a child story under an
alias, renders a view whose `when:` is `available('foo')` where
`foo` is a child intent that should be primary, and asserts that
`available` answers true (i.e. that the helper saw the rewritten
name). Companion test for `blocked_reason` returning the expected
hint.

### Notes

- Discovered via dogfood on
  `2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency`
  — story-author noticed `✗ continue —` in the rendered view and
  walked the rewriter to find the gap.
- Story-side workaround exists (rewrite the `when:` to use the
  underlying `world.X` guard expression, which IS rewritten), but
  that duplicates each room's guard logic into its view and loses
  the `blocked_reason` plumbing. Engine fix is the correct move —
  the helpers exist precisely to let authors NOT re-state guards in
  views.
- Severity P1 because every dogfood room that uses progressive-
  disclosure menu items is rendering a misleading "✗ <action>"
  pill — operators can't trust the menu colour any more.
