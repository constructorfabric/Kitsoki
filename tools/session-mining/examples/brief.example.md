# Session-mining action brief

_2 report(s) · 2 contributor(s) · vocab 2026-05-31 · promotion threshold 2 contributors._

## Build these (ranked)

### 🟢 Fix failing tests — BUILD NOW
_Run the test suite, read a failure, edit code or test, rerun until green._

**Why:** priority **0.77** · seen by **2/2** contributors · **15** occurrences · pain 🔴 high · 77% mechanical
**The move:** L1 (recurring manual work today) → **L2** target

**Gates to install (2 decision points — the judgment to keep):**
- fix code vs fix test
- flaky vs real failure

**Skeleton to script (observed tool-call shape):**
- `go test -run <Test> → Edit <file> → rerun`
- `pytest -k <test> → edit → rerun`

**First step:** script the skeleton above; wrap each gate as a named decision point (a default rule where one is obvious, else prompt a model/human); record every decision so the gate can climb toward L2.

## Full ranking

| Pattern | Verdict | Prio | Contrib | Occ | Pain | Gates |
|---|---|--:|--:|--:|:--:|--:|
| fix-failing-tests | 🟢 BUILD NOW | 0.77 | 2/2 | 15 | high | 2 |
| build-compile-fix-loop | ⚪ LATER | 0.31 | 1/2 | 12 | med | 1 |
| explore-codebase | 🔵 ALREADY MOSTLY SOLVED | 0.18 | 1/2 | 14 | low | 0 |

## Newly corroborated patterns (promote into the vocabulary)

- **ci-log-triage** — 2 contributors, 5 occ. Add to `vocab/core.yaml` (bump `vocab_version`).

## Watch list (novel, not yet corroborated)

Each needs more independent contributors before it counts. Not actionable yet.

- `warp-debug-flag` — 1 contributor(s), needs 1 more to promote
- `notebook-cell-iteration` — 1 contributor(s), needs 1 more to promote

