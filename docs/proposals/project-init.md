# Story: dev-story project init — onboard a project, fine-tune the loop

**Status:** Draft v1. A deterministic dev-story onboarding spine now works:
`go_init` and raw `onboard ...` requests run local discovery, render a reviewed
profile, and apply `.kitsoki.yaml`, `.kitsoki/project-profile.yaml`, and a
`stories/<id>-dev/` instance only after accept. `flows/init_slidey_dogfood.yaml`
uses Slidey as the first external dogfood target and stubs discovery/apply with
no LLM. Mining, profile synthesis, schema validation, readiness verification,
and the full report loop are still pending. Slidey has also been hand-onboarded
with a materialized `stories/slidey-dev/` instance and
`.kitsoki/project-profile.yaml`. The report schema is drafted + proven
(`notes/project-profile.schema.json`, validated in §Verification).
**Kind:**   story
**Epic:**   — standalone <!-- becomes an epic if Open question 1 (first-class dev-server lifecycle) is taken — that slice is runtime -->

## Why

Today a project adopts kitsoki by **hand-authoring an instance**: someone copies
`stories/kitsoki-dev/app.yaml` (or the gears team's external instance, which imports `@kitsoki/dev-story` from the gears repo), rebinds
dev-story's five `host_interfaces:` to concrete providers, fills the
External-target profile world keys, and figures out — from nothing — which dev
server to run, which tests exist, what conventions to use, and what's safe to
automate. That's expert work, it's undocumented per-project, and it's exactly
the onboarding friction that keeps the flagship dev-story from being a
first-run experience.

The operator wants a **front door**: point kitsoki at a repo and it sets itself
up — asks the few preferences it can't infer, **discovers** the project's shape,
**mines the project's own Claude Code transcripts** to learn how work is
actually done there, and emits a single schema-validated report of *what it
intends to set up* (dev server, frontend/backend, environments, rules,
conventions, testing) **before touching anything**. On confirmation it writes
the config, adopts conventions, and proves the loop works end-to-end (boots the
dev server, runs the readiness check + the existing tests, walks a golden-path
UI scenario).

This is distinct from [`stories/dev-story-mining/`](../../stories/dev-story-mining/README.md)
and [`.context/dev-story-from-transcripts.md`](../../.context/dev-story-from-transcripts.md),
which mine **kitsoki's own** transcripts to enrich dev-story's **gates**. Init
mines a **target project's** transcripts to fine-tune **that project's
profile** — the same kit ([`tools/session-mining/`](../../tools/session-mining/README.md)),
a different consumer.

## What changes

A new **init phase** woven into the dev-story hub (reached from `main` via a new
`go_init` intent — no new story, no new CLI command). Because dev-story runs
standalone with sensible defaults, `kitsoki run stories/dev-story/app.yaml` →
`init` works on a **fresh repo with no instance yet** — which is the whole
reason it belongs in the hub rather than in a story that would itself need an
instance to run.

The phase is a pipeline of `init_*` rooms, structurally identical to the design
pipeline (`rooms/design*.yaml`) and the mining story — each room produces a
schema-validated artifact in an idempotent (`once:`) `on_enter`, renders it, and
gates on `accept`/`refine`/`quit` under the existing judge-polymorphism flag:

```
main ──go_init──▶ init_intake ──▶ init_discover ──▶ init_mine ──▶ init_synthesize ──▶ init_review
                  (preferences)    (scan repo)       (mine its     (agent drafts        (PROPOSE: the
                                    no-LLM            transcripts)   the profile +         dry-run report)
                                                                     validates)               │
                                                                                    confirm ◀──┤ refine/quit
                                                                                       │       └─▶ @exit:abandoned
                                                                                       ▼          (nothing written)
                                              init_done ◀── init_verify ◀── init_apply
                                              (report)     (boot+readiness   (write config,
                                                            +tests+golden     conventions,
                                                            path)             instance)
```

The headline artifact is the **project profile** — a YAML record validated
against a pinned JSON Schema (`project-profile/v1`,
[`notes/project-profile.schema.json`](notes/project-profile.schema.json); worked
example [`notes/project-profile.example.yaml`](notes/project-profile.example.yaml)).
It captures everything the operator listed: repo/stack, `dev_server` (with a
deterministic readiness probe), `environments` (local/dev/staging/prod),
`commands` + `testing` (the *existing* mechanisms init integrates with, not
replaces), `golden_path`, `conventions` (recommend kitsoki's
`.context`/`.artifacts`/`.worktrees` or keep the project's own, with the
`.gitignore` plan), `rules`, the `kitsoki` instance binding, the `mining`
evidence, and a `setup_plan` listing exactly what will be written/run on
confirm.

**The profile is the declarative source; on confirm it compiles to a generated
dev-story instance** (`stories/<id>-dev/app.yaml`) — the same artifact
`kitsoki-dev` and `gears-rust` are by hand, now discovered and emitted. That
framing is the spine of the whole feature: init generalizes the instance-binding
pattern that already exists.

## Impact

- **Net-new (story layer):** ~8 rooms (`rooms/init_*.yaml`), ~4 prompts, the
  `project_profiler`/`init_judge` agents in `app.yaml`, one new entry intent
  (`go_init`), the `init_*` world block, and flow fixtures + a host cassette.
- **Net-new (deterministic scripts, not engine):** `scripts/discover.py` (repo
  scan), `scripts/profile_validate.py` (the pinned-schema re-check — the
  deterministic half of the validation sandwich), `scripts/apply_profile.py`
  (renders the instance from the profile, merges `.kitsoki.yaml`, manages the
  `.gitignore` block, creates convention dirs — idempotent, stamps
  `generated.at`), `scripts/readiness.sh` (boot→probe→teardown). Mining reuses
  the existing kit verbatim.
- **Engine/host changes:** **none in v1** — composes existing hosts only
  (`host.run`, `host.starlark.run`, `host.agent.{converse,task,decide}`,
  `host.chat.resolve`, `host.artifacts_dir`, `host.ide.open_file`, and the MCP
  submit validator). The one thing that *could* be a runtime slice — a
  first-class **persistent** dev-server lifecycle host — is deliberately avoided:
  init verifies readiness as a **one-shot** boot→probe→teardown via `host.run`,
  which needs no new engine surface (Open question 1).
- **Where the schema package lands on ship:** the schema + validator are a
  natural fit for `internal/lifecycle/` if
  [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) lands first (same
  `santhosh-tekuri/jsonschema/v6` validation, same YAML-record + `schema:`-pin
  conventions, same `!include` story); until then `scripts/profile_validate.py`
  carries it, exactly as `decompose_validate.py` carries
  [`work-decomposition.md`](work-decomposition.md)'s lint.
- **Docs on ship:** `docs/stories/project-init.md`; the profile schema migrates
  to its package's `schemas/`; this proposal is trimmed/deleted.
- **Compat:** additive. `kitsoki-dev`/`gears-rust` keep working untouched; init
  would *reproduce* the kitsoki-dev binding for the kitsoki repo (the example
  profile is exactly that — proven in §Verification).

## Reuse inventory

This is the proof the feature is composition, not invention.

| Pipeline step | Mechanism it reuses | Reference |
|---|---|---|
| Capture preferences | `host.agent.converse` + persistent chat (intake), or `choice: mode: form`; **never** `AskUserQuestion` | prd intake; [`operator-ask.md`](../architecture/operator-ask.md) |
| Discover repo shape | deterministic `host.starlark.run` / `host.run` script → structured bind | `stories/starlark-enrich/`; `host.run` in dev-story-mining |
| Mine the project's transcripts | the session-mining kit, recency sample | [`tools/session-mining/`](../../tools/session-mining/README.md); `dev-story-mining/rooms/mine.yaml` |
| Draft the profile (the report) | `host.agent.task` + acceptance schema, **MCP submit validator** enforces shape at the tool boundary | `work-decomposition.md`; `internal/host/agent_ask_with_mcp.go`, `schema_shorthand.go` |
| Re-check the report deterministically | pinned JSON Schema validator script (the validation sandwich) | `decompose_validate.py` pattern; proven in §Verification |
| Checkpoint gate (accept/refine/quit) | `accept`/`refine(feedback)` + cycle budget, judge polymorphism | `dev-story-mining/rooms/mine.yaml:61-108`; `stories/bugfix` |
| Propose-then-confirm | dry-run artifact, then a guarded `confirm` that runs the deterministic mutation | design publish; ideas `apply` gate (dev-story README §ideas) |
| Compile profile → instance | render `stories/<id>-dev/app.yaml` from the bindings | `stories/kitsoki-dev/app.yaml:131`; `gears-rust` External-target profile (`stories/dev-story/app.yaml:365-381`) |
| Set up conventions + `.gitignore` | deterministic write script | `publish_design.py` / `ideas_reconcile.py` pattern |
| Run readiness + tests | `host.run`, integrating the project's own `commands`/`testing` | `iface.ci.run_tests`/`build`; `make test` |
| Classify a verify failure (regression vs pre-existing) | `host.agent.decide` over a baseline re-run | the "pre-existing vs regression" gate, [`dev-story-from-transcripts.md`](../../.context/dev-story-from-transcripts.md) theme A |
| Golden-path UI scenario | Playwright in the no-LLM `--flow`/`--host-cassette` posture | `features/` + qa; `feature.schema.json`; `tools/runstatus/tests/playwright/` |

## The report — `project-profile/v1`

The full schema is [`notes/project-profile.schema.json`](notes/project-profile.schema.json)
(draft 2020-12, `additionalProperties: false` throughout, kebab ids,
`contentMediaType: text/markdown` for prose fields — the
[`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) container conventions). Top-level
shape:

| Field | Covers the operator's ask |
|---|---|
| `repo`, `stack` | what this is (fullstack/cli/…, languages, frameworks) |
| `dev_server.components[]` | running the dev server — frontend + backend split, each with a deterministic `ready` probe (http/tcp/log/command) |
| `environments[]` | local / dev / staging / prod (+ custom), url, config ref, gated deploy |
| `commands`, `testing` | the **existing** install/build/test/lint/e2e + frameworks/CI init integrates with |
| `golden_path` | the UI (or api/cli) clickthrough init proves, + regenerable `evidence` |
| `conventions` | kitsoki / project / hybrid; per-dir `.context`/`.artifacts`/`.worktrees`; the managed `.gitignore` block |
| `rules[]` | project rules (discovered from AGENTS.md/CLAUDE.md/.cursorrules, mined, or operator-stated) |
| `kitsoki` | the dev-story **instance binding** (ticket/vcs/ci/workspace/transport) + External-target `external_profile` + judge_mode/autonomy |
| `mining` | the fine-tuning evidence (job, sample, themes with already-modeled/enrich/gap) |
| `setup_plan` | **the propose-then-confirm contract**: `writes[]`, `dirs_create[]`, `gitignore_additions[]`, `verifications[]` — nothing runs until confirm |
| `readiness` | verify results; the dry-run report carries `status: not-run` |

`required` is the minimal honest core: `schema, id, title, repo, stack, kitsoki,
setup_plan`. Everything else is present when discovery/mining found it.

## World schema (sketch)

```yaml
world:
  init_preferences:   { type: object, default: {} }   # intake answers
  init_discovery:     { type: object, default: {} }    # deterministic scan result
  init_mining:        { type: object, default: {} }    # session-mining brief + themes
  init_profile:       { type: object, default: {} }    # the synthesized, validated profile
  init_profile_path:  { type: string, default: "" }    # dry-run report path in the workspace
  init_profile_decision: { type: object, default: {} } # judge verdict at init_review
  init_apply_result:  { type: object, default: {} }    # what apply wrote (instance path, files)
  init_readiness:     { type: object, default: {} }    # init_verify results
  init_cycle:         { type: int,    default: 0 }
  init_budget:        { type: int,    default: 5 }
  abandon_reason:     { type: string, default: "" }
```

`exits:` — `done: {}` (lands back in `main` with a status summary),
`abandoned: {}`. (No `requires:` — declining at `init_review` is a valid,
no-write outcome.)

## Per-room detail

### `init_intake` — capture the few things discovery can't infer
- **Surface:** `host.agent.converse` + a persistent chat (like prd discovery),
  *or* a `choice: mode: form`. Questions are forwarded via the operator-ask
  bridge — **`AskUserQuestion` is hard-denied** in agents; headless/cassette
  runs proceed on defaults ([`operator-ask.md`](../architecture/operator-ask.md)).
- **Captures:** convention choice (kitsoki/project/hybrid), tracker (local files
  / GitHub issues / Jira), autonomy + `judge_mode`, any known dev/test commands,
  external-target intent (in-repo vs foreign repo). Binds `init_preferences`.

### `init_discover` — deterministic repo scan (no LLM)
- **`on_enter`** (`once:`): `host.starlark.run`/`host.run scripts/discover.py`
  → detects package managers (go.mod, package.json, …), frontend/backend layout,
  dev/build/test commands (Makefile, package.json scripts), Dockerfiles + env
  files, CI config, existing `.context`/`.artifacts`/`.worktrees`, VCS + remote,
  and rules files. Binds `init_discovery`. Pure data; reload-safe.

### `init_mine` — fine-tune from the project's own transcripts
- **`on_enter`** (`once:`): the session-mining kit over the project's
  `~/.claude/projects/<slug>` (recency sample), producing themed signals about
  the real dev loop + pain points. Binds `init_mining`. Mirrors
  `dev-story-mining/rooms/mine.yaml`; **cassette-backed in tests** (no live LLM).

### `init_synthesize` — draft the profile (the report)
- **`on_enter`:** `host.agent.task` (`project_profiler`) reads discovery +
  mining + preferences and emits the profile into the per-session workspace
  (`host.artifacts_dir`); the **MCP submit validator** enforces the schema shape
  at the tool boundary; then a deterministic `host.run scripts/profile_validate.py`
  re-checks it against the pinned schema (the validation sandwich
  `work-decomposition.md` uses). `host.ide.open_file` opens the report. Binds
  `init_profile` + `init_profile_path`. **Nothing outside the workspace is
  touched.**

### `init_review` — PROPOSE-THEN-CONFIRM checkpoint
- **View:** renders the profile summary + the `setup_plan` (every write, dir,
  gitignore line, and verification it *will* run) so the operator sees the full
  blast radius first.
- **Intents:** `confirm` → `init_apply`; `refine(feedback)` → re-enter
  `init_synthesize` (`init_cycle++`, budget → `@exit:abandoned`);
  `regenerate`; `quit` → `@exit:abandoned`. Optional `init_judge`
  (`host.agent.decide`) advances confidently under `llm`/`llm_then_human`.

### `init_apply` — write the safe scaffolding (gated behind confirm)
- **`on_enter`:** `host.run scripts/apply_profile.py` renders
  `stories/<id>-dev/app.yaml` from `kitsoki.instance.bindings` (+
  `external_profile`), merges `.kitsoki.yaml` (`story_dirs`, default profile),
  creates the convention dirs, appends the managed `.gitignore` block, and writes
  the profile to `.kitsoki/project-profile.yaml`. Idempotent; stamps
  `generated.at`. Binds `init_apply_result`.

### `init_verify` — prove the loop (gated behind confirm)
- **`on_enter`:** runs each `setup_plan.verifications[]` via `host.run`,
  integrating the project's own commands — boot dev server → readiness probe →
  `tests` → `golden_path`. A `required` check that fails is **reported, not
  hidden**; a `host.agent.decide` classifies a test failure as regression vs
  pre-existing (theme A) so init never false-passes on a baseline-broken repo.
  Binds `init_readiness`.

### `init_done` — summary + handoff
- Renders the committed profile path, the generated instance, the readiness
  results (honestly — a failed required check shows as failed), and the next
  step (`tickets` to pick up work). `@exit:done` → `main`.

## Flow fixtures (Mode-2, intent-only, no-LLM, CI-fast)

The regression contract. All agent/mining/verify host calls are cassette-backed.

- `init_smoke` — `main → init_intake`, render.
- `init_happy_path` — full walk to `@exit:done`; asserts the profile validates,
  the instance + `.kitsoki.yaml` + dirs are written (`expect_files`), readiness
  passes.
- `init_decline` — at `init_review`, `quit` → `@exit:abandoned`; asserts
  **nothing was written** (`expect_no_host_calls` on the apply/verify handlers,
  `expect_files` absent) — the propose-then-confirm guarantee, proven.
- `init_refine_loop` — `refine` re-enters `init_synthesize`; `init_cycle`
  increments.
- `init_budget_exhausted` — `refine` at budget → `@exit:abandoned`.
- `init_verify_failure` — a `required` verification fails; `init_done` reports
  the failure (gates on profile-written, not on green): gate on deliverable
  existence, surface the failure, never false-pass.

## Verification

The report schema is real and enforcing **today**, before any room exists:

- `notes/project-profile.example.yaml` (kitsoki's own profile) **validates**
  against `notes/project-profile.schema.json` — `VALID ✓`.
- Negative cases all **reject** (`additionalProperties:false` at root + deep,
  non-kebab id, missing required `kitsoki`/`ci` binding, bad
  `stack.kind`/probe/`readiness.status` enums) — proving the schema gates, not
  just permits.

Both runs are deterministic, no LLM (YAML→JSON via the repo's vendored
`gopkg.in/yaml.v3`; validated with `jsonschema` Draft 2020-12). Re-runnable; the
JSON twin is throwaway under `.artifacts/` (gitignored).

## Tasks

```
## 0. Design review (this document)
- [ ] 0.1 Agree the profile shape + propose-then-confirm gate + profile→instance compile
- [ ] 0.2 Resolve Open questions (esp. #1 dev-server lifecycle: one-shot vs first-class host)
- [x] 0.3 Hand-author a second profile for a NON-kitsoki repo (a foreign frontend) against the
          schema; adjust the schema from friction, not theory
          (Slidey: `/Users/brad/code/slidey/.kitsoki/project-profile.yaml`
          plus `stories/slidey-dev/app.yaml`)

## 1. Deterministic spine
- [x] 1.1 scripts/init_discover.py + Slidey flow fixture coverage
- [ ] 1.2 scripts/profile_validate.py (pinned schema) + good/bad table tests
- [x] 1.3 scripts/init_apply.py: render instance + merge .kitsoki.yaml + gitignore block + dirs
- [ ] 1.4 scripts/readiness.sh: boot→probe→teardown against a toy server

## 2. The init phase
- [x] 2.1 init_discover/init_review/init_apply/init_done rooms + go_init intent
- [ ] 2.2 Probe each room (kitsoki turn …); lock the graph
- [ ] 2.3 Flow fixtures pass (done: Slidey mocked happy path + refine; pending: decline, budget_exhausted, verify_failure)
- [ ] 2.4 Host cassette for a recorded end-to-end (discover + mine + synthesize + verify), no live LLM

## 3. Live + document
- [ ] 3.1 kitsoki run stories/dev-story/app.yaml → init, end-to-end on a throwaway repo
- [ ] 3.2 Confirm init reproduces the kitsoki-dev binding for the kitsoki repo
- [ ] 3.3 docs/stories/project-init.md; migrate the schema to its package; trim/delete this proposal
```

## Open questions

1. **Dev-server lifecycle: one-shot vs first-class host.** v1 verifies readiness
   with a one-shot boot→probe→teardown script (no engine change). A real
   *persistent* dev server the operator keeps poking at across turns would be a
   `host.devserver.*` lifecycle — a **runtime** slice that turns this into an
   epic. *Lean: ship one-shot v1; spin the runtime slice only if a real need
   appears.*
2. **Profile home + `.kitsoki.yaml` relationship.** Standalone
   `.kitsoki/project-profile.yaml` (shown), or a `project:` section folded into
   `.kitsoki.yaml` (a `webconfig` runtime change)? *Lean: standalone file as the
   source of truth; init only *merges* the existing `story_dirs`/profile keys
   into `.kitsoki.yaml`.*
3. **Generate the instance vs. profile-as-runtime-config.** v1 *compiles* the
   profile to a checked-in `stories/<id>-dev/app.yaml` (visible, diffable, the
   kitsoki-dev pattern). Alternatively the runtime could bind dev-story directly
   from the profile at load with no generated file. *Lean: generate — it's the
   established pattern and stays inspectable; revisit if the generated file
   becomes noise.*
4. **Mining without prior transcripts.** A brand-new project has no Claude Code
   history. *Lean: `init_mine` degrades to a no-op theme list; the profile is
   still synthesized from discovery + preferences (mining is fine-tuning, not a
   prerequisite).*

## Non-goals

- **No new engine/host surface in v1** — composes existing hosts + deterministic
  scripts (Open question 1 gates the only candidate).
- **No new CLI command** — reached from the dev-story hub via `go_init`. (A thin
  `kitsoki init` alias is a trivial later add if wanted.)
- **No replacing the project's tools** — init *integrates with* the discovered
  `commands`/`testing`; it never installs a test framework or rewrites CI.
- **No automatic apply** — propose-then-confirm is the contract; nothing outside
  the session workspace is written or run before the operator confirms.
- **No deploy to staging/prod** — `environments` records how, init does not push
  (deploys are `gated` by schema default).
- **Not gate-mining for dev-story** — that's
  [`stories/dev-story-mining/`](../../stories/dev-story-mining/README.md); init
  mines a *target project* to tune *its* profile.
