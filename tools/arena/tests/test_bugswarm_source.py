#!/usr/bin/env python3
"""No-LLM tests for the reusable BugSwarm arena source adapter."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml  # type: ignore

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent.parent
SCRIPT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
SOURCES = REPO_ROOT / "tools/arena/corpus/sources.yaml"
SEED_ARTIFACTS = REPO_ROOT / "tools/arena/corpus/bugswarm.seed-artifacts.json"
SEED_SOURCE = REPO_ROOT / "tools/arena/corpus/bugswarm.seed.yaml"
STUDY_SOURCE = REPO_ROOT / "tools/arena/specs/bugfix-task-optimization-v1.yaml"
SHARED_CONFIG = REPO_ROOT / ".kitsoki.yaml"
LOCAL_CONFIG_TEMPLATE = REPO_ROOT / ".kitsoki.local.yaml.example"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


sources = yaml.safe_load(SOURCES.read_text(encoding="utf-8"))
bugswarm = next((s for s in sources.get("sources", []) if s.get("id") == "bugswarm"), None)
check("bugswarm source exists", bugswarm is not None, True)
check("bugswarm adapter path recorded", bugswarm.get("adapter") if bugswarm else "", "tools/arena/scripts/bugswarm_to_arena.py")
check("bugswarm verification applier path recorded", bugswarm.get("verification_applier") if bugswarm else "", "tools/arena/scripts/bugswarm_apply_verification.py")
check("bugswarm spec generator path recorded", bugswarm.get("spec_generator") if bugswarm else "", "tools/arena/scripts/bugswarm_to_arena_spec.py")
check("bugswarm oracle kind recorded", bugswarm.get("oracle_contract", {}).get("kind") if bugswarm else "", "bugswarm_fail_pass_pair")

seed_payload = yaml.safe_load(SEED_SOURCE.read_text(encoding="utf-8"))
check("seed source kind", seed_payload.get("kind"), "arena_bugswarm_source")
check("seed generated from artifacts", seed_payload.get("generated_from"), "tools/arena/corpus/bugswarm.seed-artifacts.json")
check("seed task count", seed_payload.get("task_count"), 13)
seed_task = seed_payload["tasks"][0]
check("seed task id", seed_task["id"], "bugswarm-square-okio-140452393")
check(
    "seed ticket preserves YAML-sensitive colon text",
    seed_task["ticket"],
    "Repair BugSwarm artifact square-okio-140452393: CI job 140452393 fails and job 140452394 passes.",
)
check("seed source url preserved", seed_task["meta"]["source_url"], "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/")
check("seed starts unverified red", seed_task["verified_red"], False)
check("seed starts unverified green", seed_task["verified_green"], False)

shared_profiles = yaml.safe_load(SHARED_CONFIG.read_text(encoding="utf-8")).get("harness_profiles", {})
check("shared mini profile pins exact model", shared_profiles.get("codex-gpt54-mini", {}).get("model"), "gpt-5.4-mini")
check("shared spark profile pins exact model", shared_profiles.get("codex-spark", {}).get("model"), "gpt-5.3-codex-spark")
check("shared Sonnet profile pins low effort", shared_profiles.get("claude-sonnet-low", {}).get("effort"), "low")
template_profiles = yaml.safe_load(LOCAL_CONFIG_TEMPLATE.read_text(encoding="utf-8")).get("harness_profiles", {})
check("local template pins hosted oss profile", template_profiles.get("syn-gpt-oss-120b", {}).get("model"), "hf:openai/gpt-oss-120b")

# 2026-07-11 growth (docs/proposals/bugfix-archetype-corpus-and-harness.md Slice 2):
# 12 real, filtered candidates added via the live BugSwarm REST API (bugswarm-common
# DatabaseAPI) alongside the original tutorial seed. Every new task must still be
# unverified (verification is a separate, Docker-gated step, Slice 3) and every
# image_tag must trace to a real BugSwarm artifact this session actually queried.
grown_tasks = seed_payload["tasks"][1:]
check("grown task count", len(grown_tasks), 12)
check("every grown task starts unverified red", all(t["verified_red"] is False for t in grown_tasks), True)
check("every grown task starts unverified green", all(t["verified_green"] is False for t in grown_tasks), True)
check("every grown task has a real github commit source_url", all(
    t["meta"]["source_url"].startswith("https://github.com/") and "/commit/" in t["meta"]["source_url"]
    for t in grown_tasks
), True)
grown_repos = {t["repo_label"] for t in grown_tasks}
check("grown tasks span 12 distinct repos", len(grown_repos), 12)
grown_langs = {t["meta"]["language"] for t in grown_tasks}
check("grown tasks cover both Java and Python", grown_langs, {"Java", "Python"})

# Stage-0 must keep missing model pins honest: a gpt-5.4-mini cell cannot
# silently fall back to the checked-in gpt-5.4 profile. The study source is
# declarative and never authorizes a live run by itself.
study = yaml.safe_load(STUDY_SOURCE.read_text(encoding="utf-8"))
check("study schema", study.get("schema"), "task-optimization/v1")
check("study starts live-blocked", study.get("live_status"), "blocked")
study_candidates = {candidate["key"]: candidate for candidate in study.get("candidates", [])}
check("study declares low Sonnet profile", study_candidates.get("sonnet-low", {}).get("profile"), "claude-sonnet-low")
check("study resolves exact gpt54-mini profile", study_candidates.get("gpt54-mini", {}).get("preflight"), "require-local-resolution")
check("study declares complete treatment ladder", study.get("treatments"), [
    "raw-agent", "strict-mcp-current", "strict-mcp-direct-driver",
    "strict-mcp-codeact-broad", "strict-mcp-codeact-decomposed",
    "strict-mcp-decomposed-fallback",
])
check("study defines unsupported result", study.get("preflight_contract", {}).get("unsupported_result"), "unsupported")

blocked_validate = subprocess.run(
    [sys.executable, str(REPO_ROOT / "tools/arena/arena.py"), "task-optimization", "validate", "--study", str(STUDY_SOURCE)],
    cwd=REPO_ROOT, text=True, capture_output=True,
)
check("blocked study validates without a fabricated corpus lock", blocked_validate.returncode, 0)
check("blocked study reports its boundary", "BLOCKED: bugfix-codeact-v1" in blocked_validate.stdout, True)

with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    src = tmpdir / "artifacts.json"
    out = tmpdir / "bugswarm.yaml"
    src.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": 140452393,
                "passed_job_id": 140452394,
                "language": "Java",
                "build_system": "Gradle",
                "classification": "code",
                "source_url": "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/",
            }
        ]
    }), encoding="utf-8")
    proc = subprocess.run(
        [sys.executable, str(SCRIPT), "--in", str(src), "--out", str(out)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("converter exits zero", proc.returncode, 0)
    payload = yaml.safe_load(out.read_text(encoding="utf-8"))
    check("converted kind", payload.get("kind"), "arena_bugswarm_source")
    check("converted task count", payload.get("task_count"), 1)
    task = payload["tasks"][0]
    check("task id stable", task["id"], "bugswarm-square-okio-140452393")
    check(
        "generated ticket preserves YAML-sensitive colon text",
        task["ticket"],
        "Repair BugSwarm artifact square-okio-140452393: CI job 140452393 fails and job 140452394 passes.",
    )
    check("repo label preserved", task["repo_label"], "square/okio")
    check("verified red initially false", task["verified_red"], False)
    check("verified green initially false", task["verified_green"], False)
    check("oracle kind", task["oracle"]["kind"], "bugswarm_fail_pass_pair")
    check("oracle image tag", task["oracle"]["image_tag"], "square-okio-140452393")
    check("metadata preserved", task["meta"]["build_system"], "Gradle")
    check("source url preserved", task["meta"]["source_url"], "https://www.bugswarm.org/docs/tutorials/setting-up-an-experiment/")

if failures:
    print("FAIL: bugswarm source")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: bugswarm source adapter (no LLM, no Docker)")
