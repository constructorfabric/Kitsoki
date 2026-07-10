---
name: work-decomposition
description: Turn an accepted kitsoki proposal (or an epic + its children) into a validated, reviewed YAML decomposition of right-sized agent briefs, by hand — the manual twin of the canonical `stories/deliver` story. Use when the user wants to decompose work into briefs, "break this proposal into tickets/tasks", produce a decomposition.yaml, or run the decomposition discipline outside the engine (no story session, or before wiring `deliver` into a new caller). Drives discovery → manifest → deterministic structural lint → adversarial feasibility/completeness review.
---

# Work decomposition

This skill is the **Claude-driven manual twin** of
[`stories/deliver`](../../../stories/deliver/README.md) — the canonical
decomposition story (see
[`docs/stories/deliver.md`](../../../docs/stories/deliver.md) for the
narrative overview).
It takes an *accepted* proposal (or an epic + its linked children) and produces
a `decomposition.yaml` of agent briefs — structurally validated and
adversarially reviewed — for by-hand runs, or to check out the manifest shape
before driving the same discipline through `stories/deliver`.

The same discipline `deliver` encodes applies here, just run by you instead of
the engine: the **interpretive** decisions (how to slice, is it feasible, is it
complete) are your judgment; the **structural** truth (unique ids, acyclic DAG,
coverage, path bounds) is a deterministic script whose exit code is the gate —
the kitsoki moat: separate interpretation from deterministic execution.

> Scope: this **decomposes** an accepted proposal. Authoring/scoping the
> proposal itself is the `proposal-authoring` skill. Implementing a single
> brief is the `stories/implementation/` pipeline (and `kitsoki-story-authoring`
> for story-layer work). This skill stops at a reviewed `decomposition.yaml`.

## The output shape — the heart

The deliverable is a manifest matching
[`schemas/decomposition.json`](schemas/decomposition.json) — a manual copy of
`stories/deliver`'s canonical schema, kept identical rather than left to drift.
The **required** contract (fleet's, unchanged) is `id, brief, gate_command` per
brief; a top-level `coverage_note` and per-brief `title, kind, scope[],
acceptance[], risk` are **optional** richer fields this skill still
recommends writing. `agent_brief`/`brief`, `test_plan`/`gate_command`, and
`depends_on`/`deps` are read as aliases of each other (schema
`additionalProperties: true` on brief items permits either spelling).

```yaml
coverage_note: >
  These four briefs fully cover the proposal: #1 adds the engine seam, #2 the
  story that uses it, #3 the TUI surface, #4 the docs+flow fixtures. Nothing in
  the proposal's "What changes" is left unowned, and no two briefs touch the
  same files.
briefs:
  - id: engine-once-flag
    title: Add `once:` guard to room on_enter effects
    kind: runtime
    scope: ["internal/machine/", "internal/app/imports.go"]
    deps: []
    acceptance:
      - "A reloaded room with once:true does not re-fire its on_enter oracle call"
      - "Existing rooms without once: behave unchanged"
    gate_command: "go test ./internal/machine/... -run TestOnceGuard"
    brief: >
      Implement an engine-level `once:` boolean on room on_enter effect lists...
      (self-contained — everything the implementer needs, no external context).
    risk: medium
```

`kind` ∈ `story | runtime | tui | tracing | test | docs`. `scope` globs are the
**write boundary** for the brief; new-file briefs legitimately point at paths
that don't exist yet (the validator bounds them rather than requiring a match).

## The loop

Run these phases in order. They mirror `stories/deliver`'s rooms; each gate
must pass before the next.

### 1. Load the source

Resolve the source the user named (a `docs/proposals/<slug>.md`, or an epic).
Read the full body. **Detect epic vs. focused**: an epic has `**Kind:** epic`
and a **Slices** table over linked children — if so, read *every* linked child
proposal too, and warn (non-fatal) on any child file that's missing or renamed.
Confirm back to the user: the title, kind, and (for an epic) the child count, so
they know you pointed at the right thing.

### 2. Discovery — sharpen the scope (interactive)

Before slicing, **have a short conversation** to pin the constraints a static
read can't give you:

- What must be **sequential** vs. what can be **parallel**?
- The **test strategy** (flow fixtures? go tests? manual checks?).
- **Risk areas** and explicit **non-goals** (often already in the proposal —
  confirm, don't re-derive).
- Any files/subsystems that are **off-limits** or owned elsewhere.

Capture the answers in `.context/decompose-<slug>-scope.md` (transient working
notes, per CLAUDE.md). Don't skip this even when the proposal looks complete —
the slicing quality lives here. Stop when you can state the slicing rationale in
two sentences.

### 3. Decompose — emit the brief manifest

Produce the manifest. Slicing rules:

- A brief is **right-sized** when it has one coherent goal, fits one
  implementer's head, and could land alone or behind one named dependency. Cut
  along **kind boundaries first** (runtime substrate → story → tui, tracing
  where its events are produced), then along shippable units within a kind.
- `deps` (aka `depends_on`) is the real build order — be honest; a missing
  edge is what the reviewer and the board will trip on.
- **No file double-ownership**: two briefs' `scope` globs should not overlap on
  the same file. Overlap is a slicing bug.
- `brief` (aka `agent_brief`) is **self-contained** — the implementer agent
  gets only that text plus the ticket fields, not this conversation. Spell out
  the approach, the key files, and the done-condition.
- The `coverage_note` is your **completeness claim** — write it to be attacked:
  state explicitly how the briefs together cover every item in the proposal's
  "What changes", with nothing unowned.

Write it to `.artifacts/decompose/<slug>/decomposition.yaml` (generated
artifact, not committed — CLAUDE.md).

### 4. Validate — the deterministic gate

Run the bundled validator. Its **exit code is the gate** — do not proceed to
review on a non-zero exit:

```
python3 .agents/skills/work-decomposition/scripts/validate_decomposition.py \
    .artifacts/decompose/<slug>/decomposition.yaml --repo-root .
```

It checks schema shape, **unique ids**, **dangling deps/depends_on**, an
**acyclic dependency DAG**, **scope paths bounded inside the repo** (parent dir
exists, when `scope` is present), **non-empty acceptance when present**, and a
**non-empty gate_command (or test_plan)** per brief. On failure: fix the
manifest (go back to step 3 with the errors as the brief) and re-run until
clean. This gate has teeth independent of any LLM — trust it over your own
read.

`stories/deliver/scripts/lint_decomposition.star` is the **canonical**
deterministic gate — it runs the same checks (minus JSON-schema shape) via
`host.starlark.run` inside the story, called by `deliver`'s `lint` room. This
skill's Python script is the manual-run companion for when you're not driving
`deliver` as a story.

### 5. Adversarial review — feasibility + completeness

Now switch hats and **attack your own manifest as a skeptic** (or, for a real
second opinion, spawn a sub-agent with the Agent tool to do it independently —
that's closer to `deliver`'s planned `review` room, a `host.agent.decide`
adversary gate):

- **Per brief:** is this actually buildable *as scoped*? Are its deps right and
  complete? Is anything impossible, hand-wavy, or secretly two briefs?
- **Across briefs:** do they *fully* cover the proposal — **attack the
  `coverage_note`**, name anything in "What changes" that no brief owns. Is
  there file overlap / double-ownership?
- **Default to `revise` when uncertain.** A confident "looks good" with no
  attempted refutation is not a review.

Emit a verdict: `{verdict: accept | revise, reason, questions[]}`. On `revise`,
fold `questions[]` back into step 3 and re-run validate + review. Budget the
loop (≈5 passes); if it won't converge, stop and tell the user *why* —
unconverged slicing is a signal the proposal isn't decomposable as written.

### 6. Hand back

On `accept`, present:

- the path to the validated `decomposition.yaml`,
- a one-line **board**: `status · id — title (deps)` per brief in dependency
  order, with the parallelizable briefs called out,
- the `coverage_note` and the review verdict + reason.

Note for the user that each brief is shaped to become a ticket the
`stories/implementation/` pipeline can build (`id/title/body = brief +
acceptance + scope + gate_command`) — the same shape `stories/fleet` fans
`ship-it` over when driven through `stories/deliver`.

## Quick reference

| Step | Mechanism | Gate |
|---|---|---|
| Load proposal/epic | read `docs/proposals/<slug>.md` (+ children) | right source confirmed |
| Discovery | short conversation → `.context/` scope note | slicing rationale in 2 sentences |
| Decompose | emit manifest → `.artifacts/decompose/<slug>/decomposition.yaml` | schema-shaped |
| **Validate** | `validate_decomposition.py` (CLI, manual) or `stories/deliver/scripts/lint_decomposition.star` via `host.starlark.run` (canonical, story room) | **exit code 0 / {route: "ok"}** |
| **Review** | skeptic pass (or sub-agent) | **verdict: accept** |
| Hand back | board + coverage note + verdict | — |

## Maintenance

Codex discovers this skill directly. Refresh the project-local Claude Code
symlink after adding or moving skills:

```
make setup
```

`schemas/decomposition.json` here is a manual copy of
[`stories/deliver/schemas/decomposition.json`](../../../stories/deliver/schemas/decomposition.json)
— the canonical decomposition story owns the contract now (see
[`docs/stories/deliver.md`](../../../docs/stories/deliver.md)). Keep the two files
identical rather than letting them drift; when `deliver`'s schema gains a
field, copy it here too. Likewise, when `deliver`'s `lint_decomposition.star`
gains a check, port it to `validate_decomposition.py`/`.star` here so a
by-hand run and a `deliver` run reject the same manifests.
