#!/usr/bin/env python3
"""Drive stories/scenario-qa's already-resolved transport legs CONCURRENTLY
through the deterministic, no-LLM persona-QA replay-smoke path, folding each
leg's `CompletionState` into the drive_result/judge_result shape
`stories/scenario-qa/scripts/record_leg_result.star` expects.

This is the P2.10 "parallel legs via arena" seam (see
`.context/2026-07-10-scenario-persona-qa-productization-brief.md`, section
P2.10, and `stories/scenario-qa/rooms/parallel.yaml`). Fan-out for a persona-QA
check today lives in three places — run.py's own sequential loops, arena's
containerized persona-qa job type, and stories/scenario-qa's own one-leg-at-a-
time execute/judge/recording loop; this script is the piece that lets
scenario-qa's `check ... parallel=true` hand its resolved leg set to a
concurrent runner and fold the results back through the SAME report/deck code
path a serial run uses, without scenario-qa reimplementing arena's own
CompletionState contract.

## Why not literally invoke `arena.py run` (containers)?

`tools/arena/arena/plugins/persona_qa.py` already scores a `(target, persona,
scenario)` cell against `tools.persona_qa.load_product_journey_run` — the same
CompletionState bridge this script uses. But that plugin's cell axis has no
TRANSPORT dimension (a scenario-qa leg is `(scenario, transport)`, not just
`scenario`), and a real `arena run` cell always executes in a Docker
container — a heavyweight, non-default dependency this story's opt-in
`parallel=true` path should not force on every operator. This script reuses
arena's own CompletionState contract and fold semantics (see
`tools.persona_qa.completion.to_scenario_qa_leg_result`) at the concurrency
granularity scenario-qa actually needs (one job per LEG, run locally with a
thread pool) rather than wiring a new axis into the containerized plugin for a
proof that does not yet need per-transport visual evidence (see the SCOPE note
below). Promoting this to real containerized arena cells — one per transport
leg, sharing the browser-capable image so per-transport VISUAL evidence can be
captured too — is a natural follow-up once that fidelity is needed; the
CompletionState fold this script performs would not need to change to
support it, only how each cell is launched.

## Scope (deliberately honest, not silently overclaiming)

This path is transport-BLIND: `tools/persona_qa/kit.py replay-smoke` proves
the requested scenario's cassette-backed contract is drivable for
`(project, persona, scenario)`, not per-transport VISUAL evidence the way a
live, transport-pinned `product-journey-qa-driver` dispatch
(stories/scenario-qa/rooms/execute.yaml) does. A leg that genuinely needs
live/interpretive drive (see `plan_legs.star`'s `needs_live_hint`) still shows
up honestly as `degraded-evidence` with a stated cause via
`record_leg_result.star`'s existing fail-closed logic — `parallel=true` never
fabricates transport-specific evidence it did not capture, and it never opens
a live agent session (this is why it is exercisable with zero LLM spend by
construction, with no separate `--live` flag to gate). A live, transport-aware
parallel cell is a documented follow-up (see stories/scenario-qa/README.md).

## Contract

Reads `--legs-json` (a JSON array of resolved scenario-qa legs, or an object
with an `items` key — the same shape `world.legs` already has in the story).
Prints one JSON object to stdout (with `--json-output`):

    {"status": "ok", "leg_count": N, "records": [
        {"leg": <the input leg, echoed verbatim>,
         "drive_result": {...}, "judge_result": {...}},
        ...
    ]}

`records` is ordered to match the input legs. Every record's `leg` is the
SAME object stories/scenario-qa/rooms/parallel.yaml already has in
`world.legs` — record_leg_result.star's batch mode (`legs_batch:` input) reads
it directly, so this script never needs its own copy of that Starlark's
cause/evidence-level logic.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import subprocess
import sys
from pathlib import Path
from typing import Any

# tools/arena/scripts/run_scenario_qa_legs_parallel.py -> parents[3] == REPO_ROOT.
ROOT = Path(__file__).resolve().parents[3]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from tools.persona_qa.completion import load_product_journey_run, to_scenario_qa_leg_result  # noqa: E402

KIT_CLI = ROOT / "tools" / "persona_qa" / "kit.py"


def _run_leg(leg: dict[str, Any], *, project: str, persona: str, index: int) -> dict[str, Any]:
    """Run one leg's deterministic replay-smoke cell and fold its
    CompletionState into the drive_result/judge_result shape record_leg_
    result.star expects. Never raises — every failure mode (a crashed
    subprocess, a run bundle that never printed a run_dir, a malformed run
    bundle) becomes an honest `blocked`/`degraded-evidence` record instead of
    propagating and losing every OTHER leg's result.
    """

    leg_id = str(leg.get("leg_id") or leg.get("scenario") or f"leg-{index}")
    scenario_id = str(leg.get("scenario") or "bugfix")
    seed = f"arena-parallel-{leg_id}"
    cmd = [
        sys.executable, str(KIT_CLI), "replay-smoke",
        "--project", project or "gears-rust",
        "--persona", persona or "core-maintainer",
        "--scenario", scenario_id,
        "--seed", seed,
        "--json-output",
    ]
    proc = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True)
    run_dir = _extract_run_dir(proc.stdout)
    if run_dir is None:
        blob = f"{proc.stdout}\n{proc.stderr}"
        return {
            "leg": leg,
            "drive_result": {
                "status": "blocked",
                "evidence_refs": [],
                "blockers": [
                    f"arena parallel-leg cell for '{leg_id}' produced no run_dir (exit_code={proc.returncode})"
                ],
                "harness_used": "replay",
                "summary": _first_line(blob),
            },
            "judge_result": {
                "verdict": "degraded-evidence",
                "summary": "no run bundle produced by the parallel replay-smoke cell",
                "cited_frames": [],
            },
        }

    try:
        state = load_product_journey_run(run_dir)
    except (OSError, json.JSONDecodeError) as exc:
        return {
            "leg": leg,
            "drive_result": {
                "status": "blocked",
                "evidence_refs": [],
                "blockers": [f"could not load run bundle at {run_dir}: {exc}"],
                "harness_used": "replay",
                "summary": str(exc),
            },
            "judge_result": {
                "verdict": "degraded-evidence",
                "summary": f"run bundle at {run_dir} could not be read",
                "cited_frames": [],
            },
        }

    folded = to_scenario_qa_leg_result(leg, state)
    return {"leg": leg, **folded}


def _extract_run_dir(stdout: str) -> str | None:
    """The only thing read from stdout — a path, not a verdict. Mirrors
    tools/arena/arena/plugins/persona_qa.py's `_extract_run_dir`."""

    for line in reversed(stdout.splitlines()):
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            data = json.loads(line)
        except json.JSONDecodeError:
            continue
        run_dir = data.get("run_dir") or (data.get("run") or {}).get("run_dir")
        if run_dir:
            return str(run_dir)
    return None


def _first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:200]
    return ""


def run_legs_parallel(legs: list[dict[str, Any]], *, project: str, persona: str, concurrency: int) -> list[dict[str, Any]]:
    records: list[dict[str, Any] | None] = [None] * len(legs)
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, concurrency)) as pool:
        futures = {
            pool.submit(_run_leg, leg, project=project, persona=persona, index=i): i
            for i, leg in enumerate(legs)
        }
        for future in concurrent.futures.as_completed(futures):
            i = futures[future]
            records[i] = future.result()
    return records  # type: ignore[return-value]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--legs-json", required=True, help="JSON array (or {'items': [...]})  of resolved scenario-qa legs")
    parser.add_argument("--project", default="gears-rust", help="project/target id")
    parser.add_argument("--persona", default="core-maintainer", help="persona id")
    parser.add_argument("--concurrency", type=int, default=4, help="max concurrent legs")
    parser.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    raw = json.loads(args.legs_json)
    legs = raw.get("items", []) if isinstance(raw, dict) else raw
    if not isinstance(legs, list):
        raise SystemExit("--legs-json must decode to a list or {'items': [...]}")

    records = run_legs_parallel(legs, project=args.project, persona=args.persona, concurrency=args.concurrency)

    result = {"status": "ok", "leg_count": len(records), "records": records}
    if args.json_output:
        print(json.dumps(result, sort_keys=True))
    else:
        print(f"Ran {len(records)} scenario-qa legs in parallel.")
        for record in records:
            leg = record.get("leg", {})
            print(f"- {leg.get('leg_id', '?')}: driver={record['drive_result']['status']} verdict={record['judge_result']['verdict']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
