# Prior art for the lifecycle taxonomy — industry survey + Constructor Studio (cypilot) evaluation

**Status:** Research note, 2026-06-12. Supports the design review of
[`lifecycle-taxonomy.md`](../lifecycle-taxonomy.md) (Task 0). Two questions:
does the industry already do machine-readable feature → design → plan → test
traceability, and can `constructorfabric/studio` — the upstream of what kitsoki
integrates as **cypilot** — be adapted instead of building `internal/lifecycle`?

**Method:** a multi-agent web research pass over the requirements-as-code field
(claims kept only after 3-vote adversarial verification; survivors marked *verified*),
a follow-up primary-source sweep of the agent-era spec-driven tools (cited but
single-pass), a ground-truth shallow-clone inspection of
`constructorfabric/studio` at tag v1.3.1 (HEAD 2026-06-12), and a dedicated
Gherkin fit analysis (§3) since the team uses Gherkin heavily in practice.

## Verdict in one table

| Proposal decision | Prior art | What it says |
|---|---|---|
| YAML record per object | Doorstop (verified) | One YAML file per requirement/test item in VCS is a proven container |
| Pinned JSON Schema per kind | StrictDoc critique + Open-Needs (verified) | Explicit schema beats ad-hoc-validated YAML; Open-Needs planned exactly JSON-Schema-pinned need objects (and stalled — design prior art only) |
| Kebab-case ids, plain-string refs | StrictDoc, Melexis, Doorstop (verified) | Field median: free-form-unique ids with a kind-prefix convention |
| Catalog lint: unique ids, acyclic DAG, dangling refs | StrictDoc (verified) | Cycle detection and link-integrity as hard validation errors is established |
| Acceptance-coverage as a **hard** lint | Melexis, OpenFastTrace (verified) | Most tools ship coverage *reports*; Melexis (threshold gate) and OFT (defect vocabulary) prove gates are viable. The proposal's choice is the stricter, rarer one |
| Evidence as pointers, pass/fail stays in CI | StrictDoc (verified) | Convention is file-based references to harness output, never embedded results |
| Durable/transient split | **OpenSpec** (primary-source) | Shipped precedent: durable `specs/` catalog + transient `changes/` archived on ship |
| TestSpec `given/when/then` scenario fields | **Gherkin** (§3) | The triad *is* Gherkin's grammar, carried as data; harness/fixture pointers deliberately replace Gherkin's step-glue coupling |
| Media/help/tutorials with `produced_by` | none anywhere | Genuinely novel — no tool surveyed (incl. studio) models evidence/media attachments |

## 1. Requirements-as-code tools (verified claims)

- **Doorstop** ([doorstop-dev/doorstop](https://github.com/doorstop-dev/doorstop),
  LGPLv3, active): each requirement/test item is an individual YAML file in VCS;
  prefixed ids (`SRD002`, `HLTC001`); CLI-created parent/child links; tree-wide
  integrity validation; published trace matrices. Closest container precedent.
- **StrictDoc** ([docs](https://strictdoc.readthedocs.io)): rejects Doorstop-style
  YAML ("implicitly-defined grammar … encoded 'ad-hoc' in the parsing and validation
  rules") for an explicit textX grammar. Free-form-unique UIDs with tree-wide
  uniqueness and `manage auto-uid` generation (used by Zephyr's safety CI). Exactly
  three relation types (Parent/Child/File) specialized by grammar-registered roles
  (Refines/Implements/Verifies). Cycles are validation errors. Its coverage surface
  is a *statistics report*, not a gate, and it has no built-in requirement→test
  check — test-report ingestion (JUnit/lit/Robot) is experimental and file-based.
- **Melexis sphinx-traceability-extension**
  ([docs](https://melexis.github.io/sphinx-traceability-extension/usage.html),
  GPL-3.0, active): kind-prefixed ids (`RQT-`, `DESIGN-`, `[IU]TEST-`) drive
  regex-based matrices; attribute values validated against configured regexes; the
  item-matrix `coverage` option gates on a threshold (`'>= 95'`) at build time. The
  closest shipped analogue of the proposal's acceptance-coverage lint.
- **OpenFastTrace** ([user guide](https://github.com/itsallcode/openfasttrace/blob/main/doc/user_guide.md)):
  two ideas worth stealing. (a) Revision-bearing ids `type~name~revision`
  (`feat~html-export~1`) — bumping the revision voids existing coverage, so stale
  coverage is detected, not just missing. (b) Bidirectional declarations: `Covers`
  (what I cover) and `Needs` (which artifact *types* must cover me) make coverage a
  local property; `oft trace` classifies defects as Outdated / Orphaned / Ambiguous /
  Unwanted / Duplicate — a ready-made lint-output vocabulary.
- **Open-Needs** ([open-needs.org](https://open-needs.org/)): planned
  framework-independent, JSON-Schema-pinned need catalogs — the proposal's exact
  schema posture — and stalled in concept phase (dormant since ~2022–2024). Design
  prior art, not adoptable machinery.

Refuted during verification (do not cite): Sphinx-Needs `needs_id_regex` as a hard
build failure; StrictDoc DO-178C forward/backward trace matrices; StrictDoc
"uncovered requirement report" as a coverage lint.

## 2. Agent-era spec-driven tools (primary-source sweep)

- **OpenSpec** ([Fission-AI/OpenSpec](https://github.com/Fission-AI/OpenSpec), MIT,
  54k stars, very active): the closest living relative of the whole proposal.
  Durable `openspec/specs/<capability>/spec.md` ("what IS built") vs transient
  `openspec/changes/<id>/` (`proposal.md` with Why / What Changes / Impact — nearly
  kitsoki's spine — plus `tasks.md` and **spec deltas**:
  `## ADDED|MODIFIED|REMOVED|RENAMED Requirements`). `openspec archive` merges deltas
  into the durable catalog by normalized-header match and moves the change to
  `changes/archive/YYYY-MM-DD-<id>/`. A deterministic Zod-backed `openspec validate`
  enforces required sections, duplicate-header errors, scenario shape
  (`#### Scenario:` with GIVEN/WHEN/THEN bullets, ≥1 per requirement), with
  `--strict` and versioned `--json` output. Requirement *header text* is the id —
  no numeric ids, so rename is a first-class delta op. No AC→test mapping, no
  evidence/media.
- **AWS Kiro** ([docs](https://kiro.dev/docs/specs/)): `.kiro/specs/<feature>/`
  with `requirements.md` / `design.md` / `tasks.md` (plus a distinct `bugfix.md`
  kind). Acceptance criteria use **EARS** ("WHEN … THE SYSTEM SHALL …") — constrained
  enough that Kiro extracts properties and **generates property-based tests** from
  them: the only surveyed pipeline that closes acceptance criteria → executable
  tests. Agent-enforced convention; no public schema.
- **GitHub Spec Kit** ([github/spec-kit](https://github.com/github/spec-kit)):
  typed id namespaces (`FR-###`, `SC-###`, `T###`, `US#`, `CHK###`) joined by
  `/speckit.analyze` into coverage % and orphaned-task findings — but the analyzer
  is an **LLM prompt**, not a linter; the only deterministic check is file
  existence. Borrowable: `[NEEDS CLARIFICATION: …]` as a greppable uncertainty
  marker; `[P]` parallel-safety task annotations.
- **BMAD-METHOD** ([bmad-code-org/BMAD-METHOD](https://github.com/bmad-code-org/BMAD-METHOD),
  v6, 49k stars): richest id traceability of the four (PRD `FR-N`/`UJ-N`/`SM-N`
  cross-links, an FR Coverage Map, story tasks tagged `(AC: #)`) but every quality
  gate is an LLM checklist — nothing machine-checks a PRD or story. The cautionary
  pattern: coverage-by-agent-procedure rots; coverage-by-lint doesn't. That fault
  line (OpenSpec on one side, Spec Kit/BMAD on the other) is the proposal's no-LLM
  moat split, observed in the wild.

## 3. Gherkin — how it fits this taxonomy

We use Gherkin heavily in practice, so this gets a dedicated consideration.
(Note: nothing in the kitsoki tree itself is Gherkin today — no `.feature`
files; the flow fixtures and Playwright specs are YAML/TypeScript. The triad
lives in our authoring habits and external projects, which is exactly what the
fit below assumes.)

**What Gherkin settled, as prior art.** Fifteen-plus years as the de facto
acceptance-scenario grammar ([reference](https://cucumber.io/docs/gherkin/reference/)):
a `.feature` file carries a feature narrative plus `Scenario` /
`Scenario Outline` + `Examples` / `Background` blocks in
Given/When/Then/And/But steps. Its only cross-reference mechanism is **tags**
(`@smoke`, `@REQ-123`) — flat, untyped strings, but the standard Cucumber
traceability pattern is tagging scenarios with requirement ids and joining in
reports. "Living documentation" tooling (Cucumber Reports, SpecFlow LivingDoc,
Cukedoctor) renders feature files into browsable catalogs — the closest
pre-agent-era thing to this proposal's Feature + TestSpec pair. Official
parsers exist in many languages including Go
([cucumber/gherkin](https://github.com/cucumber/gherkin); the official Go
runner is [godog](https://github.com/cucumber/godog)).

**Gherkin's two famous failure modes — and how the TestSpec dodges them:**

1. **Step-glue coupling.** Gherkin prose binds to step-definition regexes, so
   a wording change breaks tests, and scenarios drift toward stilted
   test-script phrasing to fit existing steps. The TestSpec's
   `given/when/then` are *descriptive data* mapped to `harness:` + `fixture:`
   pointers — no regex matches the prose, so wording can be edited freely.
   This decoupling is deliberate; an executable-Gherkin TestSpec would
   reintroduce the coupling.
2. **Feature-file rot.** When teams treat `.feature` files as a test artifact
   instead of a shared spec, they stop being read and rot ("BDD is discovery,
   not test automation" is the Cucumber community's own refrain). The
   proposal's durable-catalog posture + coverage lint is the structural
   counter: criteria live on the Feature, scenarios must cite them, and the
   lint notices when the mapping decays.

**Where Gherkin plugs in concretely:**

- **As a harness kind.** Add `harness: gherkin` alongside flow/unit/playwright,
  with `fixture:` pointing at a `.feature` file and the scenario cited by name
  (e.g. `fixture: features/auth.feature#valid-login`). The catalog lint can
  parse the file with the official Gherkin Go parser and hard-error when the
  cited scenario doesn't exist — the same shipped-fixture existence check the
  other harnesses get, but structurally verified instead of path-only.
- **As tag back-references.** A
  `@criterion:<feature-id>/<criterion-id>` tag convention inside `.feature`
  files gives the test side a declared link back to the catalog — the same
  shape as OFT's `Covers` and studio's `@cpt-*` code markers (§1, §4). The
  lint then computes coverage from *both* directions and can flag a scenario
  whose tag names a criterion that no longer exists.
- **As the criterion grammar** (proposal Open question 8). If Feature
  acceptance criteria adopt the structured shape, it should be the Gherkin
  triad — not EARS — because the team is already fluent in it and because a
  criterion written as given/when/then projects mechanically into a TestSpec
  scenario or a `.feature` stub. One grammar from criterion to scenario to
  executable test.
- **The one construct with no analog:** `Scenario Outline` + `Examples`
  tables (parameterized scenarios). If TestSpecs ever need parameterization,
  an `examples:` list of key/value rows on a scenario is the natural borrow —
  cheap in the schema, familiar to every Gherkin author.

**What not to do: don't make `.feature` files the Feature container.** A
feature file *looks* like the Feature object (narrative + acceptance
scenarios fused), but it cannot carry composition (`composed_of`), media,
help, tutorials, or schema'd fields; tags are its only metadata and they are
untyped. Using it as the catalog container would tie the durable catalog to
test execution and inherit the rot problem. Gherkin's right role here is the
**evidence layer**: TestSpec scenarios may *delegate to* Gherkin, never *be*
Gherkin.

## 4. Constructor Studio — the cypilot upstream, evaluated

Ground truth from a clone of [constructorfabric/studio](https://github.com/constructorfabric/studio)
(v1.3.1, Apache-2.0, last commit same-day — very active).

**Cypilot was renamed, and the drift is total.** CLI `cpt` → `cfs`; "CPT" now means
*Canonical Provenance Trace ID*; a formal migration exists
(`cfs init --migrate-from-cypilot=yes` + an orchestrated cleanup;
`guides/MIGRATING-FROM-CYPILOT.md`). The verbs `internal/host/cypilot_artifacts.go`
shells out to (`generate` / `plan` / `analyze`) no longer exist as CLI subcommands —
workflows are now agent chat-routes (`cf-sdlc-doc-prd`, `cf-sdlc-implement`, …) and
the CLI is deterministic tooling only (`cfs validate`, `list-ids`, `spec-coverage`,
`where-defined`, `where-used`, `get-content`, `map`, `kit install`).

**Model:** markdown-primary. PRD / ADR / DESIGN / DECOMPOSITION / FEATURE artifacts
are markdown files with heading structure enforced per kind (`constraints.toml`),
registered in `artifacts.toml` (TOML; JSON Schema for the registry only). The engine
is a thin Python proxy dispatching a vendored skill engine; kits bundle templates +
checklists + rules. **No TestSpec analog**: FEATURE has required Acceptance Criteria
and Definitions of Done headings, but no harness/fixture/evidence mapping and no
media model. No durable/transient split — and its SDLC kit ships a `migrate-openspec`
workflow, positioning directly against OpenSpec.

**Its traceability machinery is the adaptable part:**

- ID grammar `cpt-{system}[-{subsystem}…]-{kind}-{slug}` with per-kind configuration:
  `template`, `required`, `to_code`, `task` (checkboxes), `priority` (`p1`–`p9`
  phases), allowed headings, and `references.TARGET.coverage` (mandatory cross-refs).
- Top-down reference-coverage as hard `cfs validate` errors
  (PRD → DECOMPOSITION → DESIGN → FEATURE), plus checkbox-status consistency
  (a `[x]` reference to a `[ ]` definition is an error).
- Code-block markers (`@cpt-begin/@cpt-end:<id>:p1:inst-x`) with **bidirectional**
  enforcement: `[x]` step ⇒ marker must exist, `[ ]` step ⇒ marker must *not* exist.
  Deeper than anything the proposal attempts (and deeper than it needs in v1).

## 5. Recommendations

1. **Build `internal/lifecycle` as proposed; do not adopt studio's formats or
   machinery.** Studio is markdown + TOML + Python/agent-prompts; the proposal is
   YAML records + JSON Schema + Go. Wholesale adoption surrenders the data-primary
   premise and cannot express TestSpec evidence, media descriptors, tutorials, or
   the durable/transient split. Apache-2.0 makes borrowing concepts (or lint logic)
   unencumbered.
2. **Adapt four studio ideas into the catalog lint/schemas:** the per-ID-kind
   configuration table; top-down reference-coverage as a hard error; bidirectional
   status consistency (`[x]` definition ⇔ evidence exists); `p1`–`p9` phase markers
   if Plans grow phases.
3. **Weigh two OpenSpec ideas at design review:** structured deltas in the transient
   object (a Proposal declaring ADDED/MODIFIED/REMOVED acceptance criteria, merged
   into the Feature on ship — richer than a bare `status` flip), and a constrained
   optional grammar for Feature acceptance criteria so criterion → scenario tracing
   is checkable beyond id citation. If adopted, the grammar should be the Gherkin
   triad, not EARS — see §3 for the argument and for the `harness: gherkin` +
   tag-back-reference integration.
4. **Consider OFT-style revision integers on acceptance-criterion ids** so the lint
   can distinguish *outdated* coverage from *missing* coverage — the one verified
   mechanism nothing in the proposal currently replicates.
5. **Decide the fate of `internal/host/cypilot_artifacts.go` now, independently of
   this proposal:** re-map to the `cfs` verb set against a pinned studio release, or
   retire the provider. Its current command shapes cannot work against any upstream
   release.

## Caveats

- Studio findings are single-source (one same-day clone); re-verify CLI contracts
  against a pinned release before re-mapping the provider.
- §2 is primary-source-cited but single-pass (not adversarially verified). Kiro's
  exact task→requirement reference syntax is only confirmed by secondary sources.
- ReqIF and the formal ALM suites were not examined.
