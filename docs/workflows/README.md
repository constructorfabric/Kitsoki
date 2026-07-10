# Dev workflows — how a user does this, per surface

Five core developer workflows, each described **truthfully for today** —
what it is, how you drive it from each surface (TUI, web, VS Code,
gh-agent) where the steps actually differ, and where a surface simply
doesn't support it yet.

| Workflow | Doc |
|---|---|
| Onboard a project | [`../getting-started.md`](../getting-started.md) |
| Write a PRD, then a design/proposal | [`prd-and-design.md`](prd-and-design.md) |
| Decompose an epic → implement the briefs | [`decompose-and-implement.md`](decompose-and-implement.md) |
| File a bug | [`file-a-bug.md`](file-a-bug.md) |
| Fix a bug | [`fix-a-bug.md`](fix-a-bug.md) |

These are the **canonical, gated** references (WS-G G3 of
[`.context/dev-workflows-surface-matrix-plan.md`](../../.context/dev-workflows-surface-matrix-plan.md)):
the eventual docs-fidelity check drives a persona through a workflow using
**only** the doc below, no repo spelunking — so a stale claim here is a
seeded gate failure, not a hygiene nit. Each doc:

- describes the workflow once, in prose, then links to the story/README
  that is the authoritative implementation rather than repeating its
  contract;
- calls out *only* the places a surface's steps genuinely diverge (most
  workflows are "one engine, four projections" — see
  [`../stories/architecture.md`](../stories/architecture.md));
- says plainly, per surface, when a surface doesn't support the workflow
  yet, instead of describing an aspirational happy path.

Current mechanical/experience proof status per {workflow, surface, repo}
cell lives in the generated
[`../testing/dev-workflow-matrix.md`](../testing/dev-workflow-matrix.md)
(source: [`../../tools/dev-workflow-matrix/manifest.yaml`](../../tools/dev-workflow-matrix/manifest.yaml)) —
consult it for the standing verdict, not this index.

See also [`../getting-started.md`](../getting-started.md) for the
prerequisite (a repo must be onboarded before any of the above apply) and
its own ["after onboarding"](../getting-started.md#7-use-kitsoki-after-onboarding)
section, which points here.
