# Dev-story / bugfix unify — implementation contract (Wave 1)

Companion to the dev-story / bugfix unify work, which has since shipped
and is documented in `docs/case-studies/bug-fix.md`. Defines the
exact shared shapes that Phase 1 (bugfix skeleton + judge polymorphism +
foundational providers + judge harness) must agree on so multiple authors can
work in parallel without drifting. **All Wave 1 agents read this first.**

Status: spec for Phase 1 only. Phase 2+ extends the contract as needed.

## 1. Repository conventions to follow

- New host handlers go in `internal/host/` (flat package, same as
  `handlers.go`, `agent_ask.go`, `transport_post.go`). One file per logical
  surface (e.g. `localfiles_ticket.go`, `git_vcs.go`, `local_ci.go`,
  `git_worktree.go`, `append_file_transport.go`, `inbox_add.go`).
- Each new handler registers itself in `RegisterBuiltins` in `handlers.go`.
- Each handler ships with a `*_test.go` in the same package that exercises
  happy + at least one failure path with table-driven cases — same shape as
  `transport_post_test.go`.
- Story directories use the existing convention: `stories/<name>/{app.yaml,
  README.md, rooms/*.yaml, prompts/*.md, schemas/*.json, flows/*.yaml,
  scenarios/*.yaml}`. The mandatory README documents the contract per
  `docs/stories/imports.md` "File layout."
- Use `kitsoki test flows stories/<name>/app.yaml` for story-level testing
  (works today — see `stories/oregon-trail/flows/`).

## 2. The six `host_interfaces:` — canonical operation schemas

Every story that uses these interfaces declares them with **exactly** these
op names, input shapes, and output shapes. Providers register handlers under
the names listed in §3.

### 2.1 `ticket` — issue tracker

```yaml
host_interfaces:
  ticket:
    description: "Issue tracker abstraction (file / GitHub Issues / Jira)."
    operations:
      search:
        input:  { query: string, limit: int }
        output: { tickets: list }      # [{id,title,status,priority,assignee,url}]
      get:
        input:  { id: string }
        output: { id: string, title: string, body: string, status: string,
                  priority: string, assignee: string, url: string, comments: list }
      comment:
        input:  { id: string, body: string, thread: string }
        output: { ok: bool, comment_id: string }
      transition:
        input:  { id: string, to: string }
        output: { ok: bool }
      list_mine:
        input:  { filter: string }
        output: { tickets: list }
    default: host.local_files.ticket
```

### 2.2 `vcs` — version control + PR host

```yaml
host_interfaces:
  vcs:
    description: "Branch / commit / PR abstraction (git / GitHub / Bitbucket)."
    operations:
      branch:
        input:  { workdir: string, name: string, base: string }
        output: { ok: bool, branch: string }
      diff:
        input:  { workdir: string }
        output: { diff: string, files: list }
      commit:
        input:  { workdir: string, message: string, files: list }
        output: { ok: bool, sha: string }
      push:
        input:  { workdir: string, remote: string }
        output: { ok: bool, url: string }
      open_pr:
        input:  { workdir: string, title: string, body: string, base: string }
        output: { ok: bool, url: string, pr_id: string }
      pr_status:
        input:  { pr_id: string }
        output: { state: string, checks: list, comments: list }
      pr_comment:
        input:  { pr_id: string, body: string }
        output: { ok: bool }
    default: host.git
```

### 2.3 `ci` — build & test runner

```yaml
host_interfaces:
  ci:
    description: "Build/test runner (local make/go test, GitHub Actions, Jenkins)."
    operations:
      run_tests:
        input:  { workdir: string, target: string }
        output: { ok: bool, passed: int, failed: int, log: string, junit: string }
      build:
        input:  { workdir: string, target: string }
        output: { ok: bool, log: string }
      remote_status:
        input:  { pr_id: string }
        output: { state: string, checks: list }
    default: host.local
```

### 2.4 `workspace` — per-task working tree

```yaml
host_interfaces:
  workspace:
    description: "Working-copy manager. Local: git worktree."
    operations:
      list:
        input:  {}
        output: { workspaces: list }
      get:
        input:  { id: string }
        output: { id: string, path: string, branch: string, dirty: bool }
      create:
        input:  { name: string, ticket_id: string, base: string }
        output: { ok: bool, path: string }
      sync:
        input:  { id: string }
        output: { ok: bool, log: string }
    default: host.git_worktree
```

### 2.5 `transport` — out-of-band channel for checkpoint artifacts

```yaml
host_interfaces:
  transport:
    description: "Out-of-band channel for posting proposals, checkpoints, status."
    operations:
      post:
        input:  { thread: string, body: string }
        output: { ok: bool, message_id: string }
    default: host.append_to_file   # project wrappers can rebind to tracker comments
```

### 2.6 `inbox` — local TUI inbox mirror (NOT an iface, registered as `host.inbox.add` directly)

The inbox is intentionally **not** an `host_interfaces:` block — it has only
one op and it's always-on. Stories invoke it as a bare host call:

```yaml
on_enter:
  - invoke: host.inbox.add
    with:
      kind:    checkpoint            # checkpoint | ack | info
      title:   "Reproduction artifact: {{ world.ticket_id }}"
      thread:  "{{ world.thread }}"
      state:   bugfix_reproduce_awaiting_reply
      body:    "{{ world.reproduction_artifact.summary_markdown }}"
```

`host.inbox.add` is always-on across modes — see proposal §4.5.

**Adapter wiring (closed in commit `e6c949f`).** The handler defers
persistence to an `InboxAdder` seam (see
`internal/host/inbox_add.go`).  Production install lives at
`internal/inbox/jobstore_adapter.go::JobStoreAdder` — bridges into
`jobs.JobStore.InsertNotification` with a per-turn session ID,
injected into ctx by the orchestrator's `dispatchHostCalls`.
Severity map: `checkpoint` and `action_required` →
`SeverityActionRequired`; `ack` → `SeveritySuccess`;
`info`/unknown → `SeverityInfo`.  Without this seam (closed P1-C
from the Opus code review) every production `host.inbox.add` call
returned `persisted:false` and the notification was dropped on
the floor.

## 3. Handler names (Go side)

These are the strings passed to `Registry.Register(name, handler)`. Stories
reference them via `host_interfaces.<iface>.default` or `host_bindings`.

| Handler name | Iface op(s) it backs | File |
|---|---|---|
| `host.local_files.ticket` (prefix-fallback handler) | all `ticket.*` ops | `internal/host/localfiles_ticket.go` |
| `host.local_files.ticket.search` | optional split | — |
| `host.local_files.ticket.get` | optional split | — |
| `host.local_files.ticket.comment` | optional split | — |
| `host.local_files.ticket.transition` | optional split | — |
| `host.local_files.ticket.list_mine` | optional split | — |
| `host.git` (prefix-fallback handler) | all `vcs.*` ops | `internal/host/git_vcs.go` |
| `host.local` (prefix-fallback handler) | all `ci.*` ops | `internal/host/local_ci.go` |
| `host.git_worktree` (prefix-fallback handler) | all `workspace.*` ops | `internal/host/git_worktree.go` |
| `host.append_to_file` | `transport.post` (writes to bug file) | `internal/host/append_file_transport.go` |
| `host.inbox.add` | bare inbox call (not iface) | `internal/host/inbox_add.go` |

The proposal's `host.stdout` (a no-op transport for tests / standalone runs)
already maps to existing `host.run` patterns; if a stand-alone fallback is
needed, register `host.stdout` as a thin alias in `inbox_add.go`'s file (or
add `internal/host/stdout_transport.go`).

The runtime registry's **prefix-fallback** means a single registration of
`host.git` satisfies every `host.git.<op>` call until per-op handlers are
registered. Wave 1 ships only the prefix-fallback handlers (one per surface);
per-op handlers come later if and when an op needs distinct behaviour.

## 4. World shape — Wave 1 keys

These keys are declared in `stories/bugfix/app.yaml`'s `world:` block.
Provider handlers populate them via `bind:` projections in `on_enter`.

```yaml
world:
  # ─── Identity / ticket ──────────────────────────────────────────
  ticket_id:        { type: string, default: "" }
  ticket_title:     { type: string, default: "" }
  ticket_status:    { type: string, default: "" }
  ticket_url:       { type: string, default: "" }
  thread:           { type: string, default: "" }
  allowed_authors:  { type: list,   default: [] }

  # ─── Workspace ──────────────────────────────────────────────────
  workspace_id:     { type: string, default: "" }
  workdir:          { type: string, default: "" }
  base_branch:      { type: string, default: "" }
  feature_branch:   { type: string, default: "" }

  # ─── Pipeline control ───────────────────────────────────────────
  bugfix_mode:      { type: string, default: "full" }  # full | quick
  judge_mode:       { type: string, default: "human" } # human | llm | llm_then_human
  judge_confidence_threshold: { type: float, default: 0.8 }
  cycle:            { type: int,    default: 0 }
  last_reply_author: { type: string, default: "" }
  refine_feedback:   { type: string, default: "" }
  jump_to:           { type: string, default: "" }
  restart_from_stage: { type: string, default: "" }

  # ─── Per-room artifacts (Wave 1 ships 5; testing/reviewing collapse) ─
  reproduction_artifact:    { type: object, default: {} }
  propose_fix_artifact:     { type: object, default: {} }
  implement_review_artifact: { type: object, default: {} }
  validate_artifact:        { type: object, default: {} }
  done_artifact:            { type: object, default: {} }

  # ─── Judge state (set by judge harness, read by gate clauses) ────
  llm_verdict:      { type: object, default: {} }      # { intent, reason, confidence, verdict }

  # ─── PR (populated by pr-refinement; held here for round-trip) ───
  pr_id:            { type: string, default: "" }
  pr_url:           { type: string, default: "" }
  ci_state:         { type: string, default: "" }

  # ─── Story-level "done" sink for the standalone test mode ────────
  status:           { type: string, default: "open" }
```

Provider handlers are allowed to set additional namespaced keys
(`ticket__<x>`, `workspace__<x>`) when surfacing implementation-specific
detail, but the keys above are the canonical lingua franca.

## 5. Judge verdict schema (canonical)

`stories/bugfix/schemas/judge_verdict.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title":   "judge_verdict",
  "type":    "object",
  "required": ["verdict", "intent", "reason", "confidence"],
  "properties": {
    "verdict":    { "type": "string", "enum": ["pass", "fail", "uncertain"] },
    "intent":     { "type": "string", "enum": ["accept", "refine", "restart_from", "quit", "uncertain"] },
    "reason":     { "type": "string", "minLength": 4 },
    "confidence": { "type": "number", "minimum": 0.0, "maximum": 1.0 }
  },
  "additionalProperties": false
}
```

Used by `judge_*.md` prompts as the `schema:` argument to
`host.agent.ask_with_mcp`. The structured response is bound to
`world.llm_verdict` so gate clauses can read it.

## 6. Judge polymorphism — the canonical checkpoint shape

Every `*_awaiting_reply` state in `stories/bugfix/` follows this exact
pattern:

```yaml
<phase>_awaiting_reply:
  description: "<phase> artifact posted; awaiting verdict."
  relevant_world: [judge_mode, ticket_id, <phase>_artifact, llm_verdict]
  on_enter:
    # 1. Always: post the artifact to whichever transport is bound.
    - invoke: iface.transport.post
      with:
        thread: "{{ world.thread }}"
        body:   "{{ world.<phase>_artifact.summary_markdown }}"

    # 2. Always: mirror the artifact into the local inbox.
    - invoke: host.inbox.add
      with:
        kind:    checkpoint
        title:   "{{ world.<phase>_artifact.summary_title }}"
        thread:  "{{ world.thread }}"
        state:   <phase>_awaiting_reply
        body:    "{{ world.<phase>_artifact.summary_markdown }}"

    # 3. Conditionally: ask an LLM-judge.
    - when: "world.judge_mode == 'llm' || world.judge_mode == 'llm_then_human'"
      invoke: host.agent.ask_with_mcp
      with:
        prompt:  prompts/judge_<phase>.md
        schema:  schemas/judge_verdict.json
        context: "{{ world.<phase>_artifact }}"
      bind:
        llm_verdict: "submitted"

    # 4. Conditionally: auto-fire the LLM's intent if confident.
    - when: |
        world.judge_mode != 'human' &&
        world.llm_verdict.confidence >= world.judge_confidence_threshold &&
        world.llm_verdict.verdict != 'uncertain' &&
        world.llm_verdict.intent != 'uncertain'
      effects:
        - emit_intent: "{{ world.llm_verdict.intent }}"
          slots: { feedback: "{{ world.llm_verdict.reason }}" }

  on:
    accept:        [{ target: <next-room>_executing }]
    refine:        [{ target: <phase>_executing, effects: [{ set: { refine_feedback: "{{ slots.feedback }}", cycle: "{{ world.cycle + 1 }}" }}]}]
    restart_from:  [{ target: <phase>_executing, effects: [{ set: { restart_from_stage: "{{ slots.stage }}", cycle: 0 }}]}]
    quit:          [{ target: "@exit:abandoned" }]
```

This shape MUST be identical across all seven rooms (only `<phase>` and
`<next-room>` vary). Two things to flag for the bugfix-story author:

- The `emit_intent:` effect ships end-to-end (machine dispatch +
  orchestrator post-bind re-evaluation; see
  `internal/machine/machine.go::DispatchPostBindEmits` and
  `internal/orchestrator/orchestrator.go::settlePostBindEmits`).
  Two depth caps protect against cycles: the machine's
  `EmitIntentMaxDepth` (= 8) bounds one chain of in-machine
  dispatches; `orchestrator.OrchestratorPostBindMaxDepth` (= 4)
  bounds the outer settle loop where a host call binds → emit
  fires → target's on_enter has another host call binds → another
  emit fires.  Total budget per turn is 32 emits.  Hitting either
  cap fails loud — the orchestrator appends a `store.HarnessError`
  event and populates `TurnOutcome.HarnessError` so a caller can
  surface the "why" rather than seeing a silent half-bound limbo.
  (P1-A/B from the Opus code review; commit `ca11d6b`.)
  The author writes the shape above verbatim; no per-mode YAML forks.
- The `bind: { llm_verdict: "submitted" }` syntax follows the existing
  `host.agent.ask_with_mcp` bind convention (see `internal/host/agent_ask_with_mcp.go`).
  Because `bind:` lands at orchestrator-dispatch time (not machine-time),
  the emit_intent's `when:` guard is re-evaluated after the host call
  completes — that's what makes the LLM-judge → auto-accept hop work
  in one externally-initiated turn.
- `relevant_world` lists every key the state's view + on_enter touches so
  the TUI can subscribe properly.

## 7. Visible rooms (Wave 1 happy-path set)

Phase 1 ships **only the happy path** — no cycle budgets, no
`restart_from_stage` plumbing beyond the intent landing, only
`accept` / `refine` / `quit` plus `restart_from` as a no-op-target stub.

Each room has an `_executing` state (auto-runs the phase, binds the artifact)
and an `_awaiting_reply` state (the checkpoint per §6).

| Room | Next on `accept` |
|---|---|
| `idle` | `reproducing_executing` (via intent `start`) |
| `reproducing_executing` → `reproducing_awaiting_reply` | `proposing_executing` |
| `proposing_executing` → `proposing_awaiting_reply` | `implementing_executing` |
| `implementing_executing` (no checkpoint) | `testing_executing` |
| `testing_executing` → `testing_awaiting_reply` | `reviewing_executing` |
| `reviewing_executing` (no checkpoint) | `validating_executing` |
| `validating_executing` → `validating_awaiting_reply` | `done_executing` |
| `done_executing` → `done_awaiting_reply` | `@exit:done` |

Exits (used by parent stories that import `bugfix`):

```yaml
exits:
  done:
    description: "Pipeline succeeded; handoff to pr-refinement."
    requires: [done_artifact]
  abandoned:
    description: "User or LLM bailed."
```

Standalone (no parent) Wave 1 just terminates at `@exit:done`.

## 8. Flow fixtures Wave 1 ships

Under `stories/bugfix/flows/`:

| Flow | Judge mode | Expected outcome |
|---|---|---|
| `happy_human.yaml` | `human` | accept at every checkpoint → done |
| `happy_llm.yaml` | `llm` | LLM auto-accepts with confidence 0.9 → done |
| `happy_llm_then_human.yaml` | `llm_then_human` | LLM auto-accepts → done |
| `llm_uncertain_bails_to_human.yaml` | `llm_then_human` | LLM verdict.confidence=0.5 → state holds, human types accept → done |
| `refine_once_then_accept.yaml` | `human` | reproducing: refine → re-execute → accept → done |
| `quit_at_proposing.yaml` | `human` | quit → @exit:abandoned |
| `llm_strict_rejects.yaml` | `llm` | LLM uncertain → no human → flow expects timeout / loop break |
| `mixed_judge_swap.yaml` | starts `llm_then_human`, halfway flips to `human` via world mutation | demonstrates mid-run mode swap |

That's 8 flows minimum; aim for 10–12 to cover edge cases.

## 9. What Wave 1 does NOT include

These are explicit non-goals for Wave 1 (they land in Wave 2):

- `stories/pr-refinement/`, `stories/dev-story/`, `.kitsoki/stories/kitsoki-dev/`
- `stories/implementation/`, `stories/code-review/`, `stories/cypilot/`
- `internal/host/` handlers for github / cypilot_artifacts / jira
- The `kitsoki bug create` CLI surface (lives in
  `bug-format-proposal.md`'s Phase A; consumed but not produced by Wave 1)
- `issues/bugs/README.md` and any seed bugs (Wave 2 local-instance task)
- Cycle budgets, full `restart_from_stage` semantics, `quick_fix` /
  `skip_to_pr` / `full_pipeline` intent shortcuts
- Provider sync to external trackers (`external:` frontmatter handling)
- The `bug create` integration with kitsoki-bug-reporter agent

## 10. Filesystem contract — who touches what

Wave 1 has three independent slices. They never touch the same file.

### Slice α — bugfix story author
- Creates: `stories/bugfix/{app.yaml, README.md, rooms/*.yaml, prompts/*.md, schemas/judge_verdict.json, flows/*.yaml}`
- Reads (does not modify): `stories/robbery/`, `stories/oregon-trail/`,
  `testdata/apps/dev-story/rooms/bugfix.yaml`, `docs/stories/imports.md`,
  this contract.
- Test: `kitsoki test flows stories/bugfix/app.yaml`

### Slice β — provider host handlers author
- Creates: `internal/host/{localfiles_ticket.go, localfiles_ticket_test.go,
  git_vcs.go, git_vcs_test.go, local_ci.go, local_ci_test.go,
  git_worktree.go, git_worktree_test.go, append_file_transport.go,
  append_file_transport_test.go, inbox_add.go, inbox_add_test.go}`
- Modifies: `internal/host/handlers.go` (adds registrations in
  `RegisterBuiltins`)
- Reads: `internal/host/{handlers.go, transport_post.go, agent_ask.go,
  host.go}` for handler conventions; `internal/inbox/inbox.go`,
  `internal/transport/transport.go`, `internal/workspace/workspace.go` for
  existing service APIs; `bug-format-proposal.md` for the bug file schema;
  this contract.
- Test: `go test ./internal/host/...`

### Slice γ — judge harness author
- Creates: `internal/judges/{judges.go, judges_test.go}` —
  provides a `RunJudge(ctx, prompt, schema, context) (Verdict, error)`
  function that wraps `host.agent.ask_with_mcp` and returns a typed
  `Verdict` struct. The wrapper validates the structured response against
  the schema, returns a clear error on parse failure, and emits a typed
  `Verdict { Verdict, Intent, Reason string; Confidence float64 }`.
- Creates: `stories/bugfix/prompts/judge_reproducing.md` (and one per
  checkpointed room — copy/paste with the artifact name swapped). Slice α
  may choose to author the per-phase prompts instead; this is a soft
  boundary — coordinate via the prompts/ subtree (one prompt per file,
  no overlap on file names).
- Reads: `internal/host/agent_ask_with_mcp.go`, `internal/mcp/validator.go`,
  this contract.
- Test: `go test ./internal/judges/...`

## 11. After Wave 1

The integration test that closes Phase 1 is `kitsoki test flows
stories/bugfix/app.yaml` passing all ~10 flows. Once green:

- Wave 2 fans out on Phases 2–6 (pr-refinement, dev-story, kitsoki-dev,
  implementation, code-review, cypilot, github provider).
- The runtime gaps surfaced by Slice α (e.g. `emit_intent:` /
  `when:`-on-`on_enter`) get fixed in a focused follow-up before Wave 2,
  if they are not yet supported.

See proposal §8 for the full Phase 2–8 plan.

---

# Wave 2 contract additions (Phase 2 — pr-refinement + dev-story hub)

Wave 1 shipped `stories/bugfix/` plus the six base host_interfaces and
the canonical §6 checkpoint shape. Wave 2 (this section) adds the
`stories/pr-refinement/` first-class story and the `stories/dev-story/`
engineer's-day hub that imports bf + pr. The additions below are
strictly additive — every Wave 1 contract clause still holds.

## W2.1 — New iface op: `vcs.merge`

`pr-refinement`'s `merge_executing` room calls `iface.vcs.merge` to
land the PR after CI passes. The op extends contract §2.2's `vcs`
interface:

```yaml
vcs:
  operations:
    merge:
      input:  { pr_id: string, strategy: string }   # strategy: squash | merge | rebase
      output: { ok: bool, sha: string }
```

The default `host.git` handler backs it through the registry's
prefix-fallback (one stub returning `{ok, sha}` satisfies the call in
flow fixtures). A `host.github` rebind in Wave 3 will shell out to
`gh pr merge --<strategy>`. Other project wrappers can rebind the
same interface to their review provider's merge endpoint.

The op is **PR-refinement-owned**: per proposal §10 question 6
(pragmatic reading), the merge lives inside pr-refinement and the
parent story consumes the `merged` exit. Stories that need a separate
merge confirmation can refine instead of accept at
`merge_awaiting_reply`, falling back to ci_monitoring.

## W2.2 — `pr-refinement` world keys

The new keys declared in `stories/pr-refinement/app.yaml`:

```yaml
world:
  # PR identity
  pr_id:            { type: string, default: "" }
  pr_url:           { type: string, default: "" }
  pr_title:         { type: string, default: "" }
  pr_body:          { type: string, default: "" }

  # CI state (poll output)
  ci_state:         { type: string, default: "" }    # pending | success | failure | error
  ci_attempts:      { type: int,    default: 0 }
  ci_log:           { type: string, default: "" }
  ci_failed_checks: { type: string, default: "" }

  # Review-comment state
  pr_comments:      { type: string, default: "" }    # JSON-ish blob from iface.vcs.pr_status
  pending_comments: { type: int,    default: 0 }

  # Pipeline control
  ci_poll_budget:   { type: int,    default: 5 }
  merge_strategy:   { type: string, default: "squash" }

  # New checkpoint artifact
  diagnose_artifact: { type: object, default: {} }

  # Close-out
  merge_sha:        { type: string, default: "" }
  status:           { type: string, default: "open" }   # open | merged | abandoned
```

The existing Wave 1 keys (`judge_mode`, `judge_confidence_threshold`,
`cycle`, `refine_feedback`, `llm_verdict`, `thread`, `ticket_id`,
`ticket_title`, `workdir`, `base_branch`, `feature_branch`) carry
straight through — pr-refinement uses the same vocabulary as bugfix.

## W2.3 — New schemas

| File | Purpose |
|---|---|
| `stories/pr-refinement/schemas/judge_verdict.json` | Identical to bugfix's `judge_verdict.json` (canonical §5 schema). Verbatim duplicate so pr-refinement can be loaded without a bugfix dependency. |
| `stories/pr-refinement/schemas/diagnose_artifact.json` | New. `{ summary_title, summary_markdown, root_cause, fix_description, affected_files, failing_checks, confidence, reasoning }`. Produced by `diagnose_executing` and judged at `diagnose_awaiting_reply`. |

## W2.4 — New exits

pr-refinement adds three named return points:

| Name | requires | Description |
|---|---|---|
| `merged` | `pr_url` | PR landed. Parent story consumes. |
| `abandoned` | (none) | User or LLM bailed. |
| `pushback_resolved` | (none) | Review pushback addressed; reserved for Wave 3 re-review loops. Wave 2's flows do not exercise it. |

## W2.5 — `exports.intents:` on bugfix and pr-refinement

Wave 1's `stories/bugfix/app.yaml` and Wave 2's
`stories/pr-refinement/app.yaml` both now declare `exports.intents:`
so importing parent stories (dev-story, kitsoki-dev) can lift
imported intents into the parent's bare intent table via
`imports.<alias>.intents.import`. This is a docs-only change —
no behaviour shifts, just unlocks a Wave 2 surface.

```yaml
# stories/bugfix/app.yaml
exports:
  intents: [start, proceed, accept, refine, restart_from, quit, look]

# stories/pr-refinement/app.yaml
exports:
  intents: [open, monitor, proceed, retry, resolve, merge_now, accept, refine, quit, look]
```

## W2.6 — Dev-story import shape

`stories/dev-story/app.yaml` ships the canonical hub composition.
The bf → pr handoff is one import edge: bf's `@exit:done` writes
`pr_title` / `pr_body` in the parent via the importer's `exits.done.set`
projection (read from `world.bf__done_artifact.summary_title` /
`summary_markdown`), then transitions into the `pr` compound (entry:
`open_pr`). pr's own `world_in:` projects those parent keys into
`pr__<key>` and the pr-refinement room chain runs.

```yaml
# stories/dev-story/app.yaml (excerpt)
imports:
  bf:
    source: ../bugfix
    entry: idle
    world_in: { ticket_id: "{{ world.ticket_id }}", … }
    intents: { import: [start] }       # only bf-unique bare names
    exits:
      done:      { to: pr,   set: { pr_title: "{{ world.bf__done_artifact.summary_title }}", pr_body: "{{ world.bf__done_artifact.summary_markdown }}" } }
      abandoned: { to: main, set: { status: "abandoned" } }

  pr:
    source: ../pr-refinement
    entry: open_pr                     # bypass pr-refinement's standalone-only idle
    world_in: { ticket_id: "{{ world.ticket_id }}", pr_title: "{{ world.pr_title }}", … }
    intents: { import: [open, monitor, retry, resolve, merge_now] }
    exits:
      merged:           { to: main, set: { status: "merged", last_pr_url: "{{ world.pr__pr_url }}" } }
      abandoned:        { to: main, set: { status: "abandoned" } }
      pushback_resolved:{ to: main }
```

Two intent-import constraints learned in Wave 2:

1. **Collision rule.** A parent's `intents.import: [X]` lifts the
   child's `X` to the parent's bare intent table. The loader rejects
   the lift if the bare name already exists in the parent OR has
   already been lifted by a previous import. dev-story imports only
   the *unique* bare names from each child (bf: `start`; pr:
   `open, monitor, retry, resolve, merge_now`). Overlapping names
   like `accept` / `refine` / `proceed` / `quit` / `look` stay
   prefixed (`bf__accept`, `pr__accept`).

2. **Dispatch via prefixed name.** Inside an imported child state,
   the on-arc was rewritten to the prefixed name (`bf__accept` for
   a child arc that authored `accept:`). The runtime dispatcher
   routes through that prefixed name; the operator types
   `bf__accept` (or `pr__accept`) when inside the imported
   compound. The `intents.import` lift is purely for parent-level
   menu surfaces (e.g. type-ahead completion); it does not change
   the on-arc dispatch path.

## W2.7 — `entry: open_pr` skips pr-refinement's standalone idle

pr-refinement ships an `idle` state as its `root:` for standalone
runs and flow fixtures (the `kitsoki test flows` seedInitialState
path bypasses on_enter; an idle entry lets the first turn fire
`open_pr.on_enter` via a real transition). When dev-story imports
pr-refinement with `entry: open_pr`, the import compound drills
straight into open_pr — the standalone idle state is unreachable
from the importer.

This pattern (a standalone-only idle entry for flow fixtures + a
real entry state for importers) is reusable for any future
sub-story whose entry runs material on_enter chains.

## W2.8 — Runtime quirks confirmed (no contract change)

The runtime quirks called out in the Wave 1 follow-up landed and
continue to behave as the contract anticipates. For Wave 2 author
reference:

- `bind:` lands at orchestrator-dispatch time, not machine-time.
  `when:` guards on a subsequent `on_enter` effect that read the
  bound value DEFER via a post-bind re-evaluation pass. The bf
  story's checkpoint shape exercises this; pr-refinement's diagnose
  checkpoint reuses the exact pattern.
- **Views render AFTER binds (closed).** Previously `machine.Turn`
  rendered the view against the pre-bind world snapshot, which forced
  every checkpoint view to scatter `?? "(pending)"` defaults over any
  field populated by an `on_enter:` `bind:` — or else the render would
  error on the first entry and abort the turn. As of the
  `feat: machine — render view once after host bind settles` commit,
  `machine.Turn` SKIPS the pre-bind render when any queued host call
  declares `bind:`; the orchestrator's existing
  `dispatchHostCalls → RenderState` pass owns the final view against
  the post-bind world. Authors only need `??` defaults for
  conditionally-bound fields (e.g. `world.llm_verdict.*`, populated
  only when `judge_mode in ('llm','llm_then_human')`).
- Parallel-state `emit_intent` is **dropped with a trace warning,
  not a hard error** (closed in `fix: machine — consistent log+drop
  for parallel-state emit_intent across all three sites`).  Three
  call-sites previously disagreed — `machine.dispatchEmittedIntents`
  errored loudly, `parallel.turnParallel` warn-logged silently, and
  `machine.DispatchPostBindEmits` returned a silent no-op.  All
  three now emit `machine.intent.emit.parallel_dropped` (constant
  `trace.EvIntentEmitParallelDropped`) with attributes
  `{site, intent, state}` and return no error so an otherwise-valid
  story is not bricked by a parallel-region author shape.  Wave 2's
  pr-refinement and dev-story hub do not use parallel encoding
  regardless.
- **`emit_intent` inside on_error / timeout / on_complete chains
  steers the final landing state.** Three orchestrator callsites
  previously discarded the post-emit state path (closed P1-D from
  the Opus code review, commit `322abab`).  After the fix:
  + an `on_error:` redirect whose error state's on_enter emits a
    follow-on intent lands at the emit's target, not the literal
    error state;
  + a state with `timeout: { target: X }` whose `X.on_enter`
    emits follows through to the emit target;
  + a background job's `on_complete:` chain that emits follows
    through to the emit target (Target: effects still only fire
    when no emit has already routed).
  All three sites now call `machine.RunEffectsAndState` and route
  the returned leaf into their surrounding state-update logic.

## W2.9 — What Wave 2 does NOT include

Explicit non-goals for Wave 2 (deferred to Wave 3+):

- `.kitsoki/stories/kitsoki-dev/` instance with `host.local_files.*` bindings
  (Wave 3 - Phase 3 of the proposal). The self-hosted loop closes there.
- `stories/implementation/`, `stories/code-review/`, `stories/cypilot/`
  sub-stories (Wave 3 — Phases 5–6). dev-story's rooms for these are
  Wave-3 stubs that route back to `main`.
- A `pushback_resolved` exit consumer in pr-refinement's room graph
  (the exit is declared but no in-flow path produces it; Wave 3
  re-review loops will).
- Retiring `testdata/apps/dev-story/`. The stub still backs
  `internal/app/loader_metamode_test.go` and several flow tests;
  Wave 3 retires it once no test references it.

## W2.10 — Wave 2 test surface (acceptance)

| Story | Flow count | All pass? |
|---|---|---|
| `stories/bugfix/` | 10 | yes (Wave 1 preserved) |
| `stories/pr-refinement/` | 4 | yes |
| `stories/dev-story/` | 4 | yes (includes bf → pr full-chain) |
| `stories/oregon-trail/` | 28 | yes (no regression) |
| `.kitsoki/stories/kitsoki-dev/` (Phase 3) | 4 | yes (Wave 2 / Phase 3 — see below) |

Plus `go test ./...` is fully green.

## W2.11 - Wave 2 / Phase 3 - kitsoki-dev self-hosted appendix

Phase 3 of the proposal (the PoC milestone ★) lands
`.kitsoki/stories/kitsoki-dev/` - a ~50-line instance that imports
`stories/dev-story/` under the alias `core` with concrete bindings
to the local-files providers (`host.local_files.ticket`, `host.git`,
`host.local`, `host.git_worktree`, `host.append_to_file`,
`host.inbox.add`, `host.agent.ask_with_mcp`). The PoC is "kitsoki
working on kitsoki through its own UI, with the bug file as both
ticket and conversation log".

### W2.11.1 — Instance-level world keys (forward-looking)

`.kitsoki/stories/kitsoki-dev/app.yaml`'s `world:` block adds three keys for
the self-hosted provider boundary that aren't consumed by any current room:

```yaml
world:
  repo_root:    { type: string, default: "" }
  ticket_globs: { type: string, default: "issues/bugs/*.md,issues/features/*.md,stories/*/issues/bugs/*.md,stories/*/issues/features/*.md" }
  autonomous_default_mode: { type: string, default: "llm_then_human" }
```

These are documented at the instance for Wave 3.5 / Phase 5+
consumers:

- `repo_root` will be plumbed through to `iface.ticket.*` args as
  `root:` once the local-files provider gains explicit `root` arg
  support at the iface call site (current handler falls back to
  `$KITSOKI_TICKETS_ROOT` then `os.Getwd()`).
- `ticket_globs` will be honoured at the handler once the
  multi-glob scan ships (today the handler reads
  `<root>/issues/bugs/*.md` only). The self-hosted instance's value
  documents the FULL scan target so a future enhancement only
  needs to thread the world key through.
- `autonomous_default_mode` is informational — the actual judge
  selection happens on `world.judge_mode` (= `human` | `llm` |
  `llm_then_human`). The instance docs it as a hint for warp
  scenarios.

### W2.11.2 — `env.PWD` interpolation in `world_in:` — NOT implemented

The proposal §5.2 example shows `world_in: { repo_root: "{{ env.PWD
}}" }`. The expression engine (`internal/expr/`) has no `env.*`
namespace; the `expr.Env` struct exposes `Slots`, `World`, `Event`,
`Run`. Adding env-var access is a ~10-line enhancement (add an
`Env map[string]string` field to `expr.Env`, populated at
session-start from `os.Environ()`, accessed in expressions as
`env.PWD`). Phase 3 sidesteps this by leaving `repo_root` as an
empty default and relying on the local-files provider's cwd
fallback. Operators running from any directory other than the
kitsoki repo root will need `KITSOKI_TICKETS_ROOT` exported or
`--set repo_root=/abs/path` at session start.

### W2.11.3 — Multi-layer fold quirks fixed during Phase 3

Two latent issues in `internal/app/imports.go` /
`imports_rewriter.go` surfaced under kitsoki-dev's
second-fold-of-dev-story-which-already-imports-bf-and-pr load:

1. **Bare-name transition targets at depth > 0.** The
   `rewriteChildStateTransitions` bare-name catch wrote `../X` for
   every bare-name sibling target regardless of how deeply nested
   the state was within the alias wrapper. After fold,
   `bf.done_awaiting_reply.accept` had target `pr` (bare, from the
   dev-story @exit:done mapping). On the second fold under
   `core`, that bare `pr` was rewritten to `../pr` — but the state
   `bf.done_awaiting_reply` sits one compound deep, so the runtime
   `resolveTarget("core.bf.done_awaiting_reply", "../pr")` =
   `"core.bf.pr"` (wrong; should be `"core.pr"`).
   **Fix:** use `strings.Repeat("../", depth+1) + t` so
   N-deep states need N+1 `..` segments to walk past the alias
   wrapper to the sibling level. `dev-story`, `bugfix`,
   `pr-refinement`, `oregon-trail` all still pass post-fix.

2. **`emit_intent:` and `emit_slots:` weren't world-prefix
   rewritten.** `rewriteEffect` rewrote `When`, `Say`, `Set`,
   `Increment`, `With`, `Bind`, and nested `OnComplete`, but not
   `EmitIntent` / `EmitSlots`. After fold, `emit_intent: "{{
   world.llm_verdict.intent }}"` still referenced the unprefixed
   key, so at runtime the rendered intent was empty (or stale) and
   the autonomous mode silently no-op'd. **Fix:** add
   `eff.EmitIntent = rw.rewriteExpr(eff.EmitIntent)` + a
   `rewriteAny` loop over `eff.EmitSlots`.
   Wave 2's flow fixtures didn't exercise autonomous-mode through
   the dev-story import (the dev-story `bugfix_to_pr` flow uses
   `judge_mode: human`), so the gap only surfaced when Phase 3
   wrote `flows/pickup_autonomous_then_bail.yaml`.

Both fixes are pure rewriter-side and don't change the runtime
contract. The `internal/machine/dispatchEmittedIntents` path is
unchanged — it always dispatched whatever name the template
rendered to; the rewrite just ensures the template sees the right
world key on its inputs.

### W2.11.4 — Flow fixtures can't register REAL host handlers

`testrunner/flows.go`'s `HostHandlers` map registers only STUB
handlers via a closure over `HostStub.Data`. There's no path to
register a REAL handler (e.g. the real
`host.local_files.ticket` against a temp git repo) from a flow
fixture, so the proposal §8 Phase 3 acceptance — "assert the bug
file's `status:` is `resolved` after the run; assert at least 3
`## Comment` blocks were appended" — isn't expressible as a flow
assertion today. Phase 3 ships:

- Four supervised + autonomous flow fixtures that pin the
  state-machine walk and projected-world shape end-to-end with
  stubbed handlers — proves the import composition + judge
  polymorphism + emit_intent path through the multi-layer fold.
- A manual-walkthrough doc in `.kitsoki/stories/kitsoki-dev/README.md`
  ("Manual walkthrough — the on-disk smoke") that exercises the
  real handlers against the on-disk seed bugs. The on-disk
  byte-exact assertion is run by hand; CI would need an explicit
  "real-handler harness" mode for the testrunner that fixtures
  could opt into.

The minimal future enhancement: a new `host_handlers.<name>.real:
true` discriminator in `FlowFixture.HostHandlers`, which short-
circuits the stub closure and registers the real built-in
handler. Five-line change in `flows.go`; deferred to a follow-up
because the supervised + autonomous flows already pin the
state-machine correctness, and the on-disk side has a manual smoke
covering it.

### W2.11.5 — Phase 3 test surface

```
$ kitsoki test flows .kitsoki/stories/kitsoki-dev/app.yaml
PASS      self_host_smoke.yaml           (4 turns)
PASS      pickup_self_bug_supervised.yaml (18 turns)
PASS      pickup_story_bug_supervised.yaml (18 turns)
PASS      pickup_autonomous_then_bail.yaml (12 turns)
Summary: 4/4 flows pass
```

All four exercise the multi-layer fold under `core`. The two
supervised fixtures walk the full bf → pr chain (18 turns each)
and assert on projected `core__status` / `core__last_pr_url` at
`@exit:merged`. The autonomous-then-bail fixture pins both the
auto-fire and the mid-flow `world_override.judge_mode = "human"`
swap — proving the autonomous-to-supervised handoff works hot.

### W2.11.6 — What blocks the FULLY-real PoC today

The only thing standing between Phase 3 as-shipped and a
fully-end-to-end live run is the **bug-filing CLI from
`bug-format-proposal.md` Phase A** that lives on a parallel
worktree not yet merged into this branch. Once it lands, `/meta
kitsoki bug` and `/meta story bug` will produce properly-formed
bug files automatically, and the closed loop from proposal §5.4
runs without any hand-editing. Everything else — provider
handlers, story imports, flow-test coverage, the self-hosted manifest,
the seed bugs — is in place on this branch.

---

# Wave 3 / Phase 5 contract additions (cypilot story + GitHub / cypilot_artifacts providers)

Wave 1 shipped the six base host_interfaces and `stories/bugfix/`;
Wave 2 added `stories/pr-refinement/` and the `stories/dev-story/`
hub. Wave 3 / Phase 5 (this section) introduces:

1. The seventh host_interface — `artifact` — with the five canonical
   ops scoped in proposal §2.6.
2. Two new provider handlers — `host.gh.ticket` (GitHub Issues via
   the `gh` CLI, backs the `ticket` iface) and `host.cypilot_artifacts`
   (cypilot SDLC artifact store via the `cpt` CLI, backs the
   `artifact` iface).
3. The `stories/cypilot/` story (interim home in kitsoki; migrates to
   the cypilot upstream repo at Phase 8 per proposal §5.5).

The additions below are **strictly additive** — every Wave 1 / Wave 2
contract clause still holds.

## W3.1 — `artifact` iface canonical operation schemas

```yaml
host_interfaces:
  artifact:
    description: "Cypilot SDLC artifact store (PRD / ADR / DESIGN / DECOMPOSITION / FEATURE / CODE)."
    operations:
      list:
        input:  { kind: string }                     # "prd" | "adr" | "design" | "feature" | …
        output: { artifacts: list }                  # [{id, kind, title, path, status, …}]
      get:
        input:  { id: string }
        output: { id: string, kind: string, title: string, body: string,
                  frontmatter: object, path: string, depends_on: list }
      create:
        input:  { kind: string, title: string, slug: string, parent_id: string }
        output: { ok: bool, id: string, path: string, artifact: object }
      validate:                                      # the cypilot-analyze workflow
        input:  { id: string, mode: string }         # "deterministic" | "semantic" | "consistency"
        output: { ok: bool, findings: list, report: string }
      decompose:                                     # the cypilot-plan workflow
        input:  { id: string }                       # a PRD or DECOMPOSITION id
        output: { ok: bool, plan_path: string, phase_count: int, artifact: object }
    default: host.cypilot_artifacts
```

These map onto cypilot's three workflows (proposal §6.4):
`cypilot-analyze` → `validate`, `cypilot-plan` → `decompose`,
`cypilot-generate` → `create`. The provider implementation
(§W3.3 below) shells to `cpt` for v1; the proposal flags that today's
`cpt` CLI may need a `--json` flag and minor verb adjustments before
the idealised shapes above land exactly. The provider tolerates both
JSON envelope and plain-text fallback shapes for `list` and
`decompose`.

### W3.1.1 — The `artifact` field convention

Per W3.3 the `create` and `decompose` ops return an `artifact:`
nested object alongside the flat scalar fields. A room can bind the
whole envelope into one world slot:

```yaml
bind:
  prd_artifact: artifact            # binds the whole {id, path, kind, ...}
```

while still binding individual scalars via the dot-path syntax in
`hc.Bind`'s value (e.g. `bind: { feature_count: phase_count }` to
pull a scalar out of the same Result.Data).

## W3.2 — New handler names (Go side)

Two new prefix-fallback handler registrations land in
`internal/host/handlers.go::RegisterBuiltins`:

| Handler name | Iface op(s) it backs | File |
|---|---|---|
| `host.gh.ticket` (prefix-fallback handler) | all `ticket.*` ops via the `gh` CLI | `internal/host/github.go` |
| `host.cypilot_artifacts` (prefix-fallback handler) | all `artifact.*` ops via the `cpt` CLI | `internal/host/cypilot_artifacts.go` |

Per the registry's prefix-fallback (host.go::Get), a single
registration of `host.gh.ticket` resolves `host.gh.ticket.search`,
`host.gh.ticket.get`, etc. — same as `host.local_files.ticket` in
Wave 1.

GitHub's PR-side ops (open_pr / pr_status / pr_comment) are already
served by `host.git`'s existing `gh pr ...` shell-out (Wave 1 /
`internal/host/git_vcs.go`). A story binding GitHub picks
`host.gh.ticket` for `ticket` and keeps `host.git` for `vcs` — the
two cooperate without duplication. This is **explicit**: a parent
story's `host_bindings:` block for GitHub looks like

```yaml
host_bindings:
  ticket: host.gh.ticket
  vcs:    host.git
  # …
```

and the runtime dispatches accordingly.

## W3.3 — Provider availability + error model

Both providers shell out to a CLI that may not be installed on the
operator's machine. The contract for `cpt` / `gh` absence is:

- A `--version` probe at the top of every op (`cptCLIAvailable`,
  `ghCLIAvailable` — both go through the shared `cliExec` seam from
  `cli_exec.go`).
- If the probe fails, every op returns a clean `Result.Error` with
  an installation hint:
  - `host.cypilot_artifacts: cpt CLI not available — install cypilot from https://github.com/Acronis/cypilot or run from a checkout that has it on PATH`
  - `host.gh.ticket: gh CLI not available — install github.com/cli/cli and run gh auth login`
- The room's `on_error:` arc fires; the operator picks up at the
  fallback state. No panics, no infra-error escalation.

For `validate` specifically, a non-zero exit from `cpt analyze` is
the canonical "findings present" signal in cypilot's existing
workflows. The handler surfaces `ok: false` with `findings` +
`report` populated and DOES NOT set `Result.Error` — the
LLM-judge prompt reads the findings to decide refine vs. accept.
Authors that want the on_error arc on validate-fail can wrap the
call with an explicit `when:` against `world.validate_ok`.

## W3.4 — cypilot story world keys

New keys declared in `stories/cypilot/app.yaml`'s `world:` block:

```yaml
world:
  # Already-declared keys (ticket_id, ticket_title, workdir, judge_mode,
  # judge_confidence_threshold, cycle, refine_feedback, last_reply_author,
  # llm_verdict, pr_id, pr_url, pr_title, pr_body, status, thread) carry
  # straight through from bugfix / pr-refinement — same vocabulary.

  # ── Artifact identity ─────────────────────────────────────────────
  feature_slug:     { type: string, default: "" }   # hyphen-case slug for cpt
  feature_index:    { type: int,    default: 0 }    # current decomposed phase
  feature_count:    { type: int,    default: 0 }    # total phases produced by decompose

  # ── Per-room artifacts ────────────────────────────────────────────
  prd_artifact:           { type: object, default: {} }
  adr_artifact:           { type: object, default: {} }
  design_artifact:        { type: object, default: {} }
  decomposition_artifact: { type: object, default: {} }
  feature_artifact:       { type: object, default: {} }
  code_artifact:          { type: object, default: {} }

  # ── Validate report mirror ────────────────────────────────────────
  validate_report:        { type: string, default: "" }
  validate_ok:            { type: bool,   default: false }
```

`judge_mode` defaults to `llm_then_human` in the cypilot story
(not `human` like bugfix) because the cypilot pipeline is
autonomous-by-default per proposal §6.4 — humans intervene at
checkpoints when the LLM-judge bails.

## W3.5 — Exit surface for cypilot

```yaml
exits:
  code_ready:                                     # the SUCCESS exit
    requires: [code_artifact]                     # parent projects pr_title/pr_body
  abandoned:                                      # user/LLM quit
  validation_failed:                              # cycle-budget exhausted (Wave 4+)
```

A parent story that imports cypilot typically routes:

```yaml
imports:
  cyp:
    source: ../cypilot
    entry: idle
    world_in:
      ticket_id:    "{{ world.ticket_id }}"
      ticket_title: "{{ world.ticket_title }}"
      feature_slug: "{{ world.feature_slug }}"      # required at entry
      thread:       "{{ world.thread }}"
      workdir:      "{{ world.workdir }}"
      judge_mode:   "{{ world.judge_mode }}"
    intents:
      import: [begin, next_feature]                 # cyp-unique bare names
    exits:
      code_ready:
        to: pr                                      # handoff to pr-refinement
        set:
          pr_title: "{{ world.cyp__code_artifact.pr_title }}"
          pr_body:  "{{ world.cyp__code_artifact.pr_body }}"
      abandoned:        { to: main, set: { status: "abandoned" } }
      validation_failed: { to: main, set: { status: "validation_failed" } }
```

Overlapping names (`accept`, `refine`, `proceed`, `quit`, `look`)
stay prefixed (`cyp__accept` etc.) per Wave 2's W2.6 collision rule.

## W3.6 — Flow fixture stubs for the `artifact` iface

Flow fixtures that exercise the cypilot story stub
`host.cypilot_artifacts` with one shared `data:` dict that includes
both the flat scalar fields (id, path, plan_path, phase_count, ok,
report, findings) AND a nested `artifact:` object the rooms bind
into `world.<kind>_artifact`. Example pattern:

```yaml
host_handlers:
  host.cypilot_artifacts:
    data:
      ok: true
      id: "stub-id"
      path: "cypilot/artifacts/stub.md"
      report: "PASS"
      findings: []
      plan_path: ".plans/stub"
      phase_count: 3
      artifact:                                     # what rooms bind
        id:               "stub-id"
        path:             "cypilot/artifacts/stub.md"
        kind:             "prd"
        summary_title:    "Stub artifact"
        summary_markdown: "Stub body."
        plan_path:        ".plans/stub"
        phase_count:      3
        pr_title:         "PR title"               # for the code room
        pr_body:          "PR body"
```

Single-stub-per-handler is the prefix-fallback's design payoff:
every `iface.artifact.<op>` dispatches to the same closure and
returns the same Data, so flow authors don't have to special-case
each op.

## W3.7 — `cpt` CLI mismatch with the proposal's idealised shapes

The provider issues commands in the proposal §6.4 idealised form:
`cpt artifact list --kind <k>`, `cpt generate --kind --title --slug
--parent`, `cpt analyze --target --mode`, `cpt plan --task`.

Today's actual `cpt` CLI (per a project-specific cypilot checkout)
uses different surface shapes:

- `--json` is a TOP-LEVEL flag (`cpt --json validate ...`) rather
  than per-subcommand.
- Real subcommand verbs include `validate`, `validate-toc`,
  `list-ids`, `chunk-input`, `info`, `update` — not exactly the
  proposal's list/generate/analyze/plan vocabulary.

The provider passes `--json` defensively immediately after the
subcommand name (e.g. `cpt artifact list --json --kind <k>`); if a
specific cpt version doesn't recognise it the call exits non-zero
with stderr propagated, which the room's on_error arc handles
cleanly. Bringing the real cpt CLI into line with the proposal's
idealised vocabulary is a parallel piece of work owned by the
cypilot upstream; this provider keeps the kitsoki side stable.

## W3.8 — What Wave 3 / Phase 5 does NOT include

Explicit non-goals for Phase 5 (deferred to later phases):

- **Interactive prose editing.** Per proposal §6.4 final paragraph
  the cypilot story is autonomous-only for v1; v2 may add an
  interactive editor.
- **Parallel ADR + DESIGN.** Proposal §3 sketches them as parallel
  rooms; v1 serialises for simplicity.
- **Per-feature code rooms.** Proposal §3 has one code room per
  feature phase; v1 has one final code room.
- **Cycle budgets / `validation_failed` exit consumer.** Wave 4
  ports the L2 cycle-budget pattern from a project-specific bugfix story; until
  then the exit is declared but no in-flow path produces it.
- **Real `gh` authentication.** `host.gh.ticket` shells to a
  pre-authenticated `gh` (`gh auth login` already run). Wrapping
  the auth flow inside a state machine is out of scope.
- **`stories/cypilot/` parent integration.** `stories/dev-story/`
  does NOT yet import cypilot — that's a later wave. For Phase 5
  the cypilot story is exercised standalone via flow fixtures.
- **MCP wrapping of the providers.** Per proposal §11 / ideas.md
  "providers behind mcp" the providers stay as native kitsoki
  host handlers for v1.

## W3.9 — Wave 3 / Phase 5 test surface (acceptance)

| Story / package | Flow / test count | Pass? |
|---|---|---|
| `stories/cypilot/` | 4 flows (happy_prd_only, prd_to_feature, analyze_fails_bails, handoff_to_pr) | yes |
| `stories/bugfix/` | 10 flows (Wave 1 preserved) | yes |
| `stories/pr-refinement/` | 4 flows (Wave 2 preserved) | yes |
| `stories/dev-story/` | 4 flows (Wave 2 preserved) | yes |
| `stories/oregon-trail/` | 28 flows (no regression) | yes |
| `.kitsoki/stories/kitsoki-dev/` (Phase 3) | 4 flows (Phase 3 preserved) | yes |
| `go test ./internal/host/...` (GitHub + cypilot_artifacts) | all happy + error paths | yes |

The cypilot host tests (`github_test.go`, `cypilot_artifacts_test.go`)
mock the `gh` / `cpt` CLIs via the shared `cliExec` seam from
`cli_exec.go` — no real binaries are required. The flow fixtures stub
`host.cypilot_artifacts` per W3.6 above.

# Wave 3 / Phase 4 — cycle budgets + full checkpoint-intent vocabulary

Wave 1 shipped `stories/bugfix/` with `refine` / `accept` /
`restart_from` as intent stubs and a single global `world.cycle`.
Wave 3 / Phase 4 (this section) ports the L2 cycle-budget
pattern and the full Phase 4 vocabulary from proposal §4.2 onto the
provider-neutral story. Strictly additive - every Wave 1-3 contract
clause still holds.

## W4.1 — World keys

The bugfix story's `world:` block adds the following keys. Every
non-checkpoint room (idle, implementing, reviewing) and every
checkpoint room (reproducing, proposing, testing, validating, done)
participates.

```yaml
# Per-phase refine counters (one per checkpointed phase + per non-
# checkpoint phase for symmetry). Reset to 0 when restart_from rewinds
# into that phase; incremented on every successful refine.
reproducing_cycle:    { type: int, default: 0 }
proposing_cycle:      { type: int, default: 0 }
implementing_cycle:   { type: int, default: 0 }
testing_cycle:        { type: int, default: 0 }
reviewing_cycle:      { type: int, default: 0 }
validating_cycle:     { type: int, default: 0 }
done_cycle:           { type: int, default: 0 }

# Per-phase refine budgets. Operators override per-session via the
# import's world_in: block or the flow fixture's initial_world.
reproducing_budget:   { type: int, default: 3 }
proposing_budget:     { type: int, default: 3 }
implementing_budget:  { type: int, default: 3 }
testing_budget:       { type: int, default: 3 }
reviewing_budget:     { type: int, default: 3 }
validating_budget:    { type: int, default: 3 }
done_budget:          { type: int, default: 3 }
cycle_budget:         { type: int, default: 3 }  # documented global default; no arc reads it

# Audit + structured-abandon keys.
unsafe_jumps_made:    { type: int,    default: 0 }
abandon_reason:       { type: string, default: "" }
```

Wave 1's `world.cycle` (the global coarse counter) is preserved —
every refine arc increments BOTH `<phase>_cycle` and `cycle`.

## W4.2 — Refine budget gate

Every `<phase>_awaiting_reply.on.refine` block has a two-arm shape.
The first arm is the budget gate (a `when:` guard); the default arm
is the re-execute path.

```yaml
refine:
  - when: "world.<phase>_cycle >= world.<phase>_budget"
    target: "@exit:abandoned"
    effects:
      - set:
          abandon_reason: "<phase>_cycle_budget_exhausted"
          status:         "abandoned"
  - target: <phase>_executing
    effects:
      - set:
          refine_feedback:  "{{ slots.feedback ?? world.llm_verdict.reason }}"
          <phase>_cycle:    "{{ world.<phase>_cycle + 1 }}"
          cycle:            "{{ world.cycle + 1 }}"
```

The gate fires for both human and LLM-driven refines — a runaway
LLM-judge in `llm_then_human` mode is bounded by the same world
counter as an operator hammering the refine button.

## W4.3 — Restart-from semantics

Every checkpoint and non-checkpoint room's `on.restart_from` is a
`when:`-ladder across the well-known prior stages. Each successful
arm:

1. Targets the named `<stage>_executing` room.
2. Sets `restart_from_stage` to the slot value (audit).
3. Clears `refine_feedback`.
4. Resets the target phase's `<phase>_cycle` to 0 (fresh budget).
5. Resets the global `cycle` to 0.

Stages recognised everywhere: `reproducing`, `proposing` (alias
`propose_fix`), `implementing` (alias `implement_review`), `testing`
(alias `test`), `validating` (alias `validate`). A room only lists
the stages strictly *earlier* than itself; a stage already passed is
the legal restart target. The default arm catches unrecognised
stages and gracefully degrades to the previous-room `_executing` —
no abandon, the operator simply sees they're one room back.

## W4.4 — Jump-to semantics

Every checkpoint's `on.jump_to` is a `when:`-ladder across well-known
forward stages plus a default abandon arm:

```yaml
jump_to:
  - when: "slots.stage == 'testing' || slots.stage == 'test'"
    target: testing_executing
    effects:
      - set:
          jump_to:           "{{ slots.stage }}"
          unsafe_jumps_made: "{{ world.unsafe_jumps_made + 1 }}"
  - when: "slots.stage == 'validating' || slots.stage == 'validate'"
    target: validating_executing
    effects: [...]
  - when: "slots.stage == 'done' || slots.stage == 'pr'"
    target: done_executing
    effects: [...]
  # Unknown stage → controlled abandon
  - target: "@exit:abandoned"
    effects:
      - set:
          abandon_reason: "jump_to_unknown_stage"
          status:         "abandoned"
```

`jump_to` is unsafe by design: artifacts for the skipped rooms are
not produced. Parent stories that need the artifact chain (e.g.
pr-refinement consuming `done_artifact`) MUST NOT use `jump_to` to
skip the artifact-producing phase.

## W4.5 — Mode shortcuts (proposal §4.2)

Three intents are exposed in `idle` and `reproducing_awaiting_reply`:

| Intent | Effect |
|---|---|
| `quick_fix` | Set `bugfix_mode="quick"`. From idle, also lands at `reproducing_executing`; from the reproducing checkpoint, self-loops (just sets the flag). |
| `skip_to_pr` | Set `bugfix_mode="full"`, `restart_from_stage="validate"`, increment `unsafe_jumps_made`; jump to `validating_executing`. |
| `full_pipeline` | Set `bugfix_mode="full"`; lands at `reproducing_executing`. Useful when a previous run left `bugfix_mode="quick"` in the carried world. |

`bugfix_mode="quick"` is read at one location only:
`testing_awaiting_reply.on.accept`'s first arm:

```yaml
accept:
  - when: "world.bugfix_mode == 'quick'"
    target: done_executing
    effects: [...]
  - target: reviewing_executing  # full-mode default
    effects: [...]
```

So the quick path walks reproducing → proposing → implementing →
testing → done, skipping reviewing + validating (5 LLM calls instead
of 7).

## W4.6 — Intent surface in exports

`stories/bugfix/app.yaml` exports the full Phase 4 surface:

```yaml
exports:
  intents: [start, proceed, accept, refine, restart_from, jump_to,
            quick_fix, skip_to_pr, full_pipeline, quit, look]
```

Importing parent stories' `imports.bugfix.intents.import: [...]`
clauses pick the subset they want to surface bare in the parent's
intent table. Most parents (dev-story, kitsoki-dev) import the full
list; a project-specific wrapper can add tracker-specific intents on
top and import all of the above.

## W4.7 — Flow fixtures (acceptance)

The `stories/bugfix/flows/` directory ships 24 fixture files (42
fixture documents after `---`-delimited splitting) covering:

| Scenario | File |
|---|---|
| Refine budget exhaustion per phase | `refine_budget_exhaust_{reproducing,proposing,testing,validating,done}.yaml` |
| Refine at each stage (counter increment) | `refine_at_each_stage.yaml` |
| Restart-from each stage with cycle reset | `restart_from_each_stage.yaml`, `restart_from_resets_budget.yaml`, `restart_from_proposing.yaml` |
| Jump-to each forward stage + unknown fallback | `jump_to_each_target.yaml` |
| Quick-fix happy path | `happy_quick_fix.yaml` |
| Skip-to-PR from idle | `skip_to_pr_from_idle.yaml` |
| Full-pipeline override of carried quick mode | `full_pipeline_from_idle.yaml` |
| Mode switch mid-flow (full → quick) | `mode_switch_full_to_quick.yaml` |
| LLM verdict=refine fires the refine arc | `llm_then_human_refine_then_accept.yaml` |
| Runaway LLM bounded by budget | `budget_exhaust_llm_then_human.yaml` |

Pre-existing Wave 1/2 flows preserved: `happy_human`, `happy_llm`,
`happy_llm_then_human`, `llm_uncertain_holds`,
`refine_once_then_accept`, `mixed_judge_swap`, `quit_at_{idle,
proposing, validating}`.

Final flow count: **42/42 passing** (was 10/10 at Wave 2).

## W4.8 — What Phase 4 does NOT include

Explicit non-goals (deferred to later phases or out of scope):

- **Per-cycle-arc budget counters.** A prior L2 implementation has counters
  keyed on the arc that fired (e.g. `cycle__phase_9_7__on_validation_fail`
  vs `cycle__phase_9_7__on_validation_fail_short`). Phase 4 ships
  only a single per-phase counter — refine is the only arc that
  triggers it. If future arcs need separate budgets, extend with
  `<phase>_<arc>_cycle` keys following the same pattern.
- **Free-form `jump_to.phase` slot.** Only the well-known aliases
  are supported (matches the v1 contract). A
  `slots.stage`-templated target would require runtime templating in
  `target:` strings — out of scope.
- **Automatic restart-from on judge=`refine` with stage hint.**
  The runtime's `emit_intent` synthesises the intent name from
  `world.llm_verdict.intent`; the LLM cannot currently steer
  `slots.stage` for `restart_from` or `jump_to`. The LLM can emit
  `refine` (which doesn't take a stage), and that's the autonomous
  loop. A future runtime extension can pass the verdict's `stage`
  into the synthesised slots.

## W4.9 — Phase 4 test surface (acceptance)

| Story / package | Flow / test count | Pass? |
|---|---|---|
| `stories/bugfix/` | 42 flows (10 Wave 1 → 42 Wave 3 / Phase 4) | yes |
| `stories/pr-refinement/` | 4 flows (Wave 2 preserved) | yes |
| `stories/dev-story/` | 4 flows (Wave 2 preserved) | yes |
| `.kitsoki/stories/kitsoki-dev/` | 4 flows (Phase 3 preserved) | yes |
| `stories/cypilot/` | 4 flows (Phase 5 preserved) | yes |
| `stories/oregon-trail/` | 28 flows (no regression) | yes |
| `go test ./...` | all packages | yes |

## W4.10 — Runtime observations

No runtime patches were required for Phase 4. Two behaviours worth
noting for future authors:

1. **`world_override` in flow fixtures** is the canonical way to
   seed `<phase>_cycle` at the budget for an exhaustion test. The
   fixture runner applies `world_override` BEFORE guard evaluation
   (`internal/testrunner/flows.go:573`) so the refine arc's `when:`
   reads the seeded value on the same turn.

2. **`emit_intent`** continues to behave as the W2.8 / W3 quirks
   note documents. The Phase 4 budget-exhaust path under
   `judge_mode=llm_then_human` exercises it directly: the LLM
   verdict's `intent: refine` is dispatched in the same turn the
   judge invoke binds `world.llm_verdict`, and the refine arc's
   budget gate evaluates against the just-updated counter (which
   the `world_override` seeded). No machine changes were needed.

# Wave 3 / Phase 6 — `stories/implementation/` + `stories/code-review/`

Wave 1–4 shipped bugfix, pr-refinement, cypilot, and dev-story. Phase 6
(this section) fills out dev-story's hub with the two remaining
sub-stories from proposal §4.3:

- **`stories/implementation/`** — small-task pipeline; lighter than
  bugfix (no reproduction phase, no separate security review).
- **`stories/code-review/`** — review a teammate's PR; triggered from
  the dev-story inbox by an external "PR awaiting your review"
  notification.

Both reuse `stories/pr-refinement/` for the tail (implementation only —
code-review terminates at the review decision and does not open / merge
a PR of its own).

## W6.1 — `stories/implementation/` room set

Five visible rooms plus a `handoff` that enters the pr-refinement
import compound. Lighter than bugfix's seven rooms.

| Room | Substates | Checkpoint? | On `accept` |
|---|---|---|---|
| `idle` | one atomic | n/a | `review_task_executing` (via `start`) |
| `review_task` | `_executing`, `_awaiting_reply` | yes — `task_summary_artifact` | `write_code_executing` |
| `write_code` | `_executing`, `_awaiting_reply` | yes — `code_artifact` | `test_executing` |
| `test` | `_executing`, `_awaiting_reply` | yes — `test_artifact` | `review_executing` |
| `review` | `_executing`, `_awaiting_reply` | yes — `review_artifact` | `handoff` |
| `handoff` | one atomic | n/a | `pr` (the pr-refinement import compound) |

The `test` and `review` rooms' `refine` arcs both bounce back to
`write_code_executing` with feedback — the loop closes around the
code-write room rather than the local checkpoint. This is the
implementation-specific deviation from the bugfix pattern (where
each checkpoint's `refine` recurses into its own `_executing`).

### W6.1.1 — World keys

```yaml
world:
  # Identity / ticket
  ticket_id:        { type: string, default: "" }
  ticket_title:     { type: string, default: "" }
  ticket_body:      { type: string, default: "" }     # bound by iface.ticket.get
  thread:           { type: string, default: "" }

  # Workspace
  workspace_id:     { type: string, default: "" }
  workdir:          { type: string, default: "" }
  base_branch:      { type: string, default: "main" }
  feature_branch:   { type: string, default: "" }

  # Pipeline control
  judge_mode:                  { type: string, default: "human" }
  judge_confidence_threshold:  { type: float,  default: 0.8 }
  cycle:                       { type: int,    default: 0 }
  refine_feedback:             { type: string, default: "" }

  # Per-room artifacts
  task_summary_artifact:  { type: object, default: {} }
  code_artifact:          { type: object, default: {} }
  test_artifact:          { type: object, default: {} }
  review_artifact:        { type: object, default: {} }

  # CI scratch
  ci_log:           { type: string, default: "" }
  ci_state:         { type: string, default: "" }

  # PR handoff (populated by the pr-refinement import)
  pr_id:            { type: string, default: "" }
  pr_url:           { type: string, default: "" }
  pr_title:         { type: string, default: "" }
  pr_body:          { type: string, default: "" }
  merge_strategy:   { type: string, default: "squash" }
  last_pr_url:      { type: string, default: "" }

  # Carry-forward to @exit:done
  done_artifact:    { type: object, default: {} }
  status:           { type: string, default: "open" }
```

### W6.1.2 — Exits

| Name | Description | `requires:` keys |
|---|---|---|
| `done` | Pipeline succeeded; PR was opened + merged via pr-refinement. | `code_artifact` |
| `abandoned` | User or LLM bailed. | — |

The pr-refinement nested import projects `@exit:merged` →
implementation's `@exit:done` via:

```yaml
imports:
  pr:
    source: ../pr-refinement
    entry: open_pr
    world_in: { … }
    exits:
      merged:
        to: "@exit:done"
        set:
          status:        "merged"
          last_pr_url:   "{{ world.pr__pr_url }}"
          code_artifact: "{{ world.code_artifact }}"   # satisfies requires:
          done_artifact: "{{ world.code_artifact }}"
      abandoned:
        to: "@exit:abandoned"
        set: { status: "abandoned" }
```

The `set:` re-pins `code_artifact` on the @exit:done arc so the
static `requires: [code_artifact]` check passes (see
`internal/app/imports.go::checkExitRequiresRec`).

## W6.2 — `stories/code-review/` room set

Five visible rooms — no separate `_executing`/`_awaiting_reply`
inflation for the navigation states.

| Room | Substates | Checkpoint? | On `accept` / decision |
|---|---|---|---|
| `idle` | one atomic | n/a | `list_pending` (via `start`) |
| `list_pending` | one atomic | n/a | `review_pr_executing` (via `pick_pr` / `proceed`) |
| `review_pr` | `_executing`, `_awaiting_reply` | yes — `review_summary_artifact` | `comment_executing` |
| `comment` | `_executing` only | no | `decide_executing` (via `proceed`) |
| `decide` | `_executing`, `_awaiting_reply` | yes — `decision_artifact` | `@exit:reviewed` (via `approve` / `request_changes`) |

The `decide` room's `accept` arc is polymorphic: it reads
`world.decision_artifact.decision` and routes through the matching
`request_changes` / `approve` arm so the LLM-judge auto-fire path
works without forking the state graph.

### W6.2.1 — World keys

```yaml
world:
  # Identity / inbox-routed PR
  pr_id:           { type: string, default: "" }
  pr_url:          { type: string, default: "" }
  pr_title:        { type: string, default: "" }
  pr_author:       { type: string, default: "" }
  thread:          { type: string, default: "" }

  # Pending list
  pending_prs:     { type: string, default: "" }
  pending_count:   { type: int,    default: 0 }

  # Diff + comments
  pr_diff:         { type: string, default: "" }
  pr_comments:     { type: string, default: "" }

  # Per-room artifacts
  review_summary_artifact: { type: object, default: {} }
  decision_artifact:       { type: object, default: {} }
  draft_comment:           { type: string, default: "" }

  # Decision
  decision:        { type: string, default: "" }   # approve | request_changes | dismiss

  # Pipeline control + judge
  judge_mode:                  { type: string, default: "human" }
  judge_confidence_threshold:  { type: float,  default: 0.8 }
  cycle:                       { type: int,    default: 0 }
  llm_verdict:                 { type: object, default: {} }

  status:          { type: string, default: "open" }
```

### W6.2.2 — Exits

| Name | Description | `requires:` keys |
|---|---|---|
| `reviewed` | Final review posted (approve / request_changes). | `decision_artifact` |
| `dismissed` | User explicitly dismissed (e.g. wrong reviewer). | — |
| `abandoned` | User or LLM bailed. | — |

## W6.3 — Inbox-trigger contract (the `pick_review` arc)

The dev-story hub's `inbox.yaml` exposes a `pick_review` intent
whose slots project a "PR awaiting your review" notification's
payload into the `rev` import compound:

```yaml
# stories/dev-story/rooms/inbox.yaml
on:
  pick_review:
    - target: rev
      effects:
        - set:
            pr_id:     "{{ slots.pr_id }}"
            pr_title:  "{{ slots.pr_title ?? '' }}"
            pr_author: "{{ slots.pr_author ?? '' }}"
            thread:    "{{ slots.pr_id }}"

# stories/dev-story/app.yaml
imports:
  rev:
    source: ../code-review
    entry: idle
    world_in:
      pr_id:      "{{ world.pr_id }}"
      pr_title:   "{{ world.pr_title }}"
      pr_author:  "{{ world.pr_author }}"
      thread:     "{{ world.thread }}"
      judge_mode: "{{ world.judge_mode }}"
      judge_confidence_threshold: "{{ world.judge_confidence_threshold }}"
    intents:
      import: [pick_pr, approve, request_changes, dismiss]
    exits:
      reviewed:  { to: main, set: { status: "reviewed" } }
      dismissed: { to: main, set: { status: "dismissed" } }
      abandoned: { to: main, set: { status: "abandoned" } }
```

The inbox subsystem (`internal/inbox/`) is not modified by Phase 6;
the routing happens entirely at the YAML layer. In autonomous mode
a daemon could enumerate inbox items and call
`kitsoki session continue --intent pick_review --slots pr_id=PR-38`;
in interactive mode the operator types the same intent at the inbox
view.

## W6.4 — New intents at the dev-story hub

| Intent | Slots | Purpose |
|---|---|---|
| `go_implementation` | — | Dispatch into the `impl` import at `impl.idle`. |
| `go_code_review_story` | — | Dispatch into the `rev` import at `rev.idle` (the standalone code-review entry, distinct from the inbox-routed `pick_review` path). |
| `pick_review` | `pr_id`, `pr_title?`, `pr_author?` | Inbox-routed entry into `rev` with the notification payload. |

`go_code_review` (the legacy Wave 2 navigation intent into the stub
`code_review.yaml` room) is preserved; the stub room remains as a
documentation surface for Wave 2 compatibility. The new
`go_code_review_story` is the typed entry into the imported
story-level code-review.

## W6.5 — Sub-story alias intent collisions

The bf import already lifts the bare `start` intent into the parent;
impl and rev cannot lift it again. Both impl and rev's
`intents.import:` clause omits `start` accordingly — the operator
types `impl__start` / `rev__start` to boot each story.

## W6.6 — Runtime observations

1. **`emit_intent` auto-fires across import compound boundaries via
   IntentAliases.** Originally a runtime gap (see "Original bug
   description" below), now fixed at the dispatcher.

   Resolution mechanism. The imports rewriter at
   `internal/app/imports_rewriter.go::rewriteState` records every
   on-arc rename in a per-state `IntentAliases` map (declared on
   `app.State`). On a single fold the entry is e.g.
   `accept → bf__accept`; on a multi-layer fold the chain is updated
   in place (`accept → core__bf__accept`) and the intermediate
   spelling is also recorded (`bf__accept → core__bf__accept`) so a
   state folded N times answers to any of N+1 names.

   At dispatch time, `internal/machine/machine.go::
   resolveEmittedIntentName` walks the active leaf → root path
   consulting each state's IntentAliases map. The first hit wins;
   when no ancestor declares an alias for the emitted name, the bare
   name is returned (back-compat for standalone stories without
   imports). The resolved name is then passed to
   `findTransitionTraced` as before — the rest of the dispatcher's
   contract is unchanged.

   Regression coverage:
     - `internal/machine/emit_intent_test.go` —
       TestEmitIntent_ResolvesThroughSingleImportAlias,
       TestEmitIntent_ResolvesThroughNestedImportAliases,
       TestEmitIntent_StandaloneNoAliasMap,
       TestEmitIntent_NonexistentNameInsideAliasMapIsNoArm.
     - `stories/dev-story/flows/bf_llm_auto_advance.yaml` —
       single-layer fold (dev-story → bf).
     - `.kitsoki/stories/kitsoki-dev/flows/self_host_autonomous_smoke.yaml` —
       two-layer fold (kitsoki-dev → core → bf), bare `accept` in the
       verdict stub.
     - `stories/implementation/flows/happy_llm_then_human.yaml` was
       SIMPLIFIED: the pr-compound tail no longer needs explicit
       `pr__accept` / `pr__proceed` workarounds.

   Original bug description (kept for archaeology). The intent-name
   rewriter renames every child state's `on:` map keys to
   `<alias>__<intent>`, but the dispatcher in
   `internal/machine/machine.go::dispatchEmittedIntents` called
   `findTransition` with the BARE emit name. Inside an import
   compound the lookup silently no-op'd because the renamed key
   never matched. Documented as the canonical example of "the
   rewriter does its job; downstream needs to know about the
   rename." Fixed by threading the rewriter's bookkeeping into the
   dispatcher via `IntentAliases`.

2. **`requires:` on import-projected `@exit:` arcs** is satisfied by
   the projection's `set:` clause. `internal/app/imports.go::checkExitRequiresRec`
   walks every transition whose target is `@exit:<X>` and checks its
   effects set the requires-keys; the import rewriter expands
   `imports.<alias>.exits.<name>.set` into the rewritten arc's
   effects so the static check sees the keys on the arc. This is why
   implementation's `imports.pr.exits.merged.set` re-pins
   `code_artifact: "{{ world.code_artifact }}"` even though
   `code_artifact` was already set upstream by `write_code_executing`.

3. **The `decide` room's polymorphic `accept` arc** demonstrates a
   pattern for routing a single LLM-emitted intent into multiple
   end-state effects. The two `when:` guards on the `accept` arc
   examine `world.decision_artifact.decision` (populated by the
   LLM's structured response) and route to the matching binary
   outcome. This avoids defining one emit_intent value per outcome
   (which would force the LLM to know its target intent name) — the
   LLM just emits `accept` and the YAML decides what that means.

## W6.7 — Phase 6 test surface (acceptance)

| Suite | Pass count | Notes |
|---|---|---|
| `stories/implementation/flows/*.yaml` | 5 / 5 | happy_human, happy_llm_then_human, test_fails_refine, review_rejects_refine, quit_at_review |
| `stories/code-review/flows/*.yaml` | 4 / 4 | happy_human_approve, happy_human_request_changes, llm_judge_approves, inbox_trigger_smoke |
| `stories/dev-story/flows/*.yaml` | 6 / 6 | existing 4 + pickup_to_implementation + inbox_review_pickup |
| `stories/bugfix/flows/*.yaml` | 42 / 42 | Phase 4 expanded set; regression |
| `stories/pr-refinement/flows/*.yaml` | 4 / 4 | regression |
| `stories/cypilot/flows/*.yaml` | 4 / 4 | regression |
| `.kitsoki/stories/kitsoki-dev/flows/*.yaml` | 4 / 4 | regression |
| `stories/oregon-trail/flows/*.yaml` | 28 / 28 | regression (imports demo) |

Full `go test ./...` clean. No runtime patches required.
