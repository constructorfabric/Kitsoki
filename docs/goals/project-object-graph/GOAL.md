# GOAL — The project object graph is real, dogfooded, and drives the product site

**Status:** Active target for the `proposal/project-object-graph` line of work.
Read from this location by the driving agent (and by `/goal`-style loops).
Provenance: [`docs/proposals/project-object-graph.md`](../../proposals/project-object-graph.md)
(the epic), [`docs/competitive-analysis/project-object-graph-research.md`](../../competitive-analysis/project-object-graph-research.md)
(verified anchors), and the branch's review fixtures
(`docs/proposals/project-object-graph/seed-objects.yaml` + `viewer/`).

**One line:** Kitsoki's own features, requirements, scenarios, evidence, and
work-in-flight live as one typed, lintable YAML graph; changesets are the only
way durable nodes change; the roadmap is computed (never authored) as the
current-vs-desired delta; the graph renders in `kitsoki web` and generates the
product site's capability catalog — and the process of building all of this is
itself driven through kitsoki-dev sessions.

## Done-when (criteria — each falsifiable)

- **G1 — Substrate loads and lints.** A Go loader + type registry (envelope +
  `derives_from` derivation, per the epic's Shared decisions 1–2) loads the
  seed catalog; a deterministic catalog lint hard-fails on cycles, dangling
  refs, schema violations, and internal-node-reachable-from-public-edge.
  *Gate: `go test ./internal/graph/...` over a good/bad fixture corpus,
  seed-objects.yaml as the corpus regression.*
- **G2 — Changesets are the change mechanism.** A changeset node type +
  deterministic `graph diff` / `graph apply` (yaml.Node rewrite + full
  re-lint; exit code is the gate); applying a shipped changeset flips durable
  node status and preserves provenance. *Gate: RED→GREEN fixture tests for
  diff/apply/merge including a rejected (lint-failing) changeset.*
- **G3 — The product site is generated from the graph.** The **entire**
  `features/*.yaml` catalog is converted to graph nodes with
  `visibility: public | internal`, and the promo site / tours / help docs are
  generated from the graph (adapter first to keep `make features-check` and
  the site pipeline green, collapse the adapter once conversion is complete —
  epic Open question 3). This conversion is the payoff, not an optional demo.
  *Gate: `make features-check` + site build green with every feature sourced
  from graph nodes, and an internal node provably filtered from public
  output.*
- **G4 — Roadmap is computed.** `graph diff` between the desired-state and
  current-state subgraphs emits a work graph conforming to
  `schemas/change-node.schema.json` (so goal.py-style ledgers,
  `stories/deliver`, and `stories/fleet` can consume it unchanged). *Gate: a
  fixture pair (current, desired) whose computed delta equals a golden
  work-graph file.*
- **G5 — The graph is visible.** One graph viewer in `kitsoki web`
  (tools/runstatus) — typed-layer filtering, node detail, edge navigation.
  The two prototypes are already consolidated into one Vue app at
  `docs/proposals/project-object-graph/viewer/` (catalog view + Vue Flow/ELK
  graph canvas over the seed catalog, shared selection); remaining work is
  porting that surface into tools/runstatus fed by the G1 loader.
  *Gate: a deterministic no-LLM Playwright spec over the seeded catalog.*
- **G6 — Dogfooded throughout (cross-cutting).** Design/decomposition steps
  for this goal run as kitsoki-dev story sessions through the studio MCP with
  traces as evidence; every story-shaped addition ships no-LLM flow fixtures;
  live LLM only at named decision points, never in tests. *Gate: session
  traces referenced from the progress ledger + `kitsoki test flows` green.*
- **G7 — The proposals queue lives in the graph.** The `docs/proposals/`
  queue (epics, slices, Status lines) is converted to proposal/changeset
  nodes with markdown prose carried in fields or via `!include`; the
  `docs/proposals/README.md` "Current proposals" index is **generated** from
  the graph (the machine-checkable-docs principle — hand-maintained indexes
  rot); the epic's own slice table is the first generated view. New proposals
  are authored as nodes; conversion of the backlog proceeds
  wrapper-first so no proposal blocks on rewriting. *Gate: generated
  README index matches the catalog exactly (lint), and this goal's epic +
  children round-trip through the graph.*

## Non-goals (parked — must not gate this goal)

ISO type packs (epic slice 6), full GTS dotted-id grammar, a general
project-management web UI, re-parenting `roadmap-portfolio-work.md` (Brad's
call, epic Open question 1), and any runtime datastore — files in git remain
the store. **Converting the product site catalog (G3) and the proposals
queue (G7) is explicitly IN scope — it is the payoff and the dogfood, not a
follow-on.**
