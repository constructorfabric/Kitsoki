# kitsoki-dev — the dogfood instance

The dogfood instance described in the
[bug-fix case study](../../../docs/case-studies/bug-fix.md).
This is the PoC milestone (★): **kitsoki working on kitsoki through
its own UI**, with the bug file as both ticket and conversation log.

The whole app is ~25 lines of YAML that imports `stories/dev-story/`
under the alias `core` and binds five `host_interfaces:` to concrete
providers:

| iface       | binding                  | what it does                                                     |
|-------------|--------------------------|------------------------------------------------------------------|
| `ticket`    | `host.local_files.ticket`| reads `issues/bugs/*.md` (+ feature files); transitions status   |
| `vcs`       | `host.git`               | local git CLI — branch, commit, diff, push, open_pr, merge       |
| `ci`        | `host.local`             | `go test ./...` and friends                                      |
| `workspace` | `host.git_worktree`      | `.worktrees/<task>` per ticket                                   |
| `transport` | `host.append_to_file`    | appends `## Comment <iso> by <author>` blocks INTO the bug file  |

The bug file IS the conversation log — every checkpoint artifact
(post, judge verdict, operator reply) gets appended. Nothing is lost
when the session ends.

---

## Two-command quickstart

```
$ kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml
```

Lands at `core.landing` — dev-story's engineer's-day landing room. From
there:

```
> tickets               # → core.ticket_search
> search open kitsoki bugs
> pick 2026-05-14T103205Z-tui-view-render-before-bind
> bugfix                # → core.bf.idle
> start                 # → core.bf.reproducing_executing
> proceed → accept …    # walk the 8-room pipeline
```

When the bugfix pipeline reaches `@exit:done`, dev-story hands off
into `pr-refinement` (a.k.a. `core.pr.open_pr`). The PR is opened via
`git push` + `gh pr create` (when `gh` is on PATH); ci_monitoring
polls until checks are green; merge fires; the bug file's
frontmatter flips to `status: resolved` and gains a final `##
Comment` block with the resolution sha.

### Autonomous variant

Flip the judge mode at boot via a warp scenario:

```
$ kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml --warp scenarios/autonomous_ready.yaml
```

`judge_mode: llm_then_human` makes every checkpoint:

1. Post the artifact (transport + inbox mirror).
2. Call the LLM-judge agent prompt.
3. If `confidence >= judge_confidence_threshold` (default 0.8) AND
   neither verdict nor intent is `uncertain`, auto-emit the LLM's
   verdict intent (typically `accept`) — advancing the pipeline
   without a turn.

The pipeline holds for human input only when the LLM hedges. The
operator returns to a TUI showing "reviewing_awaiting_reply:
artifact ready" and types `accept` / `refine` / `quit`. Mode swaps
are hot — `world_override.judge_mode = "human"` at any point
re-engages supervised driving without restart.

---

## Scenarios (warp bases)

| File                              | Lands at  | Primed for                                                                                 |
|-----------------------------------|-----------|--------------------------------------------------------------------------------------------|
| `scenarios/pickup_self_bug.yaml`  | `core.landing` | the self-bug `2026-05-14T103205Z-tui-view-render-before-bind`, supervised walk           |
| `scenarios/pickup_story_bug.yaml` | `core.landing` | the story-bug under `stories/oregon-trail/issues/bugs/`, supervised walk                 |
| `scenarios/autonomous_ready.yaml` | `core.landing` | same as `pickup_self_bug` but `judge_mode: llm_then_human` for an unattended run         |

Add new scenarios by dropping a YAML file in `scenarios/`. The
flow-fixture shape (`initial_state` + `initial_world`) also works —
a flow fixture can be loaded as a warp basis verbatim.

---

## Flow fixtures (CI smoke)

Run `kitsoki test flows .kitsoki/stories/kitsoki-dev/app.yaml` to walk the
canned closed-loop tests:

| Fixture                                  | What it proves                                                                  |
|------------------------------------------|---------------------------------------------------------------------------------|
| `flows/dogfood_smoke.yaml`               | the app loads; `iface.ticket.list_mine` resolves; navigation lifts work          |
| `flows/pickup_self_bug_supervised.yaml`  | 18-turn supervised walk: ticket pick → bf 8-room → @exit:done → pr → @exit:merged → main |
| `flows/pickup_story_bug_supervised.yaml` | same walk against `stories/oregon-trail/issues/bugs/<id>.md`; proves multi-glob coverage |
| `flows/pickup_autonomous_then_bail.yaml` | `llm_then_human` auto-fires 2 checkpoints → operator flips mode → state HOLDS → manual accept resumes |

All four use stubbed `host_handlers:` — no real LLM, git, or file
I/O. The on-disk smoke is documented below under "Manual
walkthrough".

---

## ticket_globs — what the multi-glob covers

The instance world key `ticket_globs:` documents the FULL scan
surface for forward compatibility:

```
issues/bugs/*.md                   — kitsoki self-bugs
issues/features/*.md               — kitsoki PRD-track features
stories/*/issues/bugs/*.md         — per-story bugs (story authoring)
stories/*/issues/features/*.md     — per-story features
```

A bug filed via `/meta story bug` against `stories/cloak/` would
land at `stories/cloak/issues/bugs/<id>.md` and is reachable from
the same `kitsoki-dev` app — only the file path differs. This is
what "devstory oversees kitsoki AND its stories" means in practice
(proposal §5.4).

**Today the `host.local_files.ticket` handler reads literally
`<root>/issues/bugs/*.md`** — the multi-glob isn't yet honoured at
the handler level (a future enhancement, see "Runtime gaps" below).
For the supervised flow walks in `flows/`, the stubbed
`host.local_files.ticket` returns a canned list; for the manual
walkthrough below, the operator runs from the kitsoki repo root so
the handler's `<root>` resolves to `pwd` and finds
`issues/bugs/*.md`.

---

## Manual walkthrough (the on-disk smoke)

This is the loop that proves the dogfood acceptance per the
[bug-fix case study](../../../docs/case-studies/bug-fix.md):
a kitsoki bug filed in one session is fixed via the dogfood
instance in a second session, the diff lands as a real commit, the
file's `status:` is `resolved`.

### Phase 0 — file the bug

The bug-filing CLI (`kitsoki bug create`) ships on main; here is how
to use it end-to-end. Two-step shell snippet that goes from a real
bug file to the bugfix pipeline:

```
$ kitsoki bug create --target kitsoki \
    --title "TUI view renders before on_enter binds" \
    --body "Expected: first frame shows bound values. Actual: '(pending)'." \
    --severity med
issues/bugs/2026-05-15T0407Z-tui-view-renders-before-on-enter-binds.md

$ kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml
# in the TUI: > tickets → > search "tui view" → > pick <id> → > bugfix → > start …
```

The first command writes a markdown file under `$KITSOKI_REPO/issues/bugs/`
with the frontmatter schema documented in
[`docs/stories/bugs.md`](../../../docs/stories/bugs.md) (and mirrored in
[`../../../issues/README.md`](../../../issues/README.md)). The second command
boots the dogfood instance, which scans the same directory via
`host.local_files.ticket` and picks the file up as a ticket.

Two pre-seeded examples ship in `issues/bugs/` for the Phase 3
acceptance smoke (one "view-render-before-bind", one
"glamour caps prose") so the walkthrough works without filing a fresh
bug first; either path (real `bug create` or one of the seeds) is
equivalent from the pipeline's perspective.

### Phase 1 — pick up the bug

```
$ kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml
```

You land at `core.landing`. The view shows your engineer's day
landing. Type `tickets` (or `core__go_ticket_search`) to enter the
ticket-search room. Then:

```
> search "tui view render"
```

This dispatches `iface.ticket.search` against
`host.local_files.ticket` with `query: "tui view render"`. The
handler scans `issues/bugs/*.md`, matches title + body substring,
and binds the result list into `world.ticket_results`. You see:

```
Results:
  - 2026-05-14T103205Z-tui-view-render-before-bind
    "TUI view templates render BEFORE on_enter binds — first frame shows '(pending)'…"
    status: open  priority: P2
```

Pick it:

```
> pick 2026-05-14T103205Z-tui-view-render-before-bind
```

The room sets `ticket_id`, `ticket_title`, `thread`. The
`thread` value is the file path itself — that's how the transport
binding knows where to append.

### Phase 2 — walk the pipeline

```
> bugfix
```

You enter `core.bf.idle`. From here the operator types `start`
(prefixed: `core__bf__start`) to enter `reproducing_executing`. The
LLM produces a reproduction artifact; the room binds it into
`world.core__bf__reproduction_artifact`. `proceed` advances to
`reproducing_awaiting_reply` whose `on_enter` chain:

1. `iface.transport.post` — appends the reproduction artifact to
   `issues/bugs/2026-05-14T103205Z-tui-view-render-before-bind.md`
   as `## Comment <iso> by kitsoki`.
2. `host.inbox.add` — mirrors the artifact into the TUI inbox.
3. (skipped in `human` mode) `iface.agent.ask_with_mcp` — runs the
   LLM-judge prompt.
4. (skipped in `human` mode) `emit_intent` — auto-fires the verdict.

You see the reproduction artifact rendered and the available
intents: `accept | refine [feedback=…] | restart_from [stage=…] |
quit`. Type `accept` to advance to `proposing_executing`.

Repeat for each of the seven checkpoints (reproducing → proposing →
implementing → testing → reviewing → validating → done). At each
checkpoint, **a new `## Comment` block is appended to the bug
file** with the artifact's summary markdown. By the end of the
walk, the bug file looks like:

```
---
id: 2026-05-14T103205Z-tui-view-render-before-bind
title: …
status: in_progress
…
---

## Body
…the original bug description…

## Comment 2026-05-14T14:00:00Z by kitsoki
**Reproduction artifact:** Trace confirms the ordering …

## Comment 2026-05-14T14:05:01Z by user
accept

## Comment 2026-05-14T14:07:21Z by kitsoki
**Fix proposal:** Defer the first view-render until on_enter binds complete …

## Comment 2026-05-14T14:08:14Z by user
accept

… (5 more, one per checkpoint)
```

### Phase 3 — handoff to PR refinement

`done.accept` fires `@exit:done`. The exit projection lifts the
done_artifact's `summary_title` / `summary_markdown` into
`world.pr_title` / `world.pr_body`. The runtime then transitions
into the `pr` import compound (`core.pr.open_pr`).

`pr.open_pr.on_enter` runs `iface.vcs.commit` →
`iface.vcs.push` → `iface.vcs.open_pr`. With `host.git`, that's:
local commit on the feature branch, push to `origin`, then `gh pr
create` (if `gh` is on PATH; otherwise a manual URL is logged).
`world.pr_id` / `world.pr_url` are bound from the host response.

`ci_monitoring` polls `iface.ci.remote_status` until checks are
green. `merge_executing` runs `iface.vcs.merge`. `merge_awaiting_reply`
is the last checkpoint — operator accepts; `@exit:merged` fires.

### Phase 4 — close the loop

The pr's `@exit:merged` projection sets `world.core__status =
"merged"` and `world.core__last_pr_url = <pr-url>`. The handoff's
post-merge effect calls `iface.ticket.transition(to: "resolved")`
which the local-files provider applies in place — rewriting the
frontmatter:

```
---
…
status: resolved
resolved_at: 2026-05-14T15:00:00Z
resolved_in_commit: <sha>
…
---
```

Loop closed. **The bug file is now ticket, conversation thread,
audit trail, and resolution record, in one Markdown file.**

---

## Runtime gaps — what blocks the FULLY-real PoC today

The four flow fixtures in this directory pass deterministically.
The manual walkthrough above works end-to-end against the real
on-disk seeds with the existing host handlers, with three known
caveats:

1. **`world_in:` doesn't interpolate `env.PWD`.** The instance
   world declares `repo_root` and `ticket_globs` for forward
   compatibility, but the expression engine (`internal/expr/`) has
   no `env.*` namespace today. The handler falls back to
   `$KITSOKI_TICKETS_ROOT` then to `os.Getwd()`, so running from
   the repo root works. A future enhancement could expose env vars
   via the expr `Env` struct (low cost, ~10 lines) and surface them
   to `world_in:` projections.

2. **`host.local_files.ticket` scans `<root>/issues/bugs/` only.**
   The multi-glob in `world.ticket_globs` is documented but not yet
   honoured at the handler — it lists `issues/bugs/`,
   `issues/features/`, `stories/*/issues/bugs/`,
   `stories/*/issues/features/`, but only the first is read today.
   The handler accepts a `globs` arg shape ready for the
   enhancement; the ticket rooms just need to pass
   `world.ticket_globs` through to `iface.ticket.search.args`.

3. **~~`/meta kitsoki bug` doesn't emit a file yet.~~ Resolved.**
   The bug-filing CLI (`kitsoki bug create`) ships on main and
   `/meta kitsoki bug` writes to `$KITSOKI_REPO/issues/bugs/`;
   `/meta story bug` writes to `<app-dir>/issues/bugs/`. Both use the
   same on-disk format documented in [`docs/stories/bugs.md`](../../../docs/stories/bugs.md).
   The dogfood loop reads + transitions the file the producer wrote;
   the loop is now closed end-to-end.

A fourth latent issue we surfaced while building this phase:

4. **`emit_intent:` expressions weren't being world-prefix
   rewritten across imports.** When the bugfix story was folded
   under dev-story and then again under kitsoki-dev, the
   `emit_intent: "{{ world.llm_verdict.intent }}"` template still
   referenced `world.llm_verdict` (bare) at runtime, so the
   autonomous mode silently no-op'd at every checkpoint. The fix
   was a one-line addition to `internal/app/imports_rewriter.go`'s
   `rewriteEffect` (rewrite `EmitIntent` and `EmitSlots` via
   `rewriteExpr`). Wave 2's flow fixtures didn't exercise the
   autonomous-through-imports path so this only surfaced at Phase
   3. Now fixed; the contract notes' §W2.8 should be amended to
   document this as a Wave-2 runtime-rewriter quirk that
   `dispatchEmittedIntents` depends on. See
   `pickup_autonomous_then_bail.yaml` for the regression coverage.

A fifth concession the flow fixtures take:

5. **Flow fixtures can't register the REAL `host.local_files.ticket`
   against a temp git repo.** `testrunner/flows.go`'s `HostHandlers`
   map only registers STUB handlers via a closure over the
   `HostStub.Data` blob. There's no path to register a real
   handler with custom args from a fixture, so the
   "assert the bug file gained `## Comment` blocks after the run"
   check from the proposal §8 Phase 3 acceptance criteria isn't
   expressible as a flow assertion today. The closest equivalent
   in `flows/pickup_self_bug_supervised.yaml` is `expect_world`
   probes that pin the projected world state at each transition —
   not byte-exact file-state, but the same state the
   real handler would write. The manual walkthrough exercises the
   on-disk side directly.

---

## File layout

```
.kitsoki/stories/kitsoki-dev/
├── app.yaml                      — the ~50-line dogfood instance
├── README.md                     — this file
├── scenarios/                    — boot-time warp bases
│   ├── pickup_self_bug.yaml
│   ├── pickup_story_bug.yaml
│   └── autonomous_ready.yaml
└── flows/                        — deterministic flow fixtures
    ├── dogfood_smoke.yaml
    ├── pickup_self_bug_supervised.yaml
    ├── pickup_story_bug_supervised.yaml
    └── pickup_autonomous_then_bail.yaml
```

The provider-host handlers
(`internal/host/{localfiles_ticket,git_vcs,local_ci,git_worktree,append_file_transport,inbox_add}.go`)
all ship in Wave 1; this phase only composes them. No new Go code
needed.

---

## See also

- [`../../../docs/case-studies/bug-fix.md`](../../../docs/case-studies/bug-fix.md)
  — the dogfood case study (kitsoki-dev shape, closed-loop
  walkthrough, acceptance).
- [`../../../docs/proposals/notes/dev-story-implementation-contract.md`](../../../docs/proposals/notes/dev-story-implementation-contract.md)
  Wave 2 / Phase 3 appendix.
- [`../../../stories/dev-story/README.md`](../../../stories/dev-story/README.md) — the hub this
  instance imports.
- [`../../../stories/bugfix/README.md`](../../../stories/bugfix/README.md),
  [`../../../stories/pr-refinement/README.md`](../../../stories/pr-refinement/README.md) — the
  two sub-stories chained inside dev-story.
- [`../../../issues/README.md`](../../../issues/README.md) — the bug-file
  schema and frontmatter contract.
