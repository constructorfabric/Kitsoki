# PLTFRM-89912 — Code Review Report (Phase 08)

## Verdict: **BLOCKED**

## Summary

Phase 8 was invoked with **no diff and no bug description**. Both
`args.context.bug_description` and `args.context.diff_text` arrived
empty in the rendered prompt:

```
Ticket ID: PLTFRM-89912
Bug description: 

Git diff (main...HEAD):

```

There is no implementation artifact in the ticket folder either — the
expected `06.5-impl-summary.json` is missing:

```
.bug-fix/PLTFRM-89912/
├── 03-fix-proposal.md
├── 06-implementation-plan.md
├── 07-test-review-report.json
├── 07-test-review-report.md
└── test_pltfrm_89912_api.py
```

The active fix worktree (`.bug-fix/PLTFRM-89912-011/worktree`,
branch `fix/pltfrm-89912-011`) shows a single 3-line commit modifying
`stories/devstory/app.yaml` — unrelated to the
`ASK/presentation-service` SSRF that this ticket targets. Files the
fix proposal and implementation plan name (`internal/app/handlers/displayObjects.go`,
`internal/app/handlers/presentations.go`,
`internal/app/handlers/displayObjects_test.go`,
`internal/app/handlers/presentations_test.go`,
`tests/tests/test_displayObject_create.yaml`,
`tests/tests/test_rest_context_errors.yaml`) have **no edits** on
that branch.

A code review with an empty diff cannot produce an honest "APPROVED"
verdict — it would be rubber-stamping a non-existent fix and
propagating a corrupt artifact downstream to phase_9 (security
review) and phase_9_5 (build & deploy). It also cannot identify
line-level blockers in code that does not exist.

The correct action is to BLOCK and let the `on_blocker` arc route
back to phase_6_5 (Fix Implementation), which will re-run with the
upstream context and produce a real diff for review.

## Blockers

### B-01 — Phase 8 invoked with empty diff / empty bug description

- **Severity:** blocker
- **File:** `<phase-input>` (orchestrator-supplied context, not
  in-repo source)
- **Description:**
  Phase 8 received an empty `args.context.diff_text` and an empty
  `args.context.bug_description`. There is no patch to review and no
  bug summary to scope the review against. Approving an empty review
  here would silently advance the pipeline past a missing
  implementation, defeating the `on_blocker → phase_6_5` retry arc
  the room is designed around.
- **Suggestion:**
  Route back to phase_6_5 so the implementation actually runs and
  produces a non-empty diff against `main`. Before re-entering phase
  8, verify:
    1. `06.5-impl-summary.json` exists in `.bug-fix/PLTFRM-89912/`.
    2. The active worktree branch (`fix/pltfrm-89912-NNN`) has
       commits beyond `main` touching the files listed in
       `06-implementation-plan.md` (especially
       `internal/app/handlers/displayObjects.go` and
       `internal/app/handlers/presentations.go`).
    3. `bugfix context --phase phase_8` populates `diff_text` from
       that branch (the empty payload in this run suggests the
       context-builder either ran against the wrong worktree or
       found no commits — worth a glance at the run trace before
       re-entering phase_8).

## Coverage of the planned fix (deferred to next attempt)

For continuity with the fix proposal and implementation plan, the
review checklist that should run against the *real* diff once one
exists:

- **Layer 1 — `displayObjects.go`:**
  - `validateRestContextURL` rejects `@`, `%40`, `%2540` (case
    insensitive), absolute URLs (`u.Scheme != ""` or `u.Host != ""`),
    userinfo (`u.User != nil`), and CR/LF/NUL/TAB/leading-trailing
    whitespace.
  - `verifyDORequestData` walks both `RestContext[i][svc].URL` and
    every value in `RestContext[i][svc].URLVariables`.
  - Error messages do **not** echo the offending input.
  - GET-method check moved into `verifyDORequestData` per the plan
    (single source of truth).
- **Layer 2 — `presentations.go::buildRestContext`:**
  - Validator called immediately after `pongo2.RenderTemplateString`
    AND again after `replaceURLVariables`.
  - URL build switched from string concat to `url.Parse(restContextURL)`
    + `base.JoinPath(renderedURL)`; `base.Host` and `base.User` are
    asserted unchanged after `JoinPath`.
  - The non-2xx upstream branch (was lines 472-474) returns `nil`
    context map and a service-key-only error; full upstream details
    go to `logger.Errorf` only.
  - The outbound-failure branch (was line 453) likewise returns a
    service-key-only error; the rendered URL and base URL are
    server-log-only.
- **Layer 3 — `getPresentation` caller (was lines 167-170):**
  - `NewUnprocessableEntityError(nil, "REST context request failed")`
    — first arg `nil` (no `restContext` map leak), no `%+v`
    formatting of the inner error, no rendered URL in the user
    message.
- **Tests:**
  - `displayObjects_test.go` and `presentations_test.go` exist as
    new files in `internal/app/handlers/`.
  - Render-time SSRF test uses two `httptest.NewServer` listeners
    plus a `net.Listener`-level connection counter on the probe
    target, asserting zero connections — i.e. the SSRF cannot have
    fired even partially.
  - `tests/tests/test_displayObject_create.yaml` adds the five
    negative cases enumerated in D-17 of the test review.
  - `tests/tests/test_rest_context_errors.yaml` is **rewritten**
    (not added to) so it no longer asserts the buggy
    `body`/`status_code` echo.
- **Behavioural risk callouts in the PR description:**
  - 422 response shape changed (no upstream `body`/`status_code`
    keys, no `restContext` `Context` field). Search of
    `cyberstack/` for clients parsing those keys.
  - Stored display objects predating the validator may now fail at
    render time with a generic 422; flagged for follow-up DB scan.

This list is not actionable in the current phase 8 cycle (no diff to
walk). It is recorded here so the next attempt can be scored against
it directly.

## Confidence

**95 / 100** that BLOCKED is the right verdict.

The 5-point uncertainty is the unlikely possibility that the
fix-implementation phase deliberately produced a no-op diff (e.g.
the bug was already fixed on `main` between phases 3 and 6.5). Even
in that scenario the right move is to surface the no-op via phase_6_5
(which can emit a structured "already fixed" summary) rather than
silently approve an empty diff at phase_8.
