#!/usr/bin/env python3
"""flow_gate_runner.py — the no-LLM half of Task 3.3
(docs/proposals/usable-kitsoki-release-gate.md): drive one mined scenario's
S4-compiled flow fixture through a REAL `kitsoki test flows` run and turn the
resulting trace into a parity-verdict record via the S1 x S4 join that
already lives in `tools/arena/arena/plugins/usable_kitsoki_gate.py`
(`extract_turn_signals` / `build_parity_record`).

## Why `kitsoki test flows --trace-out`, not a hand-rolled Go harness

`cmd/kitsoki/test_flows.go`'s `--trace-out` flag already writes the run's
authoritative JSONL trace (the exact `store.Event` stream
`internal/testrunner/flows_workbench_smoke_trace_test.go` reads via
`OnRigClose`) to a file. That JSONL is byte-compatible with what
`extract_turn_signals` expects (`{"kind": "turn.end", "payload": {...}}` per
line) -- no new Go code, no new trace format, just the existing CLI flag
pointed at each scenario's compiled `.flow.yaml`.

## The workbench-target projection (S6 "no-llm-parity", supersedes the
## harness-only gap this module used to document)

`build_record_for_cell` now accepts an optional `target` naming one of
`flow_fixture_compiler.WORKBENCH_TARGETS` (`dev-story`, `pets-dev`,
`slidey-dev`) -- the three real, shipped `workbench:` rooms
(`docs/proposals/room-workbench.md`'s macro). When `target` is given, this
runner replays the `.{target}.flow.yaml` / `.{target}.cassette.yaml` pair
`flow_fixture_compiler.py --target <target>` compiles (see that module's
"real-workbench target" docstring section) against the target's OWN real
app (`stories/dev-story/app.yaml`, `stories/pets-dev/app.yaml`,
`stories/slidey-dev/app.yaml`), not the fixed harness app below.
`internal/orchestrator/workbench_gate_signal.go`'s `workbenchGateSignal`
fires for real on these (the dispatching state IS a `workbench:` room), so
`candidate_completed` is computed by a real engine-side join against the
scenario's own `expected_effects` (seeded via `world_override` on the
compiled fixture's final turn), not fabricated by this runner or hardcoded
either way.

When `target` is omitted (the default, back-compat path), this runner still
replays the OLD harness-stub projection
(`stories/scenario-foundry-harness/app.yaml`'s `desk` room, which has no
`workbench:` key) -- that path still produces zero `usable_kitsoki_gate`
turn signals by construction (see git history / the harness-target
compiler docstring for why it exists at all: it proves the schema/join/
rollup machinery independent of any real story's workbench wiring).
`run_calibration_gate.py`'s calibration sweep now drives the workbench-
target path across all three targets, not the harness stub -- see
`usable_kitsoki_gate_constants.py`'s calibration-contact note for the
resulting measured number.

## Surface handling

All three surfaces (`web`, `tui`, `mcp`) drive the IDENTICAL flow-replay
mechanism today -- there is no per-surface app/state-machine differentiation
in any of the target apps (dev-story/pets-dev/slidey-dev, or the harness
stub), and no real browser/PTY/MCP-stdio driver exists yet for the no-LLM
path (`WEB_SPEC`/`TUI_RUNNER`/`MCP_RUNNER` in `usable_kitsoki_gate.py`
document that gap). This is a deliberate, documented simplification: the
no-LLM contract test proves the schema / join / rollup machinery end to end
across all three surface tags, while a REAL per-surface driver (a
Playwright-driven web session, a PTY-driven TUI session) remains later,
larger, separately-gated work. Every record's `surface` field is still set
correctly per the cell it was built for, so the rollup's WORST_SURFACE_GATING
reduction is exercised for real, even though the three surfaces' underlying
runs are, today, the same run.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

# tools/usable-kitsoki-gate/flow_gate_runner.py -> parents[2] == REPO_ROOT
REPO_ROOT = Path(__file__).resolve().parents[2]
# `tools/usable-kitsoki-gate` has a hyphen in it and can't be a dotted
# import path itself; reach the plugin the same way
# test_usable_kitsoki_gate_corpus.py does -- put tools/arena on sys.path and
# import its `arena` top-level package directly, rather than pretending
# `tools` is an importable namespace package.
_ARENA_ROOT = REPO_ROOT / "tools" / "arena"
if str(_ARENA_ROOT) not in sys.path:
    sys.path.insert(0, str(_ARENA_ROOT))

from arena.plugins import usable_kitsoki_gate as gate_plugin  # noqa: E402

# `tools/session-mining` also has no dots-friendly parent package; reach
# `flow_fixture_compiler.py`'s WORKBENCH_TARGETS registry (the single source
# of truth for each target's real app path / capture intent / etc, already
# used to COMPILE the fixtures this runner replays) the same way.
_SESSION_MINING_ROOT = REPO_ROOT / "tools" / "session-mining"
if str(_SESSION_MINING_ROOT) not in sys.path:
    sys.path.insert(0, str(_SESSION_MINING_ROOT))

import flow_fixture_compiler  # noqa: E402

HARNESS_APP = REPO_ROOT / "stories" / "scenario-foundry-harness" / "app.yaml"
WORKBENCH_TARGETS = flow_fixture_compiler.WORKBENCH_TARGETS
DEFAULT_CORPUS = REPO_ROOT / gate_plugin.DEFAULT_SCENARIO_CORPUS
SURFACES = gate_plugin.SURFACES


def target_app_path(target: str | None) -> Path:
    """The real `kitsoki test flows <app>` CLI argument for `target` -- the
    workbench target's own app.yaml when given, else the harness stub
    (back-compat default). `flow_fixture_compiler.py`'s `app_rel` field is
    relative to `calibration/flows/` (where the compiled fixtures live), so
    resolve it from there, not from REPO_ROOT.
    """
    if target is None:
        return HARNESS_APP
    if target not in WORKBENCH_TARGETS:
        raise ScenarioNotFound(
            f"unknown workbench target {target!r} (known: {sorted(WORKBENCH_TARGETS)})"
        )
    app_rel = WORKBENCH_TARGETS[target]["app_rel"]
    return (DEFAULT_CORPUS / "flows" / app_rel).resolve()

# `kitsoki test flows` reuses the caller-provided test binary when
# KITSOKI_TEST_KITSOKI_BINARY is set (make test builds one for flow-heavy
# suites). Standalone invocations fall back to `go run`.
FLOW_TIMEOUT_SECONDS = 120


class ScenarioNotFound(Exception):
    pass


def load_scenario(corpus_dir: Path, scenario_id: str) -> dict[str, Any]:
    path = corpus_dir / f"{scenario_id}.json"
    if not path.exists():
        raise ScenarioNotFound(f"no scenario IR document for {scenario_id!r} at {path}")
    return json.loads(path.read_text(encoding="utf-8"))


def flow_fixture_path(corpus_dir: Path, scenario_id: str, target: str | None = None) -> Path:
    if target is None:
        return corpus_dir / "flows" / f"{scenario_id}.flow.yaml"
    return corpus_dir / "flows" / f"{scenario_id}.{target}.flow.yaml"


def list_scenario_ids(corpus_dir: Path) -> list[str]:
    """Every `scn-*.json` document directly under `corpus_dir` (mirrors
    `arena.model.load_targets_from_corpus`'s directory branch), sorted for
    determinism -- a calibration sweep's cell order must not depend on
    filesystem iteration order.
    """
    return sorted(p.stem for p in corpus_dir.glob("scn-*.json"))


def run_scenario_flow(
    scenario_id: str,
    corpus_dir: Path,
    *,
    target: str | None = None,
    kitsoki_root: Path = REPO_ROOT,
) -> tuple[subprocess.CompletedProcess, list[dict[str, Any]]]:
    """Replay one scenario's compiled flow fixture through a real
    `kitsoki test flows` run and return (process result, parsed trace
    events). Never touches an LLM or a network call -- `test_kind: flow`
    fixtures are pure state-machine + `host_cassette` replay
    (AGENTS.md: "Automated testing should never use a real LLM").

    `target=None` replays the harness-stub projection (back-compat); a real
    `target` name (`dev-story` / `pets-dev` / `slidey-dev`) replays that
    target's real `workbench:` room, run against the target's own app.yaml
    (`target_app_path`) rather than the harness stub.
    """
    flow_path = flow_fixture_path(corpus_dir, scenario_id, target)
    if not flow_path.exists():
        raise ScenarioNotFound(f"no compiled flow fixture for {scenario_id!r} at {flow_path}")
    app_path = target_app_path(target)

    with tempfile.TemporaryDirectory() as tmp:
        trace_out = Path(tmp) / f"{scenario_id}.trace.jsonl"
        kitsoki_bin = os.environ.get("KITSOKI_TEST_KITSOKI_BINARY", "").strip()
        cmd = (
            [kitsoki_bin, "test", "flows"]
            if kitsoki_bin
            else ["go", "run", "./cmd/kitsoki", "test", "flows"]
        )
        proc = subprocess.run(
            cmd + [
                str(app_path),
                "--flows", str(flow_path),
                "--trace-out", str(trace_out),
            ],
            cwd=str(kitsoki_root),
            capture_output=True,
            text=True,
            timeout=FLOW_TIMEOUT_SECONDS,
        )
        trace_events: list[dict[str, Any]] = []
        if trace_out.exists():
            for line in trace_out.read_text(encoding="utf-8").splitlines():
                line = line.strip()
                if not line:
                    continue
                trace_events.append(json.loads(line))
        return proc, trace_events


def build_record_for_cell(
    scenario_id: str,
    corpus_dir: Path,
    surface: str,
    *,
    evidence_dir: Path,
    target: str | None = None,
    kitsoki_root: Path = REPO_ROOT,
) -> dict[str, Any]:
    """Drive one (scenario, surface[, target]) cell end to end: replay the
    flow, pull the turn signals off the real trace, and join them against the
    scenario's own `abandoned` oracle via `build_parity_record` -- the exact
    join `usable_kitsoki_gate.py`'s `score()` performs for a raw-signal
    bundle, just invoked directly here instead of through an arena cell.
    """
    scenario = load_scenario(corpus_dir, scenario_id)
    proc, trace_events = run_scenario_flow(scenario_id, corpus_dir, target=target, kitsoki_root=kitsoki_root)
    turn_signals = gate_plugin.extract_turn_signals(trace_events)

    evidence_dir.mkdir(parents=True, exist_ok=True)
    evidence_name = f"{scenario_id}.{target}.{surface}.trace.jsonl" if target else f"{scenario_id}.{surface}.trace.jsonl"
    evidence_path = evidence_dir / evidence_name
    evidence_path.write_text(
        "\n".join(json.dumps(e, sort_keys=True) for e in trace_events)
        + ("\n" if trace_events else ""),
        encoding="utf-8",
    )

    notes = ""
    if proc.returncode != 0:
        blob = (proc.stdout + "\n" + proc.stderr).strip()
        notes = f"kitsoki test flows exited {proc.returncode}: {blob[:300]}"
    if target:
        notes = (f"target={target}. " + notes) if notes else f"target={target}"

    return gate_plugin.build_parity_record(
        scenario=scenario,
        surface=surface,
        turn_signals=turn_signals,
        evidence_refs=[str(evidence_path)],
        notes=notes,
    )
