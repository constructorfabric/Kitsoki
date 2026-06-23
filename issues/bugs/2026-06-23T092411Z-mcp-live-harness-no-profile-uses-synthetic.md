---
id: 2026-06-23T092411Z-mcp-live-harness-no-profile-uses-synthetic
title: "session_new {harness:live} with no profile silently resolves to a fake synthetic backend (empty output → acceptance failure)"
target: kitsoki
filed_at: 2026-06-23T09:24:11Z
status: fixed
severity: P2
component: mcp
kitsoki_rev: 154630be
trace_ref: ""
external: {}
assignee: ""
related:
  - 2026-06-23T092410Z-mcp-no-standalone-gate-runner
url: "issues/bugs/2026-06-23T092411Z-mcp-live-harness-no-profile-uses-synthetic.md"
---

## Body

Calling `session_new` with `harness: live` but no `profile` silently resolves
to a fake `synthetic-codex` backend. The agent rooms then return empty output,
which surfaces downstream as `acceptance failed after 5 attempt(s)` — a
confusing, late, expensive failure that looks like the agent failing the task
rather than "no real backend was selected."

A real LLM run required passing `profile: codex` explicitly. Discovered the
hard way during the imports-rewriter dogfood delivery: the first attempt
(`harness: live`, no profile) bounced to idle with acceptance failures; only an
explicit `profile: codex` produced real agent output.

## Expected

One of:
- `session_new {harness: live}` with no resolvable real profile is a **hard
  error** ("no LLM backend selected for harness=live; pass a profile"), or
- the resolved backend/profile is **surfaced in `session_new`'s return** so the
  caller can see it fell back to synthetic before spending five acceptance
  attempts on empty output.

## Actual

Silent fallback to a synthetic/fake backend; failure only manifests several
turns later as repeated acceptance failures with no indication the cause was
backend resolution.

## Impact

A silent landmine for MCP-first live delegation: an operator/agent asking for a
live run gets a fake one with no signal, then burns retries diagnosing a
phantom "agent can't do the task" failure.

## Notes

Surfaced during the live imports-rewriter delivery. See also
`docs/architecture/operator-ask.md` for the headless-agent conventions; this is
the analogous "fail loud, don't silently degrade" gap on the backend-resolution
path.

## Resolution

Fixed by d8194886 (option A — fail loud). `OpenDrivingSession`
(`internal/mcp/studio/handles.go`) now returns a hard `BAD_REQUEST` error when
`mode==live && profile=="" && len(harnessProfiles)>0`, BEFORE the silent
default-profile fallback — naming the boot default and telling the caller to
pass a real profile. The legacy single-default path (no profiles declared) is
untouched, and replay/default sessions are unaffected. Regression guard:
`session_live_no_profile_repro_test.go` (`TestSessionLiveNoProfile_SilentlySyntheticDefault`
+ `TestSessionLiveExplicitProfile_NotAffectedByFix`).
