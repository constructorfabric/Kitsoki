"""Arena's rollup — a thin delegating shim over the shared reporting module.

The bucket/build_rollup/write_rollup logic used to live here as a private copy
that mirrored product-journey's own rollup implementation without importing
it — two rollup brains drifting independently. It has been extracted to
`tools/persona_qa/reporting.py` as the single shared implementation
(generalized over completion-state-shaped records for both the bugfix and
persona-qa shapes); this module now only adapts arena's call sites to it,
keeping arena's title ("Arena rollup") and default axes for byte-compatible
output. See tools/persona_qa/tests/test_shared_rollup.py for the golden test
proving that byte-compatibility.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import json
import os
import sys

# tools/arena/arena/rollup.py -> parents[3] is the repo root, where
# tools/persona_qa lives as a sibling package to tools/arena.
_REPO_ROOT = Path(__file__).resolve().parents[3]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.persona_qa.reporting import build_rollup as _shared_build_rollup  # noqa: E402
from tools.persona_qa.reporting import write_rollup as _shared_write_rollup  # noqa: E402
from tools.persona_qa.reporting import _markdown as _shared_markdown  # noqa: E402

from .model import CellResult

_TITLE = "Arena rollup"


def build_rollup(results: list[CellResult]) -> dict[str, Any]:
    return _shared_build_rollup(results)


def write_rollup(results: list[CellResult], out_dir: str | Path) -> dict[str, str]:
    out = Path(out_dir)
    paths = _shared_write_rollup(results, out_dir, title=_TITLE)
    rollup = build_rollup(results)
    summary = build_summary(results, rollup, out)
    (out / "summary.json").write_text(
        json.dumps(summary, indent=2, sort_keys=True),
        encoding="utf-8",
    )
    (out / "report.md").write_text(_report_markdown(summary), encoding="utf-8")
    (out / "deck.slidey.json").write_text(
        json.dumps(_deck(summary), indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    paths["summary_json"] = str(out / "summary.json")
    paths["report"] = str(out / "report.md")
    paths["deck"] = str(out / "deck.slidey.json")
    cells_dir = out / "cells"
    cells_dir.mkdir(parents=True, exist_ok=True)
    for result in results:
        # One file per cell PER check_type (WS-G G1): the replay check keeps
        # the historical `<cell_id>.json` name; non-replay checks get a
        # `--check-<type>` suffix so a multi-check cell never overwrites itself.
        name = _safe_cell_id(result.cell_id)
        check_type = getattr(result, "check_type", "replay")
        if check_type != "replay":
            name = f"{name}--check-{_safe_cell_id(check_type)}"
        (cells_dir / f"{name}.json").write_text(
            json.dumps(result.to_dict(), indent=2, sort_keys=True),
            encoding="utf-8",
        )
    paths["cells"] = str(cells_dir)
    return paths


def build_summary(results: list[CellResult], rollup: dict[str, Any], out: Path) -> dict[str, Any]:
    cells = [r.to_dict() for r in results]
    treatment_rollups = rollup.get("by_variant", {})
    codeact_cells = [
        cell for cell in cells
        if "codeact" in str(cell.get("variant_id", "")).lower()
        or "codeact" in str(((cell.get("metrics") or {}).get("action_surface") or "")).lower()
    ]
    compliant = [
        cell for cell in codeact_cells
        if (((cell.get("metrics") or {}).get("permission_assertions") or {}).get("passed") is True)
        or str(((cell.get("metrics") or {}).get("codeact_surface") or "")).startswith("host.agent.codeact")
    ]
    return {
        "kind": "arena_run_summary",
        "run_id": out.name,
        "generated_at": os.environ.get("ARENA_GENERATED_AT", ""),
        "summary": rollup.get("summary", {}),
        "by_treatment": treatment_rollups,
        "by_target": rollup.get("by_target", {}),
        "by_job_type": rollup.get("by_job_type", {}),
        "permission_compliance": {
            "codeact_cells": len(codeact_cells),
            "compliant_cells": len(compliant),
            "rate": round(len(compliant) / len(codeact_cells), 4) if codeact_cells else None,
        },
        "cost_rollups": _cost_rollups(cells),
        "artifact_refs": {
            "rollup_json": "rollup.json",
            "rollup_md": "rollup.md",
            "report_md": "report.md",
            "deck_slidey_json": "deck.slidey.json",
            "cells_dir": "cells/",
        },
        "cells": cells,
    }


def _cost_rollups(cells: list[dict[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for cell in cells:
        treatment = str(cell.get("variant_id") or "")
        metrics = cell.get("metrics") or {}
        bucket = out.setdefault(treatment, {"tokens": 0, "cost_usd": 0.0, "cost_basis": {}})
        if isinstance(metrics.get("tokens"), int):
            bucket["tokens"] += metrics["tokens"]
        if isinstance(metrics.get("cost_usd"), (int, float)):
            bucket["cost_usd"] = round(bucket["cost_usd"] + float(metrics["cost_usd"]), 6)
        basis = str(metrics.get("cost_basis") or "unknown")
        bucket["cost_basis"][basis] = bucket["cost_basis"].get(basis, 0) + 1
    return out


def _report_markdown(summary: dict[str, Any]) -> str:
    s = summary.get("summary", {})
    lines = [
        "# Arena Run Report",
        "",
        f"- run: `{summary.get('run_id', '')}`",
        f"- cells: **{s.get('n', 0)}**",
        f"- win-rate: **{s.get('win_rate')}**",
        f"- infra failures: **{s.get('infra_failures', 0)}**",
        f"- CodeAct permission-compliance rate: **{summary.get('permission_compliance', {}).get('rate')}**",
        "",
        "## Treatments",
        "",
        "| treatment | n | win-rate | avg cost | verdicts |",
        "|---|---:|---:|---:|---|",
    ]
    for name, bucket in summary.get("by_treatment", {}).items():
        avg = bucket.get("avg_cost_usd")
        avg_text = "" if avg is None else f"${avg:.4f}"
        lines.append(f"| {name} | {bucket.get('n')} | {bucket.get('win_rate')} | {avg_text} | {bucket.get('verdicts')} |")
    lines.extend(["", "## Permission Evidence", ""])
    for cell in summary.get("cells", []):
        metrics = cell.get("metrics") or {}
        assertions = metrics.get("permission_assertions")
        if not assertions and "codeact" not in str(metrics.get("action_surface", "")).lower():
            continue
        lines.append(
            f"- `{cell.get('cell_id')}`: action_surface={metrics.get('action_surface', '')}, "
            f"capability_hash={metrics.get('capability_hash', '')}, "
            f"assertions_passed={(assertions or {}).get('passed', '')}"
        )
    lines.extend(["", "## Artifacts", ""])
    for key, ref in summary.get("artifact_refs", {}).items():
        lines.append(f"- {key}: `{ref}`")
    lines.append("")
    return "\n".join(lines)


def _deck(summary: dict[str, Any]) -> dict[str, Any]:
    treatments = summary.get("by_treatment", {})
    rows = [
        {
            "cells": [
                str(name),
                str(bucket.get("n")),
                str(bucket.get("win_rate")),
                "" if bucket.get("avg_cost_usd") is None else f"${bucket.get('avg_cost_usd'):.4f}",
            ]
        }
        for name, bucket in treatments.items()
    ]
    compliance = summary.get("permission_compliance", {})
    return {
        "_comment": "Generated deterministically by tools/arena/arena/rollup.py from summary.json/cell results. Re-run the arena report generator instead of hand-editing derived scenes.",
        "meta": {
            "title": f"Arena run {summary.get('run_id', '')}",
            "resolution": {"width": 1920, "height": 1080},
            "theme": "rose-pine-moon",
        },
        "scenes": [
            {
                "type": "title",
                "eyebrow": "Arena report",
                "title": f"Arena run {summary.get('run_id', '')}",
                "subtitle": f"{summary.get('summary', {}).get('n', 0)} cells · win-rate {summary.get('summary', {}).get('win_rate')}",
            },
            {
                "type": "table",
                "title": "Treatment leaderboard",
                "columns": ["Treatment", "Cells", "Win rate", "Avg cost"],
                "rows": rows,
                "variant": "data",
            },
            {
                "type": "cards",
                "variant": "grid",
                "title": "CodeAct compliance",
                "cards": [
                    {"label": "CodeAct cells", "sub": str(compliance.get("codeact_cells", 0)), "style": "primary"},
                    {"label": "Compliant", "sub": str(compliance.get("compliant_cells", 0)), "style": "secondary"},
                    {"label": "Rate", "sub": str(compliance.get("rate")), "style": "default"},
                ],
            },
        ],
    }


def _markdown(rollup: dict[str, Any]) -> str:
    # Kept for any existing direct callers/tests of arena's private markdown
    # helper; delegates to the shared renderer with arena's title.
    return _shared_markdown(rollup, title=_TITLE)


def _safe_cell_id(cell_id: str) -> str:
    return "".join(ch if ch.isalnum() or ch in "._-" else "-" for ch in cell_id)
