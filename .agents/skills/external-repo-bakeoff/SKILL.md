---
name: external-repo-bakeoff
description: Run a bug-fix bake-off on ANY external/open-source repo to answer "should I use kitsoki for my project?" — find real filed-issue bugs with regression-test PRs, pin reproducible baselines, onboard the repo, drive the kitsoki bugfix pipeline LIVE under one or more non-Claude provider profiles (GPT-5.5 / GLM-5.2 / …), grade each fix deterministically against the PR's own hidden oracle, and produce a report + slidey deck comparing models and cost vs the real maintainer fix. Use when the user says "bake off <repo>", "benchmark kitsoki on this repo", "should I use kitsoki for my project", "compare <modelA> vs <modelB> fixing bugs in <repo>", "run the external bake-off", or wants to evaluate kitsoki's prompts/patterns on real third-party code. Covers onboarding AND the live-drive tuning needed to get good working cases. Distinct from dogfood-marathon (kitsoki's OWN bugs over the MCP-driver agent) and matrix-task-comparison (kitsoki-vs-naive-prompt on fixed tasks).
---

# External-repo bake-off

Point the kitsoki bug-fix pipeline at a **real third-party repo**, let a non-Claude
model fix real filed-issue bugs, and **grade every fix deterministically** against
the regression test the real PR shipped. The output answers a prospective user's
question — *do I get a good fix, and at what cost vs the real one?* — and produces
a report + narrated slidey deck.

**Read first (don't re-derive):**
- [`tools/bugfix-bakeoff/external/README.md`](../../../tools/bugfix-bakeoff/external/README.md) — the repo-agnostic harness (`bench.py`, `projects/<name>/manifest.yaml`, the gated `bench_test.go`).
- [`docs/case-studies/query-string-bakeoff.md`](../../../docs/case-studies/query-string-bakeoff.md) — the worked reference run (GPT-5.5 solved query-string 3/3).
- [`tools/mcp-drive/README.md`](../../../tools/mcp-drive/README.md) — the headless MCP delegation primitive (`drive.sh`).
- [`stories/bench-bugfix/app.yaml`](../../../stories/bench-bugfix/app.yaml) — the generic bugfix instance you drive.
- [`stories/repo-bakeoff`](../../../stories/repo-bakeoff) — the drivable workflow that wraps this whole method (configure → arm oracles → run cells → score → deck); run it with `kitsoki run stories/repo-bakeoff/app.yaml`.
- MEMORY: `mcp-first-delegation-runbook` (the live-drive recipe + tuning knobs), `bakeoff-dogfood-framework`, `workflow-gate-on-independent-verify`.

The discipline that makes results worth anything: **the oracle is the verdict**
(never the model's self-report), baselines are **genuinely RED** (pre-flight!),
and a blocked provider is reported as **pending**, never as a capability result.

---

## Phase 1 — Pick the repo + find verified fixtures

Pick a repo that is **small/simple but mature** — small enough to onboard and test
in seconds, mature enough to have real filed-issue bugs fixed by PRs that *added a
regression test*. Sweet spot: a single-module library with hundreds–thousands of
commits and a `npm/pytest/cargo/go test` flow (e.g. the sindresorhus JS libs;
query-string is the shipped reference).

For **each** of 3 fixtures, find and VERIFY (delegate the GitHub spelunking to a
`general-purpose` agent — it's multi-step):
- the filed **issue** + the merged **PR** that fixed it (must ADD a regression test);
- `fix_sha` (full) and `baseline_sha = fix_sha^` (the reproducible bug-present commit);
- the **exact added test** (isolated — the same PR often also edits a pre-existing
  test; take only the cleanly-ADDED block) and the command to run just it.

**Pre-flight (load-bearing):** clone at `baseline_sha`, install, overlay ONLY the
added test, and prove it is **RED at baseline, GREEN at fix**. Discard any fixture
that is already-green at baseline (degenerate — a test added atop an already-merged
fix). This is the #1 way a bake-off goes silently wrong.

Then write `tools/bugfix-bakeoff/external/projects/<name>/`:
- `manifest.yaml` — `project.{repo,install,test_cmd,oracle.{target,run}}` +
  `bugs[].{id,baseline_sha,fix_sha,fix_source,oracle_test,oracle_match,ticket}`.
- `oracles/<bug>.test.js` — each isolated added test.

Verify the whole set is armed: `python3 bench.py verify --project <name>`
(RED@baseline / GREEN@real-fix for all). The gated `make qs-bakeoff` then also
onboards the repo and re-checks — keep it green.

---

## Phase 2 — Onboard the repo

Onboarding is just the embedded dev-story; a binary-only user runs
`kitsoki run @kitsoki/dev-story` then `onboard <path>` (writes config + instance +
`.mcp.json` + skill/agent toolkit). The gated test exercises this headlessly. For
the bake-off you mostly need a clean **worktree per cell** at the bug's baseline:

```sh
git -C <clone> worktree add --detach <cell> <baseline_sha>
git -C <cell> checkout -B bench-<bug>-<modelshort>
ln -sfn <clone>/node_modules <cell>/node_modules   # reuse one install
```

---

## Phase 3 — Drive the pipeline LIVE (the tuning that gets good cases)

**The one-command path** (codifies everything below — prefer it):
```sh
tools/bugfix-bakeoff/external/drive_cell.sh \
    --project <name> --bug <bug> --candidate <key> --score
```
It reads the manifest + `candidates.yaml`, preps the baseline worktree, bakes in
every `initial_world` knob, drives live via `drive.sh`, then scores + extracts
cost. The rest of this phase is what it automates — read it to debug or extend.

**Cheapest-viable answer — `escalate.sh`.** To answer "what is the cheapest
model/effort that fixes my bugs?", run each bug up an ordered ladder instead of
one fixed candidate:
```sh
tools/bugfix-bakeoff/external/escalate.sh --project <name> --ladder default
tools/bugfix-bakeoff/external/escalate.sh --project <name> --ladder default --dry-run  # free plan
```
It stops each bug at the first rung that reaches `solved`. Effort is a **profile**
property (`session.new` has no effort param) — a rung is a candidate row pointing
at a profile with that (model, effort); see the `candidates.yaml` header +
`ladders:`. kitsoki's OWN bugs run the same way via `--project kitsoki`
(`local_only`; verify against a throwaway `git clone --local` mirror).
Polyglot repos (a JS package not at the root) set a per-bug `oracle.setup`
(e.g. `cd sub/pkg && pnpm install`) run before the oracle.

Drive `stories/bench-bugfix` through the **headless MCP primitive**
`tools/mcp-drive/drive.sh` — NOT the in-process Agent tool (an in-process subagent
inherits the parent's empty MCP set → "No MCP servers configured" → drives
nothing). `drive.sh` is a raw `claude -p --mcp-config … --strict-mcp-config`;
orchestrator defaults to **sonnet** (it only clicks `session.*`). The **worker**
model is chosen by `session.new {profile, harness:"live"}`:
`codex-native` → GPT-5.5, `synthetic-claude` → GLM-5.2. The active profile's model
supersedes the story's agent-def models (`harness_profiles.go:21`) — no agent
edits needed.

One cell = one `drive.sh` invocation (isolated trace, no lock contention). Seed
`session.new {story_path: stories/bench-bugfix/app.yaml, harness:"live",
profile:<P>, trace:<unique path>, initial_world:{…}}`. **The `initial_world`
knobs that make it actually work** (each learned by a failure):

| knob | value | why |
|------|-------|-----|
| `workspace_id` | **`""`** | skips `iface.workspace.create`; otherwise it `git worktree add`s in the MCP's CWD repo and fails `invalid reference: <branch>` (branch lives in the external repo). Empty ⇒ the implementer edits the prepared `workdir` directly + commits there. |
| `thread` | `<bug>` | else `host.append_to_file: thread argument is required` bounces every transport-post to idle. |
| `workdir` | abs cell path | the prepared worktree on `bench-<bug>-<short>`. |
| `base_branch`/`feature_branch` | `bench-<bug>-<short>` | a ref that exists in the worktree. |
| `bf_autostart_attempted` | `true` | else idle auto-derives/clobbers `workdir`. |
| `judge_mode` | `"llm"` | auto-advances review/validate headlessly. |
| `test_cmd` | the repo's test cmd | retargets the testing room off Go (e.g. `npx ava`, `pytest -q`, `cargo test -p <crate>`). ALSO arms the **deterministic GREEN→RED gate**: with `test_cmd` set + `gate_command` empty, `implementing/on_enter` mechanically proves baseline-GREEN (HEAD~1) → reproducer-RED off the model's OWN synthesised test, before any maker spend — leak-safe (no hidden oracle exposed). Prefer this over LLM-only repro. |
| `ticket_title` | the full bug description | the reproducer is fed `ticket_id`+`ticket_title` only — pack the repro detail here (no ticket file needed; leave `gate_command` empty so the deterministic gate runs off the synthesised repro_command and the hidden oracle never leaks). |

Orchestrator prompt: drive `full_pipeline` ONCE, then only advance explicit gates
(accept/continue) and answer ask-gates affirmatively; **don't re-drive start**
(the LLM judge auto-emits accept/refine). Stop at a terminal state, ~25 turns, or
a repeated stuck state. If a `host_error` bounces to idle, read `world.last_error`
and STOP — that's a finding, not something to brute-force.

**Run cells in parallel** across DIFFERENT providers (separate MCP servers +
traces); keep one provider's cells modest. Per `stories/AGENTS.md`, a story-level
crash exposed by a run (e.g. a non-nil-safe `when` on an unbound artifact) is a
**bug to fix**, not to paper over.

**Before blaming a model, probe its provider.** A 0/N that shows *zero* worker
`agent.call.start` is almost always provider-side. Verify with a direct curl
(synthetic GLM throttles at the account level and self-resets):
```sh
curl -sS https://api.synthetic.new/anthropic/v1/messages -H "x-api-key: $SYNTHETIC_API_KEY" \
  -H anthropic-version:2023-06-01 -d '{"model":"hf:zai-org/GLM-5.2","max_tokens":16,"messages":[{"role":"user","content":"ok"}]}'
```
"exceeded your subscription rate limits" ⇒ mark that model **pending**, don't
report it as a capability result.

---

## Phase 4 — Grade, report, deck

**Grade deterministically** (independent of the model's claims). Score the cell
worktree — `bench.py` copies it, overlays the hidden oracle into `oracle.target`,
runs `oracle.run`:
```sh
QS_NODE_MODULES=<clone>/node_modules python3 bench.py score \
  --project <name> --bug <bug> --tree <cell> --candidate <model> --treatment kitsoki \
  --out results/cells/<bug>-<model>-kitsoki.json
#   exit 0 ⇔ oracle GREEN (good fix) · exit 1 ⇔ RED
```
Verdicts (shared `results/SCHEMA.md`): `solved` = oracle + full suite green;
`partial` = oracle green but a pre-existing test the fix should update is left red
(the measurable "kitsoki runs more tests" quality lever); `failed` = oracle red.

**Cost:** read the worker cost from the trace (`payload.meta.cost_usd`) for metered
providers; codex/subscription auth carries none → report token usage + note
"subscription". Line each fix up against the **real maintainer fix** (approach
match is a strong signal).

**Report + deck:** add a results section to the project case study, and author a
slidey deck — **commit only the `docs/decks/<name>-bakeoff.slidey.json` spec**.
Do NOT bundle or check in the `.slidey.html`: it's a deterministic render of the
JSON (preview it with the VS Code extension or `slidey bundle … .html` locally),
so a committed HTML is just a stale 6 MB duplicate. `*.slidey.html` is gitignored.
Scene types: `title` / `narrative` / `cards{variant:grid}` / `table{variant:data,
rows:[{cells:[…]}]}`. Numbers must come from `results/*.json` — pending cells are
marked pending, never invented. Commit results under
`tools/bugfix-bakeoff/external/results/` so the deck regenerates offline.

---

## Done when

- 3 fixtures verified RED→GREEN and committed as a `projects/<name>` manifest;
- `make qs-bakeoff` green (onboard + armed oracles);
- each model's cells driven live and **deterministically scored** (or honestly
  marked pending with the provider probe as evidence);
- a report + bundled deck comparing models, with each fix lined up against the
  real maintainer fix.
