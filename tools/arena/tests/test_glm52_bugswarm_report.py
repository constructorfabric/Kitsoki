#!/usr/bin/env python3
"""No-LLM tests for the generated GLM-5.2 + BugSwarm report."""

from __future__ import annotations

import json
import hashlib
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/glm52_bugswarm_report.py"
CONVERT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
VERIFY = REPO_ROOT / "tools/arena/scripts/bugswarm_verify_source.py"
APPLY = REPO_ROOT / "tools/arena/scripts/bugswarm_apply_verification.py"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    json_out = out / "report.json"
    md_out = out / "report.md"
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--generated-at",
            "2026-07-06T00:00:00Z",
            "--json-out",
            str(json_out),
            "--markdown-out",
            str(md_out),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("generator exits zero", proc.returncode, 0)
    report = json.loads(json_out.read_text(encoding="utf-8"))
    check("report kind", report.get("kind"), "glm52_bugswarm_bugfix_report")
    check("oss corpus task count", report["corpora"]["oss_oracle"]["task_count"], 26)
    check("bugswarm source status", report["corpora"]["bugswarm"]["source_status"], "adapter-ready")
    # 2026-07-11: real seed grew from 1 to 13 real, filtered candidates (docs/proposals/
    # bugfix-archetype-corpus-and-harness.md Slice 2) via the live BugSwarm REST API.
    # None are verified yet, so every downstream "pending" count below grows with it.
    check("bugswarm imported count", report["corpora"]["bugswarm"]["imported_task_count"], 13)
    source_mix = report["source_mix"]
    components = {component["id"]: component for component in source_mix["oss_oracle"]["components"]}
    check("source mix public target tasks", components["pre_registered_oss_targets"]["task_count"], 20)
    check("source mix public target repos", components["pre_registered_oss_targets"]["repo_count"], 10)
    check("source mix fixture tasks", components["armed_bugfix_fixtures"]["task_count"], 6)
    check("source mix fixture repos", components["armed_bugfix_fixtures"]["repo_count"], 2)
    check("source mix public oracle kind", components["pre_registered_oss_targets"]["oracle_kinds"], ["github_content"])
    check("source mix fixture oracle kind", components["armed_bugfix_fixtures"]["oracle_kinds"], ["external_bakeoff"])
    check("source mix bugswarm component", source_mix["bugswarm"]["component"], "containerized_fail_pass_ci_artifacts")
    closure = {action["corpus"]: action for action in report["evidence_closure"]["actions"]}
    check("closure oss ready to plan", closure["oss-oracle"]["status"], "ready-to-plan")
    check("closure bugswarm needs execute verification", closure["bugswarm"]["status"], "needs-execute-verification")

    glm_cells = report["glm52_bugfix_cells"]
    check("one committed glm cell", len(glm_cells), 1)
    check("glm cell treatment", glm_cells[0]["treatment"], "kitsoki")
    check("glm cell quality", glm_cells[0]["quality"], "partial")
    check("glm cell tokens", glm_cells[0]["total_tokens"], 2890980)
    matrix = report["required_glm52_matrix"]
    oss_tasks = sorted({row["task"] for row in matrix if row["corpus"] == "oss-oracle"})
    check("oss matrix uses reusable task id", oss_tasks, ["kitsoki-bug9-bugfix-test-repair"])
    oss_kitsoki = next(row for row in matrix if row["corpus"] == "oss-oracle" and row["treatment"] == "kitsoki")
    check("oss legacy task preserved", oss_kitsoki["legacy_task"], "bug9")

    headline = report["rollups"]["glm52_by_corpus_treatment"]
    check("oss kitsoki attempted", headline["oss-oracle|kitsoki"]["attempted"], 1)
    check("oss kitsoki success rate", headline["oss-oracle|kitsoki"]["success_rate"], 0.0)
    check("oss raw pending", headline["oss-oracle|raw-prompt"]["pending"], 1)
    check("bugswarm raw pending", headline["bugswarm|raw-prompt"]["pending"], 13)
    overall = report["rollups"]["glm52_by_treatment_overall"]
    check("overall kitsoki attempted", overall["kitsoki"]["attempted"], 1)
    check("overall raw attempted pending", overall["raw-prompt"]["attempted"], 0)
    check("overall raw pending count", overall["raw-prompt"]["pending"], 14)
    comparisons = report["comparisons"]
    check("overall comparison pending", comparisons["overall"]["status"], "pending")
    check("bugswarm comparison pending", comparisons["bugswarm"]["status"], "pending")
    check("overall comparison no token ratio", comparisons["overall"]["token_ratio_kitsoki_to_raw"], None)
    ledger = report["claim_ledger"]
    check("claim ledger partial", ledger["status"], "partial")
    check("claim ledger supported count", ledger["supported_count"], 3)
    check("claim ledger pending count", ledger["pending_count"], 3)
    claims = {claim["id"]: claim for claim in ledger["claims"]}
    check("claim overall token pending", claims["overall-token-usage"]["status"], "pending")
    check("claim overall success pending", claims["overall-success-rate"]["status"], "pending")
    check("claim bugswarm success pending", claims["bugswarm-success-rate"]["status"], "pending")
    check("claim bugswarm source supported", claims["bugswarm-reusable-source"]["status"], "supported")
    check("claim oss source mix supported", claims["oss-source-mix"]["status"], "supported")
    check("claim observed cell supported", claims["observed-oss-kitsoki-glm52-cell"]["status"], "supported")
    threats = report["threats_to_validity"]
    check("threats blocked", threats["status"], "blocked")
    check("threats active count", threats["active_count"], 5)
    check("threats high count", threats["high_count"], 2)
    threats_by_id = {threat["id"]: threat for threat in threats["threats"]}
    check("threat missing raw active", threats_by_id["missing-raw-glm52-arm"]["status"], "active")
    check("threat missing raw high", threats_by_id["missing-raw-glm52-arm"]["severity"], "high")
    check("threat bugswarm unverified high", threats_by_id["bugswarm-unverified-artifact"]["severity"], "high")
    audit = report["completion_audit"]
    check("completion audit incomplete", audit["status"], "incomplete")
    check("completion audit requirement count", audit["requirement_count"], 8)
    check("completion audit proven count", audit["proven_count"], 4)
    audit_requirements = {item["id"]: item for item in audit["requirements"]}
    check("audit report artifact proven", audit_requirements["report-artifact"]["status"], "proven")
    check("audit oss source proven", audit_requirements["oss-source"]["status"], "proven")
    check("audit bugswarm source proven", audit_requirements["bugswarm-source"]["status"], "proven")
    check("audit oss kitsoki proven", audit_requirements["oss-kitsoki-glm52"]["status"], "proven")
    check("audit bugswarm execute missing", audit_requirements["bugswarm-execute-verification"]["status"], "missing")
    check("audit oss raw missing", audit_requirements["oss-raw-glm52"]["status"], "missing")
    check("audit bugswarm kitsoki missing", audit_requirements["bugswarm-kitsoki-glm52"]["status"], "missing")
    check("audit bugswarm raw missing", audit_requirements["bugswarm-raw-glm52"]["status"], "missing")
    protocol = report["study_protocol"]
    check("study protocol pending", protocol["status"], "pending-evidence")
    check("study protocol candidate", protocol["candidate"], "glm-5.2")
    check("study protocol pending count", protocol["pending_cell_count"], 27)
    protocol_cells = {(cell["corpus"], cell["treatment"], cell["gate"]) for cell in protocol["pending_cells"]}
    check("study protocol oss raw gate", ("oss-oracle", "raw-prompt", "ready-to-plan") in protocol_cells, True)
    check("study protocol bugswarm kitsoki gate", ("bugswarm", "kitsoki", "execute-verify-bugswarm") in protocol_cells, True)
    protocol_steps = {step["id"]: step for step in protocol["execution_steps"]}
    check("study protocol oss step ready", protocol_steps["oss-raw-glm52"]["status"], "ready")
    check("study protocol bugswarm verification required", protocol_steps["bugswarm-execute-verification"]["status"], "required-before-live")
    check("study protocol no premature bugswarm live step", "bugswarm-glm52-cells" in protocol_steps, False)
    check("study protocol mentions claude backend", any("backend=claude" in item for item in protocol["live_controls"]), True)
    refs = report["references"]
    check("references include local evidence", refs["local_evidence"][0]["path"], "tools/bugfix-bakeoff/results/cells")
    check("references include bugswarm website", refs["upstream"][0]["url"], "https://www.bugswarm.org/")
    check("references include bugswarm rest api", refs["upstream"][2]["url"], "https://www.bugswarm.org/docs/toolset/bugswarm-rest-api/")
    check("references include seed provenance", refs["bugswarm_seed"][0]["url"], "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/")
    reproducibility = report["reproducibility"]
    check("reproducibility status", reproducibility["status"], "reproducible")
    check("reproducibility generator path", reproducibility["generator"]["path"], "tools/arena/scripts/glm52_bugswarm_report.py")
    check("reproducibility generator sha", len(reproducibility["generator"]["sha256"]), 64)
    artifact_paths = {
        artifact["path"]
        for artifact in reproducibility["artifacts"]
        if artifact.get("kind") == "file"
    }
    check("reproducibility includes corpus", "tools/arena/corpus/cost-bench.manifest.yaml" in artifact_paths, True)
    check("reproducibility includes sources", "tools/arena/corpus/sources.yaml" in artifact_paths, True)
    check("reproducibility includes bugswarm seed", "tools/arena/corpus/bugswarm.seed.yaml" in artifact_paths, True)
    bakeoff_glob = next(artifact for artifact in reproducibility["artifacts"] if artifact.get("kind") == "directory-glob")
    check("reproducibility bakeoff glob", bakeoff_glob["pattern"], "*glm-5.2*.json")
    check("reproducibility bakeoff cell", bakeoff_glob["files"][0]["path"], "tools/bugfix-bakeoff/results/cells/bug9-glm-5.2-kitsoki.json")
    check("reproducibility publishable validation", any("--require-publishable" in command for command in reproducibility["validation_commands"]), True)

    md = md_out.read_text(encoding="utf-8")
    check("markdown names pending raw arm", "oss-oracle | raw-prompt" in md, True)
    check("markdown includes overall rollup", "## Overall GLM-5.2 Treatment Rollup" in md, True)
    check("markdown includes source mix", "## Source Mix" in md, True)
    check("markdown source mix public target row", "| pre_registered_oss_targets | 20 | 10 | github_content" in md, True)
    check("markdown source mix fixture row", "| armed_bugfix_fixtures | 6 | 2 | external_bakeoff" in md, True)
    check("markdown source mix bugswarm row", "BugSwarm containerized_fail_pass_ci_artifacts" in md, True)
    check("markdown includes reproducibility ledger", "## Reproducibility Ledger" in md, True)
    check("markdown includes generator hash", "tools/arena/scripts/glm52_bugswarm_report.py` sha256" in md, True)
    check("markdown includes normal report gate", "glm52_report_gate.py --report-json" in md, True)
    check("markdown includes comparisons", "## Kitsoki vs Raw-Prompt Comparisons" in md, True)
    check("markdown includes claim ledger", "## Research Claim Ledger" in md, True)
    check("markdown includes publishable gate", "--require-publishable" in md, True)
    check("markdown claim ledger pending token", "| overall-token-usage | `pending`" in md, True)
    check("markdown claim ledger supported source", "| bugswarm-reusable-source | `supported`" in md, True)
    check("markdown includes threats", "## Threats To Validity" in md, True)
    check("markdown threats missing raw", "| missing-raw-glm52-arm | internal | `high` | `active`" in md, True)
    check("markdown includes completion audit", "## Completion Audit" in md, True)
    check("markdown audit includes execute verification", "bugswarm-execute-verification" in md, True)
    check("markdown audit includes oss raw", "oss-raw-glm52" in md, True)
    check("markdown includes study protocol", "## Study Protocol" in md, True)
    check("markdown protocol includes ready gate", "| oss-oracle | kitsoki-bug9-bugfix-test-repair | raw-prompt | `ready-to-plan` |" in md, True)
    check("markdown protocol includes execute gate", "| bugswarm | bugswarm-square-okio-140452393 | kitsoki | `execute-verify-bugswarm` |" in md, True)
    check("markdown marks overall comparison pending", "| overall | pending | 1 | 0 | n/a | n/a | Raw-prompt GLM-5.2 arm has no attempted cells. |" in md, True)
    check("markdown warns no token ratio", "must not compute a token ratio" in md, True)
    check("markdown includes closure packet", "## Evidence Closure Packet" in md, True)
    check("markdown includes gap planner", "glm52_gap_plan.py" in md, True)
    check("markdown uses default bugswarm source", "BugSwarm source: `tools/arena/corpus/bugswarm.seed.yaml`" in md, True)
    check("markdown includes provenance section", "## Provenance and References" in md, True)
    check("markdown includes bugswarm rest api reference", "https://www.bugswarm.org/docs/toolset/bugswarm-rest-api/" in md, True)
    check("markdown includes bugswarm tutorial reference", "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/" in md, True)
    check("markdown closure table includes oss ready", "| oss-oracle | `ready-to-plan`" in md, True)
    check("markdown closure table includes execute verification", "| bugswarm | `needs-execute-verification`" in md, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    artifacts = out / "artifacts.json"
    source = out / "bugswarm-source.yaml"
    verification = out / "bugswarm-verification.json"
    json_out = out / "with-bugswarm.json"
    md_out = out / "with-bugswarm.md"
    artifacts.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": "140452393",
                "passed_job_id": "140452394",
            }
        ]
    }), encoding="utf-8")
    subprocess.run([sys.executable, str(CONVERT), "--in", str(artifacts), "--out", str(source)], cwd=REPO_ROOT, check=True)
    subprocess.run([sys.executable, str(VERIFY), "--source", str(source), "--out", str(verification), "--dry-run"], cwd=REPO_ROOT, check=True)
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--generated-at",
            "2026-07-06T00:00:00Z",
            "--json-out",
            str(json_out),
            "--markdown-out",
            str(md_out),
            "--bugswarm-source",
            str(source),
            "--bugswarm-verification",
            str(verification),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("generator with bugswarm exits zero", proc.returncode, 0)
    report = json.loads(json_out.read_text(encoding="utf-8"))
    check("bugswarm imported with source", report["corpora"]["bugswarm"]["imported_task_count"], 1)
    check("bugswarm verification mode", report["corpora"]["bugswarm"]["verification_mode"], "dry-run")
    check("bugswarm verification count", report["corpora"]["bugswarm"]["verification_report_count"], 1)
    check("bugswarm dry-run verified count", report["corpora"]["bugswarm"]["verification_verified_count"], 0)
    closure = {action["corpus"]: action for action in report["evidence_closure"]["actions"]}
    check("closure bugswarm needs execute verification", closure["bugswarm"]["status"], "needs-execute-verification")
    md = md_out.read_text(encoding="utf-8")
    check("bugswarm source closure command", f"--bugswarm-source {source}" in md, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    artifacts = out / "artifacts.json"
    source = out / "bugswarm-source.yaml"
    verification = out / "bugswarm-verification.json"
    verified_source = out / "bugswarm-verified.yaml"
    rollup = out / "bugswarm-rollup.json"
    json_out = out / "with-bugswarm-rollup.json"
    md_out = out / "with-bugswarm-rollup.md"
    artifacts.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": "140452393",
                "passed_job_id": "140452394",
            }
        ]
    }), encoding="utf-8")
    subprocess.run([sys.executable, str(CONVERT), "--in", str(artifacts), "--out", str(source)], cwd=REPO_ROOT, check=True)
    verification.write_text(json.dumps({
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
        "mode": "execute",
        "task_count": 1,
        "verified_count": 1,
        "results": [
            {
                "task_id": "bugswarm-square-okio-140452393",
                "image_tag": "square-okio-140452393",
                "verified_red": True,
                "verified_green": True,
                "failed_exit_code": 1,
                "passed_exit_code": 0,
            }
        ],
    }), encoding="utf-8")
    subprocess.run(
        [
            sys.executable,
            str(APPLY),
            "--source",
            str(source),
            "--verification",
            str(verification),
            "--out",
            str(verified_source),
        ],
        cwd=REPO_ROOT,
        check=True,
    )
    rollup.write_text(json.dumps({
        "cells": [
            {
                "axis": {"task": "bugswarm-square-okio-140452393"},
                "cell_id": "bugswarm-verified--kitsoki-glm-5.2--task:bugswarm-square-okio-140452393",
                "evidence_refs": [str(verified_source) + "#bugswarm-square-okio-140452393"],
                "health": "model:result",
                "job_type": "paired-task",
                "metrics": {"cost_usd": 0.15, "tokens": 1000, "wall_s": 60.0},
                "notes": "synthetic fixture: oracle solved",
                "target_id": "bugswarm-verified",
                "trace_ref": "traces/kitsoki.jsonl",
                "variant_id": "kitsoki-glm-5.2",
                "verdict": "solved",
            },
            {
                "axis": {"task": "bugswarm-square-okio-140452393"},
                "cell_id": "bugswarm-verified--raw-prompt-glm-5.2--task:bugswarm-square-okio-140452393",
                "evidence_refs": [str(verified_source) + "#bugswarm-square-okio-140452393"],
                "health": "model:result",
                "job_type": "paired-task",
                "metrics": {"cost_usd": 0.03, "tokens": 200, "wall_s": 20.0},
                "notes": "synthetic fixture: oracle failed",
                "target_id": "bugswarm-verified",
                "trace_ref": "traces/raw.jsonl",
                "variant_id": "raw-prompt-glm-5.2",
                "verdict": "failed",
            },
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--generated-at",
            "2026-07-06T00:00:00Z",
            "--json-out",
            str(json_out),
            "--markdown-out",
            str(md_out),
            "--bugswarm-source",
            str(verified_source),
            "--bugswarm-verification",
            str(verification),
            "--bugswarm-arena-rollup",
            str(rollup),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("generator with bugswarm rollup exits zero", proc.returncode, 0)
    report = json.loads(json_out.read_text(encoding="utf-8"))
    check("bugswarm cell count", len(report["bugswarm_glm52_arena_cells"]), 2)
    headline = report["rollups"]["glm52_by_corpus_treatment"]
    check("bugswarm kitsoki attempted", headline["bugswarm|kitsoki"]["attempted"], 1)
    check("bugswarm kitsoki success", headline["bugswarm|kitsoki"]["success_rate"], 1.0)
    check("bugswarm kitsoki tokens", headline["bugswarm|kitsoki"]["total_tokens"], 1000)
    check("bugswarm raw attempted", headline["bugswarm|raw-prompt"]["attempted"], 1)
    check("bugswarm raw success", headline["bugswarm|raw-prompt"]["success_rate"], 0.0)
    check("bugswarm raw tokens", headline["bugswarm|raw-prompt"]["total_tokens"], 200)
    comparisons = report["comparisons"]
    check("bugswarm comparison complete", comparisons["bugswarm"]["status"], "complete")
    check("bugswarm comparison success delta", comparisons["bugswarm"]["success_rate_delta"], 1.0)
    check("bugswarm comparison token ratio", comparisons["bugswarm"]["token_ratio_kitsoki_to_raw"], 5.0)
    claims = {claim["id"]: claim for claim in report["claim_ledger"]["claims"]}
    check("bugswarm success claim supported", claims["bugswarm-success-rate"]["status"], "supported")
    audit = report["completion_audit"]
    audit_requirements = {item["id"]: item for item in audit["requirements"]}
    check("bugswarm execute audit proven", audit_requirements["bugswarm-execute-verification"]["status"], "proven")
    check("bugswarm kitsoki audit proven", audit_requirements["bugswarm-kitsoki-glm52"]["status"], "proven")
    check("bugswarm raw audit proven", audit_requirements["bugswarm-raw-glm52"]["status"], "proven")
    check("bugswarm fixture still incomplete without oss raw", audit["status"], "incomplete")
    protocol = report["study_protocol"]
    check("bugswarm complete protocol omits execute gate", any(cell["gate"] == "execute-verify-bugswarm" for cell in protocol["pending_cells"]), False)
    gaps = "\n".join(report["evidence_gaps"])
    check("bugswarm result gap absent", "Some imported BugSwarm tasks are missing" in gaps, False)
    md = md_out.read_text(encoding="utf-8")
    check("markdown includes bugswarm arena section", "Committed BugSwarm GLM-5.2 Arena Cells" in md, True)
    check("markdown includes bugswarm rollup input", "BugSwarm arena rollup" in md, True)
    check("markdown includes complete bugswarm comparison", "| bugswarm | complete | 1 | 1 | +1.000 | 5.000 | complete |" in md, True)

with tempfile.TemporaryDirectory() as tmp:
    out = Path(tmp)
    rollup = out / "oss-glm-rollup.json"
    json_out = out / "with-oss-rollup.json"
    md_out = out / "with-oss-rollup.md"
    rollup.write_text(json.dumps({
        "cells": [
            {
                "axis": {"task": "query-string-qs1-bugfix-test-repair"},
                "cell_id": "cost-bench-round2--kitsoki-glm-5.2--task:query-string-qs1-bugfix-test-repair",
                "evidence_refs": ["tools/arena/corpus/cost-bench.manifest.yaml#query-string-qs1-bugfix-test-repair"],
                "health": "model:result",
                "job_type": "paired-task",
                "metrics": {"cost_usd": 0.5, "tokens": 3000, "wall_s": 300.0},
                "notes": "synthetic fixture: oracle solved",
                "target_id": "cost-bench-round2",
                "trace_ref": "traces/oss-kitsoki.jsonl",
                "variant_id": "kitsoki-glm-5.2",
                "verdict": "solved",
            },
            {
                "axis": {"task": "query-string-qs1-bugfix-test-repair"},
                "cell_id": "cost-bench-round2--raw-prompt-glm-5.2--task:query-string-qs1-bugfix-test-repair",
                "evidence_refs": ["tools/arena/corpus/cost-bench.manifest.yaml#query-string-qs1-bugfix-test-repair"],
                "health": "model:result",
                "job_type": "paired-task",
                "metrics": {"cost_usd": 0.08, "tokens": 700, "wall_s": 80.0},
                "notes": "synthetic fixture: oracle failed",
                "target_id": "cost-bench-round2",
                "trace_ref": "traces/oss-raw.jsonl",
                "variant_id": "raw-prompt-glm-5.2",
                "verdict": "failed",
            },
            {
                "axis": {"task": "query-string-qs2-bugfix-test-repair"},
                "cell_id": "cost-bench-round2--kitsoki-codex-native--task:query-string-qs2-bugfix-test-repair",
                "health": "model:result",
                "job_type": "paired-task",
                "metrics": {"cost_usd": 0.1, "tokens": 999, "wall_s": 20.0},
                "target_id": "cost-bench-round2",
                "variant_id": "kitsoki-codex-native",
                "verdict": "solved",
            },
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "--generated-at",
            "2026-07-06T00:00:00Z",
            "--json-out",
            str(json_out),
            "--markdown-out",
            str(md_out),
            "--oss-arena-rollup",
            str(rollup),
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("generator with oss rollup exits zero", proc.returncode, 0)
    report = json.loads(json_out.read_text(encoding="utf-8"))
    check("oss arena glm cell count", len(report["oss_glm52_arena_cells"]), 2)
    headline = report["rollups"]["glm52_by_corpus_treatment"]
    check("oss kitsoki includes bakeoff plus arena", headline["oss-oracle|kitsoki"]["attempted"], 2)
    check("oss kitsoki token total includes both", headline["oss-oracle|kitsoki"]["total_tokens"], 2893980)
    check("oss raw attempted from arena", headline["oss-oracle|raw-prompt"]["attempted"], 1)
    check("oss raw tokens from arena", headline["oss-oracle|raw-prompt"]["total_tokens"], 700)
    comparisons = report["comparisons"]
    check("oss comparison complete with rollup", comparisons["oss-oracle"]["status"], "complete")
    check("oss comparison token ratio", comparisons["oss-oracle"]["token_ratio_kitsoki_to_raw"], 4134.257143)
    md = md_out.read_text(encoding="utf-8")
    check("markdown includes oss arena section", "Committed OSS GLM-5.2 Arena Cells" in md, True)
    check("markdown includes oss rollup input", "OSS arena GLM rollup" in md, True)

if failures:
    print("FAIL: glm52 bugswarm report")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: glm52 bugswarm report generator (no LLM, no Docker)")
