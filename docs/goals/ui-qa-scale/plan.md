# Brief — ui-qa-scale: one persona-QA/arena system + swarm UI testing

This is the working brief behind `GOAL.md` / `decomposition.yaml`. Full
code-survey evidence (file:line for every claim): `.context/persona-qa-arena-ui-scale-review.md`
(2026-07-04; transient — the durable claims are restated here).

## Why this goal exists

Two findings from the 2026-07-04 review:

1. **Product-journey and arena share concepts but zero code.** Product-journey
   (`tools/product-journey/run.py`, ~7300 lines) is the persona/scenario brain —
   matrix planning, persona lenses, the 19-check review gate, evidence contract,
   rollup + decks — and is **fully serial** (no concurrency primitives at all).
   Arena (`tools/arena`, landed as #57 + #61) is the body — containers,
   placement (local|VM), ThreadPoolExecutor concurrency, infra-vs-model retry —
   but registers only a `bugfix` plugin, scores by stdout regex, keeps its own
   thin rollup and its own verdict vocabulary, never reads product-journey's
   corpora, and its cell image has no browser. The planned convergence
   (proposal P2/P3) never got built. Every month they stay separate the two
   rollup brains and two verdict vocabularies drift further.
2. **The web server is swarm-ready but no swarm exists.** `kitsoki web` is
   genuinely multi-session — one server, N isolated sessions, each its own
   orchestrator; concurrency-safe registry (`cmd/kitsoki/registry.go`) — yet
   nothing anywhere puts N concurrent clients on one server. Sessions also leak
   (no cap/eviction). Meanwhile all the QA ingredients already exist as parts:
   Playwright drive layer (`_helpers/server.ts`), deterministic DOM-geometry +
   axe probe (`lib/ui-audit.ts`), rrweb/console/scrubbed-HAR capture (bug-report
   path), no-LLM postures (`--flow`, `--host-cassette`, `--harness replay`).

The goal fuses both: arena becomes the single front door for persona-QA sweeps,
and a swarm harness simulates dozens of users actively using the web UI — for
regression (soak, isolation, audit gates) and discovery (explorer agents whose
findings arrive reproduction-ready).

## Standing decisions

- **The swarm inverts the arena cell.** Arena cells isolate (one server per
  cell); the swarm shares (one server, many users). Resolution: the *swarm* is
  the cell — one cell = server + N drivers; users are internal. Scored in
  aggregate through the same completion-state contract.
- **Cost model is tiered.** Tier 1 replay users (scripted, `--flow`, free) are
  the bulk; tier 2 cassette-agent users (`--harness replay --recording`, free)
  add free-text realism; tier 3 live explorers are ≤3, off by default, budget-
  capped. CI never spends tokens (G5).
- **Build on the plan branch, don't redo it.** `plan/persona-qa-arena-ui-scale`
  (worktree `.worktrees/persona-qa-arena-ui-scale`) already carries a first cut:
  `tools/persona_qa/completion.py` (review→completion-state bridge),
  `tools/arena/arena/plugins/persona_qa.py`, tests, a persona-qa spec, and
  product-journey hooks. unify-contract/arena-persona-qa start by cherry-picking that branch onto
  `stabilize/ui-qa-scale` and hardening.
- **Reuse the evidence machinery.** Finding capture (swarm-capture) goes through the
  existing bug-report seams (rrweb + console + `harrec`/`harscrub`), not a new
  format. The visual MCP is NOT used by the swarm (it cold-boots a server +
  Chromium per snapshot); swarm drivers hold persistent Playwright contexts.
- **TUI is out of scope** (parked in GOAL.md): it shares the orchestrator, so
  swarm coverage transfers; a frame-assert tier can follow as its own change
  later. xterm.js exists but today's harness is replay/film, not a live driver.

## Wall map and ordering

```
WU (unify)          unify-contract contract ──► unify-rollup rollup
                    unify-corpora corpora ─┐
WA (arena persona)  unify-contract + unify-corpora ──► arena-persona-qa persona-qa plugin      arena-browser-image browser image
WS (swarm)          swarm-tier1 tier-1 swarm ──► swarm-capture capture ──► swarm-tiers23 tiers 2-3
                    swarm-session-cap session cap (independent)
                    arena-persona-qa + arena-browser-image + swarm-tier1 + unify-contract ──► swarm-arena-job swarm-as-arena-job (FLAGSHIP demo)
```

Ready at cycle 1 (scope-disjoint, dep-free): **unify-contract, unify-corpora, arena-browser-image, swarm-tier1, swarm-session-cap**.
Highest ROI first: unify-contract (lands existing branch work, stops the drift).

## Gates philosophy

Every change's standing gate is deterministic and no-LLM (G-SMOKE python tests,
G-GOTEST, G-LINT, or a Playwright spec); docker builds and live-model runs are
manual acceptance, never the CI check (G-LIVE+REPLAY stands on `replay_cmd`).
Two gates are deliberately anti-vacuous: swarm-session-cap greps for the named Go test before
`go test -run` (no silent empty-match pass), and swarm-tier1 must include a seeded
cross-talk fault proving the isolation assertion can fire.

## Interim /goal mechanics

The goal skill (`goal.py`) currently lives only on the `generalized-usage`
branch (`.worktrees/generalized-usage/.agents/skills/goal/`); the main
checkout's `.claude/skills/goal` symlink dangles. Until WM.0 (goal-seeker story)
lands there, drive this goal with:

```
python3 .worktrees/generalized-usage/.agents/skills/goal/goal.py \
  ledger|preamble|ready --goal-dir .worktrees/ui-qa-scale/docs/goals/ui-qa-scale
```

run from the primary checkout root (the change-node schema resolves from
`schemas/change-node.schema.json` at cwd). Worker worktrees:
`git worktree add -b stabilize/uqs-<id> .worktrees/uqs-<id> stabilize/ui-qa-scale`.
