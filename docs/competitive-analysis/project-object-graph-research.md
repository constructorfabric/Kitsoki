# Project object graph: competitive & prior-art research

Feeds [`project-object-graph.md`](../proposals/project-object-graph.md). Two
adversarially-verified research passes (frameworks + tool comparison) plus a
bounded single-pass gap-fill on management-system practice. Every claim is
tagged `[3-0 verified]` (unanimous adversarial vote across the source research
run) or `[single-pass]` (this document's own gap-fill, one search pass, not
independently re-verified). Sources are linked inline and tabulated in §7.

## 1. Executive summary

The proposal should anchor on five shipping precedents rather than claim
novelty for its substrate: **OpenSpec** for delta-as-document semantics
(current-state specs + delta change folders, deterministic archive-on-apply),
**Backstage's software catalog** for schema-pinned YAML node/edge mechanics
with authored-facts-vs-computed-relations separation, the **eQMS market**
(Drata/Vanta) for the desired-vs-observed drift loop as a live commercial
pattern, **XTDB**'s bitemporal model for audit-grade as-at queries, and the
**digital-thread literature** (Hedberg et al.) for the generality claim beyond
software plus the documented failure mode (ad-hoc hard-coded PLM links) a
typed edge schema fixes. Against the 23-claim tool comparison, no surveyed
requirements-management suite (Jama, Codebeamer, DOORS, StrictDoc, Doorstop)
computes a roadmap as a graph delta or records *why* a decision was made — they
model traceability and change-impact flagging, not decision provenance. The
management-system gap-fill (Annex SL/CAPA, Toyota Kata/A3, ReqIF, CMII/ITIL,
SAFe) confirms literate reviewers will expect specific vocabulary — clause →
requirement → evidence, current/target-condition delta, ECR→ECO→ECN
lifecycle states, suspect-link propagation — that the proposal should absorb
by name rather than reinvent under new labels. The two claims kitsoki can
credibly own, and that an adversarial search found no existing system
occupying, are: roadmap as a *computed* current-vs-desired graph delta at
general (not compliance-scoped) breadth, and *interpretive* decision
provenance (who/what decided, with rationale, at a named point) as first-class
graph data.

## 2. Ranked anchors (pass 1)

1. **OpenSpec — delta semantics.** `specs/` (current-state source of truth,
   structured Requirements + Given/When/Then Scenarios) vs `changes/` (delta
   folders with ADDED/MODIFIED/REMOVED requirement sections that describe only
   the diff); shipped changes archive via a deterministic apply step into
   `changes/archive/` as the chronological audit record.
   [3-0 verified] — [concepts.md](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md).
   **Caution:** the delta vocabulary is *not* a documented closed three-op set
   with a stated merge-conflict-avoidance rationale — that stronger claim was
   refuted 0-3. Cite the mechanism, not a closed-vocabulary guarantee.

2. **Backstage catalog — node/edge YAML mechanics.** Every entity is a
   schema-pinned YAML doc (`apiVersion`/`kind` envelope + `metadata` +
   kind-specific `spec`), validated by per-kind JSON Schemas; typed edges are
   entity-reference fields inside `spec` (`dependsOn`, `owner`, `system`);
   authored facts are separated from **computed** graph state — relations are
   read-only, deduced by catalog processors and materialized as two-way pairs.
   [2-1 / 3-0 / 3-0 verified] —
   [descriptor-format](https://backstage.io/docs/features/software-catalog/descriptor-format/).
   This is the strongest direct precedent for "roadmap is computed, never
   authored" (Shared decision 4). **Caution:** Backstage's kind list
   (Component/API/Group/…) is *not* a fixed closed vocabulary — that claim was
   refuted 1-2; treat kinds as an extensible convention, matching the
   proposal's own GTS-derivation stance.

3. **eQMS drift-delta loop (Drata/Vanta) — market pattern + moat contrast.**
   Controls are shared reusable nodes, mapped once and cross-linked
   many-to-many to 30+ frameworks; evidence edges are machine-populated via
   300+ integrations; the core product loop is a continuous desired-state vs
   observed-state delta (daily control statuses, immediate drift alerts) —
   structurally identical to "roadmap = delta." Vanta additionally computes
   the ISO 27001 Statement of Applicability from the control-evidence graph.
   [3-0 x5 verified] — [Drata](https://drata.com/products/compliance),
   [Vanta](https://www.vanta.com/products/iso-27001).
   **What neither captures:** *why* a control was scoped in/out — the
   human/LLM interpretive decision. ~20% of evidence stays manual; humans
   still supply risk-treatment rationale. This is the clearest existing
   commercial analog to kitsoki's decision-provenance moat, and the clearest
   gap it fills.

4. **XTDB — bitemporal audit substrate.** System time and valid time are
   recorded automatically; as-at queries reconstruct full graph state at any
   moment; out-of-order arrival and retroactive corrections are accommodated
   while system-time history stays append-only. Maps directly onto changesets
   that retroactively amend the graph without destroying provenance — what
   auditors and CAPA processes require.
   [3-0 x3 verified] — [XTDB intro](https://docs.xtdb.com/intro/what-is-xtdb.html).
   Caveat: XTDB supports explicit erasure (GDPR) — full retention is default,
   not absolute.

5. **Digital-thread literature — generality claim + failure mode.** Hedberg,
   Bajaj & Camelio 2020 (peer-reviewed): point-to-point/file-based tool
   integration is fragile and expensive (ripple effects per tool change);
   the fix is lifecycle artifacts as graph nodes with typed relationship
   edges federated over persistent global IDs; their 145-node/436-edge case
   study cut cross-domain traceability queries from hours-to-days to seconds.
   [3-0 x4 verified] —
   [Hedberg et al. 2020, PMC](https://pmc.ncbi.nlm.nih.gov/articles/PMC7437158/).
   **Attribution caveats:** the hours-to-days baseline is cited prior work, not
   a controlled same-case benchmark; the $80–180B "one DoD program" figure is
   West & Blackburn 2017's COCOMO-II estimate — attribute to them, not
   Hedberg et al.

6. **MBSE/openCAESAR convergence — independent design confirmation, with a
   sharp boundary.** Ryś et al. 2025 (Univ. Antwerp, OML/openCAESAR lineage,
   unreviewed Dec-2025 preprint): an extensible artefact ontology with
   versioning + a workflow ontology whose enactment traces are first-class
   graph nodes linking to artefacts produced/consumed — closely paralleling
   kitsoki's shape. Names the same PLM failure mode: "relations are not
   captured in a meta-model, and exist only implicitly as whatever the code
   does" (hedged with named counterexamples: Aras Innovator, 3DEXPERIENCE).
   [3-0 x3 verified, single preprint] — [arXiv:2512.09596](https://arxiv.org/pdf/2512.09596).
   **Critical distinction:** this paper records **execution provenance**
   (activity events + artifact I/O), **not interpretive decision provenance**.
   A claim attributing kitsoki's decision-provenance thesis to this paper was
   explicitly refuted [0-3] — do not cite it as validating that thesis, only
   as validating the artefact/edge substrate shape.

## 3. Management-system & practice mapping (gap-fill, single-pass)

All claims in this section are `[single-pass]` — one WebSearch pass each,
not independently re-verified. 8 searches used total against the 10-call
budget.

**(a) ISO Annex SL / CAPA.** The 10-clause harmonized structure (identical
clause numbers/titles/core text across 9001/14001/27001/45001) maps cleanly
onto the graph: clauses → `requirement` nodes, Clause 9 (Performance
evaluation: monitoring, internal audit, management review) → `evidence`
nodes produced by an audit process, Clause 10 (Improvement: nonconformity,
corrective action, continual improvement) → a changeset-shaped loop —
identify nonconformity → root-cause → corrective action → verify
effectiveness — that is structurally a changeset with a mandatory
"verify" gate before close. [single-pass] —
[Annex SL harmonized structure](https://www.parola.co.uk/ISO/Annex_SL_-_Harmonized_Structure_(HS)_-_Text.pdf),
[Grokipedia summary](https://grokipedia.com/page/Annex_SL). *Adoptable bit:*
a `nonconformity` node type whose lifecycle requires an `evidence` edge to a
"verified effective" record before it can close — the proposal's changeset
apply step should model this same verify-before-close gate, not just
apply-then-done.

**(b) Toyota A3 / Kata; value-stream mapping.** A3: Background → **Current
Condition** (data-backed) → **Target/Goal** (measurable) → root-cause →
**Countermeasures** → implementation plan → follow-up.
[single-pass] — [A3 template](https://www.learnleansigma.com/guides/a3-problem-solving/),
[ASQ A3](https://asq.org/quality-resources/a3-report). Toyota Kata formalizes
the same shape as an iterated loop: grasp **Current Condition** → establish
**Target Condition** (a future state, not a plan for getting there) →
work at the "threshold of knowledge" → develop **Countermeasures** as
obstacles are hit. [single-pass] —
[Kata target condition](https://www.ineak.com/establishing-a-target-condition/),
[Kata overview](https://reverscore.com/toyota-kata/). Value-stream mapping is
the same pattern applied to a whole process: a **current-state map** (actual
material/info flow today) and a **future-state map** (target flow), with the
future-state map explicitly serving as "a roadmap of improvements" and "a
blueprint for improvement." [single-pass] —
[VSM fundamentals, Lean Enterprise Institute](https://www.lean.org/the-lean-post/articles/understanding-the-fundamentals-of-value-stream-mapping/),
[current vs future state maps](https://blog.i-nexus.com/current-v-future-state-maps-the-what-why-how-when).
**Answer to the framing question: yes, this is literally the proposal's
current-vs-desired delta**, decades prior, at the practice layer rather than
the data layer — A3/Kata/VSM never formalize the target condition as a typed
document with a schema, and the "gap" is a human-drawn diff, not a computed
one. This is kitsoki's opening: the same current/target/gap/countermeasure
shape, but the gap is machine-computed from typed nodes instead of
hand-drawn. A literate reviewer steeped in Lean will expect the proposal to
name this lineage explicitly (the vocabulary "current state," "target/desired
state," "countermeasure ≈ change node" is not coincidental) and to state why
kitsoki's version is more than a re-skin (schema pinning + computed delta +
recorded provenance vs. whiteboard/paper artifacts).

**(c) ReqIF.** The OMG/ISO 25087 exchange format's core model: **SpecObjects**
are the requirement/artifact nodes ("arguably the most important element,"
carrying attributes like text and a human-readable ID); **SpecRelations** are
first-class *typed, directed* edges connecting a source and target SpecObject
(a SpecRelation is itself a SpecElement, so it carries its own type and
attributes — richer than a bare foreign key); **SpecTypes**
(SpecObjectType / SpecRelationType) define the AttributeDefinitions each
typed element must carry — i.e., ReqIF *is* a typed object graph, with edge
typing as a first-class concept, not a convention layered on top.
[single-pass] — [ReqIF data model video](https://www.reqif.academy/video/the-reqif-structure/),
[RMF terminology](https://download.eclipse.org/rmf/documentation/rmf-latex/mainse10.html),
[OMG ReqIF 1.2 spec](https://www.omg.org/spec/ReqIF/1.2/PDF). *Adoptable bit:*
kitsoki's edge fields (`depends_on`, `goal_ref`, etc.) should be checked
against ReqIF's SpecRelationType pattern — a *typed* relation with its own
attributes (e.g., a `verifies` edge that itself carries a verification-method
attribute) — rather than a bare string reference, for interchange
credibility if ReqIF import/export is ever wanted. This directly informs
open question 4 (full GTS id grammar / interop later).

**(d) CMII / ECR-ECO-ECN; ITIL 4 change types.** CMII's closed-loop model:
**ECR** (Engineering Change Request — problem/alternatives/risk/business
impact) → if approved → **ECO** (Engineering Change Order — the formal
instruction: affected items, BOM lines, disposition, **effectivity**) →
**ECN** (Engineering Change Notice — the notification that closes the loop
to downstream teams that must act). Effectivity is a date/serial/lot from
which the change takes effect. [single-pass] —
[ECO software guide](https://www.sibe.io/cloud-pdm/engineering-change-order-software),
[CMII closed-loop process](http://www.datajett.com/windchill/CMII_Tut/CMII_Process_Tut.htm),
[Aras CMII whitepaper](https://aras.com/wp-content/uploads/2024/03/cmii-configuration-management-systems-aras-plm-software.pdf).
ITIL 4 change enablement categorizes changes by risk/repeatability: **standard**
(pre-approved, low-risk, automatable/runbook), **normal** (requires risk
assessment + authorization, minor/major by impact-urgency), **emergency**
(time-sensitive, expedited, mandatory post-implementation review).
[single-pass] — [ITIL change types](https://virima.com/blog/understanding-itil-types-of-changes-a-comprehensive-guide),
[change enablement](https://itsm.tools/change-enablement/). *Adoptable bits,
concrete:* (1) a three-state changeset lifecycle — proposed (ECR-like) →
authorized-with-effectivity (ECO-like) → notified/closed (ECN-like) — is a
better default than "authored → applied" alone, because it separates
*approval* from *effective-at* from *downstream-notified*, three distinct
audit-relevant timestamps the current two-state model in the proposal
(interpretive author, deterministic apply) doesn't distinguish; (2) an ITIL-
style changeset **risk class** (standard/normal/emergency) determining
whether a changeset needs a `decide` gate at all — a standard/pre-approved
changeset class could skip the interpretive step entirely, which the
proposal's "interpretive steps are named decide/task calls" principle should
explicitly accommodate rather than always requiring a gate.

**(e) SAFe work hierarchy.** Epic → (Capability, Large-Solution only) →
Feature → Story → Task/Subtask, each level sized to a specific planning
horizon (Epic: multi-PI investment-gated; Feature: fits one PI; Story: fits
one iteration). [single-pass] —
[SAFe hierarchy explained](https://www.enov8.com/blog/the-hierarchy-of-safe-scaled-agile-framework-explained/),
[Tempo: which SAFe hierarchy](https://www.tempo.io/blog/which-safe-hierarchy-should-you-choose/).
**This is a tree, not a graph**: each child has exactly one parent by
convention, and tooling built on it (Jira epic-links) is criticized for
constraining visualization to that single parent/rollup path — practitioners
report the epic-relinking pattern "limits Jira's hierarchical visualization"
and prevents seeing cross-cutting roll-ups. [single-pass] —
[Jira Advanced Roadmaps hierarchy discussion](https://community.atlassian.com/forums/New-to-Jira-discussions/Advanced-Roadmaps-Hierarchy-configuration-of-new-issue-type/td-p/2570443).
*Adoptable bit, negative:* SAFe's hierarchy is the thing the proposal's
`goal / initiative / wall / epic / change / task / decision / gate` work
taxonomy must NOT collapse into — the proposal already models `depends_on` as
a DAG (not a tree) plus a separate `goal_ref` traceability edge, which is the
correct generalization of SAFe's single-parent limitation. Cite SAFe by name
as the familiar-but-insufficient baseline reviewers will compare against.

## 4. Comparison matrix + "why not just use X"

| Category | Data model | Traceability first-class? | Change/audit model | Roadmap: computed or authored? | Honest strength (don't compete here) | One-line rebuttal |
|---|---|---|---|---|---|---|
| **Project trackers** (Jira/Linear) | Issue tree, single-parent epic links | No — links are ad hoc, not typed edges with semantics | Activity log, no changeset/delta document | Authored (backlog + hand-set priority) | Team-day-to-day ticket ergonomics, huge ecosystem | Kitsoki isn't a Jira replacement (explicit non-goal); it's the typed substrate a tracker could read from |
| **Product/portfolio (SAFe tooling, Advanced Roadmaps)** | Tree hierarchy (Epic→Feature→Story) | Partial — rollup only along the tree | None beyond issue state transitions | Authored, manually rebalanced | Investment-horizon planning ceremony or scaled orgs | Kitsoki generalizes the tree to a DAG + separates the goal-trace edge from the work-decomposition edge |
| **Requirements-management suites — Jama Connect** | Item + directional link model | Yes — links have explicit upstream/downstream direction [3-0] | **Suspect-link flagging**: upstream change marks downstream relationships "suspect" automatically [3-0]; coverage-gap analysis via Filters/Trace View, but scoped to trace coverage [3-0] | Neither — computes coverage gaps, not a general roadmap delta | Mature, audited change-impact propagation UX | Kitsoki should match suspect-link propagation as a graph-diff side effect of any changeset, not invent a separate mechanism |
| **Requirements suites — Codebeamer (PTC)** | Container-based: item lives in exactly one tracker [3-0], not a free graph node | Yes — typed References (cardinality-constrained, cross-project) + untyped Associations [3-0]; relations carry semantic roles e.g. "Verifies" [3-0] | Positions relations as "critical for managing, auditing and tracing back changes," incl. requirement→task→code-change chains [3-0] | Not addressed in verified claims | Enterprise ALM audit trail, cross-project reference fields with cardinality | Kitsoki's node is graph-native (not container-scoped); Codebeamer's per-tracker containment is exactly the constraint a flat typed graph removes |
| **Requirements suites — IBM DOORS(NG)** | Baseline + link-type model | Yes (link types), but no verified claims this pass — **no verified claims; needs research** | Baselining for audit snapshots (unverified this pass) | **no verified claims; needs research** | Long incumbency in aerospace/defense-regulated traceability | — |
| **Docs-as-code requirements — StrictDoc** | Single `.sdoc` file per document (whole-document granularity), explicit **TextX grammar** for schema pinning [3-0] | Traceability present but not itself surveyed this pass | **FAQ explicitly has no** change-management, delta/audit-trail, computed-roadmap, or decision-provenance treatment [3-0] (caveat: StrictDoc does ship a separate diff feature not covered by this claim) | Not addressed | Explicit machine-checkable grammar, git-native | Confirms the gap kitsoki targets: schema-pinned docs exist, delta/roadmap/provenance do not, in the same tool |
| **Docs-as-code requirements — Doorstop** | One YAML file per requirement, tree-hierarchy of documents (parent-child) [3-0] | Yes — CLI-linked items (`doorstop link`), tree traceability [3-0] | Deterministic tree-integrity validation; **no changeset/delta ops, no decision-point recording, no roadmap-as-delta** [3-0] | Not addressed / not present | Closest existing precedent to "schema-pinned YAML in git," minimal and auditable | Doorstop proves the storage-granularity choice (one-file-per-node) works at small scale; kitsoki adds the typed-edge graph + delta layer Doorstop lacks |
| **Spec-as-code — OpenSpec** | `specs/` (current) + `changes/` (delta) folders, hand-authored delta specs [3-0] | Requirement/Scenario structured text, not a general typed graph | Deterministic archive-on-apply is the audit record [3-0] | **Authored delta**, not diffed from two subgraphs — the proposal's "roadmap computed from a graph diff" is a strict generalization | Simple, adopted spec-driven-dev workflow already familiar to LLM-agent users | Kitsoki keeps OpenSpec's document-delta ergonomics but computes the delta instead of hand-authoring it |
| **Infra-as-code / GitOps (Flux)** | Declarative desired-state YAML in git | N/A (infra objects, not project elements) | **Reconciliation**: continuous desired-vs-actual diff, auto-applied [3-0] | **Computed** — but scoped to cluster/infrastructure objects only, not features/requirements/decisions [3-0] | Proven desired-state-in-git pattern at massive operational scale | Confirms the reconciliation/delta pattern is provable at scale; kitsoki extends it from infra objects to arbitrary project elements |
| **eQMS (Drata, Vanta)** | Shared control nodes, many-to-many framework mapping | Yes — control↔requirement, control↔evidence edges [3-0] | Continuous desired-state (control requirement) vs observed-state (evidence) drift, immediate alerts [3-0]; Vanta computes SOA document from the graph [3-0] | **Computed** drift, but scoped to compliance controls, not general project work | Turnkey audit-evidence automation, 300+ integrations, live market validation of the drift-delta pattern | Best market proof the drift-delta pattern sells; kitsoki generalizes it past compliance and adds *why* a control/decision was scoped as it was |
| **PLM (Windchill/Teamcenter, CMII-certified)** | Item + BOM + change-object model | Yes, incl. cross-lifecycle | **ECR→ECO→ECN** closed loop with effectivity dates [single-pass]; "CMII-certified closed-loop change management with... traceability across lifecycle" | Authored (engineering judgment on impact) | Decades of regulated hardware-change rigor, effectivity-date semantics | Kitsoki should adopt the ECR/ECO/ECN staged-lifecycle vocabulary for changesets, but PLM's known failure mode (ad-hoc hard-coded links, per MBSE preprint + Hedberg) is exactly what a typed edge schema avoids |
| **Graph-native niche (TerminusDB)** | Schema-typed JSON document graph, git-like history/branch/diff/merge [3-0] | Depends on authored schema | Append-only delta layers as commits [3-0]; "merge" is rebase-with-semantic-diff, not 3-way git parity | Neither prescribed — general-purpose substrate | Existence proof that a typed-doc-graph-with-delta-VCS is buildable and buyable | Kitsoki avoids TerminusDB's adoption/maintainer-continuity risk by using plain YAML-in-git instead of a bespoke DB |
| **Bitemporal DB (XTDB)** | Schema-typed facts, bitemporal (system-time + valid-time) | N/A directly — a query substrate, not a project ontology | As-at audit queries, retroactive correction, append-only system time [3-0] | N/A | Regulatory-grade bitemporal query semantics | Kitsoki's git-commit history is a weaker but zero-infrastructure analog; cite XTDB as the substrate-pattern precedent, not a dependency |

## 5. The two claims kitsoki can own

**(a) Roadmap computed as current-vs-desired graph delta, at general project
scope.** Nearest neighbors are both scoped narrower: Drata/Vanta compute
drift only over compliance *controls* [3-0 verified]; Jama computes
*coverage gaps*, not a general work-graph delta [3-0 verified]; Flux/GitOps
reconciliation computes desired-vs-actual only for *infrastructure* objects
[3-0 verified]. No surveyed tool computes a roadmap as the diff between an
arbitrary desired-state subgraph (features, requirements, scenarios) and an
arbitrary current-state subgraph (implementations, evidence) at general
project breadth. State this as "scoped analogs exist; general breadth does
not," not as an unprecedented idea.

**(b) Interpretive decision provenance as first-class graph data.** The
adversarial search explicitly tried and failed to find a system that records
*why* a decision was made (rationale, at a named point, as queryable graph
data) rather than just recording *that* something changed or *what* executed:
the MBSE preprint's decision-to-result-traceability claim was refuted 0-3
against kitsoki's thesis (it records execution provenance, not interpretive
provenance); log4brains/MADR records rationale but as free-form,
non-machine-validated markdown with no graph structure; PROJECTMEM logs
agent decisions as flat immutable events with no typed edges to other
project elements (refuted 0-3 that it models a graph at all). **State this
as "unoccupied per adversarial search," not "proven impossible"** — the
search is bounded by what surfaced in ~130 verified claims across two
research passes, not an exhaustive market scan.

## 6. Failure modes to avoid (documented only)

- **Ad-hoc, hard-coded inter-artifact links.** The named PLM anti-pattern:
  "relations are not captured in a meta-model, and exist only implicitly as
  whatever the code does" [3-0, hedged with counterexamples] — the exact
  gap a typed, schema-registered edge closes. Source:
  [arXiv:2512.09596](https://arxiv.org/pdf/2512.09596); corroborated by the
  digital-thread literature's point-to-point integration fragility
  [3-0] — [Hedberg et al.](https://pmc.ncbi.nlm.nih.gov/articles/PMC7437158/).
- **Hand-maintained indexes rot.** Doorstop and StrictDoc both prove the
  schema-pinned-YAML-in-git storage layer works, but neither ships delta
  ops, decision-point recording, or a computed roadmap [3-0 each] — a
  system that stops at "typed docs in git" without the delta/computed layer
  degrades to exactly this rot risk.
- **Delta vocabulary oversold as closed.** OpenSpec's ADDED/MODIFIED/REMOVED
  is not a documented closed set with a stated conflict-avoidance
  guarantee — refuted 0-3. Don't let the proposal's own changeset operation
  vocabulary imply closure/completeness it hasn't earned; treat it as
  extensible, matching Shared decision 2 (GTS derivation is open, not closed).
- **eQMS "documentation theater" boundary.** Even the most mature drift-delta
  commercial product (Vanta/Drata) leaves ~20% of evidence manual and never
  captures *why* a control was scoped in/out [3-0] — a graph that only
  records state and evidence, without the decision layer, reproduces this
  gap rather than closing it. This is the direct argument for Shared decision
  1's node envelope needing a decision-provenance field, not an optional add-on.
- **SAFe's single-parent tree rigidity** [single-pass]: practitioner reports
  describe Jira epic-linking as constraining visualization to one rollup path
  — [Jira Advanced Roadmaps thread](https://community.atlassian.com/forums/New-to-Jira-discussions/Advanced-Roadmaps-Hierarchy-configuration-of-new-issue-type/td-p/2570443).
  The proposal's `depends_on` DAG + separate `goal_ref` edge already avoids
  this; call it out explicitly as the SAFe-hierarchy failure mode being
  designed around.

## 7. Sources

| Source | Verification | Used for |
|---|---|---|
| [OpenSpec concepts.md](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md) | 3-0 verified (+ 1 refuted sub-claim) | §2.1, §4, §6 |
| [Backstage descriptor-format](https://backstage.io/docs/features/software-catalog/descriptor-format/) | 2-1/3-0/3-0 verified (+ 1 refuted) | §2.2 |
| [Drata compliance](https://drata.com/products/compliance) | 3-0 verified | §2.3, §4, §6 |
| [Vanta ISO 27001](https://www.vanta.com/products/iso-27001) | 3-0 verified | §2.3, §4, §6 |
| [XTDB intro](https://docs.xtdb.com/intro/what-is-xtdb.html) | 3-0 verified | §2.4, §4 |
| [Hedberg et al. 2020, PMC](https://pmc.ncbi.nlm.nih.gov/articles/PMC7437158/) | 3-0 verified | §2.5, §6 |
| [arXiv:2512.09596](https://arxiv.org/pdf/2512.09596) | 3-0 verified, single preprint (+ 1 refuted attribution) | §2.6, §5, §6 |
| [Jama relationships](https://help.jamasoftware.com/ah/en/manage-content/coverage-and-traceability/relationships.html) | 3-0 verified | §4 |
| [Codebeamer user guide](https://support.ptc.com/help/codebeamer/r2.1/en/codebeamer/user_guide/31276.html) | 3-0 verified | §4 |
| [StrictDoc FAQ](https://strictdoc.readthedocs.io/en/latest/latest/docs/strictdoc_03_faq.html) | 3-0 verified | §4, §6 |
| [Doorstop README](https://github.com/doorstop-dev/doorstop) | 3-0 verified | §4, §6 |
| [Flux concepts](https://fluxcd.io/flux/concepts/) | 3-0 verified | §4 |
| [log4brains README](https://github.com/thomvaill/log4brains) | 3-0 verified | §5 |
| [PROJECTMEM, arXiv:2606.12329](https://arxiv.org/html/2606.12329) | 2-1/3-0 verified (+ 1 refuted) | §5 |
| [ERP-software.org ECM glossary](https://erp-software.org/en/glossary/engineering-change-management/) | 2-0 verified (1 sub-claim unverified) | §3d, §4 |
| [Annex SL harmonized structure (ISO/TMBG)](https://www.parola.co.uk/ISO/Annex_SL_-_Harmonized_Structure_(HS)_-_Text.pdf) | single-pass | §3a |
| [Annex SL, Grokipedia](https://grokipedia.com/page/Annex_SL) | single-pass | §3a |
| [A3 problem-solving template](https://www.learnleansigma.com/guides/a3-problem-solving/) | single-pass | §3b |
| [A3 Report, ASQ](https://asq.org/quality-resources/a3-report) | single-pass | §3b |
| [Toyota Kata target condition](https://www.ineak.com/establishing-a-target-condition/) | single-pass | §3b |
| [Toyota Kata overview](https://reverscore.com/toyota-kata/) | single-pass | §3b |
| [VSM fundamentals, Lean Enterprise Institute](https://www.lean.org/the-lean-post/articles/understanding-the-fundamentals-of-value-stream-mapping/) | single-pass | §3b |
| [Current vs future state maps](https://blog.i-nexus.com/current-v-future-state-maps-the-what-why-how-when) | single-pass | §3b |
| [ReqIF data model video](https://www.reqif.academy/video/the-reqif-structure/) | single-pass | §3c |
| [RMF/ProR terminology](https://download.eclipse.org/rmf/documentation/rmf-latex/mainse10.html) | single-pass | §3c |
| [OMG ReqIF 1.2 spec](https://www.omg.org/spec/ReqIF/1.2/PDF) | single-pass | §3c |
| [CMII closed-loop (Datajett)](http://www.datajett.com/windchill/CMII_Tut/CMII_Process_Tut.htm) | single-pass | §3d |
| [ECO software guide](https://www.sibe.io/cloud-pdm/engineering-change-order-software) | single-pass | §3d |
| [Aras CMII whitepaper](https://aras.com/wp-content/uploads/2024/03/cmii-configuration-management-systems-aras-plm-software.pdf) | single-pass | §3d |
| [ITIL 4 change types (Virima)](https://virima.com/blog/understanding-itil-types-of-changes-a-comprehensive-guide) | single-pass | §3d |
| [ITIL change enablement (itsm.tools)](https://itsm.tools/change-enablement/) | single-pass | §3d |
| [SAFe hierarchy explained (Enov8)](https://www.enov8.com/blog/the-hierarchy-of-safe-scaled-agile-framework-explained/) | single-pass | §3e |
| [Which SAFe hierarchy (Tempo)](https://www.tempo.io/blog/which-safe-hierarchy-should-you-choose/) | single-pass | §3e |
| [Jira Advanced Roadmaps hierarchy thread](https://community.atlassian.com/forums/New-to-Jira-discussions/Advanced-Roadmaps-Hierarchy-configuration-of-new-issue-type/td-p/2570443) | single-pass | §3e, §6 |

**Not independently verified / needs research:** IBM DOORS(NG) link-type and
baselining model (no verified claims survived the tool-comparison pass — see
§4 row, marked "no verified claims; needs research"). Six Sigma DMAIC/CTQ/SIPOC
and ArchiMate/TOGAF/BPMN were in the original pass-1 research scope but
produced no surviving verified claims either (noted in the pass-1 caveats)
and are out of scope for this document's gap-fill (not in the requested (a)–(e)
list).
