"""usable-kitsoki-gate job-type plugin — one arena cell = one (scenario x
surface) run of a mined scenario, per
docs/proposals/usable-kitsoki-release-gate.md Tasks 2 and 3.

Cell shape (Task 3.1, S4 landed): `scenario x surface`, one cell per
combination, enumerated the same way every other job type enumerates cells
(`arena.model.JobSpec.cells()`, target x variant x axes) — no bespoke
enumeration logic lives here. A spec's `targets_from` points at S4's
scenario-foundry IR corpus (a *directory* of `scn-*.json` documents, default
`tools/session-mining/calibration/`, the committed 18-scenario calibration
set — `arena.model.load_targets_from_corpus`'s directory branch turns each
IR document into one `Target`, `id` = the scenario id, `meta` carrying
`persona`/`goal`/`expected_effects`/`abandoned`/`provenance` verbatim); the
spec's `axes.surface` supplies `["web", "tui", "mcp"]`. Cross-producing those
gives one cell per scenario x surface — 18 scenarios x 3 surfaces = 54 cells
against the calibration set (see `specs/usable-kitsoki-gate-calibration.yaml`
and `tests/test_usable_kitsoki_gate_corpus.py`). `persona` is NOT a separate
axis to cross-multiply — a mined scenario's persona is a fixed property of
that scenario (baked into `target.meta["persona"]` from the IR document),
not an independent dimension it should be re-run under; `_coords()` reads it
from there when no explicit `persona` axis/meta override is present.

`drive_command()` dispatches on `axis["surface"]`:
  - "web" reuses the existing swarm-style harness convention: cd into
    tools/runstatus and run a Playwright spec via npx, mirroring
    `swarm.py`'s SPEC/RUNSTATUS_DIR pattern exactly.
  - "tui" / "mcp" dispatch into a workbench-driving runner script under
    `tools/usable-kitsoki-gate/` instead of a browser spec.

S1 (workbench producer contract) has landed (`internal/orchestrator/
workbench_gate_signal.go`) and S4 (scenario foundry / mined corpus) has
landed (`tools/session-mining/scenario_compiler.py` +
`tools/session-mining/calibration/`). Task 3.3's no-LLM half has now landed
two of the three harness entry points: `tools/usable-kitsoki-gate/
run_tui_gate.py` and `run_mcp_gate.py` both exist, reading this plugin's
exact `GATE_*` env var contract off `drive_command()` and driving each
scenario's compiled flow fixture through a real `kitsoki test flows` replay
(see `tools/usable-kitsoki-gate/flow_gate_runner.py`'s module docstring for
the mechanics and its documented honest gap: `stories/scenario-foundry-
harness` is not a `workbench:` room, so `candidate_completed` reads False
for every scenario today — a harness/join wiring proof, not yet a real
workbench-parity measurement; see `usable_kitsoki_gate_constants.py`'s
calibration-contact note). `tests/playwright/usable-kitsoki-gate-web.spec.ts`
(the real browser-driven web surface) still does not exist on disk — that
remains separately gated, larger, browser-specific work; `arena plan`/
`arena run` will fail to find it until it lands, which `score()` handles the
same way it handles any other harness crash: an `infra:*` health, never a
fabricated model verdict. `tools/usable-kitsoki-gate/run_calibration_gate.py`
is the standalone (non-arena, non-docker) sweep tool Task 4.2's checked-in
calibration report (`tests/fixtures/usable-kitsoki-gate/calibration-
report.json`) was produced from — it drives the same scenario x surface
cells at bounded concurrency without going through a container executor,
since the no-LLM contract needs no docker isolation. Task 3.3's LIVE half
has also landed: `drive_command(cell, live=True)` dispatches into
`tools/usable-kitsoki-gate/run_live_gate.py --live-gate`, which drives a
REAL agent against `stories/dev-story`'s real `workbench:` room (see that
script's module docstring for the double-gating -- `arena run --live` at
the top level, plus that script's own `--live-gate` argv flag, mirroring
`tools/swarm/tiers/liveExplorerCli.ts`) — real LLM spend, never run in CI,
manual-only per `tools/arena/README.md`'s existing live-path convention.

Scoring never regexes stdout for a verdict. It only reads the
`[usable-kitsoki-gate] wrote <path>` pointer line the harness is expected to
print (mirrors swarm.py's `[swarm] wrote <path>` convention), loads the
results bundle at that path, and reduces it into the three GATE_CONDITIONS
from `usable_kitsoki_gate_constants.py`. Two bundle shapes are accepted
(Task 3.2):

  1. Already-built parity-verdict records (`{"records": [...]}`, or a bare
     JSON list) — every field already schema-shaped. This is what Task 4.1's
     hand-written golden-regression fixtures use, and what any future
     harness MAY choose to write directly if it does its own join.
  2. A raw S1 signal bundle (`{"turn_signals": [...]}` or
     `{"trace_events": [...]}`) — the turn-level `usable_kitsoki_gate`
     payload(s) captured off a REAL session trace (`extract_turn_signals`
     pulls them out of raw `kind: "turn.end"` trace-event dicts), with no
     pre-built parity record at all. `score()` performs the actual S1 x S4
     join itself (`build_parity_record`): `source_completed` is read off
     THIS cell's own scenario (`cell.target.meta["abandoned"]`, the IR
     document `targets_from` already loaded it from — never re-derived,
     never re-judged by an LLM), `candidate_completed`/`silent_bounce`/
     `misroute_adjacent` are reduced across `turn_signals`, and every field
     S1 is honestly unable to compute yet (see
     `internal/orchestrator/workbench_gate_signal.go`'s HONESTY NOTE) is
     marked in the record's own `notes`, not fabricated.

Either way records are validated against `usable_kitsoki_gate_schema.json`
and reduced PER CELL. A cell in production now carries exactly one record
(one scenario x one surface), but the reduction groups by each record's own
`surface` field and gates on the MINIMUM per-surface parity
(WORST_SURFACE_GATING), not a flat aggregate, so a hand-written bundle
spanning more than one scenario/surface (golden regression fixtures, Task
4.1 -- see tests/fixtures/usable-kitsoki-gate/) is still reduced correctly.
True cross-CELL worst-surface gating (comparing separately-run cells against
each other across a real sweep) is still a higher rollup layer (Task 5,
gated on Task 3 landing, explicitly out of scope for this plugin).
"""

from __future__ import annotations

import json
import re
import shlex
import sys
from pathlib import Path
from typing import Any

# tools/arena/arena/plugins/usable_kitsoki_gate.py -> parents[4] == REPO_ROOT
# (mirrors bugfix.py's / swarm.py's / persona_qa.py's REPO_ROOT derivation).
REPO_ROOT = Path(__file__).resolve().parents[4]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from ..model import Cell, CellResult  # noqa: E402
from . import base  # noqa: E402
from . import usable_kitsoki_gate_constants as gate_constants  # noqa: E402

try:
    import jsonschema  # type: ignore
except ImportError:  # pragma: no cover - advisory only, mirrors test_usable_kitsoki_gate_schema.py
    jsonschema = None  # type: ignore[assignment]

# Path *inside the container* where the kitsoki checkout is mounted (mirrors
# bugfix.py's / swarm.py's / persona_qa.py's KITSOKI_MNT convention).
KITSOKI_MNT = "/workspace/kitsoki"
RUNSTATUS_DIR = f"{KITSOKI_MNT}/tools/runstatus"

# Not built yet (S1/S4 gated) -- see module docstring. Names fixed here so the
# eventual harness has one obvious place to land.
WEB_SPEC = "tests/playwright/usable-kitsoki-gate-web.spec.ts"
TUI_RUNNER = f"{KITSOKI_MNT}/tools/usable-kitsoki-gate/run_tui_gate.py"
MCP_RUNNER = f"{KITSOKI_MNT}/tools/usable-kitsoki-gate/run_mcp_gate.py"

# The live half of Task 3.3 (docs/proposals/usable-kitsoki-release-gate.md):
# a real agent drives a real `workbench:` room via
# `tools/usable-kitsoki-gate/run_live_gate.py` -- gated behind BOTH `arena
# run --live` (the top-level spend gate, `arena.py`'s `--live` flag) AND that
# script's own `--live-gate` argv flag (mirrors `liveExplorerCli.ts`'s
# structural gating: no env var fallback, no implicit default). Never run in
# CI -- see that script's module docstring.
LIVE_RUNNER = f"{KITSOKI_MNT}/tools/usable-kitsoki-gate/run_live_gate.py"

SURFACES = ("web", "tui", "mcp")
DEFAULT_SURFACE = "web"
# S4's committed, hand-checked calibration set (tools/session-mining/
# calibration/MANIFEST.md) — 18 scenario IR documents, one per file. This is
# the default corpus BOTH for `targets_from`-style cell enumeration (a spec
# points here, or overrides with any other directory of scenario IR docs)
# and for the `GATE_SCENARIO_CORPUS` env var threaded to the not-yet-built
# harness (Task 3.3), so a harness that needs to re-read a scenario's full
# IR document (turns, goal, expected_effects) by id can find it at
# `{GATE_SCENARIO_CORPUS}/{scenario_id}.json` without a second lookup path.
DEFAULT_SCENARIO_CORPUS = "tools/session-mining/calibration"

SCHEMA_PATH = Path(__file__).resolve().parent / "usable_kitsoki_gate_schema.json"

# `[usable-kitsoki-gate] wrote /abs/path/parity-records-<run_id>.json (N records)`
# -- the ONE line of stdout this plugin reads; everything else comes off disk.
_RESULTS_LINE_RE = re.compile(r"\[usable-kitsoki-gate\]\s+wrote\s+(\S+\.json)")

# Infra signals -- a harness crash (or a not-yet-built S1/S4 entry point)
# before any results file was ever written is not a model verdict. Kept ONLY
# as the fallback for when no results-path pointer is found in stdout at all.
_INFRA_RE = re.compile(
    r"traceback|no such file|permission denied|connection refused|"
    r"command not found|econnrefused|provider 5\d\d|modulenotfounderror",
    re.I,
)


class UsableKitsokiGatePlugin:
    name = "usable-kitsoki-gate"

    def image(self, cell: Cell) -> str:
        # Web/TUI surfaces are Playwright-driven (or, for TUI, a
        # workbench-driving spec that may still shell to a browser-hosted
        # runstatus terminal) and need the browser-capable image; MCP is a
        # headless stdio surface and does not. A `meta.image` override is
        # the escape hatch either way (mirrors swarm.py/persona_qa.py).
        override = cell.target.meta.get("image") or cell.variant.meta.get("image")
        if override:
            return str(override)
        surface = self._coords(cell)["surface"]
        if surface == "mcp":
            return "kitsoki-arena-repo-runtime:latest"
        return "kitsoki-arena-repo-runtime-browser:latest"

    def _coords(self, cell: Cell) -> dict[str, str]:
        surface = (
            cell.axis.get("surface")
            or str(cell.variant.meta.get("surface", ""))
            or DEFAULT_SURFACE
        )
        # persona is a fixed property of the scenario a cell was enumerated
        # for (Task 3.1: `target.meta["persona"]` came straight off S4's IR
        # document via `load_targets_from_corpus`'s directory branch), not an
        # independent axis to cross-multiply — so it is the LAST fallback,
        # behind an explicit axis or variant-meta override (kept for hand-
        # authored specs / existing tests that set persona explicitly).
        persona = (
            cell.axis.get("persona")
            or str(cell.variant.meta.get("persona", ""))
            or str(cell.target.meta.get("persona", ""))
        )
        return {
            "surface": surface,
            "persona": persona,
            "scenario_id": cell.target.id,
            "scenario_corpus": (
                cell.axis.get("scenario_corpus")
                or str(cell.variant.meta.get("scenario_corpus", ""))
                or DEFAULT_SCENARIO_CORPUS
            ),
            "run_id": cell.axis.get("run_id") or _sanitize(cell.id),
        }

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        coords = self._coords(cell)
        surface = coords["surface"]
        results_path = (
            f"{KITSOKI_MNT}/.artifacts/usable-kitsoki-gate/{coords['run_id']}/parity-records.json"
        )

        env: list[str] = [
            f"GATE_SURFACE={shlex.quote(surface)}",
            f"GATE_SCENARIO_CORPUS={shlex.quote(coords['scenario_corpus'])}",
            f"GATE_RUN_ID={shlex.quote(coords['run_id'])}",
            f"GATE_RESULTS_PATH={shlex.quote(results_path)}",
        ]
        if coords["scenario_id"]:
            env.append(f"GATE_SCENARIO_ID={shlex.quote(coords['scenario_id'])}")
        if coords["persona"]:
            env.append(f"GATE_PERSONA={shlex.quote(coords['persona'])}")

        if live:
            # Task 3.3's live half: a real agent drives a real `workbench:`
            # room (see run_live_gate.py's module docstring). Surface-
            # agnostic today -- the live runner's target app has one
            # workbench room, not a per-surface driver, mirroring the
            # no-LLM path's own documented "all three surfaces drive the
            # identical mechanism today" simplification. `--live-gate` is
            # this composed command's OWN second gate (on top of the
            # `arena run --live` gate that got drive_command() called with
            # live=True at all); nothing here ever fires without both.
            script = (
                f"set -euo pipefail; cd {shlex.quote(KITSOKI_MNT)} && "
                f"{' '.join(env)} python3 {shlex.quote(LIVE_RUNNER)} --live-gate"
            )
            return ["bash", "-lc", script]

        if surface == "web":
            script = (
                f"set -euo pipefail; cd {shlex.quote(RUNSTATUS_DIR)} && "
                f"{' '.join(env)} npx playwright test {shlex.quote(WEB_SPEC)}"
            )
        else:
            runner = TUI_RUNNER if surface == "tui" else MCP_RUNNER
            script = (
                f"set -euo pipefail; cd {shlex.quote(KITSOKI_MNT)} && "
                f"{' '.join(env)} python3 {shlex.quote(runner)}"
            )
        return ["bash", "-lc", script]

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(
            cell_id=cell.id,
            job_type=self.name,
            target_id=cell.target.id,
            variant_id=cell.variant.id,
            axis=dict(cell.axis),
        )
        blob = f"{stdout}\n{stderr}"
        results_path = _extract_results_path(stdout)
        if results_path is None:
            if _INFRA_RE.search(blob):
                result.verdict = "blocked"
                result.health = "infra:harness"
                result.notes = _first_line(blob)
            else:
                result.verdict = "blocked"
                result.health = "infra:missing-results-path"
                result.notes = (
                    f"no usable-kitsoki-gate results path found in stdout (exit_code={exit_code}); "
                    "the harness did not honor the '[usable-kitsoki-gate] wrote <path>' contract"
                )
            return result

        host_path = _host_results_path(results_path)
        try:
            data = json.loads(host_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            result.verdict = "blocked"
            result.health = "infra:results-malformed"
            result.notes = f"could not load usable-kitsoki-gate results at {host_path}: {exc}"
            return result

        records = data.get("records") if isinstance(data, dict) else data
        if records is None and isinstance(data, dict) and ("turn_signals" in data or "trace_events" in data):
            # Task 3.2's real join: the harness wrote S1's raw per-turn
            # signal(s) instead of an already-built parity record. Perform
            # the S1 (candidate) x S4 (source) join here, against THIS
            # cell's own scenario -- the exact IR document `targets_from`
            # enumerated this cell from (Task 3.1), never re-read from disk,
            # never re-derived.
            turn_signals = data.get("turn_signals")
            if turn_signals is None:
                turn_signals = extract_turn_signals(data.get("trace_events") or [])
            evidence_refs = data.get("evidence_refs") or [str(host_path)]
            record = build_parity_record(
                scenario={**cell.target.meta, "id": cell.target.id},
                surface=self._coords(cell)["surface"],
                turn_signals=turn_signals,
                evidence_refs=evidence_refs,
                notes=str(data.get("notes", "")),
            )
            records = [record]

        if not isinstance(records, list):
            result.verdict = "blocked"
            result.health = "infra:results-malformed"
            result.notes = (
                f"usable-kitsoki-gate results at {host_path} did not contain a 'records' array "
                "(nor a 'turn_signals'/'trace_events' raw-signal bundle)"
            )
            return result

        return _rollup_from_records(result, records, str(host_path))


def extract_turn_signals(trace_events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Pull the `usable_kitsoki_gate` payload out of raw `kind: "turn.end"`
    trace-event dicts (the JSONL shape `internal/store/event.go`'s
    `TurnEnded` writes: `{"turn":N, "seq":N, "ts":..., "kind":"turn.end",
    "payload":{...}}`).

    Turns outside a `workbench:` room never carry this key at all
    (`workbenchGateSignal` returns nil for those,
    `internal/orchestrator/workbench_gate_signal.go`) -- such events are
    silently skipped rather than treated as a missing/failed signal, exactly
    matching that producer's own "byte-identical to before this change"
    contract for ordinary turns.
    """
    signals: list[dict[str, Any]] = []
    for event in trace_events:
        if not isinstance(event, dict) or event.get("kind") != "turn.end":
            continue
        payload = event.get("payload") or {}
        signal = payload.get("usable_kitsoki_gate") if isinstance(payload, dict) else None
        if isinstance(signal, dict):
            signals.append(signal)
    return signals


def build_parity_record(
    scenario: dict[str, Any],
    surface: str,
    turn_signals: list[dict[str, Any]],
    evidence_refs: list[str],
    *,
    notes: str = "",
) -> dict[str, Any]:
    """The real S1 (candidate) x S4 (source) join Task 3.2 was missing.

    `scenario` is a full scenario IR document (S4,
    `tools/session-mining/schema/scenario_ir.schema.json`) -- typically
    `cell.target.meta` plus `cell.target.id` back-filled as `"id"`, since
    Task 3.1's `load_targets_from_corpus` directory branch already loaded
    the IR document verbatim into the Target that cell was enumerated from.
    `turn_signals` is the list of per-turn `usable_kitsoki_gate` payload
    dicts this scenario's run produced (see `extract_turn_signals`).

    - `source_completed := not scenario["abandoned"]` -- the hidden oracle
      is S4's own `outcomes.py` satisfaction signal folded into the IR at
      compile time; it is read here, never re-derived and never re-judged
      by an LLM (docs/proposals/usable-kitsoki-release-gate.md's Event/
      format model).
    - `candidate_completed` reduces S1's per-turn signal across every turn:
      true only if a signal was captured for every turn AND none of them
      failed. Per turn, S1 (`workbench_gate_signal.go`) computes that signal
      one of two ways: a REAL join against the scenario's own
      `expected_effects` (true iff dispatch succeeded AND the workbench's own
      bound close-out note actually covers every expected effect) when the
      dispatching room's world carries a `<room>_expected_effects` var (S6's
      `flow_fixture_compiler.py` real-workbench projection seeds this on a
      compiled scenario's final turn); otherwise the narrower dispatch-only
      proxy (`candidate_completed == !dispatchFailed`) unchanged from before
      that join existed. This record cannot distinguish which of the two
      produced a given turn's signal (the payload does not carry which path
      fired) -- the record's `notes` say so explicitly rather than silently
      overclaiming a stronger join than may have actually happened for any
      one turn.
    - `silent_bounce` / `misroute_adjacent` are OR'd across turns.
      `misroute_adjacent` is hard-false from every S1 signal today (S1 does
      not compute it at all); this record inherits that honest absence
      rather than fabricating a verdict, and says so in `notes`.
    """
    if not evidence_refs:
        raise ValueError(
            "build_parity_record: evidence_refs must be non-empty (schema minItems=1) "
            "-- pass at least the run directory's own results/trace path; never fabricate one"
        )

    source_completed = not bool(scenario.get("abandoned", False))
    silent_bounce = any(bool(s.get("silent_bounce")) for s in turn_signals)
    misroute_adjacent = any(bool(s.get("misroute_adjacent")) for s in turn_signals)
    candidate_completed = bool(turn_signals) and all(
        bool(s.get("candidate_completed")) for s in turn_signals
    )

    caveats: list[str] = []
    if not turn_signals:
        caveats.append("no usable_kitsoki_gate turn signal was captured for this scenario run")
    else:
        caveats.append(
            "candidate_completed reduced from S1's per-turn signal, which is a real "
            "expected_effects join when the dispatching workbench room's world carries "
            "a <room>_expected_effects var, else the dispatch-success proxy -- this "
            "record cannot tell which fired for any one turn (see workbench_gate_signal.go)"
        )
    caveats.append(
        "misroute_adjacent is hard-false from S1 today (not computed, documented gap); "
        "this record inherits that honest absence rather than a fabricated verdict"
    )
    note_text = "; ".join(part for part in [notes, *caveats] if part).strip()

    return {
        "schema_version": "1.0.0",
        "scenario_id": scenario["id"],
        "persona": str(scenario.get("persona", "")),
        "surface": surface,
        "source_completed": source_completed,
        "candidate_completed": candidate_completed,
        "silent_bounce": silent_bounce,
        "misroute_adjacent": misroute_adjacent,
        "evidence_refs": list(evidence_refs),
        "notes": note_text,
    }


def _rollup_from_records(result: CellResult, records: list[Any], results_path: str) -> CellResult:
    """Reduce the cell's parity verdict records into the three
    GATE_CONDITIONS (usable_kitsoki_gate_constants.py) for THIS cell's
    (persona, surface) combination.

    In production a single cell's records all share one `surface` value (one
    cell = one persona x surface run, per module docstring), so grouping by
    surface here is a no-op superset of the plain aggregate. But nothing stops
    a hand-written parity-records bundle (golden regression fixtures, Task 4.1)
    or a future harness change from putting more than one surface's records in
    one bundle, and `usable_kitsoki_gate_constants.WORST_SURFACE_GATING` is a
    fixed design decision (never average across surfaces) -- so this reduction
    groups by `surface` and gates on the MINIMUM per-surface parity, not the
    flat aggregate, to be correct either way. True cross-CELL worst-surface
    gating (comparing separately-run cells against each other) is still a
    higher-layer (arena rollup) concern -- see module docstring -- this is
    only the within-bundle reduction.
    """
    schema_errors: list[str] = []
    if jsonschema is not None and SCHEMA_PATH.exists():
        schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
        validator = jsonschema.Draft7Validator(schema)
        for i, record in enumerate(records):
            for err in validator.iter_errors(record):
                schema_errors.append(f"record[{i}]: {err.message}")
    else:
        schema_errors.extend(_fallback_record_schema_errors(records))

    if schema_errors:
        result.verdict = "blocked"
        result.health = "infra:results-malformed"
        result.evidence_refs = [results_path]
        result.notes = (
            f"{len(schema_errors)} parity record(s) failed schema validation: "
            + "; ".join(schema_errors[:5])
        )
        return result

    silent_bounce_count = sum(1 for r in records if r.get("silent_bounce"))
    misroute_adjacent_count = sum(1 for r in records if r.get("misroute_adjacent"))
    source_completed_count = sum(1 for r in records if r.get("source_completed"))
    both_completed_count = sum(
        1 for r in records if r.get("source_completed") and r.get("candidate_completed")
    )
    parity_pct = gate_constants.parity_percent(both_completed_count, source_completed_count)

    # Per-surface breakdown -- the gate always reduces with min(), never
    # average, per WORST_SURFACE_GATING (usable_kitsoki_gate_constants.py).
    per_surface_totals: dict[str, dict[str, int]] = {}
    for r in records:
        surface = str(r.get("surface", ""))
        bucket = per_surface_totals.setdefault(surface, {"source": 0, "both": 0})
        if r.get("source_completed"):
            bucket["source"] += 1
            if r.get("candidate_completed"):
                bucket["both"] += 1
    per_surface_parity_percent = {
        surface: gate_constants.parity_percent(totals["both"], totals["source"])
        for surface, totals in per_surface_totals.items()
    }
    worst_surface_parity_pct = (
        min(per_surface_parity_percent.values()) if per_surface_parity_percent else 100.0
    )

    passes = gate_constants.gate_passes(
        silent_bounce_count=silent_bounce_count,
        misroute_adjacent_count=misroute_adjacent_count,
        worst_surface_parity_percent=worst_surface_parity_pct,
    )

    evidence_refs = {results_path}
    for r in records:
        for ref in r.get("evidence_refs") or []:
            evidence_refs.add(str(ref))

    result.verdict = "solved" if passes else "failed"
    result.health = "model:result"
    result.trace_ref = results_path
    result.evidence_refs = sorted(evidence_refs)
    result.metrics.update({
        "record_count": len(records),
        "silent_bounce_count": silent_bounce_count,
        "misroute_adjacent_count": misroute_adjacent_count,
        "source_completed_count": source_completed_count,
        "both_completed_count": both_completed_count,
        "parity_percent": parity_pct,
        "worst_surface_parity_percent": worst_surface_parity_pct,
        "per_surface_parity_percent": per_surface_parity_percent,
    })
    result.notes = (
        f"{result.verdict}: {len(records)} record(s), parity={parity_pct:.1f}% overall, "
        f"worst_surface_parity={worst_surface_parity_pct:.1f}% "
        f"(threshold {gate_constants.PARITY_THRESHOLD_PERCENT:.1f}%), "
        f"silent_bounce={silent_bounce_count}, misroute_adjacent={misroute_adjacent_count}"
    )
    return result


def _fallback_record_schema_errors(records: list[Any]) -> list[str]:
    """Small required-field/type gate for minimal environments without jsonschema."""
    required = {
        "schema_version": str,
        "scenario_id": str,
        "persona": str,
        "surface": str,
        "source_completed": bool,
        "candidate_completed": bool,
        "silent_bounce": bool,
        "misroute_adjacent": bool,
        "evidence_refs": list,
    }
    errors: list[str] = []
    for i, record in enumerate(records):
        if not isinstance(record, dict):
            errors.append(f"record[{i}]: expected object")
            continue
        for key, typ in required.items():
            if key not in record:
                errors.append(f"record[{i}]: {key!r} is a required property")
                continue
            if not isinstance(record[key], typ):
                errors.append(f"record[{i}]: {key!r} has wrong type")
        if record.get("schema_version") != "1.0.0":
            errors.append(f"record[{i}]: schema_version must be '1.0.0'")
        if record.get("surface") not in {"web", "tui", "mcp"}:
            errors.append(f"record[{i}]: surface must be web, tui, or mcp")
        refs = record.get("evidence_refs")
        if isinstance(refs, list):
            if len(refs) == 0:
                errors.append(f"record[{i}]: evidence_refs must be non-empty")
            elif not all(isinstance(ref, str) for ref in refs):
                errors.append(f"record[{i}]: evidence_refs items must be strings")
    return errors


def _extract_results_path(stdout: str) -> str | None:
    """Pull the results-file path out of the harness's own
    `[usable-kitsoki-gate] wrote <path> (...)` stdout line -- a path, not a
    verdict (see module docstring).
    """
    match = None
    for line in stdout.splitlines():
        m = _RESULTS_LINE_RE.search(line)
        if m:
            match = m.group(1)
    return match


def _host_results_path(container_path: str) -> Path:
    """Map a results path as printed INSIDE the container back to the host
    path this process can read (mirrors swarm.py's `_host_results_path`
    convention: for `local` placement the executor mounts REPO_ROOT at
    KITSOKI_MNT, so a file written under KITSOKI_MNT/... is readable here at
    the equivalent REPO_ROOT/... path). Paths that don't carry the
    KITSOKI_MNT prefix (e.g. a test calling score() directly against a temp
    dir) are returned unchanged.
    """
    if container_path.startswith(KITSOKI_MNT + "/"):
        return REPO_ROOT / container_path[len(KITSOKI_MNT) + 1:]
    return Path(container_path)


def _sanitize(value: str) -> str:
    """A cell id (e.g. `kitsoki--core-maintainer--surface:web`) contains `:`
    and other path-unfriendly characters; turn it into a safe path segment
    for use in the container results path.
    """
    return re.sub(r"[^A-Za-z0-9_.-]+", "-", value).strip("-") or "cell"


def _first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:200]
    return ""


base.register(UsableKitsokiGatePlugin())
