---
# triage-marathon: FIXED ea2ca55a — honor post_cmd_cwd + infra-vs-semantic split + terminal msg (live dogfood drive)
id:        2026-06-10T141756Z-decide-postcmd-captured-submit-reported-abandoned
title:     "host.agent.decide with validator.post_cmd reports 'abandoned without successful submit' when the schema-valid payload WAS captured"
target:    kitsoki
filed_at:  2026-06-10T14:17:56Z
status: fixed
severity:  P1
component: runtime
kitsoki_rev: "39f2948"
trace_ref: "/home/cloud-user/.kitsoki/sessions/bugfix/d2e21c10-jira-PLTFRM-90872.jsonl"
external: {}
---

## Body

A `host.agent.decide` call that declares a `validator: { post_cmd: ... }` block
**fails the whole job** with:

```
validator: session abandoned without successful submit after 3 outer iteration(s), N attempt(s)
```

even though the LLM **did** call `mcp__validator__submit` and the validator
replied `"OK: payload validated against the schema and captured. You may end your
turn now; no need to repeat the JSON."` The error message is wrong: the submit
succeeded and the schema-valid payload was captured to disk — the **post_cmd
(semantic) gate** is what never passes, and that failure is mis-reported as a
missing submit.

This blocks every `decide` call that uses a post-submission verifier. It
surfaced migrating the `focused-engineering` bugfix room (`stories/bugfix`) off the
deprecated `host.agent.ask_with_mcp` onto `decide` — 6 phases (`phase_minus_1`,
`phase_1_7`, `phase_3`, `phase_6`, `phase_9`, `phase_9_7`, `phase_13`) each carry
a `validator.post_cmd: "python3 -m bugfix verify-*"` and all hang the same way.

### Root cause (internal/host/agent_decide.go)

When `validatorBlockPresent`, decide applies the "C1 fix" split:

1. the in-process `mcp-validator` is built **schema-only** (`schemaOnlyOpts`) and
   captures the submitted payload to `validatorOutputPath`;
2. the `post_cmd` is run **separately by the host** via
   `runDecideSandboxValidator` → `RunValidatorSandboxed` (read-only,
   network-denied sandbox, `cmd.Dir = scratchDir`).

In `runDecideWithValidatorRetryLoop`, the `mcpOutcomeSuccess` branch (schema
passed) then *requires the sandboxed post_cmd to exit 0* before accepting:

```go
case mcpOutcomeSuccess:
    if p.SandboxValidatorOpts != nil && post_cmd != "" {
        rejection, contractErr := runDecideSandboxValidator(ctx, p.ValidatorOutputPath, p.SandboxValidatorOpts)
        if contractErr != "" { return ...contractErr }
        if rejection != ""   { sandboxLastRejection = rejection; continue }  // <-- nudge & retry
    }
    return ...accept
```

`runDecideSandboxValidator` returns a non-empty `rejection` (→ retry → eventual
abandon) whenever the post_cmd is **not cleanly runnable in the sandbox**:

```go
vr, runErr := RunValidatorSandboxed(ctx, ...)
if runErr != nil {
    return fmt.Sprintf("validator sandbox: infrastructure error: %v", runErr), ""  // treated as semantic rejection
}
if vr.ExitCode == 0 { return "", "" }
// ... otherwise return stderr as a "semantic rejection"
```

So an argv0 that can't exec under the sandbox, or a verifier that can't even
start, is folded into the *semantic-rejection* path — it burns all outer
iterations and ends as "abandoned."

### Two compounding problems for the bugfix room's post_cmds

Every bugfix post_cmd is `python3 -m bugfix verify-*`:

1. **argv0 not on the read-only sandbox allowlist** — the loader already warns:
   `agent verb cross-check: decide/extract validator.post_cmd argv0 is not on
   the read-only allowlist; runtime sandbox enforces isolation argv0=python3`.
2. **cwd is dropped** — `bugfix` is importable only from `tools/loopy`, but the
   sandbox runs with `cmd.Dir = scratchDir` and **`post_cmd_cwd` is not honored**
   in this path (and `task.acceptance` dropped `post_cmd_cwd` entirely, unlike
   the `decide`/`ask` `validator:` parser which still parses it). `python3 -m
   bugfix ...` therefore exits non-zero (`No module named bugfix`).

Either alone makes the post_cmd un-passable, so the schema-valid captured verdict
is discarded on every iteration. The old `host.agent.ask_with_mcp` validator
supported `post_cmd_cwd` and did not sandbox-block the verifier, so the same
rooms worked before the agent-split.

### Why it's invisible / mis-reported

- The captured payload at `validatorOutputPath` is **never consulted as a
  fallback** in the validator-block path. (The no-validator-block path *does*
  read it via `ReadCapturedPayload` and accepts — which is exactly why a
  schema-only decide call passes.) A successful capture + failing post_cmd is
  indistinguishable from "never submitted."
- The terminal message says "abandoned without successful submit" instead of
  "post_cmd validator rejected/failed N times: <reason>", hiding the real cause.

### Repro

1. Any `decide` call with
   `validator: { post_cmd: "python3 -m bugfix verify-context", post_cmd_cwd: "tools/loopy", ... }`
   and a `schema:`. Observed on `stories/bugfix` `phase_minus_1` (ticket
   PLTFRM-90872), model `claude-opus-4-8`.
2. Claude transcript shows `mcp__validator__submit` called **3×**, each returning
   `"OK: payload validated against the schema and captured"`; job still ends
   `failed: validator: session abandoned without successful submit after 3 outer
   iteration(s), 3 attempt(s)`.

### Expected vs actual

- **Expected:** schema-valid submit + a post_cmd that can't run is either (a)
  accepted on the schema capture, or (b) surfaced as a hard, named error
  ("post_cmd could not execute" / "rejected: <stderr>") — not silently retried to
  "abandoned."
- **Actual:** un-runnable post_cmd → permanent semantic rejection → 3 wasted
  outer iterations → mis-labeled "abandoned without successful submit."

### Fix options (any one resolves the hang; #1 is the true fix)

1. **Honor `post_cmd_cwd` in the sandbox path** for `decide` (and restore it for
   `task.acceptance`), and make the read-only-sandbox argv0 allowlist
   configurable / include the declared verifier interpreter — restoring parity
   with the pre-split `ask_with_mcp` validator.
2. **Distinguish post_cmd *infrastructure failure* from *semantic rejection*.**
   A non-executable argv0 / interpreter import error / sandbox-exec failure
   (`runErr != nil`, or a recognizable "cannot start" exit) should be a hard
   `Result.Error`, not a silent semantic rejection that burns the retry budget.
3. **Fix the terminal message (min):** when `validatorOutputPath` holds a
   captured payload but the post_cmd gate failed, report
   `"post_cmd validator rejected the submit N times: <reason>"` instead of
   "abandoned without successful submit."

### Cross-references

- Likely related: `issues/bugs/2026-06-03T121407Z-agent-decide-silent-abandon-empty-artifact.md`
  (agent-decide silent abandon / empty artifact) — same family, validator
  acceptance path.
- Migration context lives in the focused-engineering worktree
  (`stories/bugfix/app.yaml`, `tools/loopy/`); the room currently works around
  this by making decide validators schema-only and relocating the `verify-*`
  checks to downstream `host.run` gates.
