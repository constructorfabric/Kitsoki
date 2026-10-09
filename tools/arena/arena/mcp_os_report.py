#!/usr/bin/env python3
"""Deterministic baseline reporter for the MCP operating-system program.

The baseline deliberately scores *recorded current-toolbox behavior*.  It is
not a Studio runtime, a policy engine, or a live evaluation driver.  Its only
inputs are checked-in JSON replay fixtures, so regenerating a review bundle is
safe in tests and never contacts a provider.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from collections import Counter
from pathlib import Path
from typing import Any


SCHEMA = "mcp_os_baseline_report/v1"
CORPUS_SCHEMA = "mcp_os_corpus/v1"
CELLS_SCHEMA = "mcp_os_replay_cells/v1"
REQUIRED_KINDS = {
    "story_edit",
    "runtime_fix",
    "trace_diagnosis",
    "documentation_change",
    "managed_workspace_mutation",
}
SAFE = "safe"
UNSAFE_OBSERVED = "unsafe-observed"


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


def sha256(value: Any) -> str:
    return hashlib.sha256(canonical_bytes(value)).hexdigest()


def _load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path}: expected JSON object")
    return value


def load_inputs(corpus_dir: str | Path) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any], dict[str, Any]]:
    root = Path(corpus_dir)
    return (
        _load(root / "corpus.json"),
        _load(root / "treatment.json"),
        _load(root / "policy.json"),
        _load(root / "cells.json"),
    )


def validate_inputs(corpus: dict[str, Any], treatment: dict[str, Any], policy: dict[str, Any], cells: dict[str, Any]) -> None:
    if corpus.get("schema_version") != CORPUS_SCHEMA:
        raise ValueError("unsupported corpus schema")
    cases = corpus.get("cases")
    if not isinstance(cases, list) or len(cases) != 12:
        raise ValueError("baseline corpus must contain exactly 12 cases")
    ids = [case.get("id") for case in cases if isinstance(case, dict)]
    if len(ids) != len(cases) or any(not isinstance(case_id, str) or not case_id for case_id in ids):
        raise ValueError("every corpus case needs a non-empty id")
    if len(set(ids)) != len(ids):
        raise ValueError("corpus case ids must be unique")
    kinds = {case.get("kind") for case in cases if isinstance(case, dict)}
    missing_kinds = REQUIRED_KINDS - kinds
    if missing_kinds:
        raise ValueError(f"baseline corpus missing required kinds: {sorted(missing_kinds)}")
    if treatment.get("id") != "current-toolbox" or treatment.get("mode") != "observation-only":
        raise ValueError("baseline treatment must remain current-toolbox observation-only")
    if policy.get("profile") != "baseline-observe-only" or policy.get("provider_calls") != "forbidden":
        raise ValueError("baseline policy must forbid provider calls")
    if cells.get("schema_version") != CELLS_SCHEMA:
        raise ValueError("unsupported replay cell schema")
    if cells.get("treatment_id") != treatment["id"] or cells.get("policy_profile") != policy["profile"]:
        raise ValueError("replay cells do not match treatment/policy")
    provenance = cells.get("provenance")
    if not isinstance(provenance, dict):
        raise ValueError("replay cells missing provenance")
    expected_hashes = {
        "corpus_sha256": sha256(corpus),
        "treatment_sha256": sha256(treatment),
        "policy_sha256": sha256(policy),
    }
    for key, expected in expected_hashes.items():
        if provenance.get(key) != expected:
            raise ValueError(f"stale replay fixture: {key} does not match source")
    replay_cells = cells.get("cells")
    if not isinstance(replay_cells, list) or len(replay_cells) != len(cases):
        raise ValueError("replay cells must cover every corpus case exactly once")
    seen: set[str] = set()
    known = set(ids)
    for cell in replay_cells:
        if not isinstance(cell, dict):
            raise ValueError("replay cell must be an object")
        case_id = cell.get("case_id")
        if case_id not in known or case_id in seen:
            raise ValueError(f"inconsistent replay case id: {case_id!r}")
        seen.add(case_id)
        if cell.get("outcome") != "observed":
            raise ValueError(f"replay cell {case_id!r} must be observed, not a claimed promotion")
        if cell.get("safety") not in {SAFE, UNSAFE_OBSERVED}:
            raise ValueError(f"replay cell {case_id!r} has unsafe or unknown safety declaration")
        if not isinstance(cell.get("evidence_ref"), str) or not cell["evidence_ref"].startswith("replay://"):
            raise ValueError(f"replay cell {case_id!r} needs a replay evidence ref")
    if seen != known:
        raise ValueError("replay cells are incomplete")


def build_report(corpus_dir: str | Path) -> dict[str, Any]:
    corpus, treatment, policy, cells = load_inputs(corpus_dir)
    validate_inputs(corpus, treatment, policy, cells)
    case_by_id = {case["id"]: case for case in corpus["cases"]}
    rendered_cells: list[dict[str, Any]] = []
    for cell in cells["cells"]:
        case = case_by_id[cell["case_id"]]
        rendered_cells.append({
            "case_id": cell["case_id"],
            "kind": case["kind"],
            "outcome": cell["outcome"],
            "safety": cell["safety"],
            "evidence_ref": cell["evidence_ref"],
        })
    rendered_cells.sort(key=lambda cell: cell["case_id"])
    kinds = Counter(cell["kind"] for cell in rendered_cells)
    safety = Counter(cell["safety"] for cell in rendered_cells)
    unsafe_count = safety[UNSAFE_OBSERVED]
    return {
        "schema_version": SCHEMA,
        "baseline_label": corpus["label"],
        "treatment": {"id": treatment["id"], "label": treatment["label"], "mode": treatment["mode"]},
        "policy": {"profile": policy["profile"], "provider_calls": policy["provider_calls"]},
        "provenance": dict(cells["provenance"]),
        "summary": {
            "case_count": len(rendered_cells),
            "by_kind": dict(sorted(kinds.items())),
            "safe_count": safety[SAFE],
            "unsafe_observed_count": unsafe_count,
            "promotion_status": "hold",
            "promotion_reason": "Baseline observes current-toolbox escape hatches; it cannot promote an operating profile.",
        },
        "cells": rendered_cells,
    }


def validate_candidate_report(report: dict[str, Any]) -> None:
    """Reject a report that tries to represent unsafe evidence as promotable.

    The baseline itself is expected to be ``hold`` with recorded unsafe cells.
    Later candidate reports must pass this stricter check before claiming a
    promotion; keeping the two modes distinct prevents a convenient baseline
    green from becoming a policy claim.
    """
    if report.get("schema_version") != SCHEMA:
        raise ValueError("unsupported baseline report schema")
    cells = report.get("cells")
    if not isinstance(cells, list) or len(cells) != 12:
        raise ValueError("candidate report is incomplete")
    ids = [cell.get("case_id") for cell in cells if isinstance(cell, dict)]
    if len(set(ids)) != len(ids) or any(not case_id for case_id in ids):
        raise ValueError("candidate report cells are internally inconsistent")
    if any(cell.get("safety") != SAFE for cell in cells):
        raise ValueError("candidate report contains unsafe cells")
    if report.get("summary", {}).get("promotion_status") != "eligible":
        raise ValueError("candidate report cannot claim promotion without explicit eligibility")


def render_markdown(report: dict[str, Any]) -> str:
    summary = report["summary"]
    lines = [
        "# MCP Operating-System Baseline",
        "",
        f"- Treatment: `{report['treatment']['id']}` — {report['treatment']['label']}",
        f"- Policy: `{report['policy']['profile']}` (provider calls: `{report['policy']['provider_calls']}`)",
        f"- Cases: **{summary['case_count']}**",
        f"- Promotion: **{summary['promotion_status']}** — {summary['promotion_reason']}",
        "",
        "## Provenance",
        "",
        f"- corpus sha256: `{report['provenance']['corpus_sha256']}`",
        f"- treatment sha256: `{report['provenance']['treatment_sha256']}`",
        f"- policy sha256: `{report['provenance']['policy_sha256']}`",
        "",
        "## Recorded replay cells",
        "",
        "| case | kind | outcome | safety | evidence |",
        "|---|---|---|---|---|",
    ]
    for cell in report["cells"]:
        lines.append("| {case_id} | {kind} | {outcome} | {safety} | `{evidence_ref}` |".format(**cell))
    lines.extend(["", "## Interpretation", "", "This is a current-toolbox observation bundle, not an operating-profile promotion. Unsafe observations remain visible so later slices must eliminate or explicitly contain them before promotion.", ""])
    return "\n".join(lines)


def render_deck(report: dict[str, Any]) -> dict[str, Any]:
    summary = report["summary"]
    rows = [{"cells": [kind, str(count)]} for kind, count in summary["by_kind"].items()]
    unsafe = [cell["case_id"] for cell in report["cells"] if cell["safety"] == UNSAFE_OBSERVED]
    return {
        "_comment": "Generated by mcp_os_report.py from checked-in replay fixtures. Do not hand-edit derived review artifacts.",
        "meta": {"title": "MCP operating-system baseline", "resolution": {"width": 1920, "height": 1080}, "theme": "rose-pine-moon"},
        "scenes": [
            {"type": "title", "eyebrow": "MCP operating system", "title": "Current-toolbox baseline", "subtitle": f"{summary['case_count']} replay cells · promotion {summary['promotion_status']}"},
            {"type": "table", "title": "Corpus coverage", "columns": ["Kind", "Cases"], "rows": rows, "variant": "data"},
            {"type": "cards", "variant": "grid", "title": "Safety posture", "cards": [
                {"label": "Safe observations", "sub": str(summary["safe_count"]), "style": "primary"},
                {"label": "Unsafe observed", "sub": str(summary["unsafe_observed_count"]), "style": "secondary"},
                {"label": "Promotion", "sub": summary["promotion_status"], "style": "default"},
            ]},
            {"type": "list", "title": "Known escape-hatch observations", "items": unsafe or ["None recorded"]},
        ],
    }


def write_bundle(corpus_dir: str | Path, out_dir: str | Path) -> dict[str, str]:
    report = build_report(corpus_dir)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    paths = {
        "report_json": out / "report.json",
        "report_md": out / "report.md",
        "deck_slidey_json": out / "deck.slidey.json",
    }
    paths["report_json"].write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    paths["report_md"].write_text(render_markdown(report), encoding="utf-8")
    paths["deck_slidey_json"].write_text(json.dumps(render_deck(report), indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return {name: str(path) for name, path in paths.items()}


def replay_cell(corpus_dir: str | Path, case_id: str) -> dict[str, Any]:
    report = build_report(corpus_dir)
    for cell in report["cells"]:
        if cell["case_id"] == case_id:
            return cell
    raise ValueError(f"unknown MCP OS replay case {case_id!r}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("report", "replay"))
    parser.add_argument("--corpus", required=True, help="checked-in mcp-os corpus directory")
    parser.add_argument("--out", help="review bundle directory (required for report)")
    parser.add_argument("--case", dest="case_id", help="case id (required for replay)")
    args = parser.parse_args(argv)
    if args.command == "report":
        if not args.out:
            parser.error("report requires --out")
        print(json.dumps(write_bundle(args.corpus, args.out), sort_keys=True))
        return 0
    if not args.case_id:
        parser.error("replay requires --case")
    print(json.dumps(replay_cell(args.corpus, args.case_id), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
