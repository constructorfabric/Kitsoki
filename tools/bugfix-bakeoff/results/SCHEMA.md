# Bake-off result contract

The coordination point for all bake-off tooling. `score.py` writes one **cell
result** JSON per grid cell; `aggregate.py` merges them into `summary.json`,
which the report + `eval_pilot_report.py` deck consume. No tool may diverge from
these shapes.

## Cell result — `results/cells/<bug>-<candidate>-<treatment>.json`

```json
{
  "bug": "bug1",
  "candidate": "opus-4.8",
  "treatment": "kitsoki",
  "profile": "claude-native",
  "model": "opus",
  "effort": "medium",
  "provider": "anthropic",

  "outcome": {
    "oracle_pass": true,            // the fix's own regression test passes
    "oracle_status": "pass|fail|noncompile|absent",
    "build_pass": true,             // go build ./... (go bugs) — n/a -> null
    "suite_pass": true,             // affected_test_pkgs green (no regressions)
    "quality": "solved|partial|failed|pending",  // solved = oracle+build+suite all green;
                                    // MAY be human/LLM-overridden (see adjudicated)
    "adjudicated": false,           // true => quality was overridden by a human/LLM
    "adjudication_note": ""         // rationale for the override (empty otherwise)
  },

  "compliance": {
    "reproduced_red": true,         // demonstrated the bug RED before fixing
    "added_regression_test": true,  // wrote its OWN test (separate from oracle)
    "suite_green": true,            // kept the rest of the suite green
    "in_scope": true,               // no unrelated edits outside the bug
    "stage_order": true,            // honored reproduce->implement->test order
    "rate": 1.0                     // mean of the five booleans
  },

  "metrics": {
    "input_tokens": 0,
    "output_tokens": 0,
    "cache_read_tokens": 0,
    "cache_write_tokens": 0,
    "total_tokens": 0,
    "cost_usd": 0.0,
    "cost_exact": true,             // false => priced from an added/est rate row
    "wall_time_s": 0.0,
    "guidance_turns": 0             // single: human nudges; kitsoki: pipeline turns
  },

  "transcript_path": ".artifacts/bugfix-bakeoff/<bug>/<candidate>-<treatment>.jsonl",
  "trace_found": true,             // kitsoki cells: was a discoverable trace found?
  "notes": "free text — e.g. oracle noncompile because candidate fixed differently"
}
```

`quality` rules: `solved` = oracle_pass && build_pass!=false && suite_pass;
`partial` = oracle_pass but a regression or build issue, OR bug plausibly fixed
but oracle noncompiles against the candidate's differing implementation (note it);
`failed` = oracle_fail. `pending` means the provider/profile/infrastructure path
blocked before a real model capability result existed; the oracle did not run and
the cell must not be counted as failed.

**Adjudication.** Some oracles are wording/implementation-coupled (they assert a
literal substring or symbol from the canonical fix) and so false-fail a
behaviorally-correct fix. A human or LLM judge may override `quality` via
`score.py --adjudication <solved|partial|failed> --adjudication-note "<why>"`.
The override sets `outcome.quality` to the adjudicated value, `adjudicated=true`,
and records the rationale — but `oracle_status` ALWAYS keeps the raw automated
result (e.g. `fail`), so the JSON never lies about what the oracle did. The
aggregate rollups (solve_rate etc.) key on the possibly-adjudicated `quality`.

## Aggregate — `results/summary.json`

```json
{
  "generated_at": "2026-06-24T..Z",     // stamped by aggregate.py at write time
  "manifest": "tools/bugfix-bakeoff/bakeoff.yaml",
  "bugs":       [ { "id","title","severity","component","fix_sha","baseline_sha","oracle_test" } ],
  "candidates": [ { "key","profile","model","effort","provider" } ],
  "treatments": ["kitsoki","single"],
  "cells":      [ <cell result>, ... ],   // up to 40
  "rollup": {
    "by_treatment":  { "kitsoki": {...}, "single": {...} },
    "by_candidate":  { "opus-4.8": {...}, ... },
    "by_cell_key":   { "opus-4.8|kitsoki": {...}, ... }
  }
}
```

Each rollup bucket: `{ "n", "solved", "partial", "failed", "pending", "solve_rate", "avg_cost_usd",
"avg_total_tokens", "avg_wall_time_s", "avg_guidance_turns", "avg_compliance" }`.
`solve_rate` should be computed over attempted cells only (`n - pending`).

## eval_pilot_report.py bridge

`aggregate.py --emit-agenteval` also writes one `agenteval.Report` JSON per bug
under `results/agenteval/<bug>/latest.json` (candidate key =
`"<candidate>|<treatment>"`), so the existing `eval_pilot_report.py --markdown
--deck` regenerates the data deck offline with zero re-spend.
