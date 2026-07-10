# Story: Roadmap and portfolio work taxonomy

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../human-action-workflows.md

## Why

Kitsoki already has pieces of rigorous planning vocabulary, but they live in
separate contexts:

- PRD/design scouts check overlap and `roadmap_fit` before drafting new work.
- Proposals model epics, focused slices, status, and lifecycle.
- Work decomposition turns accepted designs into brief DAGs with deterministic
  lint.
- The generalized-usage plan uses a traceability spine:
  `goal -> wall -> change -> gate -> integration commit -> trace`.
- Punch-list manifests prioritize short-term items and drive each through a
  story with verification.

The new human-action model should extend from that basis instead of stopping at
single tickets. A roadmap initiative is the same kind of thing as a bugfix at a
larger scale: it has an owner, rationale, options, dependencies, risks, gates,
evidence, and decisions that humans and agents need to propose, refine,
prioritize, and revisit.

## What changes

Define a portable work taxonomy and use it in planning stories:

| Level | Meaning | Existing basis |
|---|---|---|
| `goal` | durable outcome or product objective | generalized-usage `G1..G6` |
| `initiative` | strategic bet / roadmap theme spanning multiple epics | roadmap audit "done / in progress / next" clusters |
| `wall` | delivery phase or milestone grouping related changes | generalized-usage `W1..W5`, `WM`, `WH` |
| `epic` | coherent design umbrella with focused slices | proposal `Kind: epic` |
| `change` | independently gated implementation or process change | decomposition item / punch-list item |
| `task` | concrete agent, human, or script action | `host.agent.task`, proposed `host.human.task`, `host.run` |
| `decision` | recorded prioritization, approval, tradeoff, or scope call | `roadmap_fit`, proposal overlap recommendation, human decide |
| `gate` | deterministic acceptance check | flow/go test/lint/live+replay gate classes |

Add planning surfaces that can create and manage these items as first-class
artifacts. The first version can be story-level: a `roadmap` or `portfolio`
story that reads proposals, PRDs, issue queues, and decomposition manifests,
then emits a validated roadmap manifest.

## Impact

- **Story files:** likely a new `stories/roadmap/` or a focused dev-story room
  that can later be promoted
- **Runtime dependency:** benefits from `host.human.decide` for prioritization
  and approval calls, but can start with `host.agent.decide` + deterministic lint
- **Vocabulary:** `work_item.kind`, `horizon`, `priority`, `owner`,
  `depends_on`, `gate`, `status`, `evidence`, `roadmap_fit`
- **Docs on ship:** `docs/stories/roadmap.md`, proposal lifecycle docs, and
  dev-story README

## Manifest sketch

```yaml
schema: roadmap/v1
goals:
  - id: G1
    title: Install succeeds in a foreign repo
initiatives:
  - id: generalized-usage
    horizon: quarter
    priority: 1
    owner: product_owner
    goals: [G1, G2, G3, G4, G5, G6]
    status: in_progress
walls:
  - id: W1
    initiative: generalized-usage
    title: Install
    depends_on: [WM]
changes:
  - id: "0.1"
    wall: W1
    executor: agent
    gate: { class: G-SMOKE, command: make release-smoke }
decisions:
  - id: prioritize-generalized-usage
    executor: human
    role: product_owner
    kind: prioritization
    options: [now, next, later]
```

This keeps roadmap planning machine-checkable. Every item points up to a goal
and down to a gate or decision. A strategic decision can be assigned through
`host.human.decide`; an implementation change can be assigned to an agent; a
gate can run deterministically.

## Story flow

```
intake -> inventory -> classify -> prioritize -> validate -> publish -> review
```

- **intake:** collect a strategic theme, backlog, or proposal set.
- **inventory:** scan proposals, PRDs, tickets, traces, and `.context` plans.
- **classify:** map items into the taxonomy and detect overlap/supersession.
- **prioritize:** ask a human role for sequencing or tradeoff decisions when the
  evidence is not enough.
- **validate:** deterministic lint for IDs, DAG, goal coverage, gate presence,
  owner resolution, and status consistency.
- **publish:** write a roadmap manifest and summary artifact.
- **review:** use human actions for approval or requested changes.

## Tasks

```
## 1. Taxonomy
- [ ] 1.1 Define `roadmap/v1` manifest schema.
- [ ] 1.2 Add deterministic lint for DAG, goal coverage, owner resolution, and gate coverage.
- [ ] 1.3 Document how the taxonomy maps to existing PRD/proposal/decomposition vocabulary.

## 2. Story
- [ ] 2.1 Add a roadmap planning story or dev-story room.
- [ ] 2.2 Use existing overlap/roadmap-fit prompts as the inventory/scout basis.
- [ ] 2.3 Add human prioritization/review calls through `host.human.decide`.

## 3. Verification
- [ ] 3.1 No-LLM flow for an initiative with two epics, mixed agent/human changes, and one prioritization decision.
- [ ] 3.2 Lint fixtures for missing gate, dangling dependency, duplicate scope, and orphaned goal.
- [ ] 3.3 Migrate shipped docs and trim/delete this proposal.
```

## Verification

Default tests use static manifests and stubbed human decisions. No real GitHub,
LLM, or external project-management system is required.

## Open questions

1. **Story home:** new `stories/roadmap/` vs. dev-story room. *Lean: start in
   dev-story if it only consumes Kitsoki proposals; promote once it handles
   arbitrary projects.*
2. **Taxonomy breadth:** adopt every generalized-usage field now or the minimum
   set above. *Lean: minimum schema now, preserve extension fields so the full
   generalized-usage model can round-trip.*
3. **Persistent home:** `docs/roadmaps/`, `docs/goals/`, or project-local
   `.kitsoki/roadmap.yaml`. *Lean: project-local manifest plus narrative docs
   when a roadmap is published.*

## Non-goals

- A generic portfolio dashboard in v1.
- Replacing proposal or PRD authoring; roadmap planning references those
  artifacts and tracks their relationship.
