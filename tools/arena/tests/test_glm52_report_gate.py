#!/usr/bin/env python3
"""No-LLM tests for GLM-5.2 report claim gating."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/glm52_report_gate.py"
REPORT = REPO_ROOT / "docs/case-studies/bugswarm-glm52-bugfix-report.data.json"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def run_gate(path: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(SCRIPT), "--report-json", str(path)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )


def run_publish_gate(path: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(SCRIPT), "--report-json", str(path), "--require-publishable"],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )


proc = run_gate(REPORT)
check("current report passes gate", proc.returncode, 0)
check("current report pass message", "PASS: GLM-5.2 report gate" in proc.stdout, True)

proc = run_publish_gate(REPORT)
check("current report fails publishable gate", proc.returncode, 1)
check("publishable gate names claim ledger", "claim_ledger.status == 'publishable'" in proc.stdout, True)
check("publishable gate names pending cells", "zero pending GLM-5.2 headline cells" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-comparison.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["comparisons"]["overall"]["status"] = "complete"
    report["comparisons"]["overall"]["success_rate_delta"] = 0.25
    report["comparisons"]["overall"]["token_ratio_kitsoki_to_raw"] = 10.0
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad pending comparison exits nonzero", proc.returncode, 1)
    check("bad pending comparison names ratio", "must not publish token ratio while pending" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-bugswarm-closure.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    action = next(action for action in report["evidence_closure"]["actions"] if action["corpus"] == "bugswarm")
    action["status"] = "ready"
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad bugswarm closure exits nonzero", proc.returncode, 1)
    check("bad bugswarm closure names execute gate", "needs-execute-verification" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-source-mix.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    public = next(component for component in report["source_mix"]["oss_oracle"]["components"] if component["id"] == "pre_registered_oss_targets")
    public["repo_count"] = 12
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad source mix exits nonzero", proc.returncode, 1)
    check("bad source mix names 10 public targets", "10 public OSS target" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-claim-ledger.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    claim = next(claim for claim in report["claim_ledger"]["claims"] if claim["id"] == "overall-token-usage")
    claim["status"] = "supported"
    claim["missing_evidence"] = []
    report["claim_ledger"]["supported_count"] += 1
    report["claim_ledger"]["pending_count"] -= 1
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad claim ledger exits nonzero", proc.returncode, 1)
    check("bad claim ledger names pending overall", "overall-token-usage must remain pending" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-threats.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    threat = next(threat for threat in report["threats_to_validity"]["threats"] if threat["id"] == "missing-raw-glm52-arm")
    threat["severity"] = "low"
    report["threats_to_validity"]["high_count"] -= 1
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad threats exits nonzero", proc.returncode, 1)
    check("bad threats names missing raw", "missing raw GLM-5.2 arm" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-completion-audit.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["completion_audit"]["status"] = "complete"
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad completion audit exits nonzero", proc.returncode, 1)
    check("bad completion audit names pending evidence", "completion audit must remain incomplete" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-completion-next.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    missing = next(item for item in report["completion_audit"]["requirements"] if item["status"] == "missing")
    missing["next"] = ""
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad completion next exits nonzero", proc.returncode, 1)
    check("bad completion next names next step", "lacks next step" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-study-protocol-count.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["study_protocol"]["pending_cell_count"] = 0
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad study protocol count exits nonzero", proc.returncode, 1)
    check("bad study protocol count names matrix", "study protocol pending_cell_count does not match matrix" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-study-protocol-bugswarm.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["study_protocol"]["execution_steps"] = [
        step for step in report["study_protocol"]["execution_steps"]
        if step["id"] != "bugswarm-execute-verification"
    ]
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad study protocol bugswarm exits nonzero", proc.returncode, 1)
    check("bad study protocol bugswarm names execute", "must require BugSwarm execute verification" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-pending-tokens.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    row = next(row for row in report["required_glm52_matrix"] if row["quality"] == "pending")
    row["total_tokens"] = 123
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad pending tokens exits nonzero", proc.returncode, 1)
    check("bad pending tokens names problem", "pending row must not carry token evidence" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-references.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["references"]["bugswarm_seed"] = []
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad references exits nonzero", proc.returncode, 1)
    check("bad references names seed", "references missing BugSwarm seed provenance" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-repro-hash.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["reproducibility"]["generator"]["sha256"] = "0" * 64
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad reproducibility hash exits nonzero", proc.returncode, 1)
    check("bad reproducibility hash names mismatch", "reproducibility hash mismatch" in proc.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    bad = out / "bad-repro-publishable-command.json"
    report = json.loads(REPORT.read_text(encoding="utf-8"))
    report["reproducibility"]["validation_commands"] = [
        command
        for command in report["reproducibility"]["validation_commands"]
        if "--require-publishable" not in command
    ]
    bad.write_text(json.dumps(report), encoding="utf-8")
    proc = run_gate(bad)
    check("bad reproducibility command exits nonzero", proc.returncode, 1)
    check("bad reproducibility command names publishable gate", "publishable gate command" in proc.stdout, True)

if failures:
    print("FAIL: glm52 report gate")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: GLM-5.2 report gate tests (no LLM, no Docker)")
