---
assignee: ""
component: host
external: {}
filed_at: "2026-05-18T04:52:57Z"
filed_by: cloud-user
id: 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency
kitsoki_rev: 3ff850e
related:
- 2026-05-14T103205Z-tui-view-render-before-bind
severity: P2
status: resolved
target: kitsoki
title: "localfiles_ticket: rank tickets by severity + recency (today: id-ASC and priority field is always empty)"
trace_ref: ""
url: issues/bugs/2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency.md
---

## Body

The dogfood ticket browser (`core.ticket_search`, now proactive on
entry — see `stories/dev-story/rooms/ticket_search.yaml`) renders the
list returned by `iface.ticket.search`, but the host's
`host.local_files.ticket` provider doesn't return the list in any
useful order, and the per-ticket "priority" badge is always blank.
Two distinct defects, one rendered symptom:

### 1. `bugSummary` reads `priority:` but bug files use `severity:`

`internal/host/localfiles_ticket.go:473-482`:

```go
func bugSummary(b *BugFile) map[string]any {
    return map[string]any{
        "id":       b.ID,
        "title":    b.titleString(),
        "status":   b.frontString("status"),
        "priority": b.frontString("priority"),   // ← reads "priority"
        "assignee": b.frontString("assignee"),
        "url":      b.frontString("url"),
    }
}
```

But the on-disk schema (per `issues/README.md` and
`docs/proposals/bug-format-proposal.md` §2) uses
`severity: P0|P1|P2|P3`, **not** `priority:`. Existing bug files
confirm — every file under `issues/bugs/` has `severity:` set and
`priority:` absent. Result: `t.priority` is the empty string for
every locally-filed ticket, and the new ticket_search view's
`{% if t.priority %} · {{ t.priority }}{% endif %}` badge never
appears.

### 2. `listAllBugs` sorts by id ASC — no severity weighting, oldest first

`internal/host/localfiles_ticket.go:295`:

```go
sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
```

Because IDs start with an ISO-UTC timestamp, this is effectively
filed_at-ASC (oldest first). The ticket_search view papers over half
of this with `world.ticket_results|reverse` to get newest-first, but
that's a view-side hack and ignores priority entirely. A P0 filed
last week sorts below a P3 filed yesterday — wrong for any
"proactive list of probable next work" purpose.

### Expected

`iface.ticket.search` and `iface.ticket.list_mine` return tickets in
**severity ASC, then filed_at DESC** order (P0 first; within a
severity bucket, newest first), and `bugSummary` projects severity
into the response so the view can show it.

### Suggested fix sketch

1. In `bugSummary`, change `"priority": b.frontString("priority")`
   to `"severity": b.frontString("severity")` (or include both keys
   for a transition window; pick one based on whether any other
   provider returns `priority:`). Update consumers — currently only
   `stories/dev-story/rooms/ticket_search.yaml` references
   `t.priority` directly; rename to `t.severity` in lock-step.
2. In `listAllBugs`, replace the id-only `Less` with a multi-key
   comparison: parse `severity` as `P0`/`P1`/`P2`/`P3` → 0/1/2/3,
   primary key ascending; secondary key = `filed_at` descending
   (or just `ID DESC` since IDs are ISO-prefixed).
3. Drop the `|reverse` from the ticket_search view once the host
   returns the correct order — the view shouldn't be doing the sort.

### Repro

```
$ kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml
> tickets                      # → core.ticket_search, list auto-fetches
```

Observed: three seed bugs render in filed_at-ASC order (then
`|reverse`'d to DESC by the view); the `· P2` / `· P3` badges from
the view template never appear because `t.priority` is empty.

Expected: P0/P1 bugs (when any exist) appear above P2/P3, with the
severity badge populated from `severity:` frontmatter.

### Notes

- Filing this as the first dogfood-discovered bug to be driven
  through bugfix itself — closes the loop the kitsoki-dev story is
  here to demonstrate.
- `iface.ticket.list_mine` (used by `core.main`'s queue list) has
  the same defect; the fix needs to land in both call sites at once.

## Comment 2026-05-18T07:20:37Z by kitsoki



### Reproduction artifact: 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency

## Bug verified ✅

Two distinct defects in `internal/host/localfiles_ticket.go` produce one rendered symptom in the dogfood ticket browser (`stories/dev-story/rooms/ticket_search.yaml`).

### Defect 1 — `bugSummary` reads `priority:`, but bug files use `severity:`

`internal/host/localfiles_ticket.go:473-482` projects `"priority": b.frontString("priority")`. On-disk bug frontmatter uses `severity: P0|P1|P2|P3` (see `issues/README.md` and `docs/proposals/bug-format-proposal.md` §2). Every existing file under `issues/bugs/` confirms — `severity:` present, `priority:` absent. Result: `t.priority` is `""` for every locally-filed ticket, so the view's `{% if t.priority %} · {{ t.priority }}{% endif %}` badge in `stories/dev-story/rooms/ticket_search.yaml:40` never renders.

### Defect 2 — `listAllBugs` sorts by `ID` ASC

`internal/host/localfiles_ticket.go:295`:

```go
sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
```

Since IDs are ISO-timestamp prefixed, this is effectively `filed_at` ASC (oldest first). Severity has no weight. The view papers over half of this with `world.ticket_results|reverse`, but a P0 filed last week still sorts below a P3 filed yesterday.

### Reproduction

Test file already on disk: `internal/host/localfiles_ticket_repro_test.go`. Two tests encode the *expected* behaviour and therefore FAIL on HEAD:

```
$ go test ./internal/host/ -run TestRepro_ -v
=== RUN   TestRepro_BugSummary_ProjectsSeverity
    localfiles_ticket_repro_test.go:71: expected severity P0 in summary, got ""
    (full summary: map[id:... priority:"" status:"open" title:"Alpha" ...])
--- FAIL: TestRepro_BugSummary_ProjectsSeverity (0.00s)
=== RUN   TestRepro_ListAllBugs_OrdersBySeverityThenRecency
    localfiles_ticket_repro_test.go:118: ordering mismatch at index 0:
    want "2026-05-17T000000Z-new", got "2026-05-10T000000Z-old"
    (full order: [old mid new])
    defect: listAllBugs sorts by ID ASC; expected severity ASC, filed_at DESC
--- FAIL: TestRepro_ListAllBugs_OrdersBySeverityThenRecency (0.00s)
FAIL    kitsoki/internal/host    0.012s
```

The first test seeds one P0 ticket and asserts `tickets[0]["severity"] == "P0"`. The second seeds three tickets (P3 oldest, P2 middle, P0 newest) — IDs designed so id-ASC and severity-then-recency produce distinct orderings — and asserts the order is P0→P2→P3 (newP0, midP2, oldP3).

### Evidence

- `internal/host/localfiles_ticket_repro_test.go` — the deterministic test
- `stories/bugfix/evidence/2026-05-18T045257Z-localfiles-ticket-rank.log` — captured failing run

### Implicated components

- **`internal/host` package** — `localfiles_ticket.go`, specifically `bugSummary` (key projection) and `listAllBugs` (sort comparator).
- **`iface.ticket` operations** — `ticket.search` and `ticket.list_mine` both consume the broken summary + sort; per the bug report the fix must land in both call sites.
- **`stories/dev-story/rooms/ticket_search.yaml`** — view-side consumer that branches on `t.priority` and applies the `|reverse` workaround.

_phase: reproducing_2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency_0_

## Comment 2026-05-18T08:47:21Z by kitsoki



### Reproduction artifact: 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency

## Bug verified ✅

Two distinct defects in `internal/host/localfiles_ticket.go` produce one rendered symptom in the dogfood ticket browser (`stories/dev-story/rooms/ticket_search.yaml`). Both are encoded as failing Go tests on HEAD.

### Defect 1 — `bugSummary` projects `priority`, but bug files use `severity`

`internal/host/localfiles_ticket.go:473-482`:

```go
"priority": b.frontString("priority"),   // ← reads "priority"
```

On-disk bug frontmatter uses `severity: P0|P1|P2|P3` (per `issues/README.md` and `docs/proposals/bug-format-proposal.md` §2). Every file under `issues/bugs/` has `severity:` set; none has `priority:`. So `t.priority` is `""` for every locally-filed ticket, and the view's `{% if t.priority %} · {{ t.priority }}{% endif %}` badge in `stories/dev-story/rooms/ticket_search.yaml:40` never renders.

### Defect 2 — `listAllBugs` sorts by `ID` ASC

`internal/host/localfiles_ticket.go:295`:

```go
sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
```

Since IDs are ISO-timestamp-prefixed, this is effectively `filed_at` ASC (oldest first). Severity has no weight. The view papers over half of this with `world.ticket_results|reverse`, but a P0 filed last week still sorts below a P3 filed yesterday.

### Reproduction

Test file on disk: `internal/host/localfiles_ticket_repro_test.go`. Two tests encode the *expected* behaviour and therefore FAIL on HEAD (verified this turn):

```
$ go test ./internal/host/ -run TestRepro_ -v
=== RUN   TestRepro_BugSummary_ProjectsSeverity
    localfiles_ticket_repro_test.go:71: expected severity P0 in summary, got ""
    (full summary: map[assignee:"brad" id:"2026-05-18T010000Z-a" priority:"" status:"open" title:"Alpha" url:""])
--- FAIL: TestRepro_BugSummary_ProjectsSeverity (0.00s)
=== RUN   TestRepro_ListAllBugs_OrdersBySeverityThenRecency
    localfiles_ticket_repro_test.go:118: ordering mismatch at index 0:
        want "2026-05-17T000000Z-new", got "2026-05-10T000000Z-old"
        (full order: [old mid new])
        defect: listAllBugs sorts by ID ASC; expected severity ASC, filed_at DESC
--- FAIL: TestRepro_ListAllBugs_OrdersBySeverityThenRecency (0.00s)
FAIL    kitsoki/internal/host    0.013s
```

The first test seeds one P0 ticket and asserts `tickets[0]["severity"] == "P0"`. The second seeds three tickets (P3 oldest, P2 middle, P0 newest) — IDs designed so that id-ASC and severity-then-recency produce *distinct* orderings — and asserts the order is P0→P2→P3 (newP0, midP2, oldP3).

### Evidence

- `internal/host/localfiles_ticket_repro_test.go` — deterministic test (FAIL on HEAD)
- `stories/bugfix/evidence/2026-05-18T045257Z-localfiles-ticket-rank.log` — captured failing run from this turn

### Implicated components

- **`internal/host` package** — `localfiles_ticket.go`, specifically `bugSummary` (key projection) and `listAllBugs` (sort comparator).
- **`iface.ticket` operations** — `ticket.search` and `ticket.list_mine` both consume the broken summary + sort; the fix must land in both call sites.
- **`stories/dev-story/rooms/ticket_search.yaml`** — view-side consumer that branches on `t.priority` and applies the `|reverse` workaround.

_phase: reproducing_2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency_0_

## Comment 2026-05-18T08:54:20Z by kitsoki



### Fix proposal: 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency

## Bug
`iface.ticket.search` against the local-files provider produces ticket summaries with an empty `priority` field for every ticket and an ordering that puts old P3s above new P0s. The `stories/dev-story/rooms/ticket_search.yaml` browser therefore renders rows like `[open]` (no severity badge) and shows the oldest filings at the top after the `|reverse` view-side workaround.

## Root cause
Two defects in `internal/host/localfiles_ticket.go`:

1. **Wrong key in projection.** `bugSummary` (line 478) reads `b.frontString("priority")`, but bug files use `severity:` (`issues/README.md`, `docs/proposals/bug-format-proposal.md` §2). The summary's `priority` field is therefore always `""`, and the view's `{% if t.priority %}` badge never renders.
2. **Wrong sort key.** `listAllBugs` (line 295) sorts strictly by `BugFile.ID` ASC. Because IDs are ISO-timestamp-prefixed, this is effectively filed_at ASC. Severity has zero weight in the order; a `|reverse` in the view papers over the recency half but does nothing about severity.

## Fix
1. In `internal/host/localfiles_ticket.go`:
   - `bugSummary`: replace the `"priority": …` line with `"severity": b.frontString("severity")` so the summary exposes the field the contract's local-files binding actually stores. Update the doc comment on the function to reference the renamed key.
   - `listAllBugs`: replace the single-key sort with a two-key `sort.SliceStable` that orders by `severityRank(b.frontString("severity"))` ASC and, on tie, by `filed_at` (frontmatter) DESC, falling back to `b.ID` DESC when `filed_at` is missing. Add an unexported `severityRank(string) int` helper that returns `0,1,2,3` for `P0..P3` and `4` for empty/unknown.
2. In `internal/host/localfiles_ticket_test.go`: update the `sampleBug` / `sampleBugWithComment` fixtures from `priority: med|high` to `severity: P2|P0`, and change `TestLocalFilesTicket_Get_Happy` to assert on `res.Data["severity"] == "P0"` instead of `priority == "high"`. These existing tests are currently locking in the wrong schema.
3. In `stories/dev-story/rooms/ticket_search.yaml`: switch the badge from `{% if t.priority %} · {{ t.priority }}{% endif %}` to `{% if t.severity %} · {{ t.severity }}{% endif %}`, drop the `|reverse` filter on `world.ticket_results` (the host now returns the right order), and refresh the `NOTE: ordering today is filed_at-ASC …` comment to describe the post-fix behaviour (severity-then-recency, host-side).

## Affected files
- `internal/host/localfiles_ticket.go`
- `internal/host/localfiles_ticket_test.go`
- `stories/dev-story/rooms/ticket_search.yaml`

## Confidence
0.9 — both defects are pinpointed by FAILing repro tests on HEAD, the on-disk schema is documented in `issues/README.md` and `docs/proposals/bug-format-proposal.md`, and the fix surface is small and local. The 0.1 uncertainty is that the `dev-story-implementation-contract.md` §2.1 still documents `priority`; aligning the contract is a follow-up doc edit (called out in alternatives), not required for the repro tests to pass.

_phase: proposing_2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency_0_

## Comment 2026-05-18T09:15:34Z by kitsoki



### Reproduction artifact: 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency

## Bug verified ✅

Two distinct defects in `internal/host/localfiles_ticket.go` produce one rendered symptom in the dogfood ticket browser (`stories/dev-story/rooms/ticket_search.yaml`). Both are encoded as failing Go tests against HEAD of this worktree.

### Defect 1 — `bugSummary` projects `priority`, but bug files use `severity`

`internal/host/localfiles_ticket.go:473-482`:

```go
"priority": b.frontString("priority"),   // ← reads "priority"
```

On-disk bug frontmatter uses `severity: P0|P1|P2|P3` (per `issues/README.md` and `docs/proposals/bug-format-proposal.md` §2). Every file under `issues/bugs/` has `severity:` set; none has `priority:`. So `t.priority` is `""` for every locally-filed ticket, and the view's `{% if t.priority %} · {{ t.priority }}{% endif %}` badge in `stories/dev-story/rooms/ticket_search.yaml:40` never renders.

### Defect 2 — `listAllBugs` sorts by `ID` ASC

`internal/host/localfiles_ticket.go:295`:

```go
sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
```

Because IDs are ISO-timestamp-prefixed, this is effectively `filed_at` ASC (oldest first). Severity has no weight in the order. The view papers over half of this with `world.ticket_results|reverse`, but a P0 filed last week still sorts below a P3 filed yesterday.

### Reproduction

Test file on disk: `internal/host/localfiles_ticket_repro_test.go` (committed in this worktree). Two tests encode the *expected* behaviour and therefore FAIL on HEAD — re-verified this turn:

```
$ go test ./internal/host/ -run TestRepro_ -v
=== RUN   TestRepro_BugSummary_ProjectsSeverity
    localfiles_ticket_repro_test.go:71: expected severity P0 in summary, got ""
    (full summary: map[assignee:"brad" id:"2026-05-18T010000Z-a" priority:"" status:"open" title:"Alpha" url:""])
--- FAIL: TestRepro_BugSummary_ProjectsSeverity (0.00s)
=== RUN   TestRepro_ListAllBugs_OrdersBySeverityThenRecency
    localfiles_ticket_repro_test.go:118: ordering mismatch at index 0:
      want "2026-05-17T000000Z-new", got "2026-05-10T000000Z-old"
      (full order: [old mid new])
      defect: listAllBugs sorts by ID ASC; expected severity ASC, filed_at DESC
--- FAIL: TestRepro_ListAllBugs_OrdersBySeverityThenRecency (0.00s)
FAIL    kitsoki/internal/host    0.016s
```

`TestRepro_BugSummary_ProjectsSeverity` seeds a single P0 bug and asserts `tickets[0]["severity"] == "P0"`. `TestRepro_ListAllBugs_OrdersBySeverityThenRecency` seeds three bugs (P3 oldest, P2 middle, P0 newest) with IDs designed so id-ASC and severity-then-recency produce *distinct* orderings, then asserts the expected order is `newP0 → midP2 → oldP3`.

### Evidence

- `internal/host/localfiles_ticket_repro_test.go` — deterministic Go test, FAILs on HEAD.
- `stories/bugfix/evidence/2026-05-18T045257Z-localfiles-ticket-rank.log` — captured failing run.

### Implicated components

- **`internal/host` package** — `localfiles_ticket.go`, specifically `bugSummary` (key projection) and `listAllBugs` (sort comparator).
- **`iface.ticket` operations** — `ticket.search` and `ticket.list_mine` both consume the broken summary + sort; the fix must land in both call sites.
- **`stories/dev-story/rooms/ticket_search.yaml`** — view-side consumer that branches on `t.priority` and applies the `|reverse` workaround.

_phase: reproducing_2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency_0_
