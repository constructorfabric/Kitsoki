#!/usr/bin/env python3
"""run_calibration_gate.py — Task 3.3 (no-LLM half) + Task 4.2
(docs/proposals/usable-kitsoki-release-gate.md), extended by S6
"no-llm-parity" to sweep the real workbench-target projection: sweep every
scenario in a corpus x every surface x every real workbench: target
(dev-story/pets-dev/slidey-dev by default -- see `DEFAULT_TARGETS`), driving
each (scenario, surface, target) cell through
`flow_gate_runner.build_record_for_cell` at bounded concurrency (mirroring
`tools/swarm/tiers/tier2.ts`'s tier-2 "several cells in flight, bounded pool"
shape -- see that file's `buildTier2RecordingAuto` for the TS-side sibling;
this is the no-LLM, no-docker, no-browser Python equivalent for the
flow-replay substrate this gate's no-LLM path actually has today), then
rolling every record up through the SAME `usable_kitsoki_gate_constants
.gate_passes()` / worst-surface-parity reduction the arena plugin's
`score()` uses for a single cell's bundle -- just applied here to the WHOLE
swept corpus at once, since a calibration run's whole point is one rollup
number across all 18 scenarios.

This is the tool `tests/test_usable_kitsoki_gate_calibration.py` uses to
regenerate `tools/arena/tests/fixtures/usable-kitsoki-gate/calibration
-report.json` and diff it against the checked-in copy (Task 4.2's
"checked-in, diffable parity report").

Usage:
  python3 run_calibration_gate.py --out /tmp/report.json
  python3 run_calibration_gate.py --corpus tools/session-mining/calibration \\
      --surfaces web,tui,mcp --concurrency 4 --out report.json
"""

from __future__ import annotations

import argparse
import json
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flow_gate_runner as runner  # noqa: E402

_ARENA_ROOT = runner.REPO_ROOT / "tools" / "arena"
if str(_ARENA_ROOT) not in sys.path:
    sys.path.insert(0, str(_ARENA_ROOT))

from arena import model as arena_model  # noqa: E402
from arena.plugins import usable_kitsoki_gate as gate_plugin  # noqa: E402
from arena.plugins import usable_kitsoki_gate_constants as gate_constants  # noqa: E402

DEFAULT_CONCURRENCY = 2

# The three real workbench: rooms this project ships (S6 "no-llm-parity"):
# dev-story is the hand-authored primary, pets-dev/slidey-dev import it
# unmodified (see flow_fixture_compiler.py's WORKBENCH_TARGETS registry /
# module docstring for the full contract). `None` means the old harness-stub
# projection (stories/scenario-foundry-harness, never a workbench: room --
# kept purely as a back-compat / schema-proving path, no longer the default).
DEFAULT_TARGETS: tuple[str, ...] = ("dev-story", "pets-dev", "slidey-dev")


def sweep(
    corpus_dir: Path,
    surfaces: list[str],
    *,
    run_id: str,
    evidence_dir: Path,
    concurrency: int = DEFAULT_CONCURRENCY,
    targets: list[str | None] | None = None,
) -> list[dict[str, Any]]:
    """Drive every (scenario, surface, target) cell in
    `corpus_dir x surfaces x targets` and return the parity records in
    deterministic (scenario_id, surface, target) order -- regardless of which
    worker finished first, so the checked-in calibration report diffs
    cleanly run to run.

    `targets` defaults to `DEFAULT_TARGETS` (all three real workbench: rooms
    this project ships) -- pass `[None]` to reproduce the old harness-stub-
    only sweep.
    """
    if targets is None:
        targets = list(DEFAULT_TARGETS)
    scenario_ids = runner.list_scenario_ids(corpus_dir)
    cells = [
        (sid, surface, target)
        for sid in scenario_ids
        for surface in surfaces
        for target in targets
    ]

    results: dict[tuple[str, str, str | None], dict[str, Any]] = {}
    with ThreadPoolExecutor(max_workers=max(1, concurrency)) as pool:
        futures = {
            pool.submit(
                runner.build_record_for_cell,
                sid, corpus_dir, surface,
                evidence_dir=evidence_dir, target=target,
            ): (sid, surface, target)
            for sid, surface, target in cells
        }
        for future in as_completed(futures):
            sid, surface, target = futures[future]
            results[(sid, surface, target)] = future.result()

    return [results[(sid, surface, target)] for sid, surface, target in cells]


def rollup(records: list[dict[str, Any]], *, results_path: str) -> dict[str, Any]:
    """Reduce the swept records through the exact same
    `usable_kitsoki_gate.py._rollup_from_records` machinery a single arena
    cell's bundle goes through -- reused directly rather than
    re-implemented, so the calibration number and a real cell's rollup can
    never silently drift apart.
    """
    result = arena_model.CellResult(
        cell_id="usable-kitsoki-gate-calibration",
        job_type="usable-kitsoki-gate",
        target_id="calibration-sweep",
        variant_id="flow-replay",
    )
    rolled = gate_plugin._rollup_from_records(result, records, results_path)  # noqa: SLF001
    return rolled.to_dict()


def _relativize(value: str) -> str:
    """Absolute evidence/trace paths are machine- and run-specific (they live
    under whatever `--out`/evidence dir a given invocation chose); a
    CHECKED-IN diffable report (Task 4.2) must not carry them verbatim or it
    would spuriously diff on every regeneration. Paths under REPO_ROOT are
    rewritten relative to it (stable across machines/checkouts); anything
    else (e.g. a caller-supplied absolute path outside the repo) is left
    alone rather than guessed at.
    """
    try:
        p = Path(value).resolve()
        return str(p.relative_to(runner.REPO_ROOT))
    except ValueError:
        return value


def _relativize_report(report: dict[str, Any]) -> dict[str, Any]:
    for record in report["records"]:
        record["evidence_refs"] = [_relativize(ref) for ref in record["evidence_refs"]]
    rollup_dict = report["rollup"]
    rollup_dict["evidence_refs"] = [_relativize(ref) for ref in rollup_dict["evidence_refs"]]
    rollup_dict["trace_ref"] = _relativize(rollup_dict["trace_ref"])
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", default=str(runner.DEFAULT_CORPUS))
    parser.add_argument("--surfaces", default=",".join(gate_plugin.SURFACES))
    parser.add_argument(
        "--targets", default=",".join(DEFAULT_TARGETS),
        help="comma-separated workbench targets to sweep (dev-story,pets-dev,slidey-dev by default). "
             "Pass 'harness' for the old non-workbench stub projection (back-compat, never a "
             "workbench: room -- see flow_gate_runner.py's module docstring).",
    )
    parser.add_argument("--concurrency", type=int, default=DEFAULT_CONCURRENCY)
    parser.add_argument("--run-id", default="calibration")
    parser.add_argument("--out", required=True)
    parser.add_argument(
        "--relative-evidence", action="store_true",
        help="rewrite evidence/trace paths relative to the repo root for a stable, diffable report "
        "(Task 4.2's checked-in calibration report; off by default since a normal harness run wants "
        "absolute paths it can actually open)",
    )
    args = parser.parse_args()

    corpus_dir = Path(args.corpus)
    if not corpus_dir.is_absolute():
        corpus_dir = runner.REPO_ROOT / corpus_dir
    surfaces = [s.strip() for s in args.surfaces.split(",") if s.strip()]
    targets: list[str | None] = [
        None if t.strip() == "harness" else t.strip()
        for t in args.targets.split(",") if t.strip()
    ]

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path
    evidence_dir = out_path.parent / f"{args.run_id}-evidence"

    records = sweep(
        corpus_dir, surfaces, run_id=args.run_id, evidence_dir=evidence_dir,
        concurrency=args.concurrency, targets=targets,
    )
    rolled = rollup(records, results_path=str(out_path))

    report = {
        "schema_version": "1.0.0",
        "run_id": args.run_id,
        "corpus": str(corpus_dir.relative_to(runner.REPO_ROOT)) if corpus_dir.is_relative_to(runner.REPO_ROOT) else str(corpus_dir),
        "surfaces": surfaces,
        "targets": [t if t else "harness" for t in targets],
        "scenario_count": len(runner.list_scenario_ids(corpus_dir)),
        "record_count": len(records),
        "parity_threshold_percent": gate_constants.PARITY_THRESHOLD_PERCENT,
        "gate_conditions": list(gate_constants.GATE_CONDITIONS),
        "rollup": rolled,
        "records": records,
    }
    if args.relative_evidence:
        report = _relativize_report(report)

    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=False) + "\n", encoding="utf-8")
    print(f"[usable-kitsoki-gate] wrote {out_path} ({len(records)} records)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
