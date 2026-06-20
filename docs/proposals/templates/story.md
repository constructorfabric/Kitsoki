# Story: {Title}

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   — standalone   <!-- or ../{epic}.md -->

<!--
  A "story" proposal designs a new or reworked operator story under
  stories/<name>/. The novelty is almost always at the *story* layer
  (rooms, world, prompts, schemas, flows) — if you find yourself
  proposing new effects, host calls, or engine behavior, that part is a
  runtime.md proposal; split it out.

  Gold-standard stories to mimic: stories/bugfix/ (pipeline + checkpoint
  intents), stories/dev-story/ (live-result lists), stories/oregon-trail/
  (view layout), stories/robbery/ (smallest importable sub-story).
  Authoring reference: docs/stories/authoring.md, kitsoki-story-authoring skill.
-->

## Why

<!-- The operator problem, in their words. What workflow is missing or
     painful today? One short paragraph. Quote the user request if you have it. -->

## What changes

<!-- The story in one screen: new story vs. reworked rooms, the entry
     state, the exits, and the single sentence that captures the shape
     ("a pipeline-shaped story, structurally identical to bugfix"). -->

## Impact

<!-- What this touches. Story-layer only? Or does it need a host call /
     effect / widget that doesn't exist yet (→ that's a runtime slice)?
     List net-new files and any docs that will need updating on ship. -->

- **Net-new:** {N rooms, M prompts, K schemas, agents table}
- **Engine/host changes:** {none — composes existing mechanisms | see ../{runtime}.md}
- **Docs on ship:** `docs/stories/{name}.md`, this folder's `README.md` entry

## Reuse inventory

<!-- The most useful section in a story proposal: map each pipeline step
     to the existing mechanism it reuses, with a concrete reference. This
     is what proves the story is "just composition." -->

| Pipeline step | Mechanism | Reference |
|---|---|---|
| {Get free-form input} | `choice: mode: form` intake | `oregon-trail/rooms/general_store.yaml` |
| {Generate something} | `host.agent.task` + acceptance schema | `bugfix/rooms/*` task pattern |
| {Checkpoint / iterate} | `accept` / `refine` + cycle budget | `bugfix/rooms/proposing.yaml` |

## Story graph

```
idle  ── start ──▶  {room}  ── {intent} ──▶  {room}
 │  (intake)           │                        │
 └─ quit ─▶ @exit:abandoned                      └─ accept ─▶ @exit:done
```

<!-- ASCII graph: rooms, the intents on each arc, and the exits. Mark the
     checkpoint room. Keep it to what a reviewer needs to see the flow. -->

## World schema (sketch)

```yaml
world:
  {input}:        { type: string, default: "" }
  {artifact}:     { type: object, default: {} }   # task/decide result shape
  refine_feedback:{ type: string, default: "" }
  {phase}_cycle:  { type: int,    default: 0 }
  {phase}_budget: { type: int,    default: 5 }
  abandon_reason: { type: string, default: "" }
```

`exits:` — `done: { requires: [{artifact}] }`, `abandoned: {}`.

## Per-room detail

### `{room}` — {one-line purpose}

- **`on_enter`:** {host.agent.decide|task, which agent, prompt inputs,
  acceptance schema, `bind:` target}.
- **Intents:** {submit / accept / refine / regenerate / restart_from /
  quit} — params, transitions, cycle-budget gate → `@exit:abandoned`.
- **View:** {what `relevant_world` it reads; which typed elements render
  the artifact}.

<!-- Repeat per room. For checkpoint rooms, lift bugfix's intent set
     (accept / refine(feedback) / restart_from(stage) / quit) verbatim
     unless you have a reason not to. -->

### Net-new files

```
stories/{name}/
├── app.yaml
├── rooms/{…}.yaml
├── prompts/{…}.md
├── schemas/{…}.json
├── flows/{happy_path,refine_loop,budget_exhausted}.yaml
└── README.md
```

## Flow fixtures

<!-- Mode-2, intent-only, no-LLM, CI-fast. Name the fixtures and what each
     proves. These are the regression contract — see the dogfood-regression
     gap proposal for why intent-only fixtures matter. -->

- `happy_path` — {entry → … → @exit:done}.
- `refine_loop` — `refine` re-enters {room}, `{phase}_cycle` increments.
- `budget_exhausted` — `refine` at budget → `@exit:abandoned` with reason.

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml + room files with typed `extends: "base"` views + world schema
- [ ] 1.2 schemas/{…}.json; stub prompts

## 2. Lock the graph
- [ ] 2.1 Probe each room: `kitsoki turn … --state <room> --intent <x> --world @w.json`
- [ ] 2.2 Flow fixtures pass (happy_path, refine_loop, budget_exhausted)

## 3. Live + document
- [ ] 3.1 `kitsoki run` end-to-end with a throwaway input
- [ ] 3.2 README (entry state, exits, world contract, host requirements)
- [ ] 3.3 Migrate to docs/stories/{name}.md; trim/delete this proposal
```

## Open questions

<!-- Decisions you want reviewed, each with options and your lean.
     e.g. "PRD output: file vs. inline artifact. Lean: task → file." -->

1. {Question} — {options}. *Lean: {x}.*

## Non-goals

- {What this story explicitly does not do — defer to which proposal.}
