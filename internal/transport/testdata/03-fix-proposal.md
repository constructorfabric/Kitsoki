# PLTFRM-89912 — Fix Proposal: SSRF via `@` injection in `rest_context.url`

## Summary

`platform-presentation` accepts a `display_object` whose `rest_context[].url`
begins with `@<host>/<path>`. When `POST /api/presentation/v1/presentations/{key}`
renders that display object, `buildRestContext` concatenates the
user-controlled URL onto the trusted internal service base URL:

```go
// internal/app/handlers/presentations.go:409
customRequest := request.CustomMethod(v.Method, restContextURL+renderedURL)
```

The resulting string is `http://<svc>:<port>@<attacker>/<path>`. Go's
`net/url` parses everything before the `@` as URL **userinfo** and dials
`<attacker>` — full SSRF, with the service's bearer token attached.

The error path further leaks the attacker URL fragment and the upstream
status/body to the caller:

```go
// internal/app/handlers/presentations.go:472-474
if res.StatusCode >= 300 {
    e := fmt.Errorf("REST context request to %s failed", renderedURL)
    return map[string]interface{}{k: map[string]interface{}{"body": jsonBody, "status_code": res.StatusCode}}, e
}
```

```go
// internal/app/handlers/presentations.go:167-170 (caller)
restContext, err := buildRestContext(...)
if err != nil {
    return api.NewUnprocessableEntityError(restContext, "REST context request failed: %+v", err)
}
```

`NewUnprocessableEntityError` puts the `restContext` map into the response
`Context` field and the `err` (which contains `renderedURL`) into the
response message — both are sent to the unauthenticated caller.

## Root cause

Two distinct defects in the `rest_context.url` data-flow:

1. **Trusted-prefix concatenation** in `buildRestContext`: a path-shaped
   user-controlled string is appended to a trusted base URL with `+`. The
   `@` character flips the parser's interpretation of the resulting string,
   so the trusted host becomes "userinfo" and the attacker becomes the
   actual host. There is **no input validation** at create time
   (`displayObjects.Post` only validates HTTP method) and **no rendered-URL
   validation** at render time before the concatenation.
2. **Verbose error echoing**: the upstream-failure path returns the
   attacker-supplied URL fragment in the error message and the upstream
   `status_code` + decoded `body` in the response context, giving any
   caller a primitive for probing the internal network and reading
   arbitrary upstream bodies.

A previous fix for the parent ticket (PLTFRM-87475) added a `@` / `%40`
reject in `buildRestContext` (commit `3a3e144`), but that fix is **not
present on the deployed image** running on `mc-clean-25134` and addresses
only one of the two defects (it does not scrub the error response, and it
does not add input validation at create time).

## Proposed fix

Layer three independent defences. Any one of them should block the bug;
all three are needed to fully meet the ticket's "Expected Outcome".

### 1. Create-time input validation (POST /display_objects)

In `internal/app/handlers/displayObjects.go`, extend `verifyDORequestData`
(or add a sibling helper) to validate every `rest_context[].URL`:

- Reject the request with HTTP **400** if `URL` contains `@` or `%40`
  (case-insensitive). This is the minimal, ticket-mandated check.
- Reject if `URL` parses with a non-empty scheme or host — the field is
  intended to be a path appended to a service-resolved base URL, never an
  absolute URL. Use `url.Parse` and reject when `u.Scheme != ""`,
  `u.Host != ""`, or `u.User != nil`.
- Reject control characters / whitespace.
- Apply the same checks to `rest_context[].URLVariables` values, since
  `replaceURLVariables` substitutes them into the URL after rendering and
  would otherwise reintroduce the `@` injection.

This blocks the malicious display object from ever being persisted, and
returns a clean `400` per the ticket's "Expected Outcome".

### 2. Render-time defence in depth (`buildRestContext`)

In `internal/app/handlers/presentations.go::buildRestContext`, after the
pongo2 template render and after `replaceURLVariables`:

- Reject the rendered URL if it contains `@` or `%40` (catches template
  expansions of `parentContext` values that smuggle `@`).
- Build the outbound URL with `url.Parse(restContextURL)` →
  `base.JoinPath(renderedURL)` → `base.String()` rather than string
  concatenation. After joining, assert `base.Host` still equals the
  pre-join host and `base.User == nil`; if either changed, abort.

These render-time checks defend against
- a stored display object that pre-dates the create-time validator,
- template-driven URL construction (`parentContext` values containing
  `@`), and
- future call sites that bypass the create-time validator.

### 3. Generic error responses (no upstream-context echo)

In `buildRestContext`'s error paths and in the `getPresentation` caller:

- Replace `fmt.Errorf("failed to get REST data context from %s%s: %v", restContextURL, renderedURL, ...)`
  (line 453) with a generic message like `"REST context request failed
  for service %q"` using only the service-key (which is server-defined,
  not user-defined). Log the full URL/error server-side via `logger`.
- In the upstream-non-2xx path (lines 472-474) return `nil` for the
  context map, and an error that names only the service key — drop
  `body`, drop `status_code`, drop `renderedURL`.
- In `getPresentation` (line 169), call
  `api.NewUnprocessableEntityError(nil, "REST context request failed")`
  — strip the `restContext` map from the response `Context` and stop
  formatting `err` into the user-visible message.

Server-side logging keeps operability; the wire response becomes generic.

### 4. Tests

- `internal/app/handlers/displayObjects_test.go` (new or extend): table
  test for `verifyDORequestData` covering `@`, `%40`, `%2540`, absolute
  URLs (`http://...`, `https://...`), `userinfo` prefixes, control chars,
  and the legitimate path forms (`/api/2/...`, `""`).
- `internal/app/handlers/presentations_test.go` (extend the
  PLTFRM-87475-era table from commit `3a3e144`): add scenarios for
  template-injected `@`, `URLVariables`-injected `@`, and assert the
  error response contains **none** of: the rendered URL, the configured
  base URL, upstream `body`, upstream `status_code`.

## Affected files

- `internal/app/handlers/displayObjects.go` — input validation in `Post` /
  `verifyDORequestData`.
- `internal/app/handlers/presentations.go` — render-time hardening in
  `buildRestContext`; URL build via `url.Parse` + `JoinPath`; scrubbed
  error responses; generic `NewUnprocessableEntityError` call.
- `internal/app/handlers/displayObjects_test.go` — input-validation
  regression tests (new file or extend existing).
- `internal/app/handlers/presentations_test.go` — extend SSRF unit tests
  with template/URLVariables injection cases and error-response
  scrubbing assertions.

## Confidence and reasoning

**Confidence: 90.**

Repro evidence cited:

- The reproduction report `evidence/api/assertions/api-03-trigger-ssrf/`
  shows the SSRF being dispatched via `@<probe-host>.invalid/leak` against
  `mc-clean-25134`, with status 502 and the attacker probe-host echoed in
  the response — matching exactly the line-409 concatenation behaviour.
- The reproduction step `api-04-no-internal-url-leak` returns `200` only
  because the *fixed* path returns generically; today's deployed image
  fails this assertion via the upstream-context echo at lines 472-474 and
  the error formatting at lines 167-170 / 453.
- The buggy code path is reproducible by reading the source: a
  user-controlled string starting with `@` will deterministically hijack
  any URL parsed by `net/url`. This is the documented Go behaviour and is
  the same defect the prior PLTFRM-87475 fix attempted to address.
- Service trace shows `platform-presentation` (chart 0.27.203) as the
  ingress upstream for the `/presentations/{key}` reproduction request
  — fix must land in `ASK/presentation-service`. `account_server` and
  `idp` appear in the trace only because the malicious `service:
  account_server` references the account-server base URL as the
  prefix-to-be-hijacked and the IDP issued the bearer token that gets
  forwarded; neither needs code changes.

The 10-point uncertainty is mainly about (a) whether any legitimate
producer of display objects today uses a URL that starts with `@`
(unlikely — there is no syntactic reason to do so, but a quick grep of
known producers / a fleet log scan would confirm), and (b) whether the
generic-error change breaks any UI that today relies on parsing the
upstream `body` / `status_code` out of the 422 response (a search of
client code is warranted; the ticket explicitly says this echo is the
defect, so replacing it with a generic error is the intended behaviour).

## Alternative approaches considered

1. **Render-time `@` reject only (replicate PLTFRM-87475 fix exactly).**
   Rejected: ticket explicitly calls the parent fix "incorrect" and asks
   for input-time validation as primary defence; also leaves the error
   info-disclosure in place.
2. **Allow-list of permitted path prefixes per service.** Rejected:
   higher engineering cost; orthogonal to this defect; can be added
   later. The current bug is fully closed by the three layers above.
3. **Switch outbound HTTP client to one that takes a parsed `*url.URL`
   instead of a string.** Rejected as the *only* fix: doesn't help if
   the caller still constructs the `*url.URL` from the same concatenated
   string, and `gorequest`'s API is ergonomic only with strings.
   Implemented as part of layer 2 (`base.JoinPath`) which yields the
   same end state without rewriting the HTTP client.
4. **Strip `@` from the URL silently instead of rejecting.** Rejected:
   breaks the principle of explicit input validation, masks the attack
   in logs, and risks producing a different valid URL the attacker can
   still abuse (e.g., truncated path matching an internal endpoint).

## Security surface

This is **not** a UI access-control bug. The bug is a server-side request
forgery in a backend handler — fix must be in the backend; there is no
"UI hides a button" failure mode. `security_surface` is therefore set to
`null` per the prompt's guidance.
