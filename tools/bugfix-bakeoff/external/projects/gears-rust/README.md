# gears-rust corpus — provenance & how to run

A stable, reusable reference corpus of real, already-fixed bugs in **gears-rust**
(a large, mature, private Rust monorepo), captured from two 2026-06 dogfood runs of
the kitsoki `bugfix` pipeline. Each fixture pins a hermetic baseline (the real fix's
parent commit) and the real PR's regression test as the **hidden oracle** — RED at
baseline, GREEN after the real fix.

This is the heavy/heterogeneous/private counterpart to
[`../query-string`](../query-string); together they exercise the whole
manifest contract (per-bug `oracle:`, `inject: write`, `suite: false`,
`local_only`). See [`../../README.md`](../../README.md) for the contract and
[`bench.py`](../../bench.py) for the grader.

## How it was captured

1. **Marathon** — drove 10 real merged-fix baselines LIVE through `stories/bench-bugfix`
   via the studio MCP under GPT-5.5, each independently verified against the real PR's
   own regression test. Result: **10/10 shipped, all oracle-PASS**.
2. **Hard-case run** — 4 deliberately harder cases (multi-file, DB/integration oracles,
   heavy crates) to surface where the pipeline struggles. Result: **H2/H3 PASS, H1 FAIL
   (incomplete fix — reproducer asserted a near-side wire signal, not the end-to-end
   body delivery), H4 STALL (a hung maker call with no inactivity watchdog).**

Both findings drove generic, committed pipeline fixes (the reproducer "assert the
end-to-end outcome" prompt hardening; an inactivity-watchdog design for the hung-call
stall) — see the commit history for `stories/bugfix/prompts/` and the dev-story
`build_cmd`/`test_cmd` passthrough.

## The corpus

**Armable** (standalone public-API oracle under `oracles/`; the gated test proves
RED@baseline → GREEN@fix):

| bug | crate | what was wrong |
|-----|-------|----------------|
| bug1 | cf-gears-toolkit | gh-4115: k8s underscore env override of a dashed gear name is dropped |
| bug4 | cf-modkit-canonical-errors | CanonicalError lacks From<io::Error> / From<serde_json::Error> |
| bug5 | cyberware-resource-group | RG-prefix wrongly forced onto external membership types |
| bug9 | cf-modkit | duplicate YAML mapping keys silently accepted |

**Reference-only** (captured in `manifest.yaml` under `reference_only:`; oracle is a
copy-in snippet or whole-file overlay — armed once `bench.py` grows an
`inject: insert-at-marker` mode): bug2/3/6/7/8/10 (marathon, all PASS) and the four
hard cases H1 (FAIL) / H2 (PASS) / H3 (PASS) / H4 (STALL).

## Run the arming check

gears-rust is heavy + private, so it is **not** network-cloned (the manifest marks it
`local_only: true`, and the `qsbakeoff` loop skips it). Arm it against a LOCAL checkout:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
# or:
BUGFIX_BAKEOFF_REPO=/path/to/checkout \
  go test -tags gearsbakeoff -run TestGearsBakeoff -count=1 -v ./tools/bugfix-bakeoff/external/
```

The gated test clones a throwaway `--local --no-checkout` mirror (so the grader's
fix-source checkout never dirties your working tree). `bench.py` isolates the cargo
target cache **per fixture** — a shared one cross-contaminates different baselines of
the same workspace (a newer baseline's rlib would falsely turn an older baseline's RED
oracle GREEN). Skips cleanly when `BUGFIX_BAKEOFF_REPO` is unset.

## Add / promote a fixture

- **Armable:** drop `oracles/<bug>.rs` (a standalone test calling the crate's public
  API) + a `bugs[]` entry with a per-bug `oracle: {target, run, inject: write}`. No code.
- **Reference → armable:** move it from `reference_only:` into `bugs:` once it has a
  standalone oracle (or after the `insert-at-marker` inject mode exists for copy-in
  oracles).
