---
id: 2026-06-23T130300Z-validating-bounce-stale-validate-artifact
title: "bugfix validating room: fail_short / fail bounce arms never re-arm their own validate_artifact → stale verdict re-renders on return to validating"
target: story
filed_at: 2026-06-23T13:03:00Z
status: open
severity: P2
component: bugfix
kitsoki_rev: e3eb9b89
related:
  - 2026-06-23T120645Z-testing-accept-bounce-stale-review-artifact
  - 2026-06-10T140218Z-once-onenter-hostrun-bind-and-emit-dropped-on-reentry
url: "issues/bugs/2026-06-23T130300Z-validating-bounce-stale-validate-artifact.md"
---

## Body

Sibling of [[2026-06-23T120645Z-testing-accept-bounce-stale-review-artifact]],
surfaced by that ticket's "audit every cross-phase bounce arc" Notes and
confirmed while fixing it.

In `stories/bugfix/rooms/validating.yaml`, the validator agent is dispatched on
`on_enter` with `once: true` keyed on its own bind `validate_artifact`
(validating.yaml ~123/140). Two `accept` bounce arms clear the **destination**
phase's once: artifact but **not** their own `validate_artifact`:

- `fail_short` → `implementing` (~line 180): clears `implement_artifact: {}` but
  **not** `validate_artifact`.
- `fail` → `proposing` (~line 200): clears `propose_fix_artifact: {}` but
  **not** `validate_artifact`.

The `infra_error` self-loop (~line 220) clears `validate_artifact: {}` correctly,
and the `restart_from` arms do too — these two are the omissions.

So after `fail_short` bounces to `implementing` and the operator walks the fix
back through `testing → reviewing → validating`, on re-entry to `validating` the
`once:`-guarded validator is **skipped** (its key, `validate_artifact`, is still
populated from the prior cycle), so the room re-renders the **prior cycle's stale
verdict** against a tree the maker has since changed. `iface.ci.build` re-runs
(it is NOT `once:`), but the validator does not. Same livelock family as the
testing bug.

> Secondary: the `fail_short → implementing` return path also re-enters `testing`,
> whose `once:`-bound `implement_review_artifact` was just fixed to re-arm only on
> the `testing` bounce — a validating-initiated bounce does NOT pass through that
> arm, so testing's review may also re-render stale on this longer path. Audit
> whether intermediate rooms on a multi-room bounce return path re-arm their own
> once: binds, or whether the bounce arm must clear every once: artifact between
> source and destination.

## Expected

The `fail_short` and `fail` arms add `validate_artifact: {}` to their `set:`
blocks (mirroring the testing fix), so the validator re-dispatches on return.
Audit the multi-room return path for intermediate stale once: binds.

## Actual

Only the destination artifact is cleared; `validate_artifact` survives the bounce
and suppresses the validator's re-run via its own `once:` guard.

## Notes

Add a no-LLM flow regression mirroring
`stories/bugfix/flows/testing_accept_bounce_rearms_review.yaml`: drive
`validating` with a stubbed `fail_short`/`fail` verdict, bounce, walk back to
`validating`, and assert the validator re-dispatches a fresh verdict rather than
re-rendering the stale one.
