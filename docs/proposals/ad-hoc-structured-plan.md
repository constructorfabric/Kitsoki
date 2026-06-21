# Proposal: the ad-hoc structured plan — propose → accept/refine → apply → verify

**Status:** Draft v1. Nothing implemented.
**Kind:**   runtime + story (dev-story landing) + one host capability extension.
**Scope:**  the *simple* case only — a single-step **run-then-verify** plan. The
            iterate-until-green generalization stays in cherny-loop (see *Where
            this fits*).
**Relation:** a concrete, near-term rung *under* the
            [`ad-hoc-workbench`](ad-hoc-workbench.md) epic. That epic makes the
            workbench the floor and mines it into structure; this proposal fixes
            one sharp UX hole *inside* the floor — the free-text "here's my plan"
            / "ok go ahead" round-trip — by making the plan a **validated,
            executable artifact** instead of prose.

## Why

The free-form workbench (dev-story `landing`) already lands you in a
Claude-Code-like agent. But the common ad-hoc shape today is:

1. Operator: *"import the issues folder in the repo to github so we have issues to work with here."*
2. Agent: a **free-text plan** in `landing_note.summary` / `details` ("Would inspect `issues/`, map frontmatter to GitHub issue fields, prepare a dry run").
3. Operator: must type a **free-form affirmation** — "ok go ahead" — which the `default_intent: work` sink re-dispatches as another agent turn that *now* tries to do the work.

This is real and reproducible. From the newest `kitsoki-dev` trace
(`7ad0d866-…`, the issue-migration session Brad has tested repeatedly):

```
T2  core.ticket_search
  in     import the issues folder in the repo to github …   route=fallback (free_text)
  host   host.agent.task
  prompt task: # Landing — the free-form workbench …
  out    transitioned → core.landing
```

The turn ran, produced a prose plan, and parked. The flow fixture
`stories/kitsoki-dev/flows/ticket_search_freeform_work.yaml` bakes exactly this
shape — `landing_note.summary: "Planned the local issue to GitHub migration."`
The "plan" is **unstructured text** and "go ahead" is **unvalidated free text**.
Three problems:

- **The plan isn't executable.** "Go ahead" just runs the agent *again*, freshly;
  nothing guarantees the second turn does what the first turn described. (This is
  the documented "the turn ran but did the wrong thing" trap — `next_state`
  advancing proves nothing about whether the work happened. See
  `.claude/skills/kitsoki-debugging`.)
- **There's no acceptance/refine handle.** The operator can't *tweak* the plan
  ("yes but dry-run first", "skip closed issues") and get a deterministic
  re-application — they can only re-prose at the agent and hope.
- **There's no verification.** Nothing proves the work is *done*. The migration
  either created the GitHub issues or it didn't; the workbench has no gate.

**The best outcome:** the agent proposes a **YAML-structured, schema-validated
plan** that can be **executed deterministically**, **accepted as-is or refined
then applied**, and whose completion is proven by a **validation script** —
written in **Starlark**, not Bash, so it is sandboxed, traced, and replayable.

## What changes

**One sentence:** the workbench planner emits a validated `plan` artifact (a
goal, one executable step, and a Starlark verify gate) instead of prose; the
landing room gains `accept_plan` / `refine_plan` intents so the operator accepts
or refines it through normal intent routing; `apply` runs the step then runs the
verify script, and the room routes on a real pass/fail verdict.

Four coupled pieces, smallest-first:

1. **A `plan` close-out artifact** (`schemas/plan.json`). The landing agent's
   `submitted` artifact gains an optional `plan` object. When present, the
   workbench renders it as a reviewable card with **Accept / Refine / Apply**
   quick actions — the same way `landing_note.route` already renders an on-path
   bail button today. The free-text `summary`/`details` stay as the human-readable
   gloss; the `plan` is the machine-executable contract beside it.

2. **Refine through intent routing — no new mechanism.** `refine_plan` is the
   existing `default_intent: work` sink, re-aimed: a free-text utterance
   ("dry-run first", "skip closed issues") re-dispatches the planner with the
   **prior plan + the refinement** as context (the thread-continuity wiring the
   landing room already has via `landing_prior_summary` / `landing_prior_details`).
   The planner returns a revised `plan`. Accept/Apply are deterministic intents.

3. **Apply = run-then-verify, deterministically.** `apply` enters a small
   `applying` room that (a) runs the plan's single step under the existing
   write-mode grant, then (b) runs the plan's **verify gate** and binds a real
   `verify_ok`. Pass → done (summary + "captured" read-out); fail → back to the
   workbench with the gate's failure reason in `last_error`, ready to refine. This
   is **red-after-green** for ad-hoc work: prove it's done, don't assume.

4. **Verify in Starlark, with a read-only inspection capability.** The verify
   gate is `host.starlark.run` returning `{ok: bool, reason: string}`. Today's
   sandbox (`internal/host/starlark/`) exposes only `ctx.inputs`, `ctx.world`,
   `ctx.http` — **no filesystem, no subprocess** — so it cannot check "do the
   GitHub issues exist" or "is the file present". This proposal **adds a narrow,
   read-only inspection surface to the sandbox** (the "add host capabilities for
   Starlark if necessary" the brief calls for), keeping determinism + record/replay
   intact (see *The Starlark capability extension*).

A plan the agent cannot fully structure still ships **as a plan envelope** (goal
+ free-text steps + a verify stub the operator fills in or downgrades to an agent
gate) — so accept/refine/apply routing applies uniformly. The structured
single-step run-then-verify is the first concrete rung; richer multi-step plans
are an explicit non-goal here.

## Where this fits (existing state — reuse, don't reinvent)

| Existing piece | Role here | Status |
|---|---|---|
| dev-story `landing` room (`stories/dev-story/rooms/landing.yaml`) | the workbench floor; `default_intent: work`, thread continuity, the `landing_note.route` **bail-button precedent** for a structured close-out that drives the machine | landed |
| `landing-note.json` (`stories/dev-story/schemas/`) | the agent's close-out artifact; gains an optional `plan` object | landed |
| **cherny-loop** (`stories/cherny-loop/`) | the *iterate-until-green* generalization: a `gate_plan` (`schemas/gate_plan.json`) chosen by a `gate_planner`, `script\|agent\|hybrid` gates, red-before-green baseline, budget guards | landed |
| `host.starlark.run` + sandbox (`internal/host/starlark/`) | the deterministic, recordable, replayable verify engine — **extended here** with read-only inspection | landed (extended) |
| write-mode gate (`internal/host/write_mode_gate.go`) | the step's mutating tool calls already hold for an operator grant | landed |
| `host.agent.task` acceptance schema contract | forces the planner's output to validate against `plan.json` before it binds | landed |

**The clean split from cherny-loop.** cherny-loop is the heavyweight, *launched*
loop story: an operator picks it, configures a goal, and watches maker→checker
iterate to convergence. This proposal is the **inline, single-shot** case inside
the *already-running* workbench: one step, one verify, no budget loop. They share
the same spine — a structured plan + a deterministic gate — so the verify-gate
schema and the Starlark gate runner introduced here are **deliberately a subset
of cherny-loop's `gate_plan`**, and "this ad-hoc plan wants to iterate" is a
one-intent bail into cherny-loop (the same on-path-bail pattern the landing room
uses for pipelines). We do **not** rebuild cherny-loop in the workbench.

## The model

```
  Operator utterance ("migrate the repo issues to github")
        │  default_intent: work  (deterministic free-text sink — no LLM routing)
        ▼
  landing_agent (host.agent.task, read-only by default)
        │  submits landing-note { summary, details, plan? }
        ▼
  plan artifact (schema-validated)            ── prose summary shown beside it
    goal:     "migrate issues/ to GitHub issues"
    step:     { kind: run, intent: <agent task>, description, mutating: true }
    verify:   { mode: script, script: verify_issues.star, inputs: {...} }
        │
        ├── refine_plan  ─(free text → planner re-dispatch w/ prior plan)─┐
        │                                                                  │
        │   ◀──────────────── revised plan ───────────────────────────────┘
        │
        └── accept_plan / apply
                 ▼
            applying room
              1. run the step  (write-mode grant gates the mutation)
              2. run verify    (host.starlark.run → {ok, reason})
                 │
                 ├ ok    → done: summary + captured++; rest in workbench
                 └ fail  → workbench, last_error = reason; refine & re-apply
                          (or: "iterate this?" → bail to cherny-loop)
```

- **Deterministic:** plan validation against the schema, accept/apply routing,
  the verify-script run (sandboxed, no LLM, replayable), the pass/fail branch.
- **Interpretive (recorded):** the planner's draft, and the operator's
  accept/refine/apply verdict. Both land as trace events (below).
- **Inviolate:** apply never runs a mutation without the write-mode grant that
  already governs the workbench; verify never escapes the read-only sandbox.

## The plan artifact (simple case)

`stories/dev-story/schemas/plan.json` — a small, strict schema. Single step for
v1; `step` is an object, not an array, on purpose (multi-step is a non-goal).

```yaml
plan:
  goal:        "Migrate repo-local issues/ to GitHub issues."     # one line, the contract
  step:                                                            # exactly one for v1
    kind:        run                                              # run | agent  (v1: run)
    description: "Create a GitHub issue per file in issues/, mapping frontmatter."
    mutating:    true                                             # true ⇒ apply holds for write grant
    # the step is carried out by a dispatched agent task (the same landing_agent,
    # but prompted with the ACCEPTED plan as its instruction — not a fresh re-prose)
  verify:
    mode:        script                                           # script | agent | hybrid (subset of gate_plan)
    script:      verify/issues_migrated.star                      # Starlark; preferred
    inputs:      { expected_min: 3 }                              # typed, validated by the sidecar
    reason:      "Passes iff GitHub lists ≥ expected_min open issues matching the local set."
```

`mode: agent` (and `hybrid`) reuse cherny-loop's `gate_reviewer` shape verbatim
for goals no script can encode; **`script` is the default and the focus here**.
The schema is intentionally a strict subset of `gate_plan.json` so a plan can be
handed straight to cherny-loop when iteration is wanted.

## The Starlark capability extension

The verify gate wants to assert against the **working tree and a few read-only
probes** ("does this file exist", "does `gh issue list` show ≥ N"). Today's ctx
(`internal/host/starlark/ctx.go`) is `inputs` + `world` + `http` only — by design
no fs, no subprocess. We add a **narrow, read-only, recordable** surface and
nothing more:

```
ctx.fs.read(path)        -> string        # repo-relative, read-only, size-capped
ctx.fs.exists(path)      -> bool
ctx.fs.glob(pattern)     -> [path]        # deterministic, sorted
ctx.probe(name, args=[]) -> {exit, out}   # ONLY allow-listed read-only probes
```

Design rules that preserve the sandbox's contract:

- **Read-only, repo-scoped.** `ctx.fs` is rooted at the run's working dir; paths
  are cleaned and may not escape it. No write, no delete — mutation stays with the
  step's write-mode-gated agent.
- **Probes are an allow-list, not a shell.** `ctx.probe` dispatches a curated set
  of read-only commands declared per-story (e.g. `gh.issue.list`, `git.status`,
  `go.test` in `-run`-scoped/read form). It is **not** `ctx.run("anything")` —
  that would reintroduce the opaque, unsandboxed `host.run` we're moving *away*
  from. The allow-list is the safety boundary.
- **Record/replay holds.** Each `ctx.fs`/`ctx.probe` call records a summary
  `{op, path|name, status}` exactly as HTTP does today; full payloads live in
  cassettes. Flow tests inject a replay reader → no real fs/process touched → the
  no-LLM, no-cost test rule (`AGENTS.md`) is honored. Production injects a real
  read-only reader. This mirrors the existing `WithHTTP`/`HTTPFromContext`
  injection seam exactly — `WithInspector`/`InspectorFromContext`, defaulting to a
  refuse-all reader so a script that probes without an injected reader fails loud.

This is the one genuinely new engine capability the proposal asks for; everything
else is story wiring + reuse. It also pays forward: cherny-loop's `script` gate
can migrate from `host.run` (bash, opaque) to `host.starlark.run` (sandboxed,
traced) on the same surface — a strictly better checker.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| schema | `plan.json` | `{goal, step:{kind,description,mutating}, verify:{mode,script,inputs,reason}}` | strict subset of `gate_plan.json` |
| artifact field | `landing_note.plan` | the object above (optional) | beside `summary`/`details`; renders the plan card |
| intent | `accept_plan` | — | apply the plan as-is → `applying` |
| intent | `refine_plan` | re-uses `work` sink; free text + prior plan → planner | revised `plan` returned |
| intent | `apply` | — | alias of accept_plan when a plan is already accepted |
| story room | `applying` | run step (write-grant) → verify gate → route on `verify_ok` | the deterministic executor |
| world key | `accepted_plan` | object | the plan the operator accepted (frozen at accept) |
| world key | `verify_ok` | tri-state `"" \| true \| false` | bound by the gate; routing emits defer to post-bind (cherny pattern) |
| world key | `verify_reason` | string | gate failure detail → `last_error` on fail |
| ctx surface | `ctx.fs`, `ctx.probe` | read-only inspection | sandbox extension, recordable |
| event | `PlanProposed` | `{goal, mode, mutating}` | one per planner-emitted plan |
| event | `PlanDecided` | `{verdict, verify_ok, reason}` | accept/refine/apply + the gate result — the recorded decision |

## Verification (all no-LLM)

- **Plan render (flow):** boot landing → `work` with the migration utterance → a
  stubbed `host.agent.task` returns a `landing_note` *with* a `plan` → assert the
  plan card + Accept/Refine/Apply quick actions render. (Extends the existing
  `ticket_search_freeform_work.yaml` fixture — same shape, now plan-bearing.)
- **Refine (flow):** `refine_plan` free text → planner re-dispatched with prior
  plan in context (assert the dispatched **prompt** carries the prior plan, per
  the kitsoki-debugging "assert the dispatched prompt, not the verb result" rule)
  → revised plan binds.
- **Apply happy path (flow):** `accept_plan` → `applying` → stubbed step → verify
  gate (stub-by-id Starlark returning `{ok:true}`) → done state, `captured++`.
- **Apply red path (flow):** verify returns `{ok:false, reason:…}` → back to
  landing, `last_error == reason`, plan still present for refine. (Asserts we
  don't false-pass — the documented happy-path-only trap.)
- **Starlark extension (unit):** `ctx.fs.read/exists/glob` rooted + escape-proof;
  `ctx.probe` rejects non-allow-listed names; replay reader yields recorded
  summaries; refuse-all default fails loud. Cassette-back one real probe.
- **Mutation test:** revert the verify-gate binding → the red-path fixture must
  fail (proves the gate is load-bearing, not decorative).

The only genuinely-LLM step — "is the planner's plan a *good* plan" — is exercised
by hand in a dogfood run (run `kitsoki-dev`, drive the real issue→GitHub
migration end-to-end), never in CI.

## Tasks

```
## 1. Plan artifact + render
- [ ] 1.1 schemas/plan.json (strict subset of gate_plan.json); landing-note.json gains optional plan
- [ ] 1.2 landing room: render the plan card + Accept/Refine/Apply quick actions (mirror the route-bail block)
- [ ] 1.3 prompts/landing.md: teach the planner to emit a structured single-step plan (run + verify)

## 2. Refine + apply routing
- [ ] 2.1 refine_plan = re-aim the work sink: free text + prior plan → planner re-dispatch (thread continuity)
- [ ] 2.2 accept_plan/apply intents; freeze accepted_plan; enter applying
- [ ] 2.3 applying room: run step under write-grant → verify gate → bind verify_ok → route (cherny post-bind emit discipline)

## 3. Starlark inspection capability
- [ ] 3.1 ctx.fs.read/exists/glob (repo-rooted, read-only, size-capped, escape-proof)
- [ ] 3.2 ctx.probe allow-list dispatch (per-story declared read-only probes; NOT a shell)
- [ ] 3.3 WithInspector/InspectorFromContext seam + record/replay summaries + refuse-all default
- [ ] 3.4 unit + cassette tests

## 4. Verify gate + trace
- [ ] 4.1 verify modes: script (default) | agent (reuse gate_reviewer) | hybrid
- [ ] 4.2 PlanProposed / PlanDecided trace events
- [ ] 4.3 "iterate this?" on-path bail into cherny-loop (hand accepted_plan → cherny gate_plan)

## 5. Flows + docs
- [ ] 5.1 flow fixtures: plan render, refine, apply-green, apply-red, mutation test
- [ ] 5.2 docs/stories/ad-hoc-plan.md; note the cherny-loop relationship; trim/merge into ad-hoc-workbench.md
```

## Open questions

1. **`step` array vs object.** v1 is a single object (single-step is the stated
   scope). *Lean: object now,* widen to an array only when a real multi-step
   ad-hoc case demands it — and at that point reconsider whether it should just be
   cherny-loop.
2. **Who runs the step — the same `landing_agent` re-prompted with the accepted
   plan, or a dedicated `applier` agent?** *Lean: same landing_agent,* prompted
   with the *accepted plan as instruction* (not a fresh re-prose) — fewer personas,
   and the write-mode posture is already correct.
3. **`ctx.probe` allow-list scope — per-story or a global read-only set
   (`git status`, `ls`, `cat`-equivalent via `ctx.fs`)?** *Lean: a small global
   read-only base + per-story additions* (e.g. kitsoki-dev adds `gh.issue.list`),
   declared in the story like `hosts:`.
4. **Does `apply` ever auto-iterate on a red verify, or always return to the
   operator?** *Lean: always return* (this is the single-shot case; iteration is
   cherny-loop's job, one bail away). Auto-iteration here would be re-implementing
   the loop we deliberately scoped out.
5. **Relationship to the `ad-hoc-workbench` epic's mining loop.** A repeatedly
   accepted plan shape is exactly a mining signal ("capture this as an intent").
   *Lean: out of scope here* — emit `PlanDecided` so the miner can consume it
   later, but don't build the capture path in this proposal.

## Non-goals

- **Multi-step plans / DAGs.** Single step, single verify. If it needs steps, it
  needs cherny-loop or a pipeline.
- **An iterate-until-green loop in the workbench.** That *is* cherny-loop; we bail
  into it, we don't rebuild it.
- **A general `ctx.run` shell in Starlark.** The probe allow-list is the boundary;
  reintroducing arbitrary shell would undo the reason we prefer Starlark over Bash.
- **Mutating Starlark.** Verify is read-only; mutation stays with the
  write-mode-gated step agent.
- **The mining/capture-as-intent path.** Belongs to `ad-hoc-workbench.md`; this
  proposal only emits the signal.
```
