#!/usr/bin/env python3
"""Generate a paired-task arena spec for pending OSS GLM-5.2 report cells.

This script is offline and deterministic. It only reads the generated report and
the frozen OSS corpus manifest, then writes a schedulable JobSpec YAML.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("oss_to_arena_spec.py needs pyyaml")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report-json", required=True, help="JSON from glm52_bugswarm_report.py")
    parser.add_argument("--corpus", required=True, help="frozen OSS arena corpus manifest")
    parser.add_argument("--out", required=True, help="arena JobSpec YAML to write")
    parser.add_argument("--target-id", default="cost-bench")
    parser.add_argument("--target-label", default="OSS oracle cost bench")
    parser.add_argument("--image", default="kitsoki-arena/paired-task:latest")
    parser.add_argument("--candidate", default="glm-5.2")
    parser.add_argument("--kitsoki-backend", default="codex")
    parser.add_argument("--raw-backend", default="claude")
    args = parser.parse_args(argv)

    report = load_json(Path(args.report_json))
    corpus = load_yaml(Path(args.corpus))
    pending = pending_oss_rows(report)
    tasks = pending_tasks(pending, corpus)
    variants = variants_for_pending(
        pending,
        candidate=args.candidate,
        kitsoki_backend=args.kitsoki_backend,
        raw_backend=args.raw_backend,
    )
    spec = build_spec(
        corpus_path=args.corpus,
        tasks=tasks,
        variants=variants,
        target_id=args.target_id,
        target_label=args.target_label,
        image=args.image,
    )
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(spec, sort_keys=False), encoding="utf-8")
    print(f"wrote paired-task spec for {len(tasks)} OSS task(s) to {out}")
    return 0


def load_json(path: Path) -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    return data


def load_yaml(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    return data


def pending_oss_rows(report: dict[str, Any]) -> list[dict[str, Any]]:
    return [
        row
        for row in report.get("required_glm52_matrix", [])
        if isinstance(row, dict)
        and row.get("corpus") == "oss-oracle"
        and row.get("quality") == "pending"
    ]


def pending_tasks(rows: list[dict[str, Any]], corpus: dict[str, Any]) -> list[str]:
    requested = sorted({str(row.get("task") or "") for row in rows if row.get("task")})
    known = {
        str(task.get("id") or "")
        for task in corpus.get("tasks", [])
        if isinstance(task, dict) and task.get("id")
    }
    missing = sorted(set(requested) - known)
    if missing:
        raise ValueError("pending OSS task(s) are not in the corpus manifest: " + ", ".join(missing))
    return requested


def variants_for_pending(
    rows: list[dict[str, Any]],
    *,
    candidate: str,
    kitsoki_backend: str,
    raw_backend: str,
) -> list[dict[str, Any]]:
    variants: list[dict[str, Any]] = []
    treatments = sorted({normalize_treatment(str(row.get("treatment") or "")) for row in rows})
    if "kitsoki" in treatments:
        variants.append({
            "id": f"kitsoki-{candidate}",
            "treatment": "kitsoki",
            "candidate": candidate,
            "backend": kitsoki_backend,
            "model": candidate,
            "effort": "medium",
        })
    if "raw-prompt" in treatments:
        variants.append({
            "id": f"raw-prompt-{candidate}",
            "treatment": "single-briefed",
            "candidate": candidate,
            "backend": raw_backend,
            "model": candidate,
            "effort": "medium",
        })
    return variants


def normalize_treatment(value: str) -> str:
    lowered = value.strip().lower()
    if lowered.startswith("kitsoki"):
        return "kitsoki"
    if lowered.startswith("single") or lowered.startswith("raw-prompt") or lowered in {"raw", "raw-prompt"}:
        return "raw-prompt"
    return lowered


def build_spec(
    *,
    corpus_path: str,
    tasks: list[str],
    variants: list[dict[str, Any]],
    target_id: str,
    target_label: str,
    image: str,
) -> dict[str, Any]:
    return {
        "job_type": "paired-task",
        "targets": [
            {
                "id": target_id,
                "label": target_label,
                "image": image,
                "corpus": corpus_path,
            }
        ],
        "variants": variants,
        "axes": {
            "task": tasks,
        },
        "placement": {
            "hosts": ["local"],
            "concurrency": 1,
            "retry": 0,
        },
    }


if __name__ == "__main__":
    raise SystemExit(main())
