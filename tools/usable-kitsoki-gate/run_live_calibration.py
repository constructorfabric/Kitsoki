#!/usr/bin/env python3
"""run_live_calibration.py — epic-finalization LIVE sweep: drives a small,
EXPLICITLY BOUNDED set of (scenario, target) cells through the SAME
double-gated entry point `run_live_gate.py` (`main()`, imported in-process,
never a bypass) and rolls the resulting parity records up through the exact
`_rollup_from_records` reduction `run_calibration_gate.py`'s no-LLM sweep
uses -- so a live rollup and a no-LLM rollup can never silently drift onto
different join/reduction logic.

This is the live-cost-bearing sibling of `run_calibration_gate.py`, NOT a
replacement for it: `run_calibration_gate.py` still sweeps the full
18-scenario x 3-surface x 3-target no-LLM grid for CI/regression purposes.
This script exists so a deliberate, operator-run LIVE gate pass over a
*small* set of cells -- exactly the epic's own release-readiness bar
(docs/proposals/usable-kitsoki.md's finalization work) -- has a single,
reusable, reproducible entry point instead of being a one-off hand-typed
loop that leaves no trace of what was actually swept.

## Cost discipline

Unlike `run_calibration_gate.py --targets` (which defaults to ALL three
workbench targets over the FULL corpus), this script requires
`--scenarios` explicitly -- there is no "sweep everything" default, because
every cell here is a real LLM spend (an orchestrator agent driving the MCP,
PLUS one real workbench dispatch per turn -- see `run_live_gate.py`'s module
docstring). Cells run strictly SEQUENTIALLY (no concurrency), one at a time,
so an operator watching the run can abort between cells and so two live
sessions never race the same `~/.kitsoki/sessions/<app_id>/` directory's
mtime-diff trace discovery.

## Usage

  python3 run_live_calibration.py --live-gate \\
      --scenarios scn-a-0000,scn-b-0000 \\
      --targets dev-story,pets-dev,slidey-dev \\
      --run-id live-epic-finalization \\
      --out .artifacts/usable-kitsoki-gate/live-epic-finalization/report.json

`--live-gate` is required here too (forwarded verbatim to every
`run_live_gate.main()` call) -- this script performs NO gate-bypassing of
its own; it is purely a bounded loop over the real gated entry point.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_live_gate  # noqa: E402
import flow_gate_runner as runner  # noqa: E402

_ARENA_ROOT = runner.REPO_ROOT / "tools" / "arena"
if str(_ARENA_ROOT) not in sys.path:
    sys.path.insert(0, str(_ARENA_ROOT))

from arena import model as arena_model  # noqa: E402
from arena.plugins import usable_kitsoki_gate as gate_plugin  # noqa: E402
from arena.plugins import usable_kitsoki_gate_constants as gate_constants  # noqa: E402


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--live-gate", action="store_true", dest="live_gate",
                         help="required: forwarded verbatim to every run_live_gate.main() call")
    parser.add_argument("--scenarios", required=True,
                         help="comma-separated scenario IDs (no default -- every cell is real spend)")
    parser.add_argument("--targets", default=",".join(sorted(run_live_gate.WORKBENCH_TARGETS)),
                         help="comma-separated workbench targets (default: all three shipped targets)")
    parser.add_argument("--surface", default="mcp",
                         help="surface tag stamped on every record (default: mcp -- the real surface these cells drive)")
    parser.add_argument("--corpus", default=str(runner.DEFAULT_CORPUS))
    parser.add_argument("--agent-cmd", default=run_live_gate.DEFAULT_AGENT_CMD)
    parser.add_argument("--run-id", default="live-epic-finalization")
    parser.add_argument("--out", required=True)
    return parser.parse_args(argv)


def sweep(
    scenario_ids: list[str],
    targets: list[str],
    *,
    surface: str,
    corpus_dir: Path,
    agent_cmd: str,
    run_id: str,
    live_gate: bool,
    cells_dir: Path,
) -> list[dict[str, Any]]:
    """Drives every (scenario, target) cell SEQUENTIALLY through
    `run_live_gate.main()` (in-process, but the identical `--live-gate`
    literal-flag contract a subprocess invocation would use), and returns
    the parity records in deterministic (scenario, target) order.
    """
    records: list[dict[str, Any]] = []
    cells_dir.mkdir(parents=True, exist_ok=True)

    env_keys = ("GATE_SURFACE", "GATE_SCENARIO_CORPUS", "GATE_SCENARIO_ID", "GATE_TARGET", "GATE_RUN_ID", "GATE_RESULTS_PATH")
    env_backup = {k: os.environ.get(k) for k in env_keys}
    try:
        for scenario_id in scenario_ids:
            for target in targets:
                cell_results_path = cells_dir / f"{scenario_id}.{target}.json"
                os.environ["GATE_SURFACE"] = surface
                os.environ["GATE_SCENARIO_CORPUS"] = str(corpus_dir)
                os.environ["GATE_SCENARIO_ID"] = scenario_id
                os.environ["GATE_TARGET"] = target
                os.environ["GATE_RUN_ID"] = run_id
                os.environ["GATE_RESULTS_PATH"] = str(cell_results_path)

                argv = ["--agent-cmd", agent_cmd]
                if live_gate:
                    argv.insert(0, "--live-gate")
                print(f"[usable-kitsoki-gate] live cell: scenario={scenario_id} target={target} surface={surface}",
                      file=sys.stderr)
                rc = run_live_gate.main(argv)
                if rc != 0:
                    raise RuntimeError(
                        f"run_live_gate.main() returned {rc} for scenario={scenario_id} target={target} "
                        "(refused or failed -- see stderr above)"
                    )
                cell_bundle = json.loads(cell_results_path.read_text(encoding="utf-8"))
                records.extend(cell_bundle["records"])
    finally:
        for k, v in env_backup.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v

    return records


def rollup(records: list[dict[str, Any]], *, results_path: str) -> dict[str, Any]:
    """Reduces the swept live records through the SAME `_rollup_from_records`
    machinery `run_calibration_gate.py`'s no-LLM sweep uses -- never a second,
    live-only reduction implementation."""
    result = arena_model.CellResult(
        cell_id="usable-kitsoki-gate-live-calibration",
        job_type="usable-kitsoki-gate",
        target_id="live-sweep",
        variant_id="mcp-live",
    )
    rolled = gate_plugin._rollup_from_records(result, records, results_path)  # noqa: SLF001
    return rolled.to_dict()


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    scenario_ids = [s.strip() for s in args.scenarios.split(",") if s.strip()]
    targets = [t.strip() for t in args.targets.split(",") if t.strip()]
    for t in targets:
        if t not in run_live_gate.WORKBENCH_TARGETS:
            print(f"[usable-kitsoki-gate] unknown target {t!r} (known: "
                  f"{', '.join(sorted(run_live_gate.WORKBENCH_TARGETS))})", file=sys.stderr)
            return 2

    corpus_dir = Path(args.corpus)
    if not corpus_dir.is_absolute():
        corpus_dir = runner.REPO_ROOT / corpus_dir

    out_path = Path(args.out)
    if not out_path.is_absolute():
        out_path = Path.cwd() / out_path
    cells_dir = out_path.parent / f"{args.run_id}-cells"

    records = sweep(
        scenario_ids, targets,
        surface=args.surface, corpus_dir=corpus_dir, agent_cmd=args.agent_cmd,
        run_id=args.run_id, live_gate=args.live_gate, cells_dir=cells_dir,
    )
    rolled = rollup(records, results_path=str(out_path))

    report = {
        "schema_version": "1.0.0",
        "run_id": args.run_id,
        "kind": "live",
        "corpus": str(corpus_dir.relative_to(runner.REPO_ROOT)) if corpus_dir.is_relative_to(runner.REPO_ROOT) else str(corpus_dir),
        "scenario_ids": scenario_ids,
        "targets": targets,
        "surface": args.surface,
        "record_count": len(records),
        "parity_threshold_percent": gate_constants.PARITY_THRESHOLD_PERCENT,
        "rollup": rolled,
        "records": records,
    }

    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, sort_keys=False) + "\n", encoding="utf-8")
    print(f"[usable-kitsoki-gate] wrote {out_path} ({len(records)} records)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
