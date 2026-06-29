# Open-source repo test catalog

This is the landing page for reusable open-source projects we use to test
Kitsoki's developer workflows against real codebases. It tracks what exists,
what is only a candidate, which suites and harnesses are available, and which
results are already durable enough to cite.

The first implemented lane is the external bug-fix bake-off:
[`tools/bugfix-bakeoff/external/`](../../tools/bugfix-bakeoff/external/). That
harness onboards a repo, prepares parent-commit bug baselines, hides the real
PR's regression test as an oracle, drives a Kitsoki workflow, then grades the
candidate tree deterministically with no LLM.

For the operator recipe that turns a new repo's history into this manifest,
readiness, and live-cell flow, see
[`../recipes/repo-history-training-new-repo.md`](../recipes/repo-history-training-new-repo.md).

## Status legend

| Status | Meaning |
|---|---|
| Candidate | Good target, but fixtures have not been mined or verified. |
| Selected | Chosen for fixture mining; no committed manifest yet. |
| Manifested | `tools/bugfix-bakeoff/external/projects/<id>/manifest.yaml` exists. |
| Armed | Every committed oracle proves RED at baseline and GREEN at the real fix. |
| Driven | At least one live model/workflow cell has been run. |
| Reported | Results have a durable case study, report, or deck. |

## Constructor Fabric inventory

These projects are still important test targets, but they are Constructor Fabric
or local-self references rather than reusable public OSS claims. Track them here
so we can use them for workflow coverage without mixing them into the public
candidate list.

| Project | Repo type | Suites / harnesses | Implemented | Results |
|---|---|---|---|---|
| `studio` | Constructor Fabric product surface | Studio MCP, web/TUI flows, story QA, no-LLM flow fixtures, Playwright where UI evidence is required | Candidate tracking row; no external-project manifest yet | Use for dev-story and Studio-MCP workflow coverage. Needs a concrete fixture catalog before it can report reusable results. |
| `slidey` | Constructor Fabric OSS-adjacent tool repo | Node/Vue test suite, VS Code extension tests, Slidey conversion/fidelity harnesses, MCP integration checks | Candidate tracking row; not yet represented as an external bake-off project | Useful for frontend/tooling workflows and MCP setup issues. Needs selected bug fixtures and deterministic oracles. |
| `gears-rust` | Local/private Rust monorepo reference | Per-bug Cargo oracles; `GEARS_RUST_REPO=... make gears-bakeoff`; `GEARS_RUST_REPO=... make gears-history-full-smoke`; `suite: false` because the full workspace is heavy | Manifested with 4 armable bugs and additional reference-only cases | All four armable fixtures are RED->GREEN and pass the repo-history full-smoke path. The full-matrix `repo-bakeoff` flow renders the live commands and stops before cost-bearing model work. Not an OSS reuse candidate. |
| `kitsoki` | This repo, local self-benchmark | Go/TypeScript project manifest folded into the external harness; local-only mirror grading | Manifested with 3 armed fixtures | Self-benchmark support exists. Use for regression of the harness itself, not for customer-facing OSS claims. |

## Top 10 reusable OSS candidates

These are the default public open-source targets to mine next. Constructor Fabric
projects are tracked separately above. A candidate graduates only after we can
name real filed bugs, the fixing commits, isolated regression tests, the baseline
commit, and a deterministic command that proves RED at baseline and GREEN at the
real fix.

| Rank | Project | Repo URL | Product site URL | Stack / suite | Status | Implemented / results | Next action |
|---:|---|---|---|---|---|---|---|
| 1 | `postgresql/postgresql` | `https://github.com/postgres/postgres` | n/a | C, PostgreSQL regression-style local oracle | Validated local oracle | Tracked in [`tools/product-journey/catalog.json`](../../tools/product-journey/catalog.json) with `POSTGRESQL_REPO` and `bash tools/product-journey/checks/postgresql-oracle.sh`. Perspective validated with a deterministic oracle built from the real `ALTER DOMAIN VALIDATE CONSTRAINT` fix. | Convert the bespoke local oracle into the shared external-project manifest schema. |
| 2 | `kubernetes/kubernetes` | `https://github.com/kubernetes/kubernetes` | n/a | Go monorepo, Kubernetes local oracle | Validated local oracle | Tracked in [`tools/product-journey/catalog.json`](../../tools/product-journey/catalog.json) with `KUBERNETES_REPO` and `bash tools/product-journey/checks/kubernetes-oracle.sh`. Perspective validated with a deterministic oracle built from the real nil-`Memory` guard fix. | Convert the bespoke local oracle into the shared external-project manifest schema. |
| 3 | `sindresorhus/query-string` | `https://github.com/sindresorhus/query-string` | n/a | JavaScript, AVA oracle plus full `npx ava` secondary suite | Reported | Manifested and armed with 3 bugs: `qs1`, `qs2`, `qs3`. GPT-5.5 / `codex-native` solved 3/3; GLM-5.2 pending because the provider was rate-limited. See [`docs/case-studies/query-string-bakeoff.md`](../case-studies/query-string-bakeoff.md). | Keep as the reference project and expand model cells when providers are available. |
| 4 | `studio-slidey` | `https://github.com/constructorfabric/studio-slidey` | `https://constructorfabric.github.io/studio-slidey/` | Node/Vue test suite, VS Code extension tests, Slidey conversion/fidelity harnesses, MCP integration checks | Candidate tracking row | Not yet represented as an external bake-off project. | Use for frontend/tooling workflows and MCP setup issues. Needs selected bug fixtures and deterministic oracles. |
| 5 | `pillarjs/path-to-regexp` | `https://github.com/pillarjs/path-to-regexp` | n/a | JavaScript/TypeScript, Jest or project test runner | Candidate | Not implemented. | Mine 3 PRs that added parser/matcher regression tests. |
| 6 | `ljharb/qs` | `https://github.com/ljharb/qs` | n/a | JavaScript, npm test suite | Candidate | Not implemented. | Find encoding, nesting, or array-format bugs with added tests. |
| 7 | `sindresorhus/globby` | `https://github.com/sindresorhus/globby` | n/a | JavaScript, AVA | Candidate | Not implemented. | Mine ignore, cwd, gitignore, and Windows-path fixes. |
| 8 | `mrmlnc/fast-glob` | `https://github.com/mrmlnc/fast-glob` | n/a | TypeScript, npm test suite | Candidate | Not implemented. | Identify small regression-test PRs that do not require slow fixture setup. |
| 9 | `tokio-rs/tokio` | `https://github.com/tokio-rs/tokio` | n/a | Rust, Cargo test suite | Candidate | Not implemented. | Mine small regression-test PRs in runtime, sync, time, or IO components that can run without host-specific services. |
| 10 | `serde-rs/serde` | `https://github.com/serde-rs/serde` | n/a | Rust, Cargo test suite | Candidate | Not implemented. | Mine focused derive, attribute, or format-interaction fixes with narrow tests. |
| 11 | `tokio-rs/axum` | `https://github.com/tokio-rs/axum` | n/a | Rust, Cargo test suite | Candidate | Not implemented. | Mine extractor, routing, response, middleware, or tower-integration fixes with deterministic tests. |

## Harness catalog

| Harness / command | Cost | What it proves | Current coverage |
|---|---:|---|---|
| `python3 tools/bugfix-bakeoff/external/bench.py verify --project <id>` | Free | Project fixtures are armed: oracle RED at baseline and GREEN at the real fix. | `query-string`, plus Constructor Fabric/local references `gears-rust` and `kitsoki`. |
| `make qs-bakeoff` | Free, gated | Query-string clone, dev-story onboarding, and fixture arming all work end to end. | Public reference path. |
| `GEARS_RUST_REPO=... make gears-bakeoff` | Free, gated, local heavyweight | Rust oracle path works against a local mirror without dirtying the checkout. | Private/local reference only. |
| `make history-smoke HISTORY_PROJECT=<id> ...` | Free | Project preflight, scoped RED/GREEN arming, live command rendering, no-drive cell prep, readiness report, and `repo-bakeoff` flow validation all pass. | Generic external-project product gate; `gears-history-full-smoke` prepares and verifies all four armable gears-rust fixtures. |
| `make history-pending-smoke HISTORY_PROJECT=<id> ...` | Free | Blocked-provider cells roll up as `pending` in Markdown + Slidey JSON without modifying normal live results. | Generic rehearsal path for pre-attempt provider/profile blockers. |
| `POSTGRESQL_REPO=... bash tools/product-journey/checks/postgresql-oracle.sh` | Free, local heavyweight | PostgreSQL local oracle proves the real RED/GREEN split for the selected product-journey fixture. | Validated prototype; not yet an external-project manifest. |
| `KUBERNETES_REPO=... bash tools/product-journey/checks/kubernetes-oracle.sh` | Free, local heavyweight | Kubernetes local oracle proves the real RED/GREEN split for the selected product-journey fixture. | Validated prototype; not yet an external-project manifest. |
| `tools/bugfix-bakeoff/external/drive_cell.sh --project <id> --bug <bug> --candidate <candidate> --score` | Cost-bearing | A live model drives the Kitsoki workflow, then the hidden oracle grades the resulting tree. | Used for `query-string` GPT-5.5 cells. |
| `tools/bugfix-bakeoff/external/escalate.sh --project <id> --ladder <name>` | Cost-bearing | Finds the cheapest candidate rung that solves each bug. | Implemented; needs more public projects/results. |
| `python3 tools/bugfix-bakeoff/external/bench.py summarize --project <id> ...` | Free | Regenerates deterministic report/deck artifacts from committed results. | Available for external bake-off results. |

## Results catalog

| Result set | Location | Durable claim | Gaps |
|---|---|---|---|
| Query-string GPT-5.5 run | [`tools/bugfix-bakeoff/external/results/`](../../tools/bugfix-bakeoff/external/results/) and [`docs/case-studies/query-string-bakeoff.md`](../case-studies/query-string-bakeoff.md) | GPT-5.5 solved 3/3 query-string bugs through the Kitsoki bugfix pipeline; each fix passed the hidden oracle and full AVA suite. | GLM-5.2 was provider-pending; add more model cells and a single-prompt control if we need a full matrix. |
| Query-string scaffold | `make qs-bakeoff` | Public OSS onboarding and oracle arming are deterministic and free. | Keep the passing date fresh when behavior changes. |
| PostgreSQL product-journey oracle | [`tools/product-journey/catalog.json`](../../tools/product-journey/catalog.json) and `tools/product-journey/checks/postgresql-oracle.sh` | Product-journey perspective is validated with a deterministic local oracle from the real `ALTER DOMAIN VALIDATE CONSTRAINT` fix. | Convert from bespoke local script to the external-project manifest schema. |
| Kubernetes product-journey oracle | [`tools/product-journey/catalog.json`](../../tools/product-journey/catalog.json) and `tools/product-journey/checks/kubernetes-oracle.sh` | Product-journey perspective is validated with a deterministic local oracle from the real nil-`Memory` guard fix. | Convert from bespoke local script to the external-project manifest schema. |
| Gears-rust reference | [`tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml`](../../tools/bugfix-bakeoff/external/projects/gears-rust/manifest.yaml) and [`docs/recipes/repo-history-training-gears-rust.md`](../recipes/repo-history-training-gears-rust.md) | Heavy Rust repo-history loop is product-smoked across all four armable fixtures: preflight, RED/GREEN arming, live-command rendering, all-cell no-drive prep, readiness Markdown, and `repo-bakeoff` flow validation. | Private/local, so it should not be used as the public OSS proof. No live gears-rust model cells have been scored yet; those require explicit operator-approved spend. Reference-only fixtures need more injection modes before arming. |

## Candidate graduation checklist

1. Pick a repo from the top-10 list or add a new row with a concrete reason.
2. Mine at least 3 real bugs with linked issues or self-contained fixing PRs.
3. For each bug, record `fix_sha`, `baseline_sha = fix_sha^`, `fix_source`,
   the isolated regression test, and the user-facing ticket text.
4. Follow
   [`../recipes/repo-history-training-new-repo.md`](../recipes/repo-history-training-new-repo.md):
   run `make history-smoke ...` and prove the isolated oracles are RED at
   baseline and GREEN at the real fix before spending on model cells.
5. Commit `tools/bugfix-bakeoff/external/projects/<id>/manifest.yaml` plus
   `oracles/<bug>.<ext>`.
6. Add or update the project row above with suite, harness, status, and gaps.
7. Run cost-bearing cells only on explicit operator request.
8. Commit durable results under `tools/bugfix-bakeoff/external/results/` and
   summarize public claims in `docs/case-studies/` when there is enough evidence.

## Ownership notes

- This page is the tracking surface. Do not bury candidate state in `.context`
  once it affects reuse.
- Generated reports, decks, logs, and scratch notes belong under `.artifacts/`
  until they are promoted to durable docs.
- Automated tests must stay free of real LLM calls. Live model cells are
  operator-only and should be marked pending when providers fail or throttle.
