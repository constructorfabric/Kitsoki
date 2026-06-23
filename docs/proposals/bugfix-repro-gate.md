# Story: Bug-fix reproduction RED-gate

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   — standalone

## Why

> "add a reproduction step to the bug-fix story — we should be able to pass it
> something and it will check itself if it is currently reproducible"

Today the bug-fix pipeline's `reproducing` room asks an LLM `reproducer` persona
whether the bug reproduces and trusts its `bug_verified` claim — there is no
deterministic, exit-code-backed proof that the bug is *currently* reproducible
before we spend maker budget fixing it. In practice (and in this very dogfood
session) a human has to manually confirm "yes this bug still exists" with a repro
command before kicking off a fix. We want the story to do that itself: feed it a
repro command on the ticket, it runs it RED-first, and only proceeds if the bug
actually reproduces.

## What changes

A ticket can now carry a **`repro_command:`** field. At ticket-load the bugfix
story projects it into the existing `world.gate_command`. A new deterministic
**RED-gate check** runs the command on the unchanged (pre-fix) worktree *before*
the LLM reproducer / maker work:

- **RED** (non-zero exit) → bug confirmed reproducible → proceed to `proposing`.
- **GREEN** (zero exit) → cannot reproduce → route to a "not reproducible"
  needs-human exit (mirrors cherny-loop baseline's "gate proves nothing" branch).

This is structurally identical to `stories/cherny-loop/rooms/baseline.yaml`
(run `gate_command` on the unchanged artifact, branch on exit code), applied to
the ticket-driven bugfix pipeline. It makes `reproduction_artifact.bug_verified`
an *evidenced* fact rather than an LLM assertion, and it re-arms the already-built
RED-pre/GREEN-post regression discipline in `testing.yaml` + `delivery-tail`
verify, which is presently **dormant in the real dogfood path because no ticket
field feeds `gate_command` and `dev-story` doesn't project it.**

## Impact

- **Net-new:** 1 RED-gate step (extend `reproducing.yaml` `on_enter`, or a thin
  room between `idle` and `reproducing`); 1 new "not-reproducible" needs-human
  exit branch; ticket-schema field; one projection in dev-story `world_in`.
- **Engine/host changes:** none — composes existing `host.run` + worktree-snapshot
  vocabulary (`testing.yaml:133-168`) and the cherny-loop baseline pattern.
  The only Go change is additive: surface the new ticket frontmatter field in
  `internal/host/localfiles_ticket.go` (`bugSummary` + `ticket.get` output).
- **Docs on ship:** `docs/stories/bugfix.md`, `issues/README.md` (ticket schema),
  this folder's `README.md` entry; delete this proposal.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Carry a repro command on the ticket | bug frontmatter + localfiles ticket reader | `issues/README.md`, `internal/host/localfiles_ticket.go` (`bugSummary`, `get` op) |
| Project ticket field → world | `world_in` projection at ticket load | `stories/dev-story/app.yaml:1244` (`bf` import — currently omits `gate_command`) |
| Run a command, branch on exit code | `host.run` { cmd, args, cwd } → `ok`/`exit_code` | `stories/bugfix/rooms/testing.yaml:133-168` |
| RED-first "is it currently broken?" gate | run `gate_command` on unchanged artifact, RED→proceed / GREEN→rest | `stories/cherny-loop/rooms/baseline.yaml:37-73` |
| Regression confirmation after fix | gate RED on HEAD~1, GREEN on merged commit | `stories/bugfix/rooms/testing.yaml` + `stories/delivery-tail/` verify room (already built) |

## Story graph

```
idle ── start ──▶ reproducing ── repro_red ──▶ proposing ──▶ … ──▶ done
 │ (ticket load,                  │
 │  project repro_command         └─ repro_green ──▶ @exit:not-reproducible (needs-human)
 │  → world.gate_command)
 └─ quit ─▶ @exit:abandoned
```

Mark `reproducing` as the new deterministic checkpoint.

## World schema (sketch)

Reuses existing bugfix world vars — no new names strictly required:

```yaml
world:
  gate_command:          { type: string, default: "" }   # seeded from ticket.repro_command
  regression_red_pre_fix:{ type: bool,   default: false } # already exists (app.yaml:284)
  regression_pre_fix_log:{ type: string, default: "" }    # already exists
  repro_checked:         { type: bool,   default: false } # latch, mirror regression_gate_checked
```

`exits:` — add `not-reproducible: {}` alongside the existing exits.

## Per-room detail

### `reproducing` — deterministic RED-gate before the LLM reproducer

`on_enter`:
1. If `world.gate_command != ""` and not `repro_checked`: `host.run` the command
   on the pre-fix worktree (HEAD), bind `regression_red_pre_fix` = (exit != 0),
   capture `regression_pre_fix_log`, set `repro_checked`.
2. Branch:
   - `regression_red_pre_fix` (RED) → existing LLM reproducer step (now corroborating
     evidence, not sole source of truth) → `proposing`.
   - GREEN → `@exit:not-reproducible` with the captured log as the hand-off reason.
3. If `gate_command == ""` (legacy ticket, no repro field): fall through to current
   LLM-only behavior unchanged (backward compatible).

## Open questions

- Should a GREEN repro auto-close the ticket as `wontfix`/`cannot-reproduce`, or
  only route to needs-human? (Lean: needs-human first; auto-close later.)
- `repro_command` vs reusing `gate_command` as the ticket field name — `repro_command`
  reads better on a ticket; it maps to `gate_command` internally. (Lean: `repro_command`.)
- Flow-test fixtures: add a `bugfix_repro_red_then_proceed.yaml` and a
  `bugfix_repro_green_not_reproducible.yaml` mocked flow (no LLM) to gate both arcs.
