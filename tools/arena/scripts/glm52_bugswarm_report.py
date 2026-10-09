#!/usr/bin/env python3
"""Generate the GLM-5.2 + BugSwarm bugfix comparison report.

This is an offline report builder. It reads committed benchmark evidence and
corpus metadata, then emits JSON + Markdown. It never queries BugSwarm, starts
Docker, or calls an LLM. Missing cells remain explicit `pending` records.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections import Counter
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("glm52_bugswarm_report.py needs pyyaml")

REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_BAKEOFF_CELLS = REPO_ROOT / "tools/bugfix-bakeoff/results/cells"
DEFAULT_ARENA_ROUND1 = REPO_ROOT / "tools/arena/results/round-1/rollup.json"
DEFAULT_CORPUS = REPO_ROOT / "tools/arena/corpus/cost-bench.manifest.yaml"
DEFAULT_SOURCES = REPO_ROOT / "tools/arena/corpus/sources.yaml"
DEFAULT_BUGSWARM_SOURCE = REPO_ROOT / "tools/arena/corpus/bugswarm.seed.yaml"

QUALITY_VALUES = ("solved", "partial", "failed", "pending", "blocked")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--generated-at", required=True, help="stable report timestamp")
    parser.add_argument("--json-out", required=True)
    parser.add_argument("--markdown-out", required=True)
    parser.add_argument("--bugswarm-source", default=str(DEFAULT_BUGSWARM_SOURCE), help="optional converted BugSwarm YAML source")
    parser.add_argument("--bugswarm-verification", default="", help="optional bugswarm_verify_source.py JSON report")
    parser.add_argument("--bugswarm-arena-rollup", default="", help="optional arena rollup with BugSwarm GLM-5.2 cells")
    parser.add_argument("--oss-arena-rollup", default="", help="optional arena rollup with OSS oracle GLM-5.2 cells")
    parser.add_argument("--bakeoff-cells", default=str(DEFAULT_BAKEOFF_CELLS))
    parser.add_argument("--arena-rollup", default=str(DEFAULT_ARENA_ROUND1))
    parser.add_argument("--corpus", default=str(DEFAULT_CORPUS))
    parser.add_argument("--sources", default=str(DEFAULT_SOURCES))
    args = parser.parse_args(argv)

    report = build_report(
        generated_at=args.generated_at,
        report_json=Path(args.json_out),
        report_markdown=Path(args.markdown_out),
        bakeoff_cells=Path(args.bakeoff_cells),
        arena_rollup=Path(args.arena_rollup),
        corpus_path=Path(args.corpus),
        sources_path=Path(args.sources),
        bugswarm_source=Path(args.bugswarm_source) if args.bugswarm_source else None,
        bugswarm_verification=Path(args.bugswarm_verification) if args.bugswarm_verification else None,
        bugswarm_arena_rollup=Path(args.bugswarm_arena_rollup) if args.bugswarm_arena_rollup else None,
        oss_arena_rollup=Path(args.oss_arena_rollup) if args.oss_arena_rollup else None,
    )
    write_json(Path(args.json_out), report)
    write_text(Path(args.markdown_out), render_markdown(report))
    print(f"wrote {args.json_out} and {args.markdown_out}")
    return 0


def build_report(
    *,
    generated_at: str,
    report_json: Path,
    report_markdown: Path,
    bakeoff_cells: Path,
    arena_rollup: Path,
    corpus_path: Path,
    sources_path: Path,
    bugswarm_source: Path | None = None,
    bugswarm_verification: Path | None = None,
    bugswarm_arena_rollup: Path | None = None,
    oss_arena_rollup: Path | None = None,
) -> dict[str, Any]:
    corpus = load_yaml(corpus_path)
    sources = load_yaml(sources_path)
    glm_cells = load_glm_bakeoff_cells(bakeoff_cells)
    arena_cells = load_arena_cells(arena_rollup)
    bugswarm_tasks = load_bugswarm_tasks(bugswarm_source)
    verification = load_verification(bugswarm_verification)
    bugswarm_cells = load_headline_arena_cells(bugswarm_arena_rollup, corpus_name="bugswarm")
    oss_arena_cells = load_headline_arena_cells(oss_arena_rollup, corpus_name="oss-oracle")

    glm_headline_cells = map_legacy_oss_tasks(corpus, glm_cells) + oss_arena_cells
    matrix_rows = build_required_matrix(glm_headline_cells, bugswarm_tasks, bugswarm_cells)
    source_mix_data = source_mix(corpus, sources, bugswarm_tasks)
    comparisons = build_comparisons(matrix_rows)
    inputs = {
        "report_json": rel(report_json),
        "report_markdown": rel(report_markdown),
        "bakeoff_cells": rel(bakeoff_cells),
        "arena_rollup": rel(arena_rollup),
        "corpus": rel(corpus_path),
        "sources": rel(sources_path),
        "bugswarm_source": rel(bugswarm_source) if bugswarm_source else "",
        "bugswarm_verification": rel(bugswarm_verification) if bugswarm_verification else "",
        "bugswarm_arena_rollup": rel(bugswarm_arena_rollup) if bugswarm_arena_rollup else "",
        "oss_arena_rollup": rel(oss_arena_rollup) if oss_arena_rollup else "",
    }
    return {
        "kind": "glm52_bugswarm_bugfix_report",
        "version": 1,
        "generated_at": generated_at,
        "inputs": inputs,
        "reproducibility": reproducibility_ledger(
            generated_at=generated_at,
            inputs=inputs,
            bakeoff_cells=bakeoff_cells,
            arena_rollup=arena_rollup,
            corpus_path=corpus_path,
            sources_path=sources_path,
            bugswarm_source=bugswarm_source,
            bugswarm_verification=bugswarm_verification,
            bugswarm_arena_rollup=bugswarm_arena_rollup,
            oss_arena_rollup=oss_arena_rollup,
        ),
        "corpora": corpus_summary(corpus, sources, bugswarm_tasks, verification),
        "source_mix": source_mix_data,
        "glm52_bugfix_cells": glm_cells,
        "oss_glm52_arena_cells": oss_arena_cells,
        "bugswarm_glm52_arena_cells": bugswarm_cells,
        "required_glm52_matrix": matrix_rows,
        "rollups": {
            "glm52_by_corpus_treatment": rollup_required_matrix(matrix_rows),
            "glm52_by_treatment_overall": rollup_by_treatment(matrix_rows),
            "supporting_oss_codex_round1": rollup_arena_cells(arena_cells),
        },
        "comparisons": comparisons,
        "claim_ledger": claim_ledger(matrix_rows, comparisons, source_mix_data, bugswarm_tasks, verification),
        "threats_to_validity": threats_to_validity(matrix_rows, comparisons, bugswarm_tasks, verification),
        "completion_audit": completion_audit(matrix_rows, verification, bugswarm_tasks),
        "study_protocol": study_protocol(matrix_rows, bugswarm_tasks, verification, report_json, corpus_path, bugswarm_source),
        "evidence_gaps": evidence_gaps(matrix_rows, bugswarm_tasks, verification),
        "interpretation": interpretation(matrix_rows, bugswarm_tasks, verification),
        "evidence_closure": evidence_closure(matrix_rows, bugswarm_tasks, verification),
        "references": build_references(sources, bugswarm_source),
    }


def load_glm_bakeoff_cells(cells_dir: Path) -> list[dict[str, Any]]:
    cells: list[dict[str, Any]] = []
    for path in sorted(cells_dir.glob("*glm-5.2*.json")):
        data = json.loads(path.read_text(encoding="utf-8"))
        outcome = data.get("outcome") or {}
        metrics = data.get("metrics") or {}
        cells.append({
            "source": "bugfix-bakeoff",
            "corpus": "oss-oracle",
            "task": str(data.get("bug") or ""),
            "candidate": str(data.get("candidate") or ""),
            "treatment": normalize_treatment(str(data.get("treatment") or "")),
            "model": str(data.get("model") or ""),
            "provider": str(data.get("provider") or ""),
            "quality": normalize_quality(str(outcome.get("quality") or "pending")),
            "oracle_status": str(outcome.get("oracle_status") or ""),
            "adjudicated": bool(outcome.get("adjudicated")),
            "total_tokens": as_int(metrics.get("total_tokens")),
            "cost_usd": as_float(metrics.get("cost_usd")),
            "evidence": rel(path),
            "notes": str(data.get("notes") or ""),
        })
    return cells


def load_arena_cells(rollup_path: Path) -> list[dict[str, Any]]:
    if not rollup_path.exists():
        return []
    data = json.loads(rollup_path.read_text(encoding="utf-8"))
    out: list[dict[str, Any]] = []
    for cell in data.get("cells", []):
        metrics = cell.get("metrics") or {}
        out.append({
            "task": str((cell.get("axis") or {}).get("task") or ""),
            "variant": str(cell.get("variant_id") or ""),
            "treatment": treatment_from_variant(str(cell.get("variant_id") or "")),
            "quality": normalize_quality(str(cell.get("verdict") or "pending")),
            "tokens": as_int(metrics.get("tokens")),
            "cost_usd": as_float(metrics.get("cost_usd")),
            "evidence": rel(rollup_path),
        })
    return out


def load_headline_arena_cells(rollup_path: Path | None, *, corpus_name: str) -> list[dict[str, Any]]:
    if rollup_path is None or not rollup_path.exists():
        return []
    data = json.loads(rollup_path.read_text(encoding="utf-8"))
    out: list[dict[str, Any]] = []
    for cell in data.get("cells", []):
        if not isinstance(cell, dict):
            continue
        metrics = cell.get("metrics") or {}
        task = str((cell.get("axis") or {}).get("task") or "")
        variant = str(cell.get("variant_id") or "")
        treatment = treatment_from_variant(variant)
        if "glm-5.2" not in variant.lower():
            continue
        out.append({
            "source": "arena-rollup",
            "corpus": corpus_name,
            "task": task,
            "candidate": "glm-5.2",
            "treatment": treatment,
            "model": "hf:zai-org/GLM-5.2",
            "provider": str(cell.get("job_type") or "paired-task"),
            "quality": normalize_quality(str(cell.get("verdict") or "pending")),
            "oracle_status": "",
            "adjudicated": False,
            "total_tokens": as_int(metrics.get("tokens")),
            "cost_usd": as_float(metrics.get("cost_usd")),
            "evidence": rel(rollup_path),
            "trace_ref": str(cell.get("trace_ref") or ""),
            "notes": str(cell.get("notes") or ""),
        })
    return [row for row in out if row["task"] and row["treatment"] in {"kitsoki", "raw-prompt"}]


def load_bugswarm_tasks(path: Path | None) -> list[dict[str, Any]]:
    if path is None or not path.exists():
        return []
    data = load_yaml(path)
    tasks = data.get("tasks") or []
    if not isinstance(tasks, list):
        return []
    out = []
    for task in tasks:
        if not isinstance(task, dict):
            continue
        out.append({
            "id": str(task.get("id") or ""),
            "repo": str(task.get("repo_label") or task.get("repo") or ""),
            "image_tag": str(task.get("image_tag") or ""),
            "verified_red": bool(task.get("verified_red")),
            "verified_green": bool(task.get("verified_green")),
        })
    return out


def load_verification(path: Path | None) -> dict[str, Any]:
    if path is None or not path.exists():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    return data if isinstance(data, dict) else {}


def map_legacy_oss_tasks(corpus: dict[str, Any], cells: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Map legacy bugfix-bakeoff task ids onto the reusable arena corpus ids."""
    mapping: dict[tuple[str, str], str] = {}
    for task in corpus.get("tasks", []):
        if not isinstance(task, dict):
            continue
        oracle = task.get("oracle") or {}
        if not isinstance(oracle, dict) or oracle.get("kind") != "external_bakeoff":
            continue
        project = str(oracle.get("project") or "")
        bug = str(oracle.get("bug") or "")
        task_id = str(task.get("id") or "")
        if project and bug and task_id:
            mapping[(project, bug)] = task_id

    mapped: list[dict[str, Any]] = []
    for cell in cells:
        if cell.get("source") != "bugfix-bakeoff" or cell.get("corpus") != "oss-oracle":
            mapped.append(cell)
            continue
        legacy_task = str(cell.get("task") or "")
        arena_task = mapping.get(("kitsoki", legacy_task))
        if not arena_task:
            mapped.append(cell)
            continue
        copied = dict(cell)
        copied["legacy_task"] = legacy_task
        copied["task"] = arena_task
        mapped.append(copied)
    return mapped


def build_required_matrix(
    glm_cells: list[dict[str, Any]],
    bugswarm_tasks: list[dict[str, Any]],
    bugswarm_cells: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    oss_tasks = sorted({c["task"] for c in glm_cells if c["corpus"] == "oss-oracle" and c.get("task")})
    if not oss_tasks:
        oss_tasks = ["current-committed-glm52"]
    for treatment in ("kitsoki", "raw-prompt"):
        by_task = {
            c["task"]: c
            for c in glm_cells
            if c["corpus"] == "oss-oracle" and c["treatment"] == treatment
        }
        for task_id in oss_tasks:
            if task_id in by_task:
                rows.append(by_task[task_id])
            else:
                rows.append(pending_row("oss-oracle", task_id, treatment, "no committed GLM-5.2 cell"))

    bugswarm_by_key = {
        (cell["task"], cell["treatment"]): cell
        for cell in bugswarm_cells
    }
    task_ids = [task["id"] for task in bugswarm_tasks]
    if not task_ids:
        task_ids = sorted({cell["task"] for cell in bugswarm_cells})

    if task_ids:
        for task_id in task_ids:
            for treatment in ("kitsoki", "raw-prompt"):
                row = bugswarm_by_key.get((task_id, treatment))
                if row:
                    rows.append(row)
                else:
                    rows.append(pending_row("bugswarm", task_id, treatment, "BugSwarm task imported but no GLM-5.2 result cell"))
    else:
        for treatment in ("kitsoki", "raw-prompt"):
            rows.append(pending_row("bugswarm", "verified-subset", treatment, "no verified BugSwarm source imported"))
    return rows


def pending_row(corpus: str, task: str, treatment: str, reason: str) -> dict[str, Any]:
    return {
        "source": "pending",
        "corpus": corpus,
        "task": task,
        "candidate": "glm-5.2",
        "treatment": treatment,
        "model": "hf:zai-org/GLM-5.2",
        "provider": "synthetic.new",
        "quality": "pending",
        "oracle_status": "",
        "adjudicated": False,
        "total_tokens": None,
        "cost_usd": None,
        "evidence": "",
        "notes": reason,
    }


def corpus_summary(
    corpus: dict[str, Any],
    sources: dict[str, Any],
    bugswarm_tasks: list[dict[str, Any]],
    verification: dict[str, Any],
) -> dict[str, Any]:
    tasks = [t for t in corpus.get("tasks", []) if isinstance(t, dict)]
    repos = sorted({str(t.get("repo") or "") for t in tasks if t.get("repo")})
    source_rows = sources.get("sources") if isinstance(sources.get("sources"), list) else []
    bugswarm_source = next((s for s in source_rows if isinstance(s, dict) and s.get("id") == "bugswarm"), {})
    return {
        "oss_oracle": {
            "task_count": len(tasks),
            "repo_count": len(repos),
            "repos": repos,
            "bugfix_task_count": sum(1 for t in tasks if t.get("archetype") == "bugfix_test_repair"),
        },
        "bugswarm": {
            "source_status": str(bugswarm_source.get("status") or "missing"),
            "imported_task_count": len(bugswarm_tasks),
            "verified_task_count": sum(1 for t in bugswarm_tasks if t["verified_red"] and t["verified_green"]),
            "verification_report_count": int(verification.get("task_count") or 0),
            "verification_verified_count": int(verification.get("verified_count") or 0),
            "verification_mode": str(verification.get("mode") or ""),
            "source_catalog": rel(DEFAULT_SOURCES),
        },
    }


def source_mix(corpus: dict[str, Any], sources: dict[str, Any], bugswarm_tasks: list[dict[str, Any]]) -> dict[str, Any]:
    tasks = [t for t in corpus.get("tasks", []) if isinstance(t, dict)]
    repo_history = [t for t in tasks if oracle_kind(t) == "github_content"]
    bugfix_fixtures = [t for t in tasks if oracle_kind(t) == "external_bakeoff"]
    other = [t for t in tasks if oracle_kind(t) not in {"github_content", "external_bakeoff"}]
    source_rows = sources.get("sources") if isinstance(sources.get("sources"), list) else []
    bugswarm_source = next((s for s in source_rows if isinstance(s, dict) and s.get("id") == "bugswarm"), {})
    return {
        "oss_oracle": {
            "status": "frozen",
            "task_count": len(tasks),
            "repo_count": len(repo_names(tasks)),
            "selection_notes": [str(note) for note in corpus.get("selection_notes", [])],
            "components": [
                source_component(
                    "pre_registered_oss_targets",
                    "Pre-registered public OSS target libraries with GitHub-content RED/GREEN oracles.",
                    repo_history,
                ),
                source_component(
                    "armed_bugfix_fixtures",
                    "Existing hidden-oracle bugfix/failing-test fixtures from the bakeoff harness.",
                    bugfix_fixtures,
                ),
                source_component(
                    "other_oss_oracle_tasks",
                    "Other OSS-oracle tasks in the frozen manifest.",
                    other,
                ),
            ],
        },
        "bugswarm": {
            "status": str(bugswarm_source.get("status") or "missing"),
            "task_count": len(bugswarm_tasks),
            "repo_count": len({task.get("repo") for task in bugswarm_tasks if task.get("repo")}),
            "component": "containerized_fail_pass_ci_artifacts",
            "verification_gate": "execute RED/GREEN before live GLM-5.2 cells",
            "repositories": sorted({str(task.get("repo") or "") for task in bugswarm_tasks if task.get("repo")}),
        },
        "blend_policy": [
            "Keep OSS oracle tasks and BugSwarm artifacts as separate source families in denominators.",
            "Report overall GLM-5.2 treatment totals only after both Kitsoki and raw-prompt arms have attempted cells.",
            "Use total tokens as the primary cross-source cost axis; USD remains secondary and evidence-dependent.",
            "Do not count dry-run BugSwarm verification as RED/GREEN proof.",
        ],
    }


def reproducibility_ledger(
    *,
    generated_at: str,
    inputs: dict[str, str],
    bakeoff_cells: Path,
    arena_rollup: Path,
    corpus_path: Path,
    sources_path: Path,
    bugswarm_source: Path | None,
    bugswarm_verification: Path | None,
    bugswarm_arena_rollup: Path | None,
    oss_arena_rollup: Path | None,
) -> dict[str, Any]:
    generator = file_artifact(Path(__file__), "generator")
    bakeoff_glm_cells = glob_artifact(bakeoff_cells, "*glm-5.2*.json", "directory-glob")
    status = "reproducible" if bakeoff_glm_cells["files"] else "partial"
    return {
        "status": status,
        "generated_at": generated_at,
        "generator": generator,
        "regenerate_command": [
            "python3",
            "tools/arena/scripts/glm52_bugswarm_report.py",
            "--generated-at",
            generated_at,
            "--json-out",
            inputs["report_json"],
            "--markdown-out",
            inputs["report_markdown"],
        ],
        "validation_commands": [
            "python3 tools/arena/scripts/glm52_report_gate.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json",
            "python3 tools/arena/scripts/glm52_report_gate.py --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json --require-publishable",
            "python3 tools/arena/tests/test_glm52_bugswarm_report.py",
            "python3 tools/arena/tests/test_glm52_report_gate.py",
            "python3 -m py_compile tools/arena/scripts/glm52_bugswarm_report.py tools/arena/scripts/glm52_report_gate.py",
            "python3 tools/arena/tests/validate_corpus.py tools/arena/corpus/cost-bench.manifest.yaml",
            "python3 tools/arena/tests/run_no_llm.py",
        ],
        "artifacts": [
            file_artifact(corpus_path, "corpus"),
            file_artifact(sources_path, "source-catalog"),
            file_artifact(bugswarm_source, "bugswarm-source"),
            file_artifact(arena_rollup, "supporting-rollup"),
            bakeoff_glm_cells,
            file_artifact(oss_arena_rollup, "optional-oss-arena-rollup"),
            file_artifact(bugswarm_verification, "optional-bugswarm-verification"),
            file_artifact(bugswarm_arena_rollup, "optional-bugswarm-arena-rollup"),
        ],
    }


def file_artifact(path: Path | None, role: str) -> dict[str, Any]:
    if path is None:
        return {
            "role": role,
            "kind": "missing-optional",
            "path": "",
            "exists": False,
        }
    artifact: dict[str, Any] = {
        "role": role,
        "kind": "file",
        "path": rel(path),
        "exists": path.exists(),
    }
    if path.exists():
        data = path.read_bytes()
        artifact["bytes"] = len(data)
        artifact["sha256"] = hashlib.sha256(data).hexdigest()
    return artifact


def glob_artifact(directory: Path, pattern: str, role: str) -> dict[str, Any]:
    files = [file_artifact(path, "matched-file") for path in sorted(directory.glob(pattern))]
    return {
        "role": role,
        "kind": "directory-glob",
        "path": rel(directory),
        "pattern": pattern,
        "exists": directory.exists(),
        "match_count": len(files),
        "files": files,
    }


def source_component(component_id: str, description: str, tasks: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        "id": component_id,
        "description": description,
        "task_count": len(tasks),
        "repo_count": len(repo_names(tasks)),
        "repositories": repo_names(tasks),
        "oracle_kinds": sorted({oracle_kind(task) for task in tasks if oracle_kind(task)}),
        "splits": count_by(tasks, "split"),
        "archetypes": count_by(tasks, "archetype"),
    }


def repo_names(tasks: list[dict[str, Any]]) -> list[str]:
    return sorted({str(task.get("repo_label") or task.get("repo") or "") for task in tasks if task.get("repo_label") or task.get("repo")})


def oracle_kind(task: dict[str, Any]) -> str:
    oracle = task.get("oracle") if isinstance(task.get("oracle"), dict) else {}
    return str(oracle.get("kind") or "")


def count_by(tasks: list[dict[str, Any]], field: str) -> dict[str, int]:
    counts: Counter[str] = Counter()
    for task in tasks:
        value = str(task.get(field) or "")
        if value:
            counts[value] += 1
    return dict(sorted(counts.items()))


def rollup_required_matrix(rows: list[dict[str, Any]]) -> dict[str, Any]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        key = f"{row['corpus']}|{row['treatment']}"
        buckets.setdefault(key, []).append(row)
    return {key: rollup_quality(rows) for key, rows in sorted(buckets.items())}


def rollup_by_treatment(rows: list[dict[str, Any]]) -> dict[str, Any]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        buckets.setdefault(str(row["treatment"]), []).append(row)
    return {key: rollup_quality(rows) for key, rows in sorted(buckets.items())}


def build_comparisons(rows: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    corpora = sorted({str(row["corpus"]) for row in rows})
    comparisons = {
        corpus: comparison_for_rows([row for row in rows if row["corpus"] == corpus])
        for corpus in corpora
    }
    comparisons["overall"] = comparison_for_rows(rows)
    return comparisons


def comparison_for_rows(rows: list[dict[str, Any]]) -> dict[str, Any]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        buckets.setdefault(str(row["treatment"]), []).append(row)
    kitsoki = rollup_quality(buckets.get("kitsoki", []))
    raw = rollup_quality(buckets.get("raw-prompt", []))
    complete = kitsoki["attempted"] > 0 and raw["attempted"] > 0
    return {
        "status": "complete" if complete else "pending",
        "kitsoki": kitsoki,
        "raw_prompt": raw,
        "success_rate_delta": round(kitsoki["success_rate"] - raw["success_rate"], 6) if complete else None,
        "token_ratio_kitsoki_to_raw": (
            round(kitsoki["total_tokens"] / raw["total_tokens"], 6)
            if complete and kitsoki["total_tokens"] is not None and raw["total_tokens"]
            else None
        ),
        "notes": [] if complete else comparison_missing_notes(kitsoki, raw),
    }


def comparison_missing_notes(kitsoki: dict[str, Any], raw: dict[str, Any]) -> list[str]:
    notes: list[str] = []
    if kitsoki["attempted"] == 0:
        notes.append("Kitsoki GLM-5.2 arm has no attempted cells.")
    if raw["attempted"] == 0:
        notes.append("Raw-prompt GLM-5.2 arm has no attempted cells.")
    return notes


def claim_ledger(
    rows: list[dict[str, Any]],
    comparisons: dict[str, dict[str, Any]],
    mix: dict[str, Any],
    bugswarm_tasks: list[dict[str, Any]],
    verification: dict[str, Any],
) -> dict[str, Any]:
    claims = [
        comparison_claim(
            "overall-token-usage",
            "Overall GLM-5.2 token usage comparison between Kitsoki bugfix and raw prompts.",
            comparisons["overall"],
            metric="token_ratio_kitsoki_to_raw",
        ),
        comparison_claim(
            "overall-success-rate",
            "Overall GLM-5.2 success-rate comparison between Kitsoki bugfix and raw prompts.",
            comparisons["overall"],
            metric="success_rate_delta",
        ),
        comparison_claim(
            "bugswarm-success-rate",
            "BugSwarm GLM-5.2 success-rate comparison between Kitsoki bugfix and raw prompts.",
            comparisons["bugswarm"],
            metric="success_rate_delta",
        ),
        supported_claim(
            "bugswarm-reusable-source",
            "BugSwarm is represented as a reusable source family alongside the OSS oracle corpus.",
            evidence=["tools/arena/corpus/sources.yaml", "tools/arena/corpus/bugswarm.seed.yaml"],
            finding=f"Imported BugSwarm task count: {len(bugswarm_tasks)}.",
            caveat=(
                "Execute-mode RED/GREEN verification is still required before live GLM-5.2 cells."
                if not is_bugswarm_execute_verified(verification)
                else ""
            ),
        ),
        supported_claim(
            "oss-source-mix",
            "The OSS oracle corpus preserves the 10 public target source family separately from hidden bugfix fixtures.",
            evidence=["tools/arena/corpus/cost-bench.manifest.yaml", "tools/arena/corpus/sources.yaml"],
            finding=source_mix_finding(mix),
            caveat="GLM-5.2 headline cells currently cover only the committed bugfix fixture row.",
        ),
        observed_cell_claim(rows),
    ]
    return {
        "status": "publishable" if all(claim["status"] == "supported" for claim in claims) else "partial",
        "supported_count": sum(1 for claim in claims if claim["status"] == "supported"),
        "pending_count": sum(1 for claim in claims if claim["status"] == "pending"),
        "claims": claims,
    }


def comparison_claim(claim_id: str, statement: str, comparison: dict[str, Any], *, metric: str) -> dict[str, Any]:
    if comparison.get("status") == "complete" and comparison.get(metric) is not None:
        return {
            "id": claim_id,
            "status": "supported",
            "statement": statement,
            "finding": f"{metric}={comparison[metric]}.",
            "evidence": ["required_glm52_matrix", "comparisons"],
            "missing_evidence": [],
            "caveat": "",
        }
    return {
        "id": claim_id,
        "status": "pending",
        "statement": statement,
        "finding": "The claim is not yet answerable from committed evidence.",
        "evidence": ["required_glm52_matrix", "comparisons"],
        "missing_evidence": list(comparison.get("notes") or ["Both arms need attempted GLM-5.2 cells."]),
        "caveat": "No delta or token ratio is published while the comparison is pending.",
    }


def supported_claim(
    claim_id: str,
    statement: str,
    *,
    evidence: list[str],
    finding: str,
    caveat: str,
) -> dict[str, Any]:
    return {
        "id": claim_id,
        "status": "supported",
        "statement": statement,
        "finding": finding,
        "evidence": evidence,
        "missing_evidence": [],
        "caveat": caveat,
    }


def source_mix_finding(mix: dict[str, Any]) -> str:
    components = {
        component["id"]: component
        for component in mix["oss_oracle"]["components"]
        if isinstance(component, dict)
    }
    public = components.get("pre_registered_oss_targets", {})
    fixtures = components.get("armed_bugfix_fixtures", {})
    return (
        f"{public.get('task_count', 0)} tasks over {public.get('repo_count', 0)} public targets; "
        f"{fixtures.get('task_count', 0)} armed bugfix fixture tasks."
    )


def observed_cell_claim(rows: list[dict[str, Any]]) -> dict[str, Any]:
    attempted = [
        row for row in rows
        if row.get("corpus") == "oss-oracle"
        and row.get("treatment") == "kitsoki"
        and row.get("quality") in {"solved", "partial", "failed"}
    ]
    tokens = sum(row.get("total_tokens") or 0 for row in attempted)
    return supported_claim(
        "observed-oss-kitsoki-glm52-cell",
        "Committed GLM-5.2 Kitsoki bugfix evidence exists for the OSS oracle headline matrix.",
        evidence=[str(row.get("evidence") or "") for row in attempted if row.get("evidence")],
        finding=f"{len(attempted)} attempted cell(s), {tokens} total tokens.",
        caveat="This is not a Kitsoki-vs-raw comparison until the matching raw-prompt arm is attempted.",
    )


def threats_to_validity(
    rows: list[dict[str, Any]],
    comparisons: dict[str, dict[str, Any]],
    bugswarm_tasks: list[dict[str, Any]],
    verification: dict[str, Any],
) -> dict[str, Any]:
    threats = [
        validity_threat(
            "missing-raw-glm52-arm",
            "internal",
            "high" if comparisons["overall"]["status"] == "pending" else "resolved",
            "The raw-prompt GLM-5.2 arm has no committed attempted cells, so outcome deltas and token ratios are not publishable.",
            "Commit raw-prompt GLM-5.2 cells for every headline task and regenerate the report.",
            active=comparisons["overall"]["status"] == "pending",
        ),
        validity_threat(
            "bugswarm-unverified-artifact",
            "construct",
            "high" if not is_bugswarm_execute_verified(verification) else "resolved",
            "The committed BugSwarm seed is metadata-only until execute-mode RED/GREEN verification proves failed and passed scripts still reproduce.",
            "Run bugswarm_verify_source.py --execute, apply the verification report, and regenerate with --bugswarm-verification.",
            active=bool(bugswarm_tasks) and not is_bugswarm_execute_verified(verification),
        ),
        validity_threat(
            "single-observed-glm52-cell",
            "external",
            "medium",
            "The current observed GLM-5.2 evidence is one OSS Kitsoki fixture cell, so the report is a ledger rather than a generalizable performance estimate.",
            "Schedule the remaining GLM-5.2 cells and report denominators by source family.",
            active=sum(1 for row in rows if row.get("quality") in {"solved", "partial", "failed"}) < 4,
        ),
        validity_threat(
            "partial-is-not-solved",
            "construct",
            "medium",
            "The observed Kitsoki GLM-5.2 cell is partial, not solved, and success-rate claims count only solved cells.",
            "Keep partial rate separate from success rate and adjudicate oracle-coupled failures before publication.",
            active=any(row.get("quality") == "partial" for row in rows),
        ),
        validity_threat(
            "supporting-round-not-glm52",
            "external",
            "low",
            "The supporting Codex-native round checks harness behavior and token accounting but is not GLM-5.2 evidence.",
            "Keep supporting round results out of headline GLM-5.2 denominators.",
            active=True,
        ),
    ]
    active_threats = [threat for threat in threats if threat["status"] == "active"]
    return {
        "status": "blocked" if any(threat["severity"] == "high" for threat in active_threats) else "disclosed",
        "active_count": len(active_threats),
        "high_count": sum(1 for threat in active_threats if threat["severity"] == "high"),
        "threats": threats,
    }


def validity_threat(
    threat_id: str,
    category: str,
    severity: str,
    description: str,
    mitigation: str,
    *,
    active: bool,
) -> dict[str, Any]:
    return {
        "id": threat_id,
        "category": category,
        "severity": severity,
        "status": "active" if active else "resolved",
        "description": description,
        "mitigation": mitigation,
    }


def completion_audit(
    rows: list[dict[str, Any]],
    verification: dict[str, Any],
    bugswarm_tasks: list[dict[str, Any]],
) -> dict[str, Any]:
    requirements = [
        audit_requirement(
            "report-artifact",
            "Produce a generated research report with JSON data and Markdown narrative.",
            "proven",
            ["docs/case-studies/bugswarm-glm52-bugfix-report.md", "docs/case-studies/bugswarm-glm52-bugfix-report.data.json"],
            "The report is generated offline from committed inputs.",
            "",
        ),
        audit_requirement(
            "oss-source",
            "Keep the reusable OSS oracle corpus as a separate source.",
            "proven",
            ["tools/arena/corpus/cost-bench.manifest.yaml", "tools/arena/corpus/sources.yaml"],
            "The report references the frozen OSS oracle corpus and keeps it separate from BugSwarm.",
            "",
        ),
        audit_requirement(
            "bugswarm-source",
            "Make BugSwarm a reusable source alongside the OSS oracle corpus.",
            "proven" if bugswarm_tasks else "missing",
            ["tools/arena/corpus/sources.yaml", "tools/arena/corpus/bugswarm.seed.yaml"],
            f"Imported BugSwarm task count: {len(bugswarm_tasks)}.",
            "" if bugswarm_tasks else "Import BugSwarm artifact metadata with bugswarm_to_arena.py.",
        ),
        audit_requirement(
            "bugswarm-execute-verification",
            "Prove BugSwarm failed/passed artifact behavior before scheduling live cells.",
            "proven" if is_bugswarm_execute_verified(verification) else "missing",
            [str(verification.get("source") or "tools/arena/scripts/bugswarm_verify_source.py")],
            (
                f"Verification mode={verification.get('mode') or 'none'}; "
                f"verified={verification.get('verified_count', 0)}/{verification.get('task_count', 0)}."
            ),
            "" if is_bugswarm_execute_verified(verification) else "Run bugswarm_verify_source.py --execute and apply the verification report.",
        ),
        audit_requirement_for_rows(
            "oss-kitsoki-glm52",
            "Commit Kitsoki bugfix GLM-5.2 evidence for the OSS oracle corpus.",
            rows,
            corpus="oss-oracle",
            treatment="kitsoki",
        ),
        audit_requirement_for_rows(
            "oss-raw-glm52",
            "Commit raw-prompt GLM-5.2 evidence for the OSS oracle corpus.",
            rows,
            corpus="oss-oracle",
            treatment="raw-prompt",
        ),
        audit_requirement_for_rows(
            "bugswarm-kitsoki-glm52",
            "Commit Kitsoki bugfix GLM-5.2 evidence for the BugSwarm corpus.",
            rows,
            corpus="bugswarm",
            treatment="kitsoki",
        ),
        audit_requirement_for_rows(
            "bugswarm-raw-glm52",
            "Commit raw-prompt GLM-5.2 evidence for the BugSwarm corpus.",
            rows,
            corpus="bugswarm",
            treatment="raw-prompt",
        ),
    ]
    status = "complete" if all(item["status"] == "proven" for item in requirements) else "incomplete"
    return {
        "status": status,
        "proven_count": sum(1 for item in requirements if item["status"] == "proven"),
        "requirement_count": len(requirements),
        "requirements": requirements,
    }


def audit_requirement(
    requirement_id: str,
    requirement: str,
    status: str,
    evidence: list[str],
    finding: str,
    next_step: str,
) -> dict[str, Any]:
    return {
        "id": requirement_id,
        "requirement": requirement,
        "status": status,
        "evidence": evidence,
        "finding": finding,
        "next": next_step,
    }


def audit_requirement_for_rows(
    requirement_id: str,
    requirement: str,
    rows: list[dict[str, Any]],
    *,
    corpus: str,
    treatment: str,
) -> dict[str, Any]:
    matching = [row for row in rows if row.get("corpus") == corpus and row.get("treatment") == treatment]
    attempted = [row for row in matching if row.get("quality") in {"solved", "partial", "failed"}]
    evidence = [str(row.get("evidence") or "") for row in attempted if row.get("evidence")]
    if attempted:
        tokens = sum(row.get("total_tokens") or 0 for row in attempted)
        return audit_requirement(
            requirement_id,
            requirement,
            "proven",
            evidence,
            f"{len(attempted)} attempted cell(s), {tokens} total tokens.",
            "",
        )
    pending_tasks = sorted({str(row.get("task") or "") for row in matching if row.get("quality") == "pending"})
    return audit_requirement(
        requirement_id,
        requirement,
        "missing",
        [],
        f"No attempted cell is committed. Pending task(s): {', '.join(pending_tasks) if pending_tasks else 'none'}.",
        "Run the generated gap-plan commands, land the rollup, and regenerate this report.",
    )


def is_bugswarm_execute_verified(verification: dict[str, Any]) -> bool:
    return str(verification.get("mode") or "") == "execute" and int(verification.get("verified_count") or 0) > 0


def study_protocol(
    rows: list[dict[str, Any]],
    bugswarm_tasks: list[dict[str, Any]],
    verification: dict[str, Any],
    report_json: Path,
    corpus_path: Path,
    bugswarm_source: Path | None,
) -> dict[str, Any]:
    pending = [row for row in rows if row.get("quality") == "pending"]
    return {
        "status": "complete" if not pending else "pending-evidence",
        "candidate": "glm-5.2",
        "primary_cost_metric": "total_tokens",
        "success_metric": "solved / (solved + partial + failed)",
        "pending_cell_count": len(pending),
        "pending_cells": [
            {
                "corpus": row["corpus"],
                "task": row["task"],
                "treatment": row["treatment"],
                "gate": protocol_gate(row, bugswarm_tasks, verification),
            }
            for row in pending
        ],
        "execution_steps": protocol_execution_steps(pending, bugswarm_tasks, verification, report_json, corpus_path, bugswarm_source),
        "live_controls": [
            "The report generator, gap planner, and tests are offline and must not run Docker or LLMs.",
            "The operator must run no-LLM arena.py plan and non-live arena.py run before any --live command.",
            "Live commands must be explicit and include ARENA_PAIRED_TASK_ENABLE_CODEX=1.",
            "GLM-5.2 raw-prompt variants must use backend=claude so paired_task_runner dispatches through the synthetic-claude profile.",
            "BugSwarm live cells require execute-mode RED/GREEN verification before model scheduling.",
        ],
    }


def protocol_gate(row: dict[str, Any], bugswarm_tasks: list[dict[str, Any]], verification: dict[str, Any]) -> str:
    if row.get("corpus") == "bugswarm":
        if not bugswarm_tasks:
            return "import-bugswarm-source"
        if not is_bugswarm_execute_verified(verification):
            return "execute-verify-bugswarm"
    return "ready-to-plan"


def protocol_execution_steps(
    pending: list[dict[str, Any]],
    bugswarm_tasks: list[dict[str, Any]],
    verification: dict[str, Any],
    report_json: Path,
    corpus_path: Path,
    bugswarm_source: Path | None,
) -> list[dict[str, Any]]:
    steps: list[dict[str, Any]] = []
    oss_pending = [row for row in pending if row.get("corpus") == "oss-oracle"]
    bugswarm_pending = [row for row in pending if row.get("corpus") == "bugswarm"]
    report_json_arg = rel(report_json)
    corpus_arg = rel(corpus_path)
    bugswarm_source_arg = rel(bugswarm_source) if bugswarm_source else ".artifacts/bugswarm/arena-source.yaml"
    if oss_pending:
        steps.append({
            "id": "oss-raw-glm52",
            "status": "ready",
            "purpose": "Schedule missing OSS oracle raw-prompt GLM-5.2 cells with the frozen corpus manifest.",
            "pending_cells": len(oss_pending),
            "commands": [
                f"python3 tools/arena/scripts/oss_to_arena_spec.py --report-json {report_json_arg} --corpus {corpus_arg} --out .artifacts/arena/oss-glm52.yaml",
                "python3 tools/arena/arena.py plan --spec .artifacts/arena/oss-glm52.yaml",
                "python3 tools/arena/arena.py run --spec .artifacts/arena/oss-glm52.yaml --out .artifacts/arena/glm52-oss",
                "ARENA_PAIRED_TASK_ENABLE_CODEX=1 python3 tools/arena/arena.py run --spec .artifacts/arena/oss-glm52.yaml --out .artifacts/arena/glm52-oss --live",
            ],
            "report_arg": "--oss-arena-rollup .artifacts/arena/glm52-oss/rollup.json",
        })
    if bugswarm_pending:
        if not bugswarm_tasks:
            steps.append({
                "id": "bugswarm-import",
                "status": "required-before-live",
                "purpose": "Import explicit BugSwarm artifact metadata before verification or live cells.",
                "pending_cells": len(bugswarm_pending),
                "commands": [
                    "python3 tools/arena/scripts/bugswarm_to_arena.py --in .artifacts/bugswarm/artifacts.json --out .artifacts/bugswarm/arena-source.yaml",
                ],
                "report_arg": "",
            })
        if not is_bugswarm_execute_verified(verification):
            steps.append({
                "id": "bugswarm-execute-verification",
                "status": "required-before-live",
                "purpose": "Prove BugSwarm failed/passed scripts still reproduce in fresh containers.",
                "pending_cells": len(bugswarm_pending),
                "commands": [
                    f"python3 tools/arena/scripts/bugswarm_verify_source.py --source {bugswarm_source_arg} --out .artifacts/bugswarm/verification.json --execute",
                    f"python3 tools/arena/scripts/bugswarm_apply_verification.py --source {bugswarm_source_arg} --verification .artifacts/bugswarm/verification.json --out .artifacts/bugswarm/arena-source.verified.yaml",
                    (
                        f"python3 tools/arena/scripts/glm52_gap_plan.py --report-json {report_json_arg} "
                        "--json-out .artifacts/arena/glm52-gap-plan.json "
                        "--markdown-out .artifacts/arena/glm52-gap-plan.md "
                        "--bugswarm-source .artifacts/bugswarm/arena-source.verified.yaml"
                    ),
                ],
                "report_arg": "--bugswarm-verification .artifacts/bugswarm/verification.json",
            })
        else:
            steps.append({
                "id": "bugswarm-glm52-cells",
                "status": "ready",
                "purpose": "Schedule missing BugSwarm Kitsoki and raw-prompt GLM-5.2 cells from the execute-verified source.",
                "pending_cells": len(bugswarm_pending),
                "commands": [
                    f"python3 tools/arena/scripts/bugswarm_to_arena_spec.py --source {bugswarm_source_arg} --out .artifacts/bugswarm/bugswarm-glm52.yaml --kitsoki-backend codex --raw-backend claude",
                    "python3 tools/arena/arena.py plan --spec .artifacts/bugswarm/bugswarm-glm52.yaml",
                    "python3 tools/arena/arena.py run --spec .artifacts/bugswarm/bugswarm-glm52.yaml --out .artifacts/arena/bugswarm-glm52",
                    "ARENA_PAIRED_TASK_ENABLE_CODEX=1 python3 tools/arena/arena.py run --spec .artifacts/bugswarm/bugswarm-glm52.yaml --out .artifacts/arena/bugswarm-glm52 --live",
                ],
                "report_arg": "--bugswarm-arena-rollup .artifacts/arena/bugswarm-glm52/rollup.json",
            })
    return steps


def rollup_arena_cells(cells: list[dict[str, Any]]) -> dict[str, Any]:
    buckets: dict[str, list[dict[str, Any]]] = {}
    for cell in cells:
        treatment = cell["treatment"]
        if treatment not in {"kitsoki", "raw-prompt"}:
            continue
        buckets.setdefault(treatment, []).append(cell)
    return {key: rollup_quality(rows, token_key="tokens") for key, rows in sorted(buckets.items())}


def rollup_quality(rows: list[dict[str, Any]], *, token_key: str = "total_tokens") -> dict[str, Any]:
    counts = Counter(row.get("quality", "pending") for row in rows)
    attempted = counts["solved"] + counts["partial"] + counts["failed"]
    solved = counts["solved"]
    token_values = [row.get(token_key) for row in rows if isinstance(row.get(token_key), int)]
    return {
        "n": len(rows),
        "attempted": attempted,
        "solved": solved,
        "partial": counts["partial"],
        "failed": counts["failed"],
        "pending": counts["pending"],
        "blocked": counts["blocked"],
        "success_rate": round(solved / attempted, 6) if attempted else None,
        "partial_rate": round(counts["partial"] / attempted, 6) if attempted else None,
        "total_tokens": sum(token_values) if token_values else None,
        "avg_tokens": round(sum(token_values) / len(token_values), 2) if token_values else None,
    }


def evidence_gaps(rows: list[dict[str, Any]], bugswarm_tasks: list[dict[str, Any]], verification: dict[str, Any]) -> list[str]:
    gaps: list[str] = []
    if any(row["corpus"] == "oss-oracle" and row["treatment"] == "raw-prompt" and row["quality"] == "pending" for row in rows):
        gaps.append("No committed raw-prompt GLM-5.2 result exists for the OSS oracle corpus.")
    if not bugswarm_tasks:
        gaps.append("No BugSwarm artifact source has been imported and RED/GREEN verified yet.")
    elif not any(t["verified_red"] and t["verified_green"] for t in bugswarm_tasks):
        gaps.append("BugSwarm artifacts have been imported but none are verified RED/GREEN yet.")
    if bugswarm_tasks and not verification:
        gaps.append("No BugSwarm verification report is attached to this generated report.")
    if any(row["corpus"] == "bugswarm" and row["quality"] == "pending" for row in rows):
        if bugswarm_tasks:
            gaps.append("Some imported BugSwarm tasks are missing committed GLM-5.2 Kitsoki or raw-prompt result cells.")
        else:
            gaps.append("No committed GLM-5.2 Kitsoki or raw-prompt result exists for BugSwarm.")
    return gaps


def evidence_closure(rows: list[dict[str, Any]], bugswarm_tasks: list[dict[str, Any]], verification: dict[str, Any]) -> dict[str, Any]:
    pending = [row for row in rows if row["quality"] == "pending"]
    by_corpus: dict[str, list[dict[str, Any]]] = {}
    for row in pending:
        by_corpus.setdefault(str(row["corpus"]), []).append(row)
    actions = [
        closure_action_oss(by_corpus.get("oss-oracle", [])),
        closure_action_bugswarm(by_corpus.get("bugswarm", []), bugswarm_tasks, verification),
    ]
    return {
        "pending_cell_count": len(pending),
        "pending_by_corpus": {corpus: len(items) for corpus, items in sorted(by_corpus.items())},
        "actions": actions,
    }


def closure_action_oss(rows: list[dict[str, Any]]) -> dict[str, Any]:
    if not rows:
        return {
            "corpus": "oss-oracle",
            "status": "complete",
            "pending_count": 0,
            "next": "No pending OSS oracle GLM-5.2 cells.",
        }
    return {
        "corpus": "oss-oracle",
        "status": "ready-to-plan",
        "pending_count": len(rows),
        "tasks": sorted({str(row["task"]) for row in rows}),
        "treatments": sorted({str(row["treatment"]) for row in rows}),
        "next": "Run glm52_gap_plan.py; it can generate an OSS paired-task spec from the frozen corpus manifest.",
    }


def closure_action_bugswarm(rows: list[dict[str, Any]], bugswarm_tasks: list[dict[str, Any]], verification: dict[str, Any]) -> dict[str, Any]:
    if not rows:
        return {
            "corpus": "bugswarm",
            "status": "complete",
            "pending_count": 0,
            "next": "No pending BugSwarm GLM-5.2 cells.",
        }
    if not bugswarm_tasks:
        return {
            "corpus": "bugswarm",
            "status": "needs-import",
            "pending_count": len(rows),
            "next": "Import BugSwarm artifact metadata, execute RED/GREEN verification, then pass --bugswarm-source to glm52_gap_plan.py.",
        }
    if str(verification.get("mode") or "") != "execute" or int(verification.get("verified_count") or 0) == 0:
        return {
            "corpus": "bugswarm",
            "status": "needs-execute-verification",
            "pending_count": len(rows),
            "tasks": sorted({str(row["task"]) for row in rows}),
            "next": "Run bugswarm_verify_source.py --execute and apply the verification report before scheduling live GLM-5.2 cells.",
        }
    return {
        "corpus": "bugswarm",
        "status": "ready",
        "pending_count": len(rows),
        "tasks": sorted({str(row["task"]) for row in rows}),
        "treatments": sorted({str(row["treatment"]) for row in rows}),
        "next": "Run glm52_gap_plan.py with --bugswarm-source; it will generate split-backend BugSwarm live commands.",
    }


def interpretation(rows: list[dict[str, Any]], bugswarm_tasks: list[dict[str, Any]], verification: dict[str, Any]) -> list[str]:
    glm_kitsoki_attempts = [r for r in rows if r["corpus"] == "oss-oracle" and r["treatment"] == "kitsoki" and r["quality"] != "pending"]
    raw_prompt_attempts = [r for r in rows if r["treatment"] == "raw-prompt" and r["quality"] not in {"pending", "blocked"}]
    bugswarm_attempts = [r for r in rows if r["corpus"] == "bugswarm" and r["quality"] not in {"pending", "blocked"}]
    out = []
    if glm_kitsoki_attempts:
        tokens = sum(r["total_tokens"] or 0 for r in glm_kitsoki_attempts)
        out.append(
            f"Committed GLM-5.2 Kitsoki evidence contains {len(glm_kitsoki_attempts)} attempted OSS oracle cell(s), "
            f"{tokens} total tokens, and no solved cell yet."
        )
    if raw_prompt_attempts:
        tokens = sum(r["total_tokens"] or 0 for r in raw_prompt_attempts)
        out.append(f"Committed GLM-5.2 raw-prompt evidence contains {len(raw_prompt_attempts)} attempted cell(s) and {tokens} total tokens.")
    else:
        out.append("The GLM-5.2 raw-prompt arm remains pending; the report must not compute a token ratio from missing data.")
    if bugswarm_attempts:
        tokens = sum(r["total_tokens"] or 0 for r in bugswarm_attempts)
        out.append(f"Committed BugSwarm GLM-5.2 arena evidence contains {len(bugswarm_attempts)} attempted cell(s) and {tokens} total tokens.")
    if bugswarm_tasks:
        out.append(f"BugSwarm is reusable as an imported source with {len(bugswarm_tasks)} task(s) in the supplied source file.")
        if verification:
            out.append(
                f"BugSwarm verification report mode={verification.get('mode')} covers "
                f"{verification.get('task_count', 0)} task(s), with {verification.get('verified_count', 0)} verified."
            )
    else:
        out.append("BugSwarm is adapter-ready in the source catalog, but the committed report has no imported artifact subset yet.")
    return out


def build_references(sources: dict[str, Any], bugswarm_source: Path | None) -> dict[str, Any]:
    source_rows = sources.get("sources") if isinstance(sources.get("sources"), list) else []
    bugswarm = next((s for s in source_rows if isinstance(s, dict) and s.get("id") == "bugswarm"), {})
    upstream = bugswarm.get("upstream") if isinstance(bugswarm.get("upstream"), dict) else {}
    refs = {
        "local_evidence": [
            {
                "label": "GLM-5.2 bugfix bakeoff cells",
                "path": "tools/bugfix-bakeoff/results/cells",
                "purpose": "committed Kitsoki/raw-prompt bugfix cells and usage evidence",
            },
            {
                "label": "OSS oracle corpus",
                "path": "tools/arena/corpus/cost-bench.manifest.yaml",
                "purpose": "frozen reusable OSS task source and deterministic oracle metadata",
            },
            {
                "label": "BugSwarm source catalog",
                "path": "tools/arena/corpus/sources.yaml",
                "purpose": "adapter contract, required metadata fields, and verification contract",
            },
        ],
        "upstream": [
            {
                "label": "BugSwarm website",
                "url": str(upstream.get("website") or "https://www.bugswarm.org/"),
                "purpose": "dataset and project entry point",
            },
            {
                "label": "BugSwarm client",
                "url": str(upstream.get("client") or "https://github.com/BugSwarm/client"),
                "purpose": "artifact client and image execution interface",
            },
            {
                "label": "BugSwarm REST API",
                "url": str(upstream.get("rest_api") or "https://www.bugswarm.org/docs/toolset/bugswarm-rest-api/"),
                "purpose": "artifact metadata filtering and retrieval interface",
            },
            {
                "label": "BugSwarm paper",
                "url": str(upstream.get("paper") or "https://arxiv.org/abs/1903.06725"),
                "purpose": "published dataset/infrastructure description",
            },
        ],
        "bugswarm_seed": seed_references(bugswarm_source),
    }
    return refs


def seed_references(path: Path | None) -> list[dict[str, str]]:
    if path is None or not path.exists():
        return []
    data = load_yaml(path)
    refs: list[dict[str, str]] = []
    for task in data.get("tasks", []):
        if not isinstance(task, dict):
            continue
        meta = task.get("meta") if isinstance(task.get("meta"), dict) else {}
        source_url = str(meta.get("source_url") or "")
        if not source_url:
            continue
        refs.append({
            "label": f"BugSwarm seed artifact {task.get('image_tag') or task.get('id')}",
            "url": source_url,
            "purpose": str(meta.get("selection_note") or "seed artifact provenance"),
        })
    return refs


def render_markdown(report: dict[str, Any]) -> str:
    lines: list[str] = []
    corpora = report["corpora"]
    rollups = report["rollups"]["glm52_by_corpus_treatment"]
    overall_rollups = report["rollups"]["glm52_by_treatment_overall"]
    cells = report["glm52_bugfix_cells"]
    oss_arena_cells = report["oss_glm52_arena_cells"]
    bugswarm_cells = report["bugswarm_glm52_arena_cells"]
    inputs = report["inputs"]
    lines.extend([
        "# BugSwarm + GLM-5.2 bugfix comparison report",
        "",
        f"Generated at: `{report['generated_at']}`.",
        "",
        "This report is generated offline from committed evidence. It does not call",
        "BugSwarm, Docker, or any LLM. Missing cells are reported as `pending`.",
        "",
        "## Research Question",
        "",
        "Compare the Kitsoki `bugfix` pipeline with raw prompts on GLM-5.2,",
        "using total token usage as the primary cost axis, and prepare the same",
        "success-rate comparison for a BugSwarm corpus alongside the existing",
        "OSS oracle corpus.",
        "",
        "The current committed evidence does not yet contain the full GLM-5.2",
        "matrix. This report therefore separates observed results from missing",
        "cells instead of imputing raw-prompt or BugSwarm numbers.",
        "",
        "## Method",
        "",
        "The headline matrix has one row per `(corpus, treatment)` bucket.",
        "A cell is counted as attempted only when its quality is `solved`,",
        "`partial`, or `failed`; `pending` and `blocked` are excluded from the",
        "model-quality denominator. Token totals are summed only from committed",
        "cell evidence that records real usage.",
        "",
        "Inputs:",
        "",
        f"- Report JSON: `{inputs['report_json']}`.",
        f"- Report Markdown: `{inputs['report_markdown']}`.",
        f"- GLM-5.2 bakeoff cells: `{inputs['bakeoff_cells']}`.",
        f"- Arena supporting rollup: `{inputs['arena_rollup']}`.",
        f"- OSS oracle corpus: `{inputs['corpus']}`.",
        f"- Source catalog: `{inputs['sources']}`.",
        f"- OSS arena GLM rollup: `{inputs['oss_arena_rollup'] or 'not supplied'}`.",
        f"- BugSwarm source: `{inputs['bugswarm_source'] or 'not supplied'}`.",
        f"- BugSwarm verification report: `{inputs['bugswarm_verification'] or 'not supplied'}`.",
        f"- BugSwarm arena rollup: `{inputs['bugswarm_arena_rollup'] or 'not supplied'}`.",
        "",
        "Primary metrics:",
        "",
        "- success rate: `solved / (solved + partial + failed)`.",
        "- partial rate: reported separately because hidden oracles can be",
        "  implementation-coupled.",
        "- total tokens: provider-neutral primary cost measure.",
        "- USD cost: secondary; only shown where committed cell evidence provides",
        "  it.",
        "",
        "## Corpus Coverage",
        "",
        "| corpus | tasks | repositories | verified/imported status |",
        "|---|---:|---:|---|",
        f"| OSS oracle corpus | {corpora['oss_oracle']['task_count']} | {corpora['oss_oracle']['repo_count']} | frozen and locally validated |",
        f"| BugSwarm | {corpora['bugswarm']['imported_task_count']} | n/a | {corpora['bugswarm']['source_status']}; converted verified tasks: {corpora['bugswarm']['verified_task_count']}; verification report: {corpora['bugswarm']['verification_verified_count']}/{corpora['bugswarm']['verification_report_count']} ({corpora['bugswarm']['verification_mode'] or 'none'}) |",
        "",
        "The OSS oracle corpus remains the active internal benchmark source. It",
        "covers the pre-registered public OSS targets plus existing hidden-oracle",
        "bugfix fixtures. BugSwarm is represented separately in the source",
        "catalog, so its fail/pass CI artifact sampling process does not get",
        "collapsed into the OSS oracle denominator.",
        "",
        "BugSwarm source contract:",
        "",
        "- import explicit exported artifact metadata with",
        "  `tools/arena/scripts/bugswarm_to_arena.py`.",
        "- require `image_tag`, `repo`, `failed_job_id`, and `passed_job_id`.",
        "- treat the failed job as RED and the passed job as GREEN inside the",
        "  artifact image.",
        "- keep imported tasks unattempted until Docker verification proves both",
        "  sides still reproduce.",
        "- verify with `tools/arena/scripts/bugswarm_verify_source.py`; dry-run",
        "  mode records the Docker commands, while `--execute` runs each side in",
        "  separate fresh containers.",
        "",
    ])
    lines.extend(render_source_mix(report))
    lines.extend(render_reproducibility(report))
    lines.extend([
        "",
        "## GLM-5.2 Headline Matrix",
        "",
        "| corpus | treatment | n | attempted | solved | partial | failed | pending | success rate | tokens |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|",
    ])
    for key, bucket in sorted(rollups.items()):
        corpus, treatment = key.split("|", 1)
        lines.append(
            "| {corpus} | {treatment} | {n} | {attempted} | {solved} | {partial} | {failed} | {pending} | {rate} | {tokens} |".format(
                corpus=corpus,
                treatment=treatment,
                n=bucket["n"],
                attempted=bucket["attempted"],
                solved=bucket["solved"],
                partial=bucket["partial"],
                failed=bucket["failed"],
                pending=bucket["pending"],
                rate=format_rate(bucket["success_rate"]),
                tokens=format_int(bucket["total_tokens"]),
            )
        )
    lines.extend([
        "",
        "## Overall GLM-5.2 Treatment Rollup",
        "",
        "| treatment | n | attempted | solved | partial | failed | pending | success rate | tokens |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|",
    ])
    for treatment, bucket in sorted(overall_rollups.items()):
        lines.append(
            "| {treatment} | {n} | {attempted} | {solved} | {partial} | {failed} | {pending} | {rate} | {tokens} |".format(
                treatment=treatment,
                n=bucket["n"],
                attempted=bucket["attempted"],
                solved=bucket["solved"],
                partial=bucket["partial"],
                failed=bucket["failed"],
                pending=bucket["pending"],
                rate=format_rate(bucket["success_rate"]),
                tokens=format_int(bucket["total_tokens"]),
            )
        )
    lines.extend(render_comparisons(report))
    lines.extend(render_claim_ledger(report))
    lines.extend(render_threats_to_validity(report))
    lines.extend(render_completion_audit(report))
    lines.extend(render_study_protocol(report))
    lines.extend([
        "",
        "## Committed GLM-5.2 Cells",
        "",
        "| task | treatment | quality | tokens | cost | evidence |",
        "|---|---|---|---:|---:|---|",
    ])
    if cells:
        for cell in cells:
            lines.append(
                f"| {cell['task']} | {cell['treatment']} | {cell['quality']} | "
                f"{format_int(cell['total_tokens'])} | {format_cost(cell['cost_usd'])} | `{cell['evidence']}` |"
            )
    else:
        lines.append("| none | none | pending | n/a | n/a | n/a |")
    lines.extend([
        "",
        "## Committed OSS GLM-5.2 Arena Cells",
        "",
        "| task | treatment | quality | tokens | cost | evidence |",
        "|---|---|---|---:|---:|---|",
    ])
    if oss_arena_cells:
        for cell in oss_arena_cells:
            lines.append(
                f"| {cell['task']} | {cell['treatment']} | {cell['quality']} | "
                f"{format_int(cell['total_tokens'])} | {format_cost(cell['cost_usd'])} | `{cell['evidence']}` |"
            )
    else:
        lines.append("| none | none | pending | n/a | n/a | n/a |")
    lines.extend([
        "",
        "## Committed BugSwarm GLM-5.2 Arena Cells",
        "",
        "| task | treatment | quality | tokens | cost | evidence |",
        "|---|---|---|---:|---:|---|",
    ])
    if bugswarm_cells:
        for cell in bugswarm_cells:
            lines.append(
                f"| {cell['task']} | {cell['treatment']} | {cell['quality']} | "
                f"{format_int(cell['total_tokens'])} | {format_cost(cell['cost_usd'])} | `{cell['evidence']}` |"
            )
    else:
        lines.append("| none | none | pending | n/a | n/a | n/a |")
    lines.extend([
        "",
        "## Evidence Gaps",
        "",
    ])
    lines.extend(f"- {gap}" for gap in report["evidence_gaps"])
    lines.extend(render_evidence_closure_packet(report))
    lines.extend([
        "",
        "## Interpretation",
        "",
    ])
    lines.extend(f"- {item}" for item in report["interpretation"])
    lines.extend(render_references(report))
    lines.extend([
        "",
        "Bottom line: the committed GLM-5.2 evidence is not yet sufficient to",
        "claim Kitsoki beats or loses to raw prompts. The report is useful now as",
        "a reproducible evidence ledger and corpus scaffold; the headline",
        "comparison still requires raw-prompt GLM-5.2 cells and verified",
        "BugSwarm cells.",
    ])
    lines.extend([
        "",
        "## Supporting Codex-Native OSS Round",
        "",
        "The existing arena `round-1` results are supporting evidence for the",
        "Kitsoki-vs-raw-prompt harness and token accounting, but they are not",
        "GLM-5.2 cells. They should not be used to answer the GLM headline.",
        "",
    ])
    supporting = report["rollups"]["supporting_oss_codex_round1"]
    if supporting:
        lines.extend([
            "| treatment | n | attempted | solved | failed | success rate | tokens |",
            "|---|---:|---:|---:|---:|---:|---:|",
        ])
        for treatment, bucket in sorted(supporting.items()):
            lines.append(
                f"| {treatment} | {bucket['n']} | {bucket['attempted']} | {bucket['solved']} | "
                f"{bucket['failed']} | {format_rate(bucket['success_rate'])} | {format_int(bucket['total_tokens'])} |"
            )
    return "\n".join(lines) + "\n"


def render_source_mix(report: dict[str, Any]) -> list[str]:
    mix = report["source_mix"]
    components = [component for component in mix["oss_oracle"]["components"] if component["task_count"] > 0]
    lines = [
        "## Source Mix",
        "",
        "The OSS oracle source and BugSwarm are kept as separate source families",
        "so the report can show blended overall treatment totals without hiding",
        "which evidence came from deterministic GitHub-content oracles, hidden",
        "bugfix fixtures, or containerized fail/pass CI artifacts.",
        "",
        "| source component | tasks | repos | oracle kinds | split | repositories |",
        "|---|---:|---:|---|---|---|",
    ]
    for component in components:
        lines.append(
            "| {id} | {tasks} | {repos} | {oracles} | {splits} | {repositories} |".format(
                id=component["id"],
                tasks=component["task_count"],
                repos=component["repo_count"],
                oracles=", ".join(component["oracle_kinds"]) or "n/a",
                splits=format_counts(component["splits"]),
                repositories=", ".join(component["repositories"]) or "n/a",
            )
        )
    bugswarm = mix["bugswarm"]
    lines.append(
        "| BugSwarm {component} | {tasks} | {repos} | fail/pass artifact scripts | verification-gated | {repositories} |".format(
            component=bugswarm["component"],
            tasks=bugswarm["task_count"],
            repos=bugswarm["repo_count"],
            repositories=", ".join(bugswarm["repositories"]) or "pending verified import",
        )
    )
    lines.extend([
        "",
        "Blend policy:",
        "",
    ])
    lines.extend(f"- {policy}" for policy in mix["blend_policy"])
    return lines


def render_reproducibility(report: dict[str, Any]) -> list[str]:
    ledger = report["reproducibility"]
    generator = ledger["generator"]
    lines = [
        "",
        "## Reproducibility Ledger",
        "",
        f"Status: `{ledger['status']}`. Generator: `{generator['path']}` "
        f"sha256 `{generator.get('sha256', 'missing')}`.",
        "",
        "Regenerate with:",
        "",
        "```bash",
        " ".join(ledger["regenerate_command"]),
        "```",
        "",
        "| artifact | kind | status | bytes | sha256 |",
        "|---|---|---|---:|---|",
    ]
    for artifact in ledger.get("artifacts") or []:
        if artifact.get("kind") == "directory-glob":
            status = f"{artifact.get('match_count', 0)} match(es)"
            lines.append(f"| `{artifact['path']}/{artifact['pattern']}` | directory-glob | {status} | n/a | n/a |")
            for matched in artifact.get("files") or []:
                lines.append(
                    f"| `{matched['path']}` | file | present | {format_int(matched.get('bytes'))} | `{matched.get('sha256', 'missing')}` |"
                )
            continue
        status = "present" if artifact.get("exists") else "missing"
        sha = artifact.get("sha256") or "n/a"
        lines.append(
            f"| `{artifact.get('path') or artifact.get('role')}` | {artifact.get('kind')} | "
            f"{status} | {format_int(artifact.get('bytes'))} | `{sha}` |"
        )
    lines.extend([
        "",
        "Validation commands:",
        "",
    ])
    lines.extend(f"- `{command}`" for command in ledger.get("validation_commands") or [])
    return lines


def render_claim_ledger(report: dict[str, Any]) -> list[str]:
    ledger = report["claim_ledger"]
    lines = [
        "",
        "## Research Claim Ledger",
        "",
        f"Status: `{ledger['status']}` ({ledger['supported_count']} supported, {ledger['pending_count']} pending).",
        "",
        "Publication gate:",
        "",
        "```bash",
        "python3 tools/arena/scripts/glm52_report_gate.py \\",
        "  --report-json docs/case-studies/bugswarm-glm52-bugfix-report.data.json \\",
        "  --require-publishable",
        "```",
        "",
        "| claim | status | finding | missing evidence / caveat |",
        "|---|---|---|---|",
    ]
    for claim in ledger["claims"]:
        missing = "; ".join(claim.get("missing_evidence") or [])
        caveat = str(claim.get("caveat") or "")
        detail = "; ".join(item for item in (missing, caveat) if item) or "none"
        lines.append(
            f"| {claim['id']} | `{claim['status']}` | "
            f"{clean_sentence(str(claim.get('finding') or ''))} | "
            f"{clean_sentence(detail)} |"
        )
    return lines


def render_threats_to_validity(report: dict[str, Any]) -> list[str]:
    ledger = report["threats_to_validity"]
    lines = [
        "",
        "## Threats To Validity",
        "",
        f"Status: `{ledger['status']}` ({ledger['active_count']} active, {ledger['high_count']} high severity).",
        "",
        "| threat | category | severity | status | mitigation |",
        "|---|---|---|---|---|",
    ]
    for threat in ledger["threats"]:
        lines.append(
            f"| {threat['id']} | {threat['category']} | `{threat['severity']}` | "
            f"`{threat['status']}` | {clean_sentence(str(threat.get('mitigation') or ''))} |"
        )
    return lines


def format_counts(counts: dict[str, int]) -> str:
    if not counts:
        return "n/a"
    return ", ".join(f"{key}:{value}" for key, value in sorted(counts.items()))


def render_completion_audit(report: dict[str, Any]) -> list[str]:
    audit = report["completion_audit"]
    lines = [
        "",
        "## Completion Audit",
        "",
        f"Status: `{audit['status']}` ({audit['proven_count']}/{audit['requirement_count']} requirements proven).",
        "",
        "| requirement | status | finding | next |",
        "|---|---|---|---|",
    ]
    for item in audit["requirements"]:
        next_step = item.get("next") or "done"
        lines.append(
            f"| {item['id']} | `{item['status']}` | "
            f"{clean_sentence(str(item.get('finding') or ''))} | "
            f"{clean_sentence(str(next_step))} |"
        )
    return lines


def render_study_protocol(report: dict[str, Any]) -> list[str]:
    protocol = report["study_protocol"]
    lines = [
        "",
        "## Study Protocol",
        "",
        f"Status: `{protocol['status']}`. Candidate: `{protocol['candidate']}`. "
        f"Primary cost metric: `{protocol['primary_cost_metric']}`.",
        "",
        f"Success metric: `{protocol['success_metric']}`.",
        "",
        "| corpus | task | treatment | gate |",
        "|---|---|---|---|",
    ]
    pending_cells = protocol.get("pending_cells") or []
    if pending_cells:
        for cell in pending_cells:
            lines.append(f"| {cell['corpus']} | {cell['task']} | {cell['treatment']} | `{cell['gate']}` |")
    else:
        lines.append("| none | none | none | complete |")
    lines.extend([
        "",
        "Execution steps:",
        "",
    ])
    for step in protocol.get("execution_steps") or []:
        lines.extend([
            f"- `{step['id']}`: `{step['status']}`; {clean_sentence(str(step.get('purpose') or ''))}.",
        ])
        if step.get("report_arg"):
            lines.append(f"  Report regeneration argument: `{step['report_arg']}`.")
        if step.get("commands"):
            lines.append("  Commands:")
            for command in step["commands"]:
                lines.append(f"  - `{command}`")
    lines.extend([
        "",
        "Live controls:",
        "",
    ])
    lines.extend(f"- {control}" for control in protocol.get("live_controls") or [])
    return lines


def render_evidence_closure_packet(report: dict[str, Any]) -> list[str]:
    inputs = report["inputs"]
    rows = [
        row for row in report["required_glm52_matrix"]
        if row["quality"] == "pending"
    ]
    if not rows:
        return [
            "",
            "## Evidence Closure Packet",
            "",
            "No pending GLM-5.2 headline cells remain in this report.",
        ]
    closure = report["evidence_closure"]
    commands = [
        "python3 tools/arena/scripts/glm52_gap_plan.py \\",
        f"  --report-json {inputs['report_json']} \\",
        "  --json-out .artifacts/arena/glm52-gap-plan.json \\",
        "  --markdown-out .artifacts/arena/glm52-gap-plan.md",
    ]
    if inputs.get("oss_arena_rollup"):
        commands.append("  # pass --oss-spec <paired-task spec> when scheduling missing OSS cells")
    if inputs.get("bugswarm_source"):
        commands[-1] += " \\"
        commands.append(f"  --bugswarm-source {inputs['bugswarm_source']}")
    else:
        commands.append("  # pass --bugswarm-source <execute-verified BugSwarm YAML> after importing artifacts")
    return [
        "",
        "## Evidence Closure Packet",
        "",
        "Generate the offline execution packet for the pending headline cells with:",
        "",
        "```bash",
        *commands,
        "```",
        "",
        "The packet emits no-spend `arena.py plan` / arming commands and, only",
        "after a spec passes audit, explicit `ARENA_PAIRED_TASK_ENABLE_CODEX=1",
        "... --live` commands for operator execution.",
        "",
        "| corpus | status | pending | next |",
        "|---|---|---:|---|",
        *[
            f"| {action['corpus']} | `{action['status']}` | {action['pending_count']} | {action['next']} |"
            for action in closure["actions"]
        ],
    ]


def render_comparisons(report: dict[str, Any]) -> list[str]:
    lines = [
        "",
        "## Kitsoki vs Raw-Prompt Comparisons",
        "",
        "| scope | status | Kitsoki attempted | raw attempted | success delta | token ratio | notes |",
        "|---|---|---:|---:|---:|---:|---|",
    ]
    for scope, comparison in sorted(report["comparisons"].items()):
        notes = "; ".join(comparison.get("notes") or []) or "complete"
        lines.append(
            "| {scope} | {status} | {kitsoki_attempted} | {raw_attempted} | {delta} | {ratio} | {notes} |".format(
                scope=scope,
                status=comparison["status"],
                kitsoki_attempted=comparison["kitsoki"]["attempted"],
                raw_attempted=comparison["raw_prompt"]["attempted"],
                delta=format_signed_rate(comparison["success_rate_delta"]),
                ratio=format_float(comparison["token_ratio_kitsoki_to_raw"]),
                notes=notes,
            )
        )
    return lines


def render_references(report: dict[str, Any]) -> list[str]:
    refs = report["references"]
    lines = [
        "",
        "## Provenance and References",
        "",
        "Local evidence:",
        "",
    ]
    for ref in refs.get("local_evidence", []):
        lines.append(f"- `{ref['path']}` — {clean_sentence(ref['purpose'])}.")
    lines.extend([
        "",
        "Upstream references:",
        "",
    ])
    for ref in refs.get("upstream", []):
        lines.append(f"- {ref['label']}: {ref['url']} — {clean_sentence(ref['purpose'])}.")
    seed_refs = refs.get("bugswarm_seed", [])
    if seed_refs:
        lines.extend([
            "",
            "BugSwarm seed provenance:",
            "",
        ])
        for ref in seed_refs:
            lines.append(f"- {ref['label']}: {ref['url']} — {clean_sentence(ref['purpose'])}.")
    return lines


def clean_sentence(value: str) -> str:
    return value.strip().rstrip(".")


def load_yaml(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    return data if isinstance(data, dict) else {}


def write_json(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def write_text(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def normalize_treatment(value: str) -> str:
    value = value.strip().lower()
    if value in {"single", "single-briefed", "single-naive", "raw", "raw-prompt"}:
        return "raw-prompt"
    if value == "kitsoki":
        return "kitsoki"
    return value or "unknown"


def treatment_from_variant(variant: str) -> str:
    lowered = variant.lower()
    if lowered.startswith("kitsoki"):
        return "kitsoki"
    if lowered.startswith("single") or lowered.startswith("raw-prompt"):
        return "raw-prompt"
    return "unknown"


def normalize_quality(value: str) -> str:
    value = value.strip().lower()
    return value if value in QUALITY_VALUES else "pending"


def as_int(value: Any) -> int | None:
    return value if isinstance(value, int) else None


def as_float(value: Any) -> float | None:
    return float(value) if isinstance(value, (int, float)) else None


def format_int(value: Any) -> str:
    return f"{value:,}" if isinstance(value, int) else "n/a"


def format_cost(value: Any) -> str:
    return f"${value:.6f}" if isinstance(value, (int, float)) else "n/a"


def format_rate(value: Any) -> str:
    return f"{value:.3f}" if isinstance(value, (int, float)) else "n/a"


def format_signed_rate(value: Any) -> str:
    return f"{value:+.3f}" if isinstance(value, (int, float)) else "n/a"


def format_float(value: Any) -> str:
    return f"{value:.3f}" if isinstance(value, (int, float)) else "n/a"


def rel(path: Path | None) -> str:
    if path is None:
        return ""
    try:
        return str(path.resolve().relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


if __name__ == "__main__":
    raise SystemExit(main())
