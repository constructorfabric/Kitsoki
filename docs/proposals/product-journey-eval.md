# Epic: Product journey end-to-end eval

**Status:** v1 trimmed. Runner, catalog, evidence artifacts, and local product-site guidance are implemented; PostgreSQL and Kubernetes remain planned perspective lanes.
**Kind:**   epic
**Slices:** 4 (3/4 shipped)

## Why

We need to prove Kitsoki is useful *before* a maintainer starts integrating it by running an end-to-end, reproducible, skeptical journey across product website/docs/tutorials/onboarding and live bug-fix work on real open-source projects. The current setup has the necessary bug-fix grader and onboarding machinery, but no single orchestrated journey harness for that full flow.

## What changes

The end-state is: from one command, a deterministic journey controller can

- stage and serve a production build of the Kitsoki product site/docs locally,
- run a skeptical explorer flow on a real project (onboarding + design + fixes),
- collect deterministic outcomes (hidden-oracle grade, suite results) and deterministic usability signal,
- write evidence-backed findings for every bug discovered in web/TUI/docs/VS Code surfaces,
- and render a Slidey deck from run artifacts that is reviewable by humans.

The first implementable milestone is `gears-rust`, with the following perspectives queued for follow-up checks: PostgreSQL and Kubernetes.

## Impact

- **Spans:** tooling/runtime (`tools/product-journey`), docs (`docs/proposals`, `docs/decks`), and testing infrastructure (reuse `tools/bugfix-bakeoff/external` fixtures; no engine changes).
- **Net surface:** one new run manifest + runner script, one proposal, one deck, and a structured run log in `.context`.
- **Docs on ship:** a new proposal + deck plus `tools/product-journey/README.md`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Journey runner + catalog | tooling | Script + catalog to run a project journey in one entrypoint (`tools/product-journey/run.py`) | — | Done | [`product-journey-run.md`](../tools/product-journey/run.py) |
| 2 | Harness bridge to existing grader | runtime | Reuse `external.bench.py` + `gears-rust` external manifest as the first production project slice for deterministic bug scoring | 1 | Done | `tools/bugfix-bakeoff/external/` |
| 3 | Evidence deck + run log | docs | Commit a progress deck and per-run log schema that is safe to regenerate and diff | 1 | Done | `docs/decks/product-journey-eval.slidey.json`, `.context/product-journey-runlog.md` |
| 4 | Perspective expansion | runtime | Add PostgreSQL + Kubernetes perspective checks in catalog and runbook output after gears-rust first milestone | 1 | Partially shipped | `tools/product-journey/catalog.json` |

## Sequencing

```
#1 (runner) ──▶ #2 (bugfix bridge) ──▶ #3 (evidence artifacts)
                     └─────────▶ #4 (perspective expansion)
```

## Shared decisions

1. **Keep evaluation deterministic for scoring and no-LLM by default.** All automated checks in this epic reuse existing no-LLM infrastructure (session fixtures, benchmark oracle scoring, deterministic cassettes where available). Live LLM runs stay explicit and opt-in.
2. **Use the existing external benchmark manifest as the shared bug corpus contract.** Adding new project families should not change the runner shape; add project entries in a new `tools/product-journey/catalog.json` and reuse the existing `bench.py` contract.

## Cross-cutting open questions

1. **Perspective granularity.** *Lean:* each perspective initially tracks project-level readiness (`planned`, `runner-ready`, `stalled`) before adding separate metrics.

## Non-goals

- Building a full replacement for existing bugfix-tape scripts.
- Introducing a new hidden-oracle format. Existing `tools/bugfix-bakeoff/external` schemas remain the grading contract.
- Replacing project onboarding itself; this epic orchestrates around existing onboarding and bug-fix stories.

## Implementation tasks

- [x] Add `tools/product-journey` runner/collation scaffolding.
- [x] Add `tools/product-journey/README.md` with start command and expected outputs.
- [x] Add initial catalog row for `gears-rust` with run mode wired to existing external bake-off fixtures.
- [x] Add PostgreSQL and Kubernetes perspective rows with status + planned checks.
- [x] Add `.context/product-journey-runlog.md` and append run entries when checks execute.
- [x] Add `docs/decks/product-journey-eval.slidey.json` and keep it sync-able from run logs.
- [x] Document the local production build requirement for the Kitsoki product site and QA runner.
