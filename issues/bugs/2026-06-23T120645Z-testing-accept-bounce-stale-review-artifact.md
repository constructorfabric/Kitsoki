---
# triage-marathon: ALREADY-FIXED in main — 631da61e — re-arm test_author on testing accept failed/blocked bounce
id: 2026-06-23T120645Z-testing-accept-bounce-stale-review-artifact
title: "bugfix testing room: accept→implementing bounce never re-arms its own review artifact → stale failed verdict loops forever against a green tree"
target: story
filed_at: 2026-06-23T12:06:45Z
status: fixed
severity: P1
component: bugfix
kitsoki_rev: e3eb9b89
trace_ref: "ca598c80-7d6a-471b-9c1b-192b4d516d3f (.artifacts/dogfood-once-onenter.trace.jsonl)"
external: {}
assignee: ""
related:
  - 2026-06-10T140218Z-once-onenter-hostrun-bind-and-emit-dropped-on-reentry
url: "issues/bugs/2026-06-23T120645Z-testing-accept-bounce-stale-review-artifact.md"
---

## Body

In `stories/bugfix/rooms/testing.yaml`, the `accept` arc's `failed/blocked →
implementing` arm clears `implement_artifact: {}` (re-arms the implementer) but
**not** `implement_review_artifact`. The test-review agent (`test_author`) on the
testing room's `on_enter` is dispatched `once: true` keyed on
`implement_review_artifact` (binds `implement_review_artifact: submitted`).

So on the return trip `implementing → testing`:

- `iface.ci.run_tests` re-runs (it is NOT `once:` — shared `ci_log` scratch), but
- the `once:`-guarded `test_author` is **skipped** (its key, `implement_review_artifact`,
  is still populated from the prior cycle), so
- the room re-renders the **prior cycle's stale `failed` verdict** against a
  worktree the maker has since made green.

An operator following the menu loops `testing ↔ implementing` forever — re-running
`go test ./...` each pass, never re-reviewing it, never reaching `reviewing` —
burning maker budget until `implementing_budget` exhausts and the run abandons.

This is the **same `once:`-staleness-on-re-entry family** as the ticket whose
dogfood run surfaced it ([[2026-06-10T140218Z-once-onenter-hostrun-bind-and-emit-dropped-on-reentry]]):
a `once:` bind that is never cleared on the path that re-enters its room.

### Where it surfaced

Dogfood delivery of GitHub #15 (the once-onenter engine fix). Session
`dogfood-once-onenter` (`ca598c80-7d6a-471b-9c1b-192b4d516d3f`). The fix tree was
green, but `accept` re-rendered a stale `failed`; only `refine` (whose effect
block DOES `set: implement_review_artifact: {}`, testing.yaml:353) produced a
fresh, correct `passed` verdict on the identical tree.

## Expected

The `failed/blocked → implementing` arm (testing.yaml ~line 268) must also clear
its own phase's review artifact so `test_author` re-arms on re-entry:

```yaml
- set:
    refine_feedback:    "{{ world.implement_review_artifact.summary_markdown }}"
    implementing_cycle: "{{ world.implementing_cycle + 1 }}"
    cycle:              "{{ world.cycle + 1 }}"
    implement_artifact:        {}   # re-arm the implementer (already present)
    implement_review_artifact: {}   # ← MISSING: re-arm test_author too
```

## Actual

Only `implement_artifact: {}` is cleared. `implement_review_artifact` survives, so
`test_author`'s `once:` guard suppresses the re-review and the stale verdict
persists across the bounce. `refine` and the `restart_from` arms clear it
correctly; the `accept` failed/blocked arm is the lone omission.

## Impact

P1: a `failed`/`blocked` testing verdict makes the bugfix pipeline livelock
(testing↔implementing) and ultimately abandon a fix that is actually green —
the headline `accept` affordance, not an edge path. Wastes maker spend and
masquerades as "the agent can't fix it."

## Notes

Audit every cross-phase bounce arc in the bugfix rooms for the same omission —
each bounce must clear BOTH the destination artifact AND the bouncing phase's
own `once:`-bound review/scratch artifact. Candidate to check: `validating`'s
`fail_short` / `fail` arms.

Add a no-LLM flow regression: drive `testing` with a stubbed `failed` review,
bounce to `implementing`, return to `testing`, and assert `test_author` is
re-dispatched (fresh `implement_review_artifact`) rather than the stale verdict
re-rendering.
