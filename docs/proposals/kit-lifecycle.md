# Runtime: Kit lifecycle — staged updates with acceptance trials (S7)

**Status:** In progress. Staging substrate + `kit update`/`kit reject` landed; fixture `acceptance:` block landed; `kit trial`/`kit accept`, validation ledger, and MCP surface in flight.
**Kind:**   runtime
**Epic:**   ./kits.md (S7 — lifecycle)

## Why

A project that imports a kit it does not own (`gears-rust` importing
`@kitsoki/dev-story`) has no safe path to a newer version. `kit update` was
an honest stub; the only mechanism was `kit add` overwrite with git as the
undo, and consumers pre-declared future hosts in hand-maintained comment
blocks to survive version skew (gears-rust `app.yaml`, commit `bdf7ebc5`
there). Meanwhile everything needed to *judge* an upgrade already exists —
`kit verify` conformance, no-LLM flow/cassette replay, `trace to-flow`,
task-case oracles — but nothing composes them into "trial the new version
without accepting it, without breaking my productivity, and without
re-spending LLM time on work I already validated."

## What changes

One sentence: **a locked kit gains a staged-candidate lifecycle —
`kit update` stages a semver-gated re-resolution beside the lock,
`kit trial` judges it with deterministic gates plus a spend-gated baseline
ledger, and `kit accept`/`kit reject` promote or drop it atomically.**

- `.kitsoki/kits.staged.lock` (internal/kitstage) holds candidates pinned
  by content (commit cache / new tree cache); the accepted `kits.lock` is
  never touched until accept. Reject is residue-free by construction.
- Resolution stays untouched by default; `--staged` / `KITSOKI_KIT_STAGED`
  (or per-call `staged:` over MCP) resolves selected kits to their
  candidates, ahead of `kit dev` and `$KITSOKI_REPO` overrides.
- `kit update` emits an upgrade plan: from/to snapshots, changed files
  (`kitgit.TreeDiff` over cached trees), and the candidate's
  `compat.renamed` hints annotated with the consumer references they touch
  (first consumer of the `compat:` block).
- Flow fixtures gain a session-level `acceptance:` block — a
  version-portable outcome contract (terminal-state family, key world
  vars, order-free host-call set, files) distinct from per-turn route
  assertions; `trace to-flow --acceptance` drafts it from a real trace.
- `kit trial` runs gates against the staged resolution: contract/
  conformance (`kitverify` with the lockfile-backed extends resolver),
  frozen cross-version replay (dual legs: locked tree vs staged tree, with
  a route-drift report — the acceptance verdict decides, the diff
  explains), the idempotent onboarding/validation sweep (no writes outside
  `.artifacts/`, digest-checked), and ledger-gated baseline tasks.
- The validation ledger (`.kitsoki/qa/validation-ledger.yaml`, tracked)
  records (case, kit tree hash, instance digest, oracle hash) → result, so
  an already-validated real task is SKIPPED, not re-run; live validations
  are `taskcase.CostPolicy`-gated and operator-approved.
- Failures become a migration worklist with stable item ids, operator
  status carry-forward, and concrete suggested edits; receipts
  (`kitsoki/kit-upgrade-receipt/v1`, digest-pinned) gate `kit accept`
  fail-closed.

## Impact

- **Code seams:** `internal/kitstage` (new), `internal/kitworklist` (new),
  `internal/kittrial` (new), `internal/kitgit` (tree cache + TreeDiff),
  `internal/kitlock` (`constraint:`), `internal/testrunner`
  (`acceptance:` block, `PlayedEpisodes`), `cmd/kitsoki/kit_update.go` /
  `kit_trial.go`, `cmd/kitsoki/resolver.go` (staged hook),
  `internal/mcp/studio/kit_tools.go`.
- **Vocabulary:** no new effects or host calls; new CLI verbs and fixture
  fields only (table below).
- **Stories affected:** none at load time; dev-story's `kit.yaml` will
  carry `compat.renamed` data when its next rev renames anything.
- **Backward compat:** staging is inert unless selected; lockfiles without
  `constraint:` parse unchanged; fixtures without `acceptance:` behave
  exactly as before.
- **Docs on ship:** `docs/testing/story-upgrade.md`, `docs/stories/kits.md`
  lifecycle section.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| CLI | `kit update <name>` | `--to --source --check-only --json` | stages a candidate + plan; never mutates kits.lock |
| CLI | `kit trial <name>` | `--json --fail-fast` (`--live` later) | judges the candidate; writes receipt + worklist |
| CLI | `kit accept / kit reject <name>` | — | promote atomically / drop residue-free |
| flag | `--staged` / `KITSOKI_KIT_STAGED` | `all` \| names | staged resolution opt-in; named-but-unstaged fails loudly |
| fixture | `acceptance:` | `final_state_in / world / host_calls / files` | session-level, version-portable outcome contract |
| file | `.kitsoki/kits.staged.lock` | `kitstage.File` | candidate pins + from-snapshots |
| file | `.kitsoki/kit-update/<name>/plan.yaml` | `kitsoki/kit-update-plan/v1` | changed files + rename hints |
| file | `.kitsoki/kit-update/<name>/worklist.yaml` | `kitsoki/kit-migration-worklist/v1` | stable-id items, operator carry-forward |
| file | `.kitsoki/qa/validation-ledger.yaml` | `kitsoki/validation-ledger/v1` | baseline-task skip index |
| file | receipts | `kitsoki/kit-upgrade-receipt/v1` | trial → `.artifacts/kit-trial/…`; accept → `.kitsoki/receipts/kit-update/` |

## The model

Deterministic vs interpretive stays sharp: every gate that can run without
an LLM does (contract checks, cassette replays under
`KITSOKI_CASSETTE_STRICT=1`, digest sweeps), and the report *measures* that
— per-gate cost sums only agent calls whose transport is not `cassette`,
so "zero spend" is an observed fact, not a claim. The only interpretive
step is a live baseline validation, which is explicitly operator-approved,
recorded to the ledger, and auto-minted (`trace to-flow --acceptance`)
into next upgrade's deterministic evidence. Route drift between versions
is never silently absorbed: the dual-leg diff (states visited, host-call
multiset, cassette episode economics) is attached to the acceptance
verdict so an operator sees *why* a contract held or broke.

## Open questions

- Journey-freeze artifacts as a G2 fixture source depend on the `qa`
  machinery (productization capsule) landing; until then G2 draws from
  consumer flows, the profile's onboarding `deterministic_flow`, and
  promoted baselines.
- Readiness command re-runs (project build/test) inside trial are
  deliberately opt-in — cheap for small repos, minutes for large ones.
- Mechanical `compat.renamed` application stays out (worklist carries the
  exact edits; "no self-migration" per the kits epic).
- Git-tier import sources bypass the ImportResolver, so `--staged` covers
  `@kitsoki/<name>` imports; git-sourced kits trial through the kit-level
  gates until a deeper seam is justified.
