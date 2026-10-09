#!/usr/bin/env python3
"""arena — CLI front door for the generalized comparison-job runner.

Subcommands:
  treatments                 list reusable action-surface treatment drivers
  validate --spec S          validate a job spec (no Docker, no LLM)
  doctor [--spec S]          validate local prerequisites such as Docker
  plan   --spec S            enumerate cells for a job spec (no execution)
  run    --spec S --out D    run the sweep in containers, write per-cell results + rollup
                             (no-LLM arming by default; --live to spend)
  plugins                    list registered job types

Cost discipline: `run` defaults to the deterministic no-LLM path (oracle arming
for bugfix). `--live` is the only way to spend, and it is explicit.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

from arena.executor import CellExecutor, DockerBackend
from arena.model import (
    Cell, JobSpec, TaskOptimizationStudy, canonical_json, load_task_optimization_receipts,
    receipt_sha256, analyze_task_optimization, compare_task_optimization, select_task_optimization_champion, task_optimization_status,
)
from arena.task_optimization_receipt import validate_scored_attempt_receipt
from arena.task_optimization_runner import run as run_task_optimization
from arena.placement import run_sweep
from arena.plugins import base as plugins
from arena.rollup import write_rollup
from arena.treatments import treatment_catalog, validate_driver_errors

# Default mounts: the kitsoki checkout (carrying the bakeoff harness + bench.py)
# read-write into the container at the path the plugins expect.
REPO_ROOT = Path(__file__).resolve().parents[2]


def _make_mounts_for(spec):
    """Build a (cell, host) → mounts resolver from the spec's placement.

    The kitsoki checkout (carrying bench.py + the bakeoff harness) mounts to
    `/workspace/kitsoki`. A remote host resolves `-v` source paths on ITS OWN
    daemon, so `placement.host_repo[host]` declares the checkout path on that
    host; `local` defaults to this machine's REPO_ROOT.
    """
    host_repo = dict(spec.placement.host_repo)
    host_repo.setdefault("local", str(REPO_ROOT))

    def _mounts_for(cell: Cell, host: str) -> dict[str, str]:
        src = host_repo.get(host)
        if src is None:
            raise SystemExit(
                f"placement.host_repo has no checkout path for host '{host}'. "
                f"Add it to the spec (e.g. host_repo:\n    {host}: /opt/bakeoff/repos/kitsoki)."
            )
        mounts = {src: "/workspace/kitsoki"}
        primary_git_dir = _primary_git_dir_for_worktree(Path(src))
        if primary_git_dir is not None:
            # A git-worktree checkout's `.git` is a FILE pointing at an absolute
            # host path (the primary checkout's `.git/worktrees/<name>`, whose
            # own `commondir` then points back at the primary `.git` for shared
            # objects/refs). Verified live: any git command run against a
            # worktree mount fails inside the container with "fatal: not a git
            # repository: <that absolute path>" unless the SAME absolute path
            # also resolves inside the container — so mount the primary
            # checkout's .git at its own identical path (read-only: cells only
            # need to read history, e.g. `git clone --local` for project=kitsoki
            # tasks; never write back into the primary checkout).
            mounts[primary_git_dir] = f"{primary_git_dir}:ro"
        codex_home = os.environ.get("ARENA_CODEX_HOME_SRC")
        if codex_home:
            mounts[codex_home] = "/workspace/codex-home"
        return mounts

    return _mounts_for


def _primary_git_dir_for_worktree(checkout: Path) -> str | None:
    """If `checkout` is a git WORKTREE (`.git` is a file, not a dir), return the
    primary checkout's `.git` directory path (absolute, host-side) so the
    caller can mount it at the identical path inside the container. Returns
    None for an ordinary checkout (`.git` already a directory) — nothing extra
    to mount."""
    git_path = checkout / ".git"
    if git_path.is_dir() or not git_path.is_file():
        return None
    text = git_path.read_text(encoding="utf-8").strip()
    if not text.startswith("gitdir:"):
        return None
    worktree_git_dir = Path(text[len("gitdir:"):].strip())
    # worktree_git_dir looks like <primary>/.git/worktrees/<name>; the primary
    # .git dir is two levels up.
    primary_git_dir = worktree_git_dir.parent.parent
    return str(primary_git_dir) if primary_git_dir.is_dir() else None


def cmd_plan(args: argparse.Namespace) -> int:
    spec = JobSpec.load(args.spec)
    cells = spec.cells()
    check_types = [c.check_type for c in spec.checks]
    print(
        f"job_type={spec.job_type}  cells={len(cells)}  hosts={spec.placement.hosts}"
        f"  checks={check_types}"
    )
    if spec.targets_from:
        print(f"  targets_from={spec.targets_from} (product-journey corpus, read-only)")
    if spec.target_proof_from:
        print(f"  target_proof_from={spec.target_proof_from}")
    if spec.persona_axis_from:
        print(f"  persona_axis_from={spec.persona_axis_from} (product-journey corpus, read-only)")
    for c in cells:
        print(f"  {c.id}")
    return 0


def cmd_treatments(args: argparse.Namespace) -> int:
    rows = treatment_catalog(include_aliases=args.aliases)
    if args.json:
        print(json.dumps(rows, indent=2, sort_keys=True))
        return 0
    columns = [
        ("id", "Treatment"),
        ("action_surface", "Surface"),
        ("driver", "Driver"),
        ("required_variant_fields", "Requires"),
        ("option_fields", "Options"),
        ("aliases", "Aliases"),
        ("summary", "Summary"),
    ]
    table = []
    for row in rows:
        table.append({
            "id": row.get("id", ""),
            "action_surface": row.get("action_surface", ""),
            "driver": row.get("driver", ""),
            "required_variant_fields": ", ".join(row.get("required_variant_fields", []) or []),
            "option_fields": ", ".join(row.get("option_fields", []) or []),
            "aliases": ", ".join(row.get("aliases", []) or ([f"alias for {row['alias_for']}"] if row.get("alias_for") else [])),
            "summary": row.get("summary", ""),
        })
    print(_format_table(table, columns))
    return 0


def cmd_validate(args: argparse.Namespace) -> int:
    errors, warnings = validate_spec(args.spec, live=args.live)
    for warning in warnings:
        print(f"WARN: {warning}")
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print(f"OK: {args.spec}")
    return 0


def cmd_doctor(args: argparse.Namespace) -> int:
    errors, warnings = validate_spec(args.spec, live=args.live) if args.spec else ([], [])
    print("Arena doctor", flush=True)
    if args.spec:
        print(f"- spec: {args.spec}", flush=True)
    docker = _check_docker()
    if docker:
        errors.append(docker)
    else:
        print("- docker: OK", flush=True)
    for warning in warnings:
        print(f"WARN: {warning}", flush=True)
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("OK: arena is ready")
    return 0


def cmd_run(args: argparse.Namespace) -> int:
    spec = JobSpec.load(args.spec)
    backend = DockerBackend()
    executor = CellExecutor(backend, mounts_for=_make_mounts_for(spec))
    if args.live:
        print("LIVE run — this WILL spend on LLM calls.", file=sys.stderr)
    results = run_sweep(
        spec, executor, live=args.live,
        on_result=lambda r: print(f"  {r.cell_id} ({r.check_type}): {r.verdict} [{r.health}]"),
    )
    paths = write_rollup(results, args.out)
    write_run_manifest(spec, args.spec, args.out, args.live)
    print(f"\nrollup → {paths['summary']}")
    return 0


def write_run_manifest(spec: JobSpec, spec_path: str, out_dir: str, live: bool) -> None:
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    lines = [
        "kind: arena_run",
        f"run_id: {out.name}",
        f"spec_path: {spec_path}",
        f"job_type: {spec.job_type}",
        f"live: {'true' if live else 'false'}",
        f"targets: {len(spec.targets)}",
        f"variants: {len(spec.variants)}",
        f"cells: {len(spec.cells())}",
        "checks:",
    ]
    lines.extend(f"  - {check.check_type}" for check in spec.checks)
    lines.append("artifacts:")
    lines.extend([
        "  summary_json: summary.json",
        "  report_md: report.md",
        "  deck_slidey_json: deck.slidey.json",
        "  rollup_json: rollup.json",
        "  cells_dir: cells/",
    ])
    (out / "run.yaml").write_text("\n".join(lines) + "\n", encoding="utf-8")


def cmd_plugins(_args: argparse.Namespace) -> int:
    for name in plugins.known():
        print(name)
    return 0


def _write_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    # Status/analysis artifacts are replaceable projections, but readers must
    # never observe a partially-written JSON document.
    payload = (json.dumps(value, indent=2, sort_keys=True) + "\n").encode("utf-8")
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix=f".{path.name}.", delete=False) as tmp:
        tmp.write(payload)
        tmp.flush()
        os.fsync(tmp.fileno())
        temporary = Path(tmp.name)
    os.replace(temporary, path)


def _study_or_error(path: str) -> TaskOptimizationStudy | None:
    try:
        return TaskOptimizationStudy.load(path)
    except (OSError, ValueError) as exc:
        print(f"ERROR: could not load task-optimization study {path}: {exc}", file=sys.stderr)
        return None


def cmd_task_optimization_validate(args: argparse.Namespace) -> int:
    study = _study_or_error(args.study)
    if study is None:
        return 1
    if study.blocked_by:
        print(f"BLOCKED: {study.study_id}: {'; '.join(study.blocked_by)}")
        return 0
    try:
        corpus = study.corpus()
    except (OSError, ValueError) as exc:
        print(f"ERROR: invalid corpus lock: {exc}", file=sys.stderr)
        return 1
    print(f"OK: {study.study_id} tasks={len(corpus['tasks'])} candidates={len(study.candidates)} treatments={len(study.treatments)}")
    return 0


def cmd_task_optimization_plan(args: argparse.Namespace) -> int:
    study = _study_or_error(args.study)
    if study is None:
        return 1
    if study.blocked_by:
        print(f"ERROR: task-optimization study is blocked: {'; '.join(study.blocked_by)}", file=sys.stderr)
        return 1
    try:
        plan = study.plan(repeat_phase=args.repeat_phase)
        lock = study.lock()
    except (OSError, ValueError) as exc:
        print(f"ERROR: could not materialize task-optimization plan: {exc}", file=sys.stderr)
        return 1
    out = Path(args.out)
    _write_json(out / "plan.json", plan)
    _write_json(out / "study.lock.json", lock)
    lines = [f"# Task optimization plan: {study.study_id}", "", f"- cells: **{plan['cell_count']}**",
             f"- repeat phase: `{args.repeat_phase}`", f"- live gate: `{study.live_gate_env}=1` plus explicit `--live`", "",
             "## Cells", ""]
    lines.extend(f"- `{cell['id']}` — {cell['split']} — {cell['status']}" for cell in plan["cells"])
    (out / "plan.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"plan → {out / 'plan.json'} ({plan['cell_count']} cells)")
    return 0


def cmd_task_optimization_arm(args: argparse.Namespace) -> int:
    """Create a no-spend arm receipt; live intent is gated even at arming.

    This command intentionally does not dispatch a provider. It exists so an
    operator cannot mistake a generated plan for authorization to spend.
    """
    study = _study_or_error(args.study)
    if study is None:
        return 1
    if study.blocked_by:
        print(f"ERROR: task-optimization study is blocked: {'; '.join(study.blocked_by)}", file=sys.stderr)
        return 1
    if not args.live or os.environ.get(study.live_gate_env) != "1":
        print(f"ERROR: task-optimization arming requires --live and {study.live_gate_env}=1; no provider was called", file=sys.stderr)
        return 1
    try:
        plan = study.plan(repeat_phase=args.repeat_phase)
        lock = study.lock()
    except (OSError, ValueError) as exc:
        print(f"ERROR: could not arm task-optimization study: {exc}", file=sys.stderr)
        return 1
    out = Path(args.out)
    _write_json(out / "study.lock.json", lock)
    _write_json(out / "armed.json", {"schema": "task-optimization/arm/v1", "study_id": study.study_id,
                                       "cell_count": plan["cell_count"], "live_gate_env": study.live_gate_env,
                                       "status": "armed", "provider_dispatched": False})
    print(f"armed → {out / 'armed.json'} (no provider dispatched)")
    return 0


def _load_json(path: str | Path, *, label: str) -> dict[str, object]:
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"{label} must be a JSON object")
    return data


def _write_immutable_json(path: Path, value: object) -> None:
    """Create an evidence receipt once; never silently replace it."""
    payload = canonical_json(value)
    path.parent.mkdir(parents=True, exist_ok=True)
    # O_EXCL gives concurrent recorders a single linearizable append point.
    # The loser may only accept byte-identical evidence, never replace it.
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o644)
    except FileExistsError:
        if path.read_bytes() != payload:
            raise ValueError(f"immutable receipt already exists with different bytes: {path}")
        return
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(payload)
            out.flush()
            os.fsync(out.fileno())
    except BaseException:
        # A write failure must not leave a corrupt receipt that blocks resume.
        try:
            path.unlink()
        except FileNotFoundError:
            pass
        raise


def cmd_task_optimization_preflight(args: argparse.Namespace) -> int:
    study = _study_or_error(args.study)
    if study is None:
        return 1
    if study.blocked_by:
        print(f"ERROR: task-optimization study is blocked: {'; '.join(study.blocked_by)}", file=sys.stderr)
        return 1
    try:
        receipt = study.preflight(config_path=args.config, launch_agent=args.launch_agent,
                                  working_dir=args.working_dir)
        _write_immutable_json(Path(args.out) / "preflight.json", receipt)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not preflight task-optimization study: {exc}", file=sys.stderr)
        return 1
    invalid = [record["candidate_id"] for record in receipt["candidates"] if record["status"] == "invalid"]
    unsupported = [record["candidate_id"] for record in receipt["candidates"] if record["status"] == "unsupported"]
    print(f"preflight → {Path(args.out) / 'preflight.json'} ({len(receipt['candidates'])} candidate(s); invalid={len(invalid)}; unsupported={len(unsupported)})")
    return 1 if invalid else 0


def _task_optimization_inputs(args: argparse.Namespace) -> tuple[dict[str, object], dict[str, object], list[dict[str, object]]]:
    plan = _load_json(args.plan, label="plan")
    preflight = _load_json(args.preflight, label="preflight")
    if plan.get("schema") != "task-optimization/v1":
        raise ValueError("plan has unsupported task-optimization schema")
    receipts = load_task_optimization_receipts(args.attempts, plan=plan, preflight=preflight)
    return plan, preflight, receipts


def cmd_task_optimization_record(args: argparse.Namespace) -> int:
    """Append one pre-scored attempt receipt after verifying frozen inputs."""
    try:
        if Path(args.attempts).resolve() != Path(args.out).resolve():
            raise ValueError("attempts and out must name the same append-only receipt directory")
        plan, preflight, _ = _task_optimization_inputs(args)
        receipt = _load_json(args.receipt, label="attempt receipt")
        if receipt.get("schema") != "task-optimization/attempt/v1":
            raise ValueError("attempt receipt has unsupported schema")
        if receipt.get("study_id") != plan.get("study_id"):
            raise ValueError("attempt receipt study_id does not match plan")
        if receipt.get("plan_sha256") != receipt_sha256(plan):
            raise ValueError("attempt receipt plan_sha256 does not match immutable plan")
        expected_preflight = str(preflight.get("preflight_sha256") or receipt_sha256(preflight))
        if receipt.get("preflight_sha256") != expected_preflight:
            raise ValueError("attempt receipt preflight_sha256 does not match immutable preflight")
        attempt_id = str(receipt.get("attempt_id") or "")
        cell_id = str(receipt.get("cell_id") or "")
        if not attempt_id or not cell_id:
            raise ValueError("attempt receipt requires non-empty attempt_id and cell_id")
        planned = {str(cell["id"]): cell for cell in plan.get("cells", [])}
        if cell_id not in planned:
            raise ValueError("attempt receipt cell_id is absent from immutable plan")
        candidate_id = str(receipt.get("candidate_id") or "")
        if candidate_id != str(planned[cell_id].get("candidate_id") or ""):
            raise ValueError("attempt receipt candidate_id does not match planned cell")
        preflight_candidates = {str(record.get("candidate_id")): record for record in preflight.get("candidates", []) if isinstance(record, dict)}
        if preflight_candidates.get(candidate_id, {}).get("status") != "ready":
            raise ValueError("attempt receipt candidate lacks a ready preflight receipt")
        validate_scored_attempt_receipt(
            receipt,
            receipt_path=args.receipt,
            preflight_candidate=preflight_candidates[candidate_id],
            requires_codeact_runtime="codeact" in str(planned[cell_id].get("treatment") or ""),
        )
        destination = Path(args.out) / cell_id / attempt_id / "receipt.json"
        _write_immutable_json(destination, receipt)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not record task-optimization attempt: {exc}", file=sys.stderr)
        return 1
    print(f"attempt → {destination}")
    return 0


def cmd_task_optimization_run(args: argparse.Namespace) -> int:
    """Run frozen campaign cells through an explicit, injectable executor.

    The command never guesses a provider command.  A live caller supplies a
    treatment adapter which receives the materialized request path and emits a
    canonical attempt receipt on stdout; its scoring evidence is still checked
    before Arena commits the receipt.  Without --live this is a no-spend plan.
    """
    try:
        plan, preflight, _ = _task_optimization_inputs(args)

        def execute(_cell: dict[str, object], request: dict[str, object]) -> dict[str, object]:
            if not args.executor_command:
                raise ValueError("live task-optimization dispatch requires --executor-command")
            command = [*args.executor_command, "--request", str(request["request_path"])]
            completed = subprocess.run(command, text=True, capture_output=True, check=False)
            if completed.returncode != 0:
                # A launcher failure is infrastructure evidence, not a model
                # result.  It remains resumable under the plan retry budget.
                return {"status": "blocked", "health": "infra:executor",
                        "retryable": True, "notes": _first_nonempty_line(completed.stderr) or f"executor exit {completed.returncode}"}
            try:
                value = json.loads(completed.stdout)
            except json.JSONDecodeError as exc:
                raise ValueError("executor stdout must be one JSON attempt receipt") from exc
            if not isinstance(value, dict):
                raise ValueError("executor stdout must be one JSON attempt receipt object")
            return value

        summary = run_task_optimization(
            plan=plan, preflight=preflight, attempts_dir=args.attempts, out_dir=args.out,
            live=args.live, executor=execute if args.live else None, max_cells=args.max_cells,
        )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not run task-optimization campaign: {exc}", file=sys.stderr)
        return 1
    mode = "dry dispatch" if summary.dry_run else "live dispatch"
    print(f"{mode} → {Path(args.out) / 'dispatch.json'} (cells={len(summary.dispatched)})")
    return 0


def cmd_task_optimization_status(args: argparse.Namespace) -> int:
    try:
        plan, _preflight, receipts = _task_optimization_inputs(args)
        champion = _load_json(args.champion, label="champion") if args.champion else None
        if champion is not None and (champion.get("study_id") != plan.get("study_id") or champion.get("plan_sha256") != receipt_sha256(plan)):
            raise ValueError("champion receipt does not match immutable plan")
        if champion is not None and (not champion.get("candidate_id") or not champion.get("treatment")):
            raise ValueError("champion receipt must name a candidate and treatment")
        status = task_optimization_status(plan, receipts, champion=champion)
        _write_json(Path(args.out) / "status.json", status)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not aggregate task-optimization status: {exc}", file=sys.stderr)
        return 1
    print(f"status → {Path(args.out) / 'status.json'} ({status['phase']}; resume={len(status['resume_cell_ids'])})")
    return 0


def cmd_task_optimization_select_champion(args: argparse.Namespace) -> int:
    try:
        plan, _preflight, receipts = _task_optimization_inputs(args)
        champion = select_task_optimization_champion(plan, receipts)
        _write_immutable_json(Path(args.out) / "champion.json", champion)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not select task-optimization champion: {exc}", file=sys.stderr)
        return 1
    print(f"champion → {Path(args.out) / 'champion.json'} ({champion['candidate_id']})")
    return 0


def cmd_task_optimization_analyze(args: argparse.Namespace) -> int:
    """Materialize a deterministic, treatment-aware learning analysis."""
    try:
        plan, _preflight, receipts = _task_optimization_inputs(args)
        analysis = analyze_task_optimization(plan, receipts)
        _write_immutable_json(Path(args.out) / "analysis.json", analysis)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not analyze task-optimization campaign: {exc}", file=sys.stderr)
        return 1
    eligible = sum(1 for arm in analysis["arms"] if arm["promotion_eligible"])
    print(f"analysis → {Path(args.out) / 'analysis.json'} (eligible arms={eligible})")
    return 0


def cmd_task_optimization_compare(args: argparse.Namespace) -> int:
    """Materialize pairwise solve-set/token comparisons without provider work."""
    try:
        plan, _preflight, receipts = _task_optimization_inputs(args)
        comparison = compare_task_optimization(plan, receipts)
        _write_immutable_json(Path(args.out) / "comparison.json", comparison)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not compare task-optimization campaign: {exc}", file=sys.stderr)
        return 1
    print(f"comparison → {Path(args.out) / 'comparison.json'} (pairs={len(comparison['comparisons'])})")
    return 0


def cmd_task_optimization_confirm(args: argparse.Namespace) -> int:
    """Write immutable confirmation evidence for the frozen arm; no dispatch occurs."""
    try:
        plan, _preflight, receipts = _task_optimization_inputs(args)
        champion = _load_json(args.champion, label="champion")
        if champion.get("study_id") != plan.get("study_id") or champion.get("plan_sha256") != receipt_sha256(plan):
            raise ValueError("champion receipt does not match immutable plan")
        if not champion.get("candidate_id") or not champion.get("treatment"):
            raise ValueError("champion receipt must name a candidate and treatment")
        status = task_optimization_status(plan, receipts, champion=champion)
        confirmation = [cell for cell in status["cells"] if cell["split"] == "confirmation" and cell["candidate_id"] == champion["candidate_id"] and cell["treatment"] == champion["treatment"]]
        if not confirmation:
            raise ValueError("plan contains no confirmation cells for frozen champion arm")
        receipt = {"schema": "task-optimization/confirmation/v1", "study_id": plan["study_id"],
                   "plan_sha256": receipt_sha256(plan), "champion_sha256": str(champion.get("champion_sha256") or receipt_sha256(champion)),
                   "candidate_id": champion["candidate_id"], "treatment": champion["treatment"],
                   "phase": status["phase"], "complete": status["confirmation_complete"],
                   "resume_cell_ids": [cell["id"] for cell in confirmation if cell["status"] in {"planned", "running", "retryable_infra"}],
                   "cells": confirmation}
        receipt["confirmation_sha256"] = receipt_sha256(receipt)
        _write_immutable_json(Path(args.out) / "confirmation.json", receipt)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"ERROR: could not materialize task-optimization confirmation: {exc}", file=sys.stderr)
        return 1
    print(f"confirmation → {Path(args.out) / 'confirmation.json'} ({'complete' if receipt['complete'] else 'resume'})")
    return 0


def validate_spec(spec_path: str, *, live: bool = False) -> tuple[list[str], list[str]]:
    errors: list[str] = []
    warnings: list[str] = []
    try:
        spec = JobSpec.load(spec_path)
    except Exception as exc:  # noqa: BLE001 - CLI preflight must surface parse errors plainly.
        return [f"could not load spec {spec_path}: {exc}"], warnings
    try:
        plugins.get(spec.job_type)
    except KeyError as exc:
        errors.append(str(exc))
    cells = spec.cells()
    if not cells:
        errors.append("spec enumerates zero cells")
    if spec.job_type == "paired-task":
        presets = spec.options.get("capability_presets")
        presets_json = json.dumps(presets, sort_keys=True) if isinstance(presets, dict) else ""
        for variant in spec.variants:
            treatment = str(variant.meta.get("treatment") or variant.id)
            ns = argparse.Namespace(
                treatment=treatment,
                backend=variant.backend,
                agent=str(variant.meta.get("agent") or ""),
                worker_profile=str(variant.meta.get("worker_profile") or ""),
                implementation_mode=str(variant.meta.get("implementation_mode") or ""),
                capability_preset=str(variant.meta.get("capability_preset") or ""),
                capability_presets_json=presets_json,
            )
            for error in validate_driver_errors(ns):
                errors.append(f"variant {variant.id}: {error}")
        gate = str(spec.options.get("live_gate_env") or "ARENA_PAIRED_TASK_ENABLE_CODEX")
        if live and os.environ.get(gate) != "1":
            errors.append(f"live paired-task dispatch is gated; set {gate}=1 to spend")
        elif not live:
            warnings.append(f"live paired-task dispatch remains disabled unless {gate}=1 and arena run --live are both set")
    return errors, warnings


def _check_docker() -> str:
    version_error = _run_docker_probe(["docker", "version"], "docker version", 10)
    if version_error:
        return _with_docker_context_hint(version_error)
    api_error = _run_docker_probe(["docker", "ps", "--format", "{{.ID}}"], "docker container API", 10)
    if api_error:
        return _with_docker_context_hint(api_error)
    return ""


def _run_docker_probe(cmd: list[str], label: str, timeout_s: int) -> str:
    try:
        proc = subprocess.run(cmd, text=True, capture_output=True, timeout=timeout_s)
    except FileNotFoundError:
        return "docker CLI not found on PATH"
    except subprocess.TimeoutExpired:
        return f"{label} timed out after {timeout_s}s"
    if proc.returncode == 0:
        return ""
    detail = _first_nonempty_line(proc.stderr) or _first_nonempty_line(proc.stdout) or f"exit {proc.returncode}"
    return f"docker unavailable: {label} failed: {detail}"


def _with_docker_context_hint(error: str) -> str:
    context_hint = _docker_context_hint()
    if context_hint:
        return f"{error}; {context_hint}"
    return error


def _docker_context_hint() -> str:
    try:
        proc = subprocess.run(["docker", "context", "ls"], text=True, capture_output=True, timeout=5)
    except Exception:  # noqa: BLE001 - best-effort hint only.
        return ""
    if proc.returncode != 0:
        return ""
    lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if len(lines) <= 1:
        return ""
    current = [line for line in lines[1:] if "*" in line.split()[:2]]
    available = [line.split()[0] for line in lines[1:] if "context not found" not in line.lower()]
    if current and "context not found" in current[0].lower() and available:
        return f"active Docker context appears stale; try DOCKER_CONTEXT={available[0]}"
    return ""


def _first_nonempty_line(text: str) -> str:
    for line in text.splitlines():
        line = line.strip()
        if line:
            return line[:240]
    return ""


def _format_table(rows: list[dict[str, object]], columns: list[tuple[str, str]]) -> str:
    if not rows:
        return "(none)"
    widths = []
    for key, title in columns:
        widths.append(max(len(title), *(len(str(row.get(key, ""))) for row in rows)))
    header = "  ".join(title.ljust(widths[idx]) for idx, (_key, title) in enumerate(columns))
    rule = "  ".join("-" * widths[idx] for idx in range(len(columns)))
    body = [
        "  ".join(str(row.get(key, "")).ljust(widths[idx]) for idx, (key, _title) in enumerate(columns))
        for row in rows
    ]
    return "\n".join([header, rule, *body])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="arena", description=__doc__)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_plan = sub.add_parser("plan", help="enumerate cells without executing")
    p_plan.add_argument("--spec", required=True)
    p_plan.set_defaults(func=cmd_plan)

    p_treatments = sub.add_parser("treatments", help="list reusable treatment drivers")
    p_treatments.add_argument("--aliases", action="store_true", help="include alias rows")
    p_treatments.add_argument("--json", action="store_true", help="emit machine-readable catalog JSON")
    p_treatments.set_defaults(func=cmd_treatments)

    p_validate = sub.add_parser("validate", help="validate a job spec without docker or LLM calls")
    p_validate.add_argument("--spec", required=True)
    p_validate.add_argument("--live", action="store_true", help="also enforce live-run gates")
    p_validate.set_defaults(func=cmd_validate)

    p_doctor = sub.add_parser("doctor", help="check local arena prerequisites")
    p_doctor.add_argument("--spec", default="", help="optionally validate a job spec too")
    p_doctor.add_argument("--live", action="store_true", help="also enforce live-run gates")
    p_doctor.set_defaults(func=cmd_doctor)

    p_run = sub.add_parser("run", help="run the sweep in containers")
    p_run.add_argument("--spec", required=True)
    p_run.add_argument("--out", required=True)
    p_run.add_argument("--live", action="store_true", help="spend on real LLM drives (default: no-LLM arming)")
    p_run.set_defaults(func=cmd_run)

    p_plugins = sub.add_parser("plugins", help="list registered job types")
    p_plugins.set_defaults(func=cmd_plugins)

    p_task_opt = sub.add_parser("task-optimization", help="plan, preflight, and resume a locked task-optimization study")
    task_opt_sub = p_task_opt.add_subparsers(dest="task_optimization_cmd", required=True)
    p_task_validate = task_opt_sub.add_parser("validate", help="validate study and frozen corpus inputs without LLM calls")
    p_task_validate.add_argument("--study", required=True)
    p_task_validate.set_defaults(func=cmd_task_optimization_validate)
    p_task_plan = task_opt_sub.add_parser("plan", help="write deterministic plan and study lock without LLM calls")
    p_task_plan.add_argument("--study", required=True)
    p_task_plan.add_argument("--out", required=True)
    p_task_plan.add_argument("--repeat-phase", default="screening")
    p_task_plan.set_defaults(func=cmd_task_optimization_plan)
    p_task_arm = task_opt_sub.add_parser("arm", help="record explicit live authorization without dispatching a provider")
    p_task_arm.add_argument("--study", required=True)
    p_task_arm.add_argument("--out", required=True)
    p_task_arm.add_argument("--repeat-phase", default="screening")
    p_task_arm.add_argument("--live", action="store_true")
    p_task_arm.set_defaults(func=cmd_task_optimization_arm)
    p_task_preflight = task_opt_sub.add_parser("preflight", help="resolve local profile catalogs into immutable dry-run launch-plan receipts without a provider call")
    p_task_preflight.add_argument("--study", required=True)
    p_task_preflight.add_argument("--out", required=True)
    p_task_preflight.add_argument("--config", default=str(REPO_ROOT / ".kitsoki.yaml"), help="effective Kitsoki config; its local overlay is loaded by the native resolver")
    p_task_preflight.add_argument("--launch-agent", default="task-optimization-preflight", help="freestanding agent used solely to materialize the native dry-run launch plan")
    p_task_preflight.add_argument("--working-dir", default=str(REPO_ROOT), help="required launch context; must resolve exactly in every plan")
    p_task_preflight.set_defaults(func=cmd_task_optimization_preflight)
    p_task_record = task_opt_sub.add_parser("record", help="append one immutable scored attempt receipt")
    p_task_record.add_argument("--plan", required=True)
    p_task_record.add_argument("--preflight", required=True)
    p_task_record.add_argument("--attempts", required=True, help="existing append-only attempt receipt directory")
    p_task_record.add_argument("--receipt", required=True, help="pre-scored attempt JSON")
    p_task_record.add_argument("--out", required=True, help="append-only attempt receipt directory")
    p_task_record.set_defaults(func=cmd_task_optimization_record)
    p_task_run = task_opt_sub.add_parser("run", help="schedule only ready/resumable frozen cells; dry by default")
    p_task_run.add_argument("--plan", required=True, help="immutable plan.json")
    p_task_run.add_argument("--preflight", required=True, help="immutable preflight.json")
    p_task_run.add_argument("--attempts", required=True, help="append-only attempt receipt directory")
    p_task_run.add_argument("--out", required=True, help="run artifacts, isolated workspaces, and dispatch receipt")
    p_task_run.add_argument("--max-cells", type=int, default=0, help="cap selected cells; 0 means all resumable cells")
    p_task_run.add_argument("--live", action="store_true", help="execute the explicit treatment adapter; requires frozen live gate")
    p_task_run.add_argument("--executor-command", nargs="+", default=[], help="adapter command; Arena appends --request <dispatch-request.json>")
    p_task_run.set_defaults(func=cmd_task_optimization_run)
    p_task_status = task_opt_sub.add_parser("status", help="aggregate immutable attempts and list resume cells")
    p_task_status.add_argument("--plan", required=True)
    p_task_status.add_argument("--preflight", required=True)
    p_task_status.add_argument("--attempts", required=True)
    p_task_status.add_argument("--champion", default="")
    p_task_status.add_argument("--out", required=True)
    p_task_status.set_defaults(func=cmd_task_optimization_status)
    p_task_champion = task_opt_sub.add_parser("select-champion", aliases=["freeze"], help="freeze deterministic champion after learning completion")
    p_task_champion.add_argument("--plan", required=True)
    p_task_champion.add_argument("--preflight", required=True)
    p_task_champion.add_argument("--attempts", required=True)
    p_task_champion.add_argument("--out", required=True)
    p_task_champion.set_defaults(func=cmd_task_optimization_select_champion)
    p_task_analyze = task_opt_sub.add_parser("analyze", help="write immutable candidate+treatment promotion analysis")
    p_task_analyze.add_argument("--plan", required=True)
    p_task_analyze.add_argument("--preflight", required=True)
    p_task_analyze.add_argument("--attempts", required=True)
    p_task_analyze.add_argument("--out", required=True)
    p_task_analyze.set_defaults(func=cmd_task_optimization_analyze)
    p_task_compare = task_opt_sub.add_parser("compare", help="write immutable pairwise candidate+treatment comparison")
    p_task_compare.add_argument("--plan", required=True)
    p_task_compare.add_argument("--preflight", required=True)
    p_task_compare.add_argument("--attempts", required=True)
    p_task_compare.add_argument("--out", required=True)
    p_task_compare.set_defaults(func=cmd_task_optimization_compare)
    p_task_confirm = task_opt_sub.add_parser("confirm", help="write immutable confirmation status for the frozen champion arm")
    p_task_confirm.add_argument("--plan", required=True)
    p_task_confirm.add_argument("--preflight", required=True)
    p_task_confirm.add_argument("--attempts", required=True)
    p_task_confirm.add_argument("--champion", required=True)
    p_task_confirm.add_argument("--out", required=True)
    p_task_confirm.set_defaults(func=cmd_task_optimization_confirm)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
