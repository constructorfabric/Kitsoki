#!/usr/bin/env python3
"""No-LLM tests for OSS GLM-5.2 paired-task spec generation."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    print("SKIP: pyyaml not installed")
    sys.exit(0)

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/oss_to_arena_spec.py"
CORPUS = REPO_ROOT / "tools/arena/corpus/cost-bench.manifest.yaml"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    report = out / "report.json"
    spec = out / "oss-glm52.yaml"
    report.write_text(json.dumps({
        "required_glm52_matrix": [
            {
                "corpus": "oss-oracle",
                "task": "kitsoki-bug9-bugfix-test-repair",
                "treatment": "raw-prompt",
                "quality": "pending",
            },
            {
                "corpus": "oss-oracle",
                "task": "kitsoki-bug9-bugfix-test-repair",
                "treatment": "kitsoki",
                "quality": "partial",
            },
            {
                "corpus": "bugswarm",
                "task": "bugswarm-square-okio-140452393",
                "treatment": "raw-prompt",
                "quality": "pending",
            },
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--report-json",
            str(report),
            "--corpus",
            str(CORPUS),
            "--out",
            str(spec),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("generator exits zero", proc.returncode, 0)
    data = yaml.safe_load(spec.read_text(encoding="utf-8"))
    check("spec kind", data["job_type"], "paired-task")
    check("spec corpus", data["targets"][0]["corpus"], str(CORPUS))
    check("spec task", data["axes"]["task"], ["kitsoki-bug9-bugfix-test-repair"])
    check("one pending variant", [v["id"] for v in data["variants"]], ["raw-prompt-glm-5.2"])
    check("raw backend is claude", data["variants"][0]["backend"], "claude")

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    report = out / "report.json"
    spec = out / "bad.yaml"
    report.write_text(json.dumps({
        "required_glm52_matrix": [
            {
                "corpus": "oss-oracle",
                "task": "current-committed-glm52",
                "treatment": "raw-prompt",
                "quality": "pending",
            },
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--report-json",
            str(report),
            "--corpus",
            str(CORPUS),
            "--out",
            str(spec),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("unknown task exits nonzero", proc.returncode != 0, True)
    check("unknown task names problem", "not in the corpus manifest" in proc.stderr, True)

if failures:
    print("FAIL: oss to arena spec")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: OSS paired-task spec generation (no LLM, no Docker)")
