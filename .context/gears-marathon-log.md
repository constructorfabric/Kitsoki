# gears-rust bugfix marathon — running log

Goal: drive the kitsoki **bugfix** dev-story LIVE over 10 real gears-rust bugs
(each already fixed by a high-quality merged PR = baseline) entirely through the
kitsoki MCP studio (via the `kitsoki-mcp-driver` agent), independently verify each
fix against the real PR's regression test (the HIDDEN oracle), and harden the
generic `@kitsoki/dev-story` + the gears-rust instance so the pipeline solves bugs
reliably with correct project conventions — **fully autonomous, no hand-holding.**

Tracking: `.artifacts/gears-marathon/` — `cases.yaml` (the 10 baselines),
`attempts.jsonl` (append-only), `gen_table.py` → `STATUS.md` (deterministic table),
`verify/` (per-bug oracle harnesses), `traces/`, `slidey/`.

Method: `dogfood-marathon` skill + `.context/bakeoff-learnings.md` gotchas.

---

## 2026-06-25 — Session bootstrap

**Orientation.** Prior work proved the bake-off loop on `query-string` (fast JS
oracle, see `.artifacts/qs-bakeoff/`). The goal demands **gears-rust** specifically,
so the gating risk is Rust build/test time.

**Feasibility (PASS).** Cut a detached worktree at `e3ab3c27` (= `a7080261^`,
bug1's parent) and ran `cargo test -p cf-gears-toolkit --lib bootstrap::config`:
clean build + test in **~54s**. The Rust loop is viable per cell.

**bug1 selection — gh-4115** (`a7080261`, "normalize underscore→dash for k8s env-var
overrides of dashed gear names"). Single-file fix in `cf-gears-toolkit`; oracle test
`test_gh4115_dashed_gear_name_env_override_works` calls only the public API
`AppConfig::load_layered`. Clean, deterministic, behavioural oracle.

**bug1 RED pre-flight (PASS = genuinely RED).** Injected a public-API-only RED-check
integration test (`verify/bug1-oracle.rs`, using the existing `temp-env` dev-dep) into
the baseline worktree: `priority == 100`, expected `50` → the env override is silently
dropped at baseline. Confirms a real behavioural bug, not a test-on-top-of-merged-fix
(gotcha #2, avoids a degenerate cell).

**Candidate pool (10 pinned in `cases.yaml`).** bug1 confirmed-RED; bug2–bug10 are
focused single-package behavioural fixes with regression tests (oagw / errors /
resource-group / account-management / modkit-db), to be pinned + RED-confirmed before
driving. Flaky-timing test fixes deliberately excluded (non-deterministic oracle).

**Infra stood up.** `gen_table.py` (no-dep YAML/JSONL → STATUS.md), seeded
`attempts.jsonl`, slidey deck scaffold under `slidey/`.

### Next
- Drive bug1 through `stories/bugfix` live via `kitsoki-mcp-driver`
  (`harness:live`, explicit `trace:`, `base=e3ab3c27`, scoped `test_cmd`, fresh
  per-case worktree), then independently verify with the bug1 oracle.
