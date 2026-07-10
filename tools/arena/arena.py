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
from pathlib import Path

from arena.executor import CellExecutor, DockerBackend
from arena.model import Cell, JobSpec
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

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
