---
# triage-marathon: ALREADY-FIXED in main — integrate moved to stories/delivery-tail/rooms/integrate.yaml; runs world.gate_command verbatim as $1; live bugfix drive self-terminated
component: stories/ship-it
filed_at: "2026-06-22T14:30:00Z"
id: 2026-06-22T143000Z-ship-it-integrate-hardcoded-build-check
severity: P2
status: fixed
target: kitsoki
title: ship-it integrate hardcodes `go build ./... && go test ./...` post-merge build-check instead of the configured gate — blocks integration on unrelated reds + couples ship-it to Go
url: issues/bugs/2026-06-22T143000Z-ship-it-integrate-hardcoded-build-check.md
---

## Body

`stories/ship-it/rooms/integrate.yaml:60` hardcodes the post-merge build-check:

```
BUILD_CMD="go build ./... && go test ./..."
```

This is wrong for two reasons:
1. **It runs a gate the operator did not configure.** ship-it already carries a
   deterministic `gate_command` (used by the maker and re-run by the `verify`
   room on the merged commit). integrate should run THAT, not a hardcoded one.
2. **It couples ship-it to Go** and **catches pre-existing, unrelated reds.**
   Shipping slice 4 this way, integrate's hardcoded `go test ./...` tripped on an
   unrelated pre-existing main-red (`TestOperatorAskListener_UsesSocketSafeTempDir`,
   env-specific) and blocked integration even though the brief's own gate was
   green — worked around only via the `build_check_disabled` escape hatch.

### Expected
`stories/git-ops/rooms/integrate.yaml:76` already does it right —
`BUILD_CMD="{{ world.build_check_cmd }}"` (configurable). ship-it's integrate
should use the **configured gate_command** (seed `build_check_cmd` from it), OR
drop integrate's separate build-check entirely and rely on the `verify` room,
which already re-runs the configured gate on the merged commit. The redundant,
hardcoded, Go-coupled second gate must go.

### Repro / gate
A `stories/ship-it` flow where the configured `gate_command` differs from
`go test ./...`: integrate must run the CONFIGURED gate (RED while hardcoded —
the wrong gate runs; GREEN once integrate uses `gate_command`). Keep ship-it 6/6.

## Comment 2026-06-22T09:06:50Z by kitsoki


### Reproduction artifact: ship-it-integrate-hardcoded-build-check

## Bug: `ship-it/rooms/integrate.yaml` hardcodes `BUILD_CMD` instead of using `world.gate_command`

### What is broken

The `integrate` room in `stories/ship-it/rooms/integrate.yaml` (line 60) hardcodes the build check command:

```bash
BUILD_CMD="go build ./... && go test ./..."
```

This completely ignores `world.gate_command`, the user-configured deterministic gate that the `ship-it` pipeline is built around. In contrast, the `verify` room (which runs after integrate) correctly uses `cmd: "{{ world.gate_command }}"`.

This breaks the core contract stated in both `app.yaml` and `verify.yaml`: *"the SAME gate_command is re-run identically on the merged commit."* The integrate and verify steps are running different commands.

### How this was reproduced

**1. Static code inspection (`repro.sh`):**

Running `.artifacts/bug-ship-it-buildcheck/repro.sh` from the worktree confirms:
- `integrate.yaml:60` → `BUILD_CMD="go build ./... && go test ./..."` (hardcoded)
- `verify.yaml:46` → `cmd: "{{ world.gate_command }}"` (correct)

The script exits 1 (FAIL) with the full mismatch printed.

**2. Flow test (`stories/ship-it/flows/bug_hardcoded_buildcheck.yaml`):**

A new flow test models the scenario: `gate_command` is `"npm test"` (a non-Go project). The `integrate_exec` mock returns `build_fail` with `"go: no Go files in /workspace/darkmode"` — what the hardcoded `go build ./...` produces on a JS project.

`kitsoki test flows stories/ship-it/app.yaml --flows stories/ship-it/flows/bug_hardcoded_buildcheck.yaml` → **PASS** — the buggy end state (`__exit__needs-human`, `integrate_outcome: build_fail`) is correctly reproduced.

### Consequences

| Scenario | With bug | Expected |
|---|---|---|
| Non-Go repo (npm, cargo, make) | integrate always fails — `go build` finds no `.go` files | integrate runs `gate_command`, succeeds if gate passes |
| Go repo with narrower gate (e.g. `go test ./internal/ratelimit/`) | integrate runs full `go build ./... && go test ./...` — different command from verify | integrate and verify run identical `gate_command` |
| `build_check_disabled: true` | build check skipped (escape hatch works) | same |

### Where the evidence lives

- `.artifacts/bug-ship-it-buildcheck/repro.sh` — shell script that detects and exits non-zero on the hardcoded line
- `stories/ship-it/flows/bug_hardcoded_buildcheck.yaml` — flow test capturing the buggy behavior (PASS = bug present)

### Fix

In `integrate.yaml`, replace:
```bash
BUILD_CMD="go build ./... && go test ./..."
```
with:
```bash
BUILD_CMD="{{ world.gate_command }}"
```

This aligns integrate with verify's discipline and restores the pipeline's core invariant.

_phase: reproducing_ship-it-integrate-hardcoded-build-check_0_

## Comment 2026-06-22T09:07:37Z by kitsoki

### Fix proposal: ship-it-integrate-hardcoded-build-check

## Bug

`stories/ship-it/rooms/integrate.yaml` line 60 hardcodes the build-check command:

```bash
BUILD_CMD="go build ./... && go test ./..."
```

This ignores `world.gate_command` — the user-configured deterministic gate that the entire `ship-it` pipeline is built around. The `verify` room (which runs directly after) correctly uses `cmd: "{{ world.gate_command }}"`, so integrate and verify execute **different commands**, violating the pipeline's core invariant: *"the SAME gate_command is re-run identically on the merged commit."*

## Root Cause

The hardcoded value was written into the shell preamble of `integrate.yaml`'s executor script and was never templated. The `gate_command` world variable exists and is used correctly in `verify.yaml`, but was overlooked when the integrate shell block was authored — likely because the initial implementation targeted Go repositories only.

## Fix

In `stories/ship-it/rooms/integrate.yaml` line 60, replace:

```bash
BUILD_CMD="go build ./... && go test ./..."
```

with:

```bash
BUILD_CMD="{{ world.gate_command }}"
```

No other changes are required. This single substitution makes integrate use the same command that verify uses, restoring the pipeline invariant for all project types (npm, cargo, make, etc.).

## Affected Files

- `stories/ship-it/rooms/integrate.yaml` (line 60)

## Confidence: 0.97

The reproduction evidence is unambiguous (static grep + flow test), the fix is a single-token change, and the parallel with `verify.yaml` leaves no doubt about intent.

_phase: proposing_ship-it-integrate-hardcoded-build-check_0_
