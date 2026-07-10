# Runtime: a YAML lifecycle taxonomy (features → proposals → plans → test specs)

**Status:** Draft v1. Initial design for review — nothing implemented yet.
Now slice 1 of the [`project-object-graph.md`](project-object-graph.md) epic;
amend toward the shared node envelope + GTS-style derivation per that epic's
Shared decisions 1–2.
**Kind:**   runtime
**Epic:**   [`project-object-graph.md`](project-object-graph.md)

## Why

The early lifecycle of a piece of work — *what is this feature, why are we
building it, how is the work cut, how do we know it works* — exists in kitsoki
today as four disconnected representations:

- **Features** are an unstructured prose list (`docs/features/mvp.md`). There
  is no machine-readable catalog, no composition (feature-of-features), and no
  place to attach the videos, screenshots, help, and tutorials we already
  produce (the tour manifests under `tools/runstatus/src/tour/` and the
  Playwright recording specs are TypeScript data with no back-link to any
  feature).
- **Proposals** are well-shaped markdown (`docs/proposals/templates/`), but
  the spine (`Why / What changes / Impact / Tasks`) is only a convention —
  nothing can traverse, validate, or aggregate it.
- **Implementation plans** exist only as the `decomposition.yaml` brief
  manifest ([`work-decomposition.md`](work-decomposition.md)) — schema-enforced
  and linted, but not linked back to a feature, and without per-file change
  descriptions.
- **Test specs** don't exist as objects at all. Acceptance lives ad hoc inside
  proposals and briefs; the evidence (flow fixtures, Playwright specs, demo
  videos) has no declared relationship to the feature it verifies.

So the chain *feature → proposal → plan → task → test spec* cannot be walked
by a tool, a story, or a lint. The dev-story `idea` flow and the decompose
story each invent their own intermediate shapes because there is no shared
domain model to emit into.

Prior research settled the substrate questions
([`artifact-format.md`](artifact-format.md)): schema-pinned validation via
`santhosh-tekuri/jsonschema/v6`, `yaml.Node` for lossless rewrite, markdown
carried in schema'd string fields (`contentMediaType: text/markdown`). That
proposal keeps the **markdown-with-frontmatter** container for artifacts that
are documents first. This proposal is the **data-primary direction taken to
its conclusion**: the lifecycle objects are *records* first, so the container
is pure YAML (`.yaml`), with markdown embedded — inline as block scalars, or
via `!include` for long-form prose — so every object is fully processable as
structured data.

Industry prior art and an evaluation of Constructor Studio (the renamed
cypilot upstream) are in
[`notes/lifecycle-taxonomy-prior-art.md`](notes/lifecycle-taxonomy-prior-art.md).
Short version: every load-bearing decision here has direct precedent
(per-object YAML records — Doorstop; pinned schemas — StrictDoc/Open-Needs;
hard coverage lint — Melexis/OpenFastTrace; durable/transient split —
OpenSpec's shipped `specs/` vs `changes/` model), the media/evidence
descriptors are genuinely novel, and studio is a concept donor but not an
adoptable substrate. Open questions 7–9 below come from that research.

## What changes

A small family of versioned domain objects, each a YAML document validated
against a pinned JSON Schema:

| Object | One line | Durability |
|---|---|---|
| **Feature** | A capability, composable from sub-features; carries media, help, tutorials, and acceptance criteria at every level | durable catalog |
| **Proposal** | The design argument for a change; the existing spine, as data | transient queue (trim/delete on ship, unchanged) |
| **Plan** | The implementation decomposition: overall brief + tasks with expected files and per-file change descriptions | transient (dies with its proposal) |
| **TestSpec** | Scenarios derived from a feature's acceptance criteria, pointing at the harness + fixture that verify them and the evidence they produce | durable catalog |

Plus the shared conventions they ride on: a `schema:` pin per file, kebab-case
ids with plain-string cross-references, a generalized `!include` (extracted
from the cassette loader), and a deterministic two-layer validation pipeline
(per-file JSON Schema + catalog-level lint: unique ids, acyclic DAGs, no
dangling refs, acceptance-coverage).

```
            (durable catalog)                    (transient queue)
┌──────────────────────────────┐
│ Feature                      │  motivates   ┌────────────────────┐
│  ├─ composed_of: [Feature…]  │─────────────▶│ Proposal           │
│  ├─ media / help / tutorials │              │  (spine as data)   │
│  └─ acceptance: [criteria]   │              └─────────┬──────────┘
└──────────────┬───────────────┘                        │ decomposes into
               │ criteria are                           ▼
               │ verified by                  ┌────────────────────┐
               ▼                              │ Plan               │
┌──────────────────────────────┐   verifies  │  brief             │
│ TestSpec                     │◀────────────│  tasks:            │
│  scenarios ──▶ harness,      │  (tasks cite│   ├─ expected_files│
│  fixture, evidence (media)   │   specs)    │   └─ depends_on    │
└──────────────────────────────┘             └────────────────────┘
```

The durable/transient split mirrors the existing proposal lifecycle: Features
and TestSpecs accumulate and stay current (they describe the product);
Proposals and Plans are working documents that get trimmed and deleted as
work ships, with their outcome reflected back onto the Feature's `status`.
(The same split OpenSpec ships as durable `specs/` vs archived `changes/` —
see the [prior-art note](notes/lifecycle-taxonomy-prior-art.md) §2 and Open
question 9.)

### Where PRD and ADR sit

The two classic SDLC artifacts deliberately do **not** become objects here:

- **PRD.** A PRD is a *view over the durable catalog*, not a fifth object: the
  Feature tree (`composed_of` DAG) with summaries, acceptance criteria, and
  status *is* the product-requirements content, so a PRD can be rendered from
  the catalog rather than authored beside it and allowed to drift. (Constructor
  Studio and BMAD both keep PRD as a separately-authored markdown artifact and
  then need cross-reference lints to keep it consistent with FEATURE specs —
  [prior-art note](notes/lifecycle-taxonomy-prior-art.md) §2/§4; deriving the
  view avoids that whole failure class.) Where a kitsoki story drives an
  *external* project's SDLC (the cypilot story, gears-rust's `docs/PRD.md`),
  PRD remains the external tool's artifact kind — this taxonomy is kitsoki's
  internal model.
- **ADR.** Proposals are transient, so durable decision rationale needs a home
  that survives the trim-on-ship. That home already exists: proposal content
  worth keeping moves to narrative docs under `docs/architecture/` on ship —
  an ADR is exactly that residue, and v1 keeps it as plain markdown (decisions
  are *documents first*, the `artifact-format.md` split, so they take the
  markdown-with-frontmatter container, not this YAML one). The trace is
  preserved through the Feature's design history (`proposals:`, Open
  question 5) and ordinary links into `docs/architecture/`. If decisions ever
  need linting (supersedes-chains, status), a `lifecycle/adr/v1` schema can be
  added without disturbing the four objects.

## Impact

- **Code seams:** new `internal/lifecycle/` (loader, schema registry, lint).
  `!include` resolution extracted from
  `internal/testrunner/cassette.go:291` (`resolveIncludes`) into a shared
  package both consumers use. Schema validation wired the same way as
  `internal/host/agent_extract_helpers.go:66` (`jsonSchemaValidate`).
- **Schemas:** `internal/lifecycle/schemas/{common,feature,proposal,plan,test-spec}-v1.json`,
  embedded via `go:embed`, `$id` convention per
  `internal/app/schemas/choice.schema.json`
  (`https://kitsoki.dev/schemas/lifecycle/<object>/v1.json`).
- **Vocabulary:** no new effects or host calls in v1. The objects are inert
  data plus a lint; stories adopt them later (see Non-goals).
- **Stories affected:** none change behavior in v1. The decompose story
  ([`work-decomposition.md`](work-decomposition.md)) is the natural future
  *producer* of Plan objects — its brief shape is deliberately a subset of the
  Plan task shape (Open question 2). The dev-story `idea` flow is the natural
  future producer of Proposal objects.
- **Backward compat:** existing markdown proposals keep working untouched. A
  Proposal object can wrap a legacy file wholesale (`body: !include …`) so
  adoption is incremental, new-work-first.
- **Dependencies:** none new — `gopkg.in/yaml.v3`,
  `santhosh-tekuri/jsonschema/v6` already in `go.mod` (same promotion note as
  `artifact-format.md`).
- **Docs on ship:** `docs/architecture/lifecycle-taxonomy.md`; the seeded
  catalog replaces `docs/features/mvp.md`.

## Container conventions (shared by all four objects)

1. **YAML-primary.** The file *is* the record. No frontmatter fence, no
   markdown body — markdown lives in fields.
2. **Markdown fields**, typed `{"type": "string", "contentMediaType":
   "text/markdown"}` (advisory, per `artifact-format.md` — goldmark with the
   pinned dialect does the real parse when a consumer needs one). Two ways to
   author them, same field either way:
   - inline block scalar (`summary: |`) for short prose;
   - `!include help.md` for long-form prose an author wants to edit as a real
     markdown file. Resolution happens at load, relative to the YAML file,
     path-jailed to the catalog — the exact semantics the cassette
     preprocessor already enforces (`cassette.go:309-365`: no absolute paths,
     no escape from the base dir, `.md`/`.txt` → string, `.json` → structure).
3. **Schema pin.** First key of every file: `schema: lifecycle/<object>/v1`.
   Load fails fast on schema violation — the write-time-validation invariant
   from `artifact-format.md`, applied at read here since these files are
   hand-authored.
4. **Ids and refs.** Ids are kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`, the
   `decomposition.yaml` pattern). Cross-references are plain string ids; the
   *field name* carries the type (`composed_of:` lists feature ids,
   `proposal:` names a proposal id). The catalog lint resolves them; a
   dangling ref is a hard failure, same as the decompose dangling-id check.
5. **Media descriptor**, reused at every level (feature, tutorial step,
   test-spec evidence):

   ```yaml
   - kind: video            # video | screenshot | gif | contact-sheet
     path: media/agent-actions.mp4   # relative to this YAML file
     caption: "The drawer opening on a tool-call row"   # markdown, short
     alt: "Agent Actions drawer expanded"               # plain text, a11y
     produced_by: tools/runstatus/tests/playwright/agent-actions-video.spec.ts
   ```

   `produced_by` points at the deterministic producer (Playwright spec, tour
   manifest, `kitsoki-ui-demo` run) so media is *regenerable*, not just
   stored — the same source-pointer discipline as the chapter sidecar
   shipped for mockup-video (`host.video.frame`, see
   [`docs/architecture/hosts.md`](../architecture/hosts.md)).

## The objects

Worked examples below use the agent-actions feature (real media, real specs)
so the shapes are grounded, not hypothetical.

### Feature (`lifecycle/feature/v1`)

```yaml
schema: lifecycle/feature/v1
id: web-agent-actions
title: Agent Actions drawer
status: shipped          # idea | proposed | planned | in-progress | shipped
summary: |
  Per-tool-call detail for every agent in the web UI: a drawer on each
  agent-call row showing the full action transcript.
help: !include help.md   # long-form operator help, edited as markdown
composed_of:             # feature composition — an acyclic DAG, parent→child
  - web-agent-actions-drawer
  - web-agent-actions-video-tour
media:
  - kind: video
    path: media/agent-actions.mp4
    caption: "Feature tour: opening the drawer from the transcript"
    alt: "Agent Actions drawer demo"
    produced_by: tools/runstatus/tests/playwright/agent-actions-video.spec.ts
tutorials:
  - id: first-look
    title: Reading an agent's actions
    audience: operator           # operator | author | developer
    steps:
      - title: Open a session with agent activity
        body: |
          From the home screen, pick any run whose transcript shows a
          spinner row — that row is a live agent call.
        media:
          - kind: screenshot
            path: media/tutorial-step-1.png
            alt: "Transcript with a tool-call row highlighted"
      - title: Expand the drawer
        body: |
          Click the row. The drawer shows each tool call with input,
          output, and timing.
acceptance:              # the seed of TestSpecs — criteria live HERE
  - id: drawer-opens-from-row
    criterion: |
      Clicking a tool-call row in the transcript opens the drawer with
      that call's detail, without losing transcript scroll position.
  - id: streaming-rows-update
    criterion: |
      A row for an in-flight call updates live; the drawer reflects new
      tool calls as they stream.
proposals: [agent-action-transcripts]   # design history, current + past
```

Notes:

- **Composition is parent→child only** (`composed_of`); the lint builds the
  reverse index and rejects cycles, exactly like the decompose DAG check.
  Every level of the tree is a full Feature — so a sub-feature carries its own
  media, help, tutorials, and acceptance, satisfying "at each level" by
  construction rather than by special-casing.
- **Acceptance criteria belong to the Feature**, not the proposal or plan —
  they survive the transient documents and are what TestSpecs trace back to.
- `status` is the one field tools rewrite (when a plan completes). Rewrites go
  through `yaml.Node` so hand-authored comments and `!include` tags survive —
  the `artifact-format.md` fidelity decision applies verbatim.

### Proposal (`lifecycle/proposal/v1`)

The existing spine, as data. Markdown templates stay the *authoring* surface
for humans; this is the processable form.

```yaml
schema: lifecycle/proposal/v1
id: agent-action-transcripts
title: Surface per-tool-call detail via transcript sidecars
status: draft            # draft | accepted | superseded   (file deletion = shipped)
kind: tracing            # story | runtime | tui | tracing | epic
epic: null               # or the epic's proposal id
features: [web-agent-actions]    # what this proposal advances
why: !include why.md             # the spine sections are markdown fields —
what_changes: !include what-changes.md   # include'd or inline, author's choice
impact: |
  - **Code seams:** …
design: !include design.md       # everything between Impact and Tasks
tasks:                           # the phased checklist, as data
  - id: sidecar-writer
    title: Persist RawEvents to a per-call sidecar keyed by call_id
    done: false
open_questions:
  - id: sidecar-retention
    question: Do sidecars get pruned with their trace, or independently?
    lean: with the trace
non_goals:
  - Inlining transcript detail into the trace itself
body: null   # legacy escape hatch: `body: !include ../old-proposal.md`
             # wraps an existing markdown proposal wholesale, structured
             # fields null — lets the catalog index legacy proposals
             # without rewriting them
```

The `status` enum is deliberately small because the existing lifecycle already
encodes the rest: *shipped* is not a status — it's the file being trimmed and
eventually deleted, with the Feature's `status` advancing instead.

### Plan (`lifecycle/plan/v1`)

The implementation decomposition. The task shape is a strict superset of the
`decomposition.yaml` brief
([`work-decomposition.md`](work-decomposition.md) — id / title / kind / goal /
scope / depends_on / acceptance / test_plan / agent_brief / risk), adding the
file-by-file contract.

```yaml
schema: lifecycle/plan/v1
id: agent-action-transcripts-plan
proposal: agent-action-transcripts
brief: |
  Persist the claude stream-json we already capture to per-call sidecars,
  expose them through a Transcript seam on AskResponse, render in the web
  drawer. Three tasks, runtime → tracing → tui order.
tasks:
  - id: sidecar-writer
    title: Per-call transcript sidecar
    kind: runtime          # story | runtime | tui | tracing | test | docs
    goal: |
      Every agent call with raw events lands a sidecar keyed by call_id;
      the trace carries a pointer only.
    depends_on: []
    risk: medium
    expected_files:        # the file-by-file contract
      - path: internal/host/agent_transcript.go
        action: create     # create | modify | delete
        changes: |
          Sidecar writer: serialize `ClaudeRun.RawEvents` to
          `<trace-dir>/transcripts/<call_id>.jsonl`; fsync before the
          trace event referencing it is emitted.
      - path: internal/host/agent.go
        action: modify
        changes: |
          Thread the sidecar path into the `agent.call.*` trace event as
          `transcript_ref`; no inlined detail.
    acceptance:
      - Sidecar exists for every agent call in a recorded run
      - Trace event carries transcript_ref and no inlined events
    test_specs: [web-agent-actions-spec]   # which TestSpecs this satisfies
    agent_brief: |
      Self-contained implementer prompt, ≥80 chars, exactly as the
      decomposition brief field works today …
```

A Plan is transient: it exists while its proposal is being executed and is
deleted with it. The durable residue is the Feature status flip, the shipped
code, and the TestSpecs now marked passing.

### TestSpec (`lifecycle/test-spec/v1`)

Derived from a feature's acceptance criteria — every scenario must cite one.

```yaml
schema: lifecycle/test-spec/v1
id: web-agent-actions-spec
feature: web-agent-actions
scenarios:
  - id: drawer-from-row
    acceptance: drawer-opens-from-row    # MUST name a criterion id on the feature
    given: A session transcript with at least one completed tool-call row
    when: The operator clicks the row
    then: |
      The drawer opens with that call's transcript; scroll position is
      unchanged.
    harness: playwright      # flow | unit | playwright | manual
    fixture: tools/runstatus/tests/playwright/agent-actions-video.spec.ts
    evidence:                # optional; reuses the media descriptor
      - kind: video
        path: ../features/web-agent-actions/media/agent-actions.mp4
        alt: "Recorded evidence of drawer behavior"
        produced_by: tools/runstatus/tests/playwright/agent-actions-video.spec.ts
  - id: streaming-update
    acceptance: streaming-rows-update
    given: A live run with an in-flight agent call
    when: New tool calls stream in
    then: The open drawer appends rows without a reload.
    harness: flow
    fixture: stories/dev-story/flows/agent-actions-streaming.yaml
status: implemented        # specified | implemented   (pass/fail is CI's job, not the file's)
```

Notes:

- `harness` + `fixture` make the spec *navigable to its executable test*, and
  the lint can warn when `status: implemented` names a fixture that doesn't
  exist. Pass/fail state deliberately does **not** live in the file — that
  would rot instantly; CI owns it. The file owns the *mapping*.
- `evidence` is how demo videos / QA screenshots become declared, regenerable
  proof attached to a scenario — the seam the `kitsoki-ui-qa` skill judges
  against today, made addressable.
- **Gherkin.** The `given/when/then` fields are Gherkin's triad carried as
  *descriptive data*: nothing regex-matches the prose (no step-definition
  coupling — Gherkin's classic failure mode), because the executable link is
  the `harness` + `fixture` pointer instead. Real `.feature` files slot in as
  a harness, not a container: `harness: gherkin` with
  `fixture: <path>.feature#<scenario-name>`, lint-parsed via the official
  Gherkin Go parser so a missing scenario is a hard error (Open question 10;
  the full fit analysis is
  [prior-art note §3](notes/lifecycle-taxonomy-prior-art.md)).

## Validation

Two deterministic layers, no LLM anywhere (the moat split as in the decompose
story: interpretation happens in whatever story *authors* these files;
checking them is pure engine):

1. **Per-file: JSON Schema.** Load → resolve `!include` → normalize the YAML
   tree to the JSON value model (the `artifact-format.md` task-1.3a lesson:
   unquoted timestamps/`yes`-`no` coerce; normalize before validating) →
   validate against the pinned schema.
2. **Catalog lint** (`kitsoki lifecycle lint`, also a corpus test in CI):
   - ids unique per object type; all refs resolve (no dangling feature /
     proposal / plan / spec / acceptance-criterion ids);
   - `composed_of` and `depends_on` are acyclic (topo-sort, the
     `decompose_validate.py` checks generalized);
   - every `media.path` and every `expected_files.path` parent dir exists;
     `!include` targets exist and stay inside the catalog;
   - **coverage:** every feature acceptance criterion is cited by ≥1 TestSpec
     scenario (warn in v1, promote to error once the catalog is seeded);
   - consistency warnings: `status: shipped` feature still referenced by a
     `draft` proposal; `implemented` spec with a missing fixture.

## Layout

```
docs/features/<feature-id>/
  feature.yaml
  help.md                  # !include targets live next to their object
  media/…                  # committed evidence/media (or produced_by-regenerable)
  tutorials/…              # optional, for long tutorial bodies via !include
docs/proposals/<id>.md     # human-authored markdown, unchanged, AND/OR
docs/proposals/<id>.yaml   # the structured form (Open question 3)
docs/proposals/plans/<id>.yaml      # transient, deleted on ship
docs/features/<feature-id>/specs/<id>.yaml   # durable, lives with its feature
```

Lean: features and their specs cohabit (a spec without its feature is
meaningless); plans cohabit with proposals (same lifecycle, same fate).

## Relationship to existing work

| Existing thing | Relationship |
|---|---|
| [`artifact-format.md`](artifact-format.md) | Sibling substrate. Shares: schema pinning, `santhosh-tekuri` validation, `yaml.Node` rewrites, `contentMediaType`, JSON-model normalization. Differs: pure-YAML container, no frontmatter fence. Neither blocks the other; if `internal/artifact` lands first, `internal/lifecycle` uses its registry/validate internals. |
| Cassette `!include` (`internal/testrunner/cassette.go:291`) | Extracted to a shared package; cassettes become a consumer of the extraction (behavior unchanged, tests stay green). |
| `decomposition.yaml` ([`work-decomposition.md`](work-decomposition.md)) | Plan task = brief + `expected_files`. The decompose story should eventually emit `lifecycle/plan/v1` instead of its own shape (Open question 2). |
| Tour manifests / Playwright specs (`tools/runstatus/src/tour/`) | Become `produced_by` targets — the regeneration pointers for feature media. No change to them. |
| dev-story `idea` flow (`stories/dev-story/`) | Future producer of `lifecycle/proposal/v1`; its existing `schemas/design-artifact.json` is the precedent for schema-gated authoring. Not changed in v1. |
| `docs/features/mvp.md` | Seeded into real Feature objects, then deleted. |
| Constructor Studio (cypilot upstream) + `internal/host/cypilot_artifacts.go` | Concept donor, not substrate ([prior-art note](notes/lifecycle-taxonomy-prior-art.md) §4–5): adapt its per-ID-kind lint config, top-down coverage errors, and bidirectional status consistency into the catalog lint; do not adopt its markdown+TOML containers. Separately, the provider's `cpt generate/plan/analyze` verbs no longer exist upstream (`cpt`→`cfs` rename) — re-map against a pinned release or retire it, independent of this proposal. |

## Tasks

```
## 0. Design review (this document)
- [ ] 0.1 Agree the four-object model + durable/transient split
- [ ] 0.2 Resolve Open questions 1–3 (layout, plan/brief unification, proposal
          migration) and the research-derived 7–10 (criterion revisions, criterion
          grammar, ship deltas, Gherkin interop)
- [ ] 0.3 Hand-author the agent-actions worked example end-to-end (all four files)
          against draft schemas; adjust schemas from friction, not theory

## 1. Substrate
- [ ] 1.1 Extract !include: shared package; cassette loader consumes it; tests green
- [ ] 1.2 internal/lifecycle: loader (YAML → include-resolve → JSON-normalize),
          go:embed schema registry, the five v1 schemas (common + 4 objects)
- [ ] 1.3 Catalog lint (DAG, refs, coverage, paths) + `kitsoki lifecycle lint`

## 2. Verification
- [ ] 2.1 Schema unit table: good/bad fixture per object, incl. a coercion case
- [ ] 2.2 Lint unit table: cycle, dangling ref, missing include, uncovered criterion
- [ ] 2.3 Corpus test: lint the seeded catalog in CI

## 3. Seed + document
- [ ] 3.1 Seed 2–3 real features (agent-actions, operator-ask, story-editor)
          with media/help/specs; delete docs/features/mvp.md
- [ ] 3.2 docs/architecture/lifecycle-taxonomy.md; trim/delete this proposal
```

## Verification

No LLM needed anywhere. Schema validation and lint are table tests over
fixtures (2.1/2.2); the seeded catalog is the corpus regression (2.3) — the
same "author the schema from the corpus" gate `artifact-format.md` uses for
tickets. The `!include` extraction is gated on the existing cassette
determinism tests passing unchanged
(`internal/testrunner/cassette_determinism_test.go`).

## Open questions

1. **Catalog layout** — features under `docs/features/<id>/` (lean, shown
   above) vs. a new top-level `product/` root? *Lean: `docs/features/` —
   it exists, and the durable catalog is documentation.*
2. **Unify Plan tasks with decomposition briefs** — should
   `schemas/decomposition.json` be retired in favor of `lifecycle/plan/v1`
   (the decompose story emits Plans), or do both shapes live? *Lean: unify;
   the brief shape was designed to be emitted by an agent and the Plan task
   is a superset — but the decompose story isn't built yet, so this is cheap
   to decide now and expensive later.*
3. **Proposal migration posture** — new proposals authored as YAML-primary
   (markdown sections via `!include`, so the prose is still pleasant to edit),
   or markdown stays primary with `.yaml` as a generated/wrapper index
   (`body: !include`)? *Lean: wrapper-first (zero migration, catalog gains
   coverage immediately), native YAML for new proposals once the authoring
   flow emits them — but this changes what the templates are, so it needs a
   real decision.*
4. **`!include` semantics** — keep the proven textual preprocessor
   (line-anchored tag, path-jailed) or move to proper `yaml.Node` tag
   resolution? *Lean: extract the preprocessor as-is (known limits: no
   includes inside anchors, `cassette.go:306`); revisit only if a consumer
   needs nested/structured includes.*
5. **Reverse links** — should a Feature list its `proposals:` (shown above),
   or should that be lint-derived from Proposal→`features:` only, to keep one
   direction authoritative? *Lean: one direction (Proposal→Feature) is
   authored; the feature-side list is derived at lint/load time. The example
   above shows it authored — pick one in review.*
6. **Tutorial depth** — are tutorials a list-of-steps inline (shown), or do
   they deserve their own object type with `!include`d step bodies once one
   gets long? *Lean: inline until a real tutorial outgrows it.*
7. **Criterion revisions** — add an OpenFastTrace-style revision integer to
   acceptance-criterion ids, so the coverage lint can flag a TestSpec scenario
   as *outdated* (criterion changed since the scenario cited it) rather than
   only present/absent? *Lean: yes — one optional integer field in the schema,
   a warn-level lint; the only verified mechanism the proposal otherwise
   lacks (prior-art note §1).*
8. **Constrained criterion grammar** — should Feature acceptance criteria
   offer an optional structured shape instead of only free prose? Kiro and
   OpenSpec both show constrained criteria are what make downstream
   tooling possible (prior-art note §2). *Lean: optional `given/when/then`
   keys alongside `criterion:`, free prose stays valid — and the shape is the
   Gherkin triad, not EARS: we already use Gherkin heavily, and a Gherkin
   criterion projects mechanically into a TestSpec scenario or a `.feature`
   stub (prior-art note §3).*
9. **Criterion deltas on ship** — when a Proposal ships, should it carry
   structured ADDED/MODIFIED/REMOVED acceptance-criterion deltas that merge
   into the Feature (OpenSpec's archive mechanic), instead of hand-editing
   the Feature plus the `status` flip? *Lean: defer to v2 — v1 keeps the
   manual flip; revisit when a story automates the ship step.*
10. **Gherkin interop depth** — `harness: gherkin` with
    `fixture: <path>.feature#<scenario-name>` and a lint that parses cited
    `.feature` files (official Gherkin Go parser) to hard-error on missing
    scenarios — and optionally a `@criterion:<feature-id>/<criterion-id>`
    tag convention so coverage is computable from the test side too
    (prior-art note §3). How much lands in v1? *Lean: the harness kind +
    parsed existence check in v1 (it's the same shipped-fixture lint the
    other harnesses get, just structural); tag back-references when a real
    `.feature` corpus exists to lint.*

## Non-goals

- **No engine/story changes in v1.** No new effects, host calls, or world
  semantics; no story emits or consumes these objects yet. Producers (idea
  flow, decompose story) adopt the schemas in their own proposals.
- **No web UI for the catalog.** Rendering features/tutorials/specs in
  `kitsoki web` is a natural follow-on (the media descriptors are
  display-ready), not v1.
- **No generated test code.** TestSpecs map to fixtures; they don't generate
  them.
- **No mass migration of the 30+ existing markdown proposals.** Wrapper
  objects only, opt-in, new-work-first.
- **No pass/fail state in TestSpec files.** CI owns execution results; the
  files own the mapping.
- **Not replacing flow fixtures, cassettes, or the trace format.** Those are
  execution artifacts; this taxonomy sits upstream of them.
