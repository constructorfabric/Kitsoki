# Testing the MCP operating-system candidate

The MCP operating-system promotion decision is replay-only. Its test surface
uses stored cells and fake backends; it must not call an LLM or a provider.

## Required no-LLM checks

Run the matrix contract test from the repository root:

```sh
python3 tools/arena/tests/test_mcp_operating_system_decision.py
```

It expands a 12-case × 3-profile (36-cell) matrix through the real Arena
plugin, but the backend is a fixture responder. It asserts that `strict` is
HOLD because `trace-stalled-turn` fails correctness, that legacy and escape
safety failures remain visible, and that cost/latency are not considered before
hard gates pass. It also rejects a forged eligible decision and proves that
live calibration is forbidden in tests.

Run the Studio MCP schema gate when the candidate profile/tool surface is in
scope:

```sh
GOCACHE=/private/tmp/kitsoki-gocache go test ./internal/mcp/studio -run 'TestToolSchema'
```

The normal package gate is useful after final integration:

```sh
GOCACHE=/private/tmp/kitsoki-gocache go test ./internal/mcp/studio
```

## Regenerate review evidence

Generate a fresh offline review bundle under `.artifacts`:

```sh
python3 tools/arena/arena/mcp_operating_system_report.py report \
  --spec tools/arena/specs/mcp-operating-system-replay.yaml \
  --out .artifacts/mcp-operating-system/replay
```

Review the generated `report.json` (full hard-gate matrix), `report.md` (human
summary), `decision.json` (machine record), and `deck.slidey.json` (derived
review deck) together. `visual-review-input.json` ties the deck to the decision
digest. Generated artifacts stay in `.artifacts`; the replay spec and tests are
the durable review surface.

## Promotion rule

Only `strict` can become eligible. Every strict safety and correctness cell
must pass before cost or latency are considered. The current correct verdict is
**HOLD**: `trace-stalled-turn` fails correctness. A passing safety total, an
escape/legacy result, edited JSON, or a favorable cost number cannot override
that hard gate.

When repairing a failure, update the recorded replay/cassette and rerun the
full matrix. Do not use a live model to make an automated test pass.

## Live-calibration firewall

The command below validates an authorization record but intentionally does not
dispatch a provider call:

```sh
python3 tools/arena/arena/mcp_operating_system_report.py live-calibration \
  --spec tools/arena/specs/mcp-operating-system-replay.yaml \
  --operator-authorization I_UNDERSTAND_LIVE_CALIBRATION \
  --budget-usd 1.00
```

It is an operator-only approval boundary, never a test command. It needs the
exact token and a budget of at least USD 1.00, returns
`authorized-not-dispatched`, and must be paired with a separately approved live
execution procedure. Do not add it to CI, flow tests, package tests, or replay.

## Failure triage

Treat a strict hard-gate failure as a rollout stop:

1. Preserve the replay report and evidence reference.
2. Diagnose the recorded trace with bounded explain/diagnose tools.
3. Repair the strict implementation and its no-LLM fixture or cassette.
4. Rerun the complete matrix and schema gate.
5. Keep the default profile unchanged unless a new decision is eligible and
   final integration approves the switch.

Do not route around a strict failure through `host.run`, `host.patch`, a raw
worktree, or legacy and then record that success as strict evidence.
