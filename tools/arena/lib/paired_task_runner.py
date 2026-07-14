#!/usr/bin/env python3
"""Run one frozen paired-task arena cell.

The runner is intentionally small: it materializes the pinned task baseline,
optionally dispatches a live worker, then scores the candidate tree with the
task's frozen deterministic oracle. `--arm-only` never calls a model; `--live`
only calls a live CLI when ARENA_PAIRED_TASK_ENABLE_CODEX=1 is set.
"""

from __future__ import annotations

import argparse
import traceback
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("paired_task_runner.py needs pyyaml")

def default_kitsoki_root(env: dict[str, str] | None = None) -> Path:
    """Resolve the checkout in both container and native invocations.

    Docker jobs set KITSOKI_ROOT to /workspace/kitsoki. Native calibration does
    not, so falling back to that container-only path made the runner unable to
    import its local pricing module before a cell even started.
    """
    configured = (env if env is not None else os.environ).get("KITSOKI_ROOT", "").strip()
    if configured:
        return Path(configured).resolve()
    return Path(__file__).resolve().parents[3]


KITSOKI_ROOT = default_kitsoki_root()
DEFAULT_CORPUS = KITSOKI_ROOT / "tools/arena/corpus/cost-bench.manifest.yaml"
BENCH = KITSOKI_ROOT / "tools/bugfix-bakeoff/external/bench.py"
DRIVE_SH = KITSOKI_ROOT / "tools/mcp-drive/drive.sh"
BENCH_BUGFIX_STORY = KITSOKI_ROOT / "stories/bench-bugfix/app.yaml"

sys.path.insert(0, str(KITSOKI_ROOT / "tools/arena"))
sys.path.insert(0, str(KITSOKI_ROOT / "tools/session-mining"))
from arena.treatments import (  # noqa: E402
    CODEACT_CAPABILITY_PRESET,
    DEFAULT_CAPABILITY_PRESETS,
    DEFAULT_CODEACT_AGENT,
    DriverResult,
    DriverServices,
    TREATMENT_DRIVERS,
    assert_codeact_launch_plan,
    capability_hash,
    capability_preset_json,
    canonical_json,
    known_treatments,
    merged_capability_presets,
    resolve_treatment_driver,
    treatment_catalog,
    validate_driver_args,
)
from pricing import price_for  # noqa: E402  (path set above; single price table for the repo)

# Paired-task's `--model` axis names a WORKER model the same way
# bugfix-bakeoff/external/candidates.yaml does; this is the one place that
# maps that model name to the kitsoki harness `profile` bench-bugfix needs, so
# the kitsoki arm and the single-briefed arm use the IDENTICAL worker model —
# only the process (kitsoki pipeline vs raw codex exec) differs.
MODEL_TO_PROFILE = {
    "glm-5.2": "synthetic-claude",
    "kimi-k2.7-code": "synthetic-kimi-k27",
    "gpt-5.4": "codex-gpt54",
    "gpt-5.5": "codex-native",
    # Spark is a native Codex candidate.  Keep the runner's default profile
    # resolution aligned with the local profile name used by the Stage-1
    # paired raw/strict-CodeAct comparison; callers may still override this
    # explicitly with --worker-profile.
    "gpt-5.3-codex-spark": "codex-spark",
}

MODEL_TO_RAW_CLAUDE_PROFILE = {
    "glm-5.2": "synthetic-claude",
    "kimi-k2.7-code": "synthetic-kimi-k27",
}

RAW_ALLOWED_TOOLS = "Bash,Edit,Write,Read,Glob,Grep,MultiEdit"
SUBSCRIPTION_RAW_CLAUDE_MODELS = {
    "glm-5.2",
    "hf:zai-org/glm-5.2",
}

DEFAULT_LIVE_GATE_ENV = "ARENA_PAIRED_TASK_ENABLE_CODEX"


def zero_metrics(**extra: Any) -> dict[str, Any]:
    metrics: dict[str, Any] = {
        "cost_usd": 0.0,
        "tokens": 0,
        "input_tokens": 0,
        "cached_input_tokens": 0,
        "output_tokens": 0,
        "reasoning_output_tokens": 0,
        "cost_basis": "unknown",
        "mcp_calls": 0,
        "codeact_eval_calls": 0,
        "codeact_domain_errors": 0,
        "codeact_done_rejections": 0,
    }
    metrics.update(extra)
    return metrics


def write_task_file(args: argparse.Namespace, task: dict[str, Any], trace_ref: str) -> Path:
    task_file = Path(trace_ref).with_suffix(".task.md")
    task_file.write_text(build_prompt(args, task), encoding="utf-8")
    return task_file


def treatment_services() -> DriverServices:
    return DriverServices(
        kitsoki_root=KITSOKI_ROOT,
        dispatch_single_prompt=dispatch_single_prompt,
        dispatch_kitsoki=dispatch_kitsoki,
        zero_metrics=zero_metrics,
        container_path=container_path,
        write_task_file=write_task_file,
        ensure_kitsoki_launcher=ensure_kitsoki_launcher,
        first_line=first_line,
        redact_cmd=redact_cmd,
        codex_output_metrics=codex_output_metrics,
        codeact_text_metrics=codeact_text_metrics,
    )


def blended_cost_usd(model: str, tokens: int) -> tuple[float, bool]:
    """Rough USD cost from a TOTAL token count only.

    codex exec's summary reports one combined "tokens used" figure with no
    input/output split, so this can't be the exact per-bucket price_for()/
    message_cost() computation the mining tools use on real usage blocks.
    Blend input+output at 1:1 as a documented approximation; is_exact always
    false here regardless of the model's own is_estimate flag.
    """
    price, _ = price_for(model)
    blended_rate = (price.input + price.output) / 2
    return round(tokens * blended_rate / 1e6, 6), False


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--task", required=True)
    parser.add_argument("--treatment", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument(
        "--corpus",
        default=os.environ.get("ARENA_PAIRED_TASK_CORPUS", str(DEFAULT_CORPUS)),
        help="arena task corpus/source YAML; defaults to the built-in cost bench manifest",
    )
    parser.add_argument(
        "--story",
        default="",
        help="kitsoki story app.yaml the dispatch_kitsoki treatment drives; defaults to BENCH_BUGFIX_STORY (stories/bench-bugfix/app.yaml)",
    )
    parser.add_argument("--backend", default="")
    parser.add_argument("--model", default="")
    parser.add_argument("--effort", default="")
    parser.add_argument("--agent", default="")
    parser.add_argument("--worker-profile", default="")
    parser.add_argument("--orchestrator-model", default="")
    parser.add_argument("--implementation-mode", default="")
    parser.add_argument("--capability-preset", default="")
    parser.add_argument("--capability-presets-json", default="")
    parser.add_argument("--live-gate-env", default=DEFAULT_LIVE_GATE_ENV)
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--arm-only", action="store_true")
    parser.add_argument("--work-root", default=os.environ.get("ARENA_PAIRED_TASK_WORK_ROOT", "/workspace/kitsoki/.artifacts/arena/paired-task-work"))
    parser.add_argument(
        "--keep-workdir",
        action="store_true",
        default=os.environ.get("ARENA_PAIRED_TASK_KEEP_WORKDIR") == "1",
        help="keep the per-cell checkout under --work-root for debugging; default removes it after scoring",
    )
    args = parser.parse_args(argv)

    if args.live == args.arm_only:
        return emit(
            verdict="blocked",
            notes="exactly one of --live or --arm-only is required",
            exit_code=2,
        )

    corpus_path = Path(args.corpus)
    task = load_task(args.task, corpus_path)
    if args.arm_only:
        return arm_task(task, args.target)
    return run_live(args, task)


def load_task(task_id: str, corpus_path: Path) -> dict[str, Any]:
    corpus = yaml.safe_load(corpus_path.read_text(encoding="utf-8"))
    if not isinstance(corpus, dict):
        sys.exit(f"corpus must be a mapping: {corpus_path}")
    for task in corpus.get("tasks", []):
        if task.get("id") == task_id:
            out = dict(task)
            out["_corpus_path"] = str(corpus_path)
            return out
    sys.exit(f"unknown corpus task {task_id!r} in {corpus_path}")


def arm_task(task: dict[str, Any], target: str) -> int:
    oracle = task.get("oracle") or {}
    kind = oracle.get("kind")
    started = time.monotonic()
    if kind == "github_content":
        baseline = fetch_raw(oracle["repo"], task["baseline_sha"], oracle["file"])
        fixed = fetch_raw(oracle["repo"], task["fix_sha"], oracle["file"])
        red = baseline is None or oracle["required_text"] not in baseline
        green = fixed is not None and oracle["required_text"] in fixed
        return emit(
            verdict="armed" if red and green else "failed",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref="",
            notes=f"arm github_content red={red} green={green}",
            target=target,
        )
    if kind == "external_bakeoff":
        cmd = [
            "python3",
            str(BENCH),
            "verify",
            "--project",
            str(oracle["project"]),
            "--bug",
            str(oracle["bug"]),
        ]
        if str(oracle.get("project")) == "kitsoki":
            cmd.extend(["--repo-dir", str(KITSOKI_ROOT)])
        proc = subprocess.run(cmd, cwd=KITSOKI_ROOT, text=True, capture_output=True)
        return emit(
            verdict="armed" if proc.returncode == 0 else "failed",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref="",
            notes=first_line(proc.stdout + "\n" + proc.stderr),
            target=target,
            exit_code=0 if proc.returncode == 0 else 1,
        )
    if kind == "bugswarm_fail_pass_pair":
        red = task.get("verified_red") is True
        green = task.get("verified_green") is True
        image_tag = str(oracle.get("image_tag") or task.get("image_tag") or "")
        return emit(
            verdict="armed" if red and green else "failed",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref="",
            notes=f"arm bugswarm_fail_pass_pair red={red} green={green} image_tag={image_tag}",
            target=target,
            exit_code=0 if red and green else 1,
        )
    return emit(verdict="blocked", notes=f"unknown oracle kind {kind!r}", target=target, exit_code=2)


def run_live(args: argparse.Namespace, task: dict[str, Any]) -> int:
    started = time.monotonic()
    work_root = Path(args.work_root)
    work_root.mkdir(parents=True, exist_ok=True)
    cell_id = safe_name(f"{task['id']}--{args.treatment}--{args.backend or 'backend'}--{args.model or 'model'}")
    trace_ref = str(work_root / f"{cell_id}.jsonl")
    tree = work_root / cell_id
    if tree.exists():
        shutil.rmtree(tree)
    reset_cell_trace(Path(trace_ref))

    try:
        materialize_baseline(task, tree)
        baseline_ref = assert_materialized_baseline(task, tree)
        prewarm = prepare_live_tree(task, tree)
    except (Exception, SystemExit) as exc:  # noqa: BLE001 - report unsupported sources as cell results.
        return emit(
            verdict="blocked",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref=container_path(trace_ref),
            notes=f"baseline materialization failed: {exc}",
            target=args.target,
            exit_code=0,
        )
    if not prewarm["ok"]:
        if not args.keep_workdir:
            cleanup_cell_workdir(tree)
        return emit(
            verdict="blocked",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref=container_path(trace_ref),
            notes=f"deterministic prewarm failed: {prewarm['note']}",
            metrics={"prewarm_wall_s": prewarm["wall_s"]},
            target=args.target,
            exit_code=0,
        )
    dispatch = dispatch_worker(args, task, tree, trace_ref)
    # A nonterminal live MCP trace is not a candidate result. In particular,
    # tearing down the orchestrator's temporary auth home while a supervised
    # worker is still running kills that worker and can leave a partly-mutated
    # tree that a hidden oracle would mislabel as an ordinary model failure.
    # Fail closed and preserve the workdir/trace for diagnosis instead of
    # spending a deterministic score on an invalid cell.
    if dispatch.blocked:
        score = {
            "verdict": "blocked",
            "evidence": "",
            "notes": "external oracle skipped because live dispatch did not reach a terminal trace",
        }
    else:
        score = score_tree(task, tree)
    metrics = dict(dispatch.metrics or {})
    if baseline_ref:
        metrics["baseline_sha"] = baseline_ref
    metrics["prewarm_wall_s"] = prewarm["wall_s"]
    metrics.update(diff_stats(tree, baseline_ref))
    cost_usd = metrics.get("cost_usd")
    if not isinstance(cost_usd, (int, float)):
        cost_usd = None
    tokens = metrics.get("tokens")
    if not isinstance(tokens, int):
        tokens = 0
    verdict = score["verdict"]
    if dispatch.blocked:
        verdict = "blocked"
    notes = "; ".join(part for part in [prewarm["note"], dispatch.notes, score.get("notes", "")] if part)
    evidence_refs = [task_ref(task), score.get("evidence", "")]
    if dispatch.launch_plan_ref:
        evidence_refs.append(dispatch.launch_plan_ref)
    if dispatch.transcript_ref and dispatch.transcript_ref != container_path(trace_ref):
        evidence_refs.append(dispatch.transcript_ref)
    preserve_incomplete = dispatch.blocked or metrics.get("measurement_status") == "incomplete"
    if not args.keep_workdir and not preserve_incomplete:
        cleanup_cell_workdir(tree)
        notes = "; ".join(part for part in [notes, f"removed scratch workdir {container_path(str(tree))}"] if part)
    elif preserve_incomplete:
        notes = "; ".join(part for part in [notes, f"preserved incomplete scratch workdir {container_path(str(tree))}"] if part)
    return emit(
        verdict=verdict,
        cost_usd=cost_usd,
        tokens=tokens,
        wall_s=elapsed(started),
        evidence_refs=evidence_refs,
        trace_ref=dispatch.trace_ref or container_path(trace_ref),
        notes=notes,
        metrics=metrics,
        target=args.target,
        exit_code=0,
    )


def reset_cell_trace(trace: Path) -> None:
    """Remove stale per-cell telemetry before a retained-workdir rerun.

    A kept work root is useful for inspecting a failed candidate tree, but its
    prior Studio trace is a session journal, not an append-only benchmark log.
    Reusing it made a fresh `session.new` inherit a terminal state and reject
    `full_pipeline` immediately. Cell JSONs are distinct durable records; only
    the transient trace and driver sidecars are reset here.
    """
    for path in (
        trace,
        trace.with_suffix(".prompt.md"),
        trace.with_suffix(".thread.md"),
        trace.with_suffix(".drive-log.json"),
    ):
        path.unlink(missing_ok=True)


def cleanup_cell_workdir(tree: Path) -> None:
    if not tree.exists():
        return
    # Cells routinely install a large node_modules tree. Recursively chmodding
    # it before removal turns cleanup into an unbounded post-model delay. The
    # cell container owns the workdir; let rmtree remove it directly and only
    # repair an individual permission failure via onerror.
    shutil.rmtree(tree, onerror=remove_readonly)


def remove_readonly(func: Any, path: str, _exc_info: Any) -> None:
    os.chmod(path, 0o700)
    func(path)


def materialize_baseline(task: dict[str, Any], tree: Path) -> None:
    oracle = task.get("oracle") or {}
    if oracle.get("kind") == "bugswarm_fail_pass_pair":
        materialize_bugswarm_baseline(task, tree)
        return
    if oracle.get("kind") == "github_content":
        repo = str(oracle["repo"])
        run(["git", "init", "-q", str(tree)], cwd=KITSOKI_ROOT)
        run(["git", "remote", "add", "origin", f"https://github.com/{repo}.git"], cwd=tree)
        run(["git", "fetch", "-q", "--depth", "1", "origin", str(task["baseline_sha"])], cwd=tree)
        run(["git", "checkout", "-q", "FETCH_HEAD"], cwd=tree)
        return
    if oracle.get("kind") == "external_bakeoff":
        project = str(oracle["project"])
        bug = str(oracle["bug"])
        meta = json.loads(
            run(
                ["python3", str(BENCH), "meta", "--project", project, "--bug", bug],
                cwd=KITSOKI_ROOT,
                capture=True,
            ).stdout
        )
        # Fetch only the frozen baseline commit. A regular clone retains later
        # commits, including the benchmark's known fix SHA, which lets a worker
        # read the answer from repository history rather than solve the task.
        repo = str(meta.get("repo") or ".")
        remote = str(KITSOKI_ROOT) if repo == "." else repo
        baseline = str(meta["baseline_sha"])
        if repo == ".":
            # Local corpus metadata may use a convenient short SHA. Resolve it
            # in the harness source repository before the isolated fetch; the
            # resulting cell still receives only this one full baseline object.
            baseline = run(
                ["git", "rev-parse", f"{baseline}^{{commit}}"],
                cwd=KITSOKI_ROOT,
                capture=True,
            ).stdout.strip()
        run(["git", "init", "-q", str(tree)], cwd=KITSOKI_ROOT)
        run(["git", "remote", "add", "origin", remote], cwd=tree)
        run(["git", "fetch", "-q", "--depth", "1", "origin", baseline], cwd=tree)
        run(["git", "checkout", "-q", "FETCH_HEAD"], cwd=tree)
        return
    raise SystemExit(f"cannot materialize oracle kind {oracle.get('kind')!r}")


def prepare_live_tree(task: dict[str, Any], tree: Path) -> dict[str, Any]:
    """Run the corpus-declared setup before either live treatment starts.

    Preparation is deterministic harness work, not an agent choice.  It makes
    both treatments start from the same ready baseline and fails closed before
    a provider call when a project cannot be prepared.
    """
    # The bugfix story commits a verified repair before it can move to testing.
    # Arena cells are disposable clones and must not inherit a developer's
    # identity; set an explicit local identity here so a missing container-level
    # gitconfig cannot spend a worker call and then bounce the pipeline to idle.
    for key, value in (("user.name", "Kitsoki Arena"), ("user.email", "arena@kitsoki.local")):
        identity = subprocess.run(
            ["git", "config", key, value], cwd=tree, text=True, capture_output=True,
        )
        if identity.returncode != 0:
            return {"ok": False, "note": f"git config {key}: {first_line(identity.stderr)}", "wall_s": 0.0}
    oracle = task.get("oracle") or {}
    if oracle.get("kind") != "external_bakeoff":
        return {"ok": True, "note": "prewarm: git identity configured; not required", "wall_s": 0.0}
    project = str(oracle["project"])
    bug = str(oracle["bug"])
    meta = json.loads(run(
        ["python3", str(BENCH), "meta", "--project", project, "--bug", bug],
        cwd=KITSOKI_ROOT,
        capture=True,
    ).stdout)
    install = str(meta.get("install") or "").strip()
    if not install:
        return {"ok": True, "note": "prewarm: git identity configured; no install command", "wall_s": 0.0}
    timeout_s = int(os.environ.get("ARENA_PAIRED_TASK_PREWARM_TIMEOUT_S", "300"))
    started = time.monotonic()
    try:
        proc = subprocess.run(
            ["sh", "-lc", install], cwd=tree, text=True, capture_output=True, timeout=timeout_s,
        )
    except subprocess.TimeoutExpired:
        return {"ok": False, "note": f"{install!r} timed out after {timeout_s}s", "wall_s": elapsed(started)}
    if proc.returncode != 0:
        return {"ok": False, "note": first_line(proc.stdout + "\n" + proc.stderr), "wall_s": elapsed(started)}
    return {"ok": True, "note": f"prewarm: git identity configured; {install}", "wall_s": elapsed(started)}


def materialize_bugswarm_baseline(task: dict[str, Any], tree: Path) -> None:
    checkout = ((task.get("meta") or {}).get("bugswarm_checkout_path") or "").strip()
    if checkout:
        src = Path(checkout)
        if not src.exists():
            raise SystemExit(f"BugSwarm checkout path does not exist: {checkout}")
        shutil.copytree(src, tree, dirs_exist_ok=False)
        return
    image = bugswarm_image(task)
    source_dir = bugswarm_source_dir(task)
    tree.parent.mkdir(parents=True, exist_ok=True)
    container = run(["docker", "create", image, "bash", "-lc", "true"], cwd=KITSOKI_ROOT, capture=True).stdout.strip()
    if not container:
        raise SystemExit(f"docker create returned no container id for {image}")
    try:
        run(["docker", "cp", f"{container}:{source_dir}/.", container_path(tree)], cwd=KITSOKI_ROOT)
    finally:
        subprocess.run(["docker", "rm", "-f", container], cwd=KITSOKI_ROOT, text=True, capture_output=True)


def required_baseline_sha(task: dict[str, Any]) -> str:
    """Return the immutable baseline revision a live cell is allowed to edit.

    BugSwarm's verifier observes this value inside the failed image.  Treating
    it as optional at dispatch time let a reused checkout silently turn a
    comparison into a different-baseline run.
    """
    meta = task.get("meta") or {}
    return str(meta.get("failed_commit_sha") or task.get("baseline_sha") or "").strip().lower()


def assert_materialized_baseline(task: dict[str, Any], tree: Path) -> str:
    expected = required_baseline_sha(task)
    actual = current_head(tree).lower()
    if not expected:
        if (task.get("oracle") or {}).get("kind") == "bugswarm_fail_pass_pair":
            raise SystemExit(f"BugSwarm task {task.get('id')!r} has no verified failed_commit_sha")
        return actual
    if not actual:
        raise SystemExit(f"materialized task {task.get('id')!r} has no readable git HEAD; expected {expected}")
    if actual != expected:
        raise SystemExit(f"materialized baseline mismatch for {task.get('id')!r}: HEAD={actual}, expected failed_commit_sha={expected}")
    return actual


def bugswarm_image(task: dict[str, Any]) -> str:
    oracle = task.get("oracle") or {}
    image_tag = str(oracle.get("image_tag") or task.get("image_tag") or "")
    if not image_tag:
        raise SystemExit(f"BugSwarm task {task.get('id')!r} missing image_tag")
    meta = task.get("meta") or {}
    repo = str(meta.get("bugswarm_image_repo") or os.environ.get("ARENA_BUGSWARM_IMAGE_REPO") or "bugswarm/cached-images")
    return f"{repo}:{image_tag}"


def bugswarm_source_dir(task: dict[str, Any]) -> str:
    meta = task.get("meta") or {}
    explicit = str(meta.get("bugswarm_source_dir") or "").strip()
    if explicit:
        return explicit.rstrip("/")
    repo = str(task.get("repo_label") or task.get("repo") or "").strip("/")
    if "/" not in repo:
        raise SystemExit(f"BugSwarm task {task.get('id')!r} needs repo_label owner/name or meta.bugswarm_source_dir")
    return f"/home/travis/build/failed/{repo}"


def dispatch_worker(args: argparse.Namespace, task: dict[str, Any], tree: Path, trace_ref: str) -> DriverResult:
    if args.backend == "synthetic":
        return DriverResult(notes="synthetic backend: no live model call; baseline scored directly", trace_ref=container_path(trace_ref), metrics=zero_metrics(action_surface="synthetic"))
    if args.backend not in {"codex", "claude"}:
        return DriverResult(blocked=True, infra_class="infra:harness", notes=f"unsupported live backend {args.backend!r}", trace_ref=container_path(trace_ref), metrics=zero_metrics())
    driver = resolve_treatment_driver(args.treatment)
    if driver is None:
        return DriverResult(
            blocked=True,
            infra_class="infra:harness",
            notes=f"unknown paired-task treatment {args.treatment!r}; known: {', '.join(known_treatments())}",
            trace_ref=container_path(trace_ref),
            metrics=zero_metrics(),
        )
    validation_error = validate_driver_args(args)
    if validation_error:
        return DriverResult(
            blocked=True,
            infra_class="infra:harness",
            notes=validation_error,
            trace_ref=container_path(trace_ref),
            metrics=zero_metrics(),
        )
    gate_env = (args.live_gate_env or DEFAULT_LIVE_GATE_ENV).strip() or DEFAULT_LIVE_GATE_ENV
    if os.environ.get(gate_env) != "1":
        return DriverResult(
            blocked=True,
            infra_class="infra:harness",
            notes=f"live dispatch disabled; set {gate_env}=1 to spend",
            trace_ref=container_path(trace_ref),
            metrics=zero_metrics(live_gate_env=gate_env),
        )
    return driver.run(args, task, tree, trace_ref, treatment_services())


def dispatch_single_prompt(args: argparse.Namespace, task: dict[str, Any], tree: Path, trace_ref: str) -> dict[str, Any]:
    if args.backend == "claude":
        return dispatch_single_prompt_claude(args, task, tree, trace_ref)
    return dispatch_single_prompt_codex(args, task, tree, trace_ref)


def dispatch_single_prompt_codex(args: argparse.Namespace, task: dict[str, Any], tree: Path, trace_ref: str) -> dict[str, Any]:
    """The naive baseline: one raw `codex exec` call, no kitsoki pipeline at all."""
    prompt = build_prompt(args, task)
    cmd = [
        "codex",
        "exec",
        "-C",
        # The runner invokes Codex with cwd=tree. `tree` can be relative in a
        # native run, so -C must be absolute rather than resolving it a second
        # time beneath the already-selected cwd.
        str(tree.resolve()),
        "--skip-git-repo-check",
        "--dangerously-bypass-approvals-and-sandbox",
        "-s",
        "danger-full-access",
        "--ignore-user-config",
        "--ephemeral",
        "--json",
    ]
    if args.model:
        cmd.extend(["--model", args.model])
    if args.effort:
        # Codex otherwise inherits the operator's global effort (currently
        # potentially `max`, which GPT-5.4 rejects). The experiment's declared
        # effort is part of the matched cell contract and must reach raw Codex.
        cmd.extend(["--config", f'model_reasoning_effort="{args.effort}"'])
    cmd.append(prompt)
    env, cleanup_auth_home = isolated_codex_env()
    try:
        proc = subprocess.run(
            cmd,
            cwd=tree,
            text=True,
            capture_output=True,
            timeout=int(os.environ.get("ARENA_CODEX_TIMEOUT_S", "900")),
            env=env,
        )
    finally:
        cleanup_auth_home()
    Path(trace_ref).write_text(json.dumps({
        "cmd": redact_cmd(cmd),
        "environment_isolation": codex_isolation_metadata(),
        "returncode": proc.returncode,
        "stdout_tail": proc.stdout[-4000:],
        "stderr_tail": proc.stderr[-4000:],
    }, indent=2), encoding="utf-8")
    metrics = codex_output_metrics(proc.stdout + "\n" + proc.stderr, args.model or "gpt-5.5")
    return {
        "blocked": proc.returncode != 0,
        "notes": f"codex exit={proc.returncode}: {first_line(proc.stdout + chr(10) + proc.stderr)}"
        + (f"; {metrics['cost_note']}" if metrics.get("cost_note") else ""),
        "metrics": metrics,
    }


def dispatch_single_prompt_claude(args: argparse.Namespace, task: dict[str, Any], tree: Path, trace_ref: str) -> dict[str, Any]:
    """Raw Claude-compatible one-shot baseline, used for synthetic.new GLM cells."""
    invocation = raw_claude_invocation_for_model(args.model)
    if "blocked" in invocation:
        return {
            "blocked": True,
            "notes": str(invocation["blocked"]),
            "metrics": {"cost_usd": 0.0, "tokens": 0},
        }
    prompt = build_prompt(args, task)
    cmd = [
        "claude",
        "-p",
        prompt,
        "--model",
        str(invocation["model"]),
        "--permission-mode",
        "acceptEdits",
        "--allowedTools",
        RAW_ALLOWED_TOOLS,
        "--output-format",
        "json",
    ]
    max_budget = _resolve_raw_claude_max_budget_usd(args.model, str(invocation["model"]))
    if max_budget:
        cmd.extend(["--max-budget-usd", max_budget])
    timeout = int(os.environ.get("ARENA_CLAUDE_TIMEOUT_S", "900"))
    try:
        proc = subprocess.run(
            cmd,
            cwd=tree,
            text=True,
            capture_output=True,
            timeout=timeout,
            env=dict(os.environ, **invocation["env"]),
        )
        timed_out = False
    except subprocess.TimeoutExpired as exc:
        timed_out = True
        proc = subprocess.CompletedProcess(cmd, 124, stdout=(exc.stdout or ""), stderr=(exc.stderr or ""))
    Path(trace_ref).write_text(json.dumps({
        "cmd": redact_cmd(cmd),
        "profile": invocation["profile"],
        "model": invocation["model"],
        "env_keys": sorted(invocation["env"].keys()),
        "max_budget_usd": max_budget,
        "timeout_s": timeout,
        "returncode": proc.returncode,
        "stdout_tail": proc.stdout[-4000:],
        "stderr_tail": proc.stderr[-4000:],
    }, indent=2), encoding="utf-8")
    metrics = claude_output_metrics(proc.stdout + "\n" + proc.stderr, str(invocation["model"]))
    note = f"claude exit={proc.returncode}: {first_line(proc.stdout + chr(10) + proc.stderr)}"
    if timed_out:
        note = f"claude timed out at {timeout}s; "
        raw_line = first_line(proc.stdout + chr(10) + proc.stderr)
        if raw_line:
            note += raw_line
    if metrics.get("cost_note"):
        note += f"; {metrics['cost_note']}"
    return {
        "blocked": proc.returncode != 0 or timed_out,
        "notes": note,
        "metrics": {"cost_usd": metrics["cost_usd"], "tokens": metrics["tokens"]},
    }


def _resolve_raw_claude_max_budget_usd(task_model: str, invocation_model: str) -> str:
    if _is_subscription_raw_claude_model(task_model) or _is_subscription_raw_claude_model(invocation_model):
        return ""
    configured = os.environ.get("ARENA_CLAUDE_MAX_BUDGET_USD", "").strip()
    if configured:
        return configured
    return ""


def _is_subscription_raw_claude_model(model: str) -> bool:
    return model.strip().lower() in SUBSCRIPTION_RAW_CLAUDE_MODELS


def raw_claude_invocation_for_model(model: str) -> dict[str, Any]:
    profile_name = MODEL_TO_RAW_CLAUDE_PROFILE.get(model)
    if not profile_name:
        return {"blocked": f"no raw claude profile mapped for model {model!r}"}
    profile = load_harness_profile(profile_name)
    if not profile:
        return {"blocked": f"harness profile {profile_name!r} is not configured"}
    if str(profile.get("backend") or "") != "claude":
        return {"blocked": f"harness profile {profile_name!r} must use backend 'claude'"}
    env, missing = expand_profile_env(profile.get("env") or {})
    if missing:
        return {"blocked": f"harness profile {profile_name!r} references unset env var(s): {', '.join(missing)}"}
    return {
        "profile": profile_name,
        "model": str(profile.get("model") or model),
        "env": env,
    }


def load_harness_profile(profile_name: str) -> dict[str, Any]:
    profile: dict[str, Any] = {}
    for path in (KITSOKI_ROOT / ".kitsoki.yaml", KITSOKI_ROOT / ".kitsoki.local.yaml"):
        if not path.exists():
            continue
        data = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        if not isinstance(data, dict):
            continue
        profiles = data.get("harness_profiles") or {}
        if isinstance(profiles, dict) and isinstance(profiles.get(profile_name), dict):
            profile.update(profiles[profile_name])
    if not profile and profile_name == "synthetic-claude":
        return {
            "backend": "claude",
            "model": "hf:zai-org/GLM-5.2",
            "env": {
                "ANTHROPIC_BASE_URL": "https://api.synthetic.new/anthropic",
                "ANTHROPIC_AUTH_TOKEN": "${SYNTHETIC_API_KEY}",
            },
        }
    return profile


def expand_profile_env(env_map: dict[str, Any]) -> tuple[dict[str, str], list[str]]:
    env: dict[str, str] = {}
    missing: list[str] = []
    for key, value in env_map.items():
        text = str(value)
        expanded = os.path.expandvars(text)
        if "${" in expanded:
            missing.append(str(key))
            continue
        env[str(key)] = expanded
    return env, missing


def _orchestrator_backend_for(model: str) -> str:
    """Mirror drive.sh's own auto-detect: gpt-*/codex*/o3*/o4* -> codex, else
    claude. Kept in sync manually (drive.sh has no --print-backend to query)
    so dispatch_kitsoki's prompt tool-name spelling (see build_kitsoki_prompt)
    matches whichever backend MCP_DRIVE_BACKEND will actually select."""
    lowered = model.lower()
    if lowered.startswith(("gpt-", "codex", "o3", "o4")):
        return "codex"
    return "claude"


def codex_auth_source() -> Path:
    """Return the one credential file permitted in a paired live cell."""
    configured = os.environ.get("CODEX_HOME", "").strip()
    codex_home = Path(configured) if configured else Path.home() / ".codex"
    auth = codex_home / "auth.json"
    if not auth.is_file():
        raise SystemExit(
            "Codex paired-task isolation needs auth.json in CODEX_HOME (or ~/.codex); "
            "run `codex login` before enabling a live cell"
        )
    return auth


def isolated_codex_env(*, source_auth: Path | None = None, base_env: dict[str, str] | None = None) -> tuple[dict[str, str], Callable[[], None]]:
    """Create an auth-only disposable HOME/CODEX_HOME for a live cell.

    The operator's normal CODEX_HOME includes history, local configuration, and
    memories that can contaminate a paired evaluation. Codex subscription auth
    needs only auth.json, so the temporary home is created outside the task
    workspace and removed as soon as the outer process exits.
    """
    auth = source_auth or codex_auth_source()
    if not auth.is_file():
        raise SystemExit(f"Codex paired-task isolation auth file is missing: {auth}")
    root = Path(tempfile.mkdtemp(prefix="kitsoki-paired-codex-home-"))
    codex_home = root / ".codex"
    codex_home.mkdir(mode=0o700)
    target_auth = codex_home / "auth.json"
    shutil.copy2(auth, target_auth)
    target_auth.chmod(0o600)
    env = dict(base_env if base_env is not None else os.environ)
    env["HOME"] = str(root)
    env["CODEX_HOME"] = str(codex_home)

    def cleanup() -> None:
        shutil.rmtree(root, ignore_errors=True)

    return env, cleanup


def codex_isolation_metadata() -> dict[str, Any]:
    """Non-secret provenance retained in each cell record."""
    return {
        "mode": "auth-only-temporary-home",
        "credential_files": ["auth.json"],
        "operator_codex_home_forwarded": False,
        "temporary_home_removed_after_cell": True,
    }


def dispatch_kitsoki(args: argparse.Namespace, task: dict[str, Any], tree: Path, trace_ref: str) -> dict[str, Any]:
    """Drive the REAL kitsoki bugfix pipeline (stories/bench-bugfix/app.yaml) via
    a headless codex orchestrator + the studio MCP — the same live-delegation
    primitive tools/bugfix-bakeoff/external/drive_cell.sh already proved out for
    the internal bake-off. `trace_ref` becomes the kitsoki session's own trace
    (session_new {trace: ...}), so cost/tokens below are read off REAL recorded
    agent-call usage, not a codex stdout summary line."""
    model = args.model or "gpt-5.5"
    profile = args.worker_profile or MODEL_TO_PROFILE.get(model)
    if not profile:
        return {
            "blocked": True,
            "notes": f"no kitsoki harness profile mapped for model {model!r}; add one to MODEL_TO_PROFILE",
            "metrics": zero_metrics(action_surface="kitsoki-studio-mcp"),
        }
    try:
        bin_dir = ensure_kitsoki_launcher()
        branch = f"paired-task-{safe_name(task['id'])}"
        run(["git", "checkout", "-q", "-B", branch], cwd=tree)
        test_cmd = test_cmd_for(task)
    except (Exception, SystemExit) as exc:  # noqa: BLE001 - report, don't crash the sweep
        return {
            "blocked": True,
            "notes": f"kitsoki dispatch setup failed: {exc}",
            "metrics": zero_metrics(action_surface="kitsoki-studio-mcp"),
        }

    # A paired comparison must not accidentally use a newer orchestrator than
    # its declared worker/model axis.  Special studies can name an explicit
    # orchestrator model, but the safe default is the cell's own model.
    orchestrator_model = args.orchestrator_model or os.environ.get("ARENA_PAIRED_TASK_ORCHESTRATOR_MODEL") or model
    orchestrator_backend = os.environ.get("ARENA_PAIRED_TASK_ORCHESTRATOR_BACKEND") or _orchestrator_backend_for(orchestrator_model)

    thread = Path(trace_ref).with_suffix(".thread.md")
    prompt = build_kitsoki_prompt(args, task, tree, trace_ref, thread, profile, branch, test_cmd, orchestrator_backend)
    prompt_file = Path(trace_ref).with_suffix(".prompt.md")
    prompt_file.write_text(prompt, encoding="utf-8")

    try:
        env, cleanup_auth_home = isolated_codex_env()
    except SystemExit as exc:
        return {
            "blocked": True,
            "notes": str(exc),
            "metrics": zero_metrics(action_surface="kitsoki-studio-mcp"),
        }
    env["PATH"] = f"{bin_dir}:{env.get('PATH', '')}"
    env["MCP_DRIVE_BACKEND"] = orchestrator_backend
    env["MCP_DRIVE_MODEL"] = orchestrator_model
    # codex does not forward the parent env to the MCP servers it spawns; the
    # kitsoki MCP process forks the WORKER (codex-native) and needs the same
    # ChatGPT-subscription auth the orchestrator itself is using, AND the same
    # PATH (verified live: without PATH forwarded, the worker-side codex
    # backend's `exec.LookPath("codex")` fails inside the spawned kitsoki MCP
    # subprocess even though codex is on PATH for THIS process — internal/host's
    # ResolveBin then reported "codex binary not found", proving the pipeline's
    # own harness-profile routing was correct; only env propagation was missing).
    env["MCP_DRIVE_FORWARD_ENV"] = "CODEX_HOME,HOME,PATH,SYNTHETIC_API_KEY,ANTHROPIC_AUTH_TOKEN,ANTHROPIC_BASE_URL"

    cmd = [str(DRIVE_SH), "--prompt-file", str(prompt_file)]
    try:
        try:
            proc = subprocess.run(
                cmd,
                cwd=KITSOKI_ROOT,
                text=True,
                capture_output=True,
                env=env,
                timeout=int(os.environ.get("ARENA_CODEX_TIMEOUT_S", "1800")),
            )
        except subprocess.TimeoutExpired as exc:
            return {
                "blocked": True,
                "notes": f"drive.sh timed out after {exc.timeout}s",
                "metrics": zero_metrics(action_surface="kitsoki-studio-mcp", studio_mcp_trace_ref=container_path(trace_ref)),
            }
    finally:
        cleanup_auth_home()

    drive_log = Path(trace_ref).with_suffix(".drive-log.json")
    drive_log.write_text(json.dumps({
        "cmd": redact_cmd(cmd),
        "environment_isolation": codex_isolation_metadata(),
        "returncode": proc.returncode,
        "stdout_tail": proc.stdout[-4000:],
        "stderr_tail": proc.stderr[-4000:],
    }, indent=2), encoding="utf-8")

    try:
        metrics = real_trace_metrics(trace_ref, model)
    except (Exception, SystemExit) as exc:  # noqa: BLE001 - trace may be absent on an early failure
        metrics = zero_metrics(action_surface="kitsoki-studio-mcp", cost_note=f"trace metrics unavailable: {exc}")
    notes = f"drive.sh exit={proc.returncode}: {first_line(proc.stdout + chr(10) + proc.stderr)}"
    if metrics.get("cost_note"):
        notes += f"; {metrics['cost_note']}"
    metrics["studio_mcp_trace_ref"] = container_path(trace_ref)
    metrics["implementation_mode"] = args.implementation_mode or "agent_task"
    metrics["worker_profile"] = profile
    metrics["story_path"] = str(args.story or BENCH_BUGFIX_STORY)
    nonterminal_runtime = trace_has_unclosed_runtime(trace_ref)
    invalid_sequence = trace_has_invalid_sequence(trace_ref)
    if nonterminal_runtime or invalid_sequence:
        metrics["measurement_status"] = "incomplete"
        if nonterminal_runtime:
            notes += "; incomplete strict trace: supervised runtime lifecycle is invalid"
        if invalid_sequence:
            notes += "; incomplete strict trace: EventSink sequence contract is invalid"
    incomplete_measurement = metrics.get("measurement_status") == "incomplete"
    return {
        "blocked": proc.returncode != 0 or incomplete_measurement or nonterminal_runtime or invalid_sequence,
        "notes": notes,
        "metrics": metrics,
    }


def kitsoki_runtime_binary_path(
    *,
    kitsoki_root: Path | None = None,
    env: dict[str, str] | None = None,
) -> Path:
    """Return the platform-local CLI path for a paired-task runtime.

    Arena mounts the source checkout into its Linux cell at
    ``/workspace/kitsoki``. That mount can contain a macOS ``bin/kitsoki``
    created by a host-side run. Executing it in the cell fails with ``Exec
    format error``. Keep cell binaries outside the mount so Go builds them for
    the container platform and never overwrites the operator's binary.
    """
    root = kitsoki_root or KITSOKI_ROOT
    values = env if env is not None else os.environ
    override = values.get("KITSOKI_ARENA_RUNTIME_BIN_DIR", "").strip()
    if override:
        return Path(override) / "kitsoki"
    if root == Path("/workspace/kitsoki"):
        return Path("/tmp/kitsoki-arena-bin") / "kitsoki"
    return root / "bin" / "kitsoki"


def ensure_kitsoki_launcher() -> Path:
    """Provide a PATH launcher without compiling a repo-local Kitsoki binary.

    Nested MCP clients require a ``kitsoki`` command, but a generated binary is
    stale-prone and bypasses the normal ``go run`` development surface. The Go
    build cache still makes repeated invocations cheap while the launcher keeps
    every dispatch pinned to this checkout's source.
    """
    launcher = kitsoki_runtime_binary_path()
    launcher.parent.mkdir(parents=True, exist_ok=True)
    source = shlex.quote(str(KITSOKI_ROOT / "cmd" / "kitsoki"))
    contents = f"#!/usr/bin/env bash\nset -euo pipefail\nexec go run {source} \"$@\"\n"
    if not launcher.exists() or launcher.read_text(encoding="utf-8") != contents:
        launcher.write_text(contents, encoding="utf-8")
        launcher.chmod(0o755)
    return launcher.parent


def test_cmd_for(task: dict[str, Any]) -> str:
    """The real public test command for this task's tree, if one exists.

    A no-op command makes the strict GREEN→RED reproducer gate lie: both the
    dirty tree and the stashed baseline pass, so a real authored RED test is
    reported as green. Empty intentionally skips that gate; the story still
    requires the worker's structured reproduction artifact and later promotes
    its own repro_command into the regression gate. A task may declare a public
    focused `test_cmd` without revealing its hidden oracle.
    """
    explicit = str(task.get("test_cmd") or "").strip()
    if explicit:
        return explicit
    oracle = task.get("oracle") or {}
    if oracle.get("kind") == "external_bakeoff":
        meta = json.loads(
            run(
                ["python3", str(BENCH), "meta", "--project", str(oracle["project"]), "--bug", str(oracle["bug"])],
                cwd=KITSOKI_ROOT,
                capture=True,
            ).stdout
        )
        return str(meta.get("test_cmd") or "").strip()
    return ""


def build_kitsoki_prompt(
    args: argparse.Namespace,
    task: dict[str, Any],
    tree: Path,
    trace_ref: str,
    thread: Path,
    profile: str,
    branch: str,
    test_cmd: str,
    orchestrator_backend: str,
) -> str:
    """Adapted from drive_cell.sh's "Drive ONE kitsoki bug-fix pipeline cell"
    template — same shape, but paired-task tasks come from cost-bench.manifest
    (no candidates.yaml/manifest.yaml plumbing to thread through).

    Tool-name spelling MUST match the orchestrator backend: codex's
    tool_search indexes the kitsoki MCP server's own dotted names (studio.ping,
    session.new, ...) so reusing them verbatim works; the CLAUDE backend
    client-side-renames every dotted MCP tool name to mcp__kitsoki__session_new
    (underscored), so a literal "session.new" in the prompt names a tool that
    doesn't exist under claude. dispatch_kitsoki derives orchestrator_backend
    from the chosen orchestrator model (see _orchestrator_backend_for) and
    passes it here so the two stay in sync."""
    tool = (lambda dotted: dotted) if orchestrator_backend == "codex" else (
        lambda dotted: "mcp__kitsoki__" + dotted.replace(".", "_")
    )
    ticket_title = benchmark_ticket_title(task)
    ticket_body = benchmark_ticket_body(task)
    acceptance_contract = task.get("acceptance_contract") or []
    acceptance_line = (
        f"       acceptance_contract: {json.dumps(acceptance_contract, sort_keys=True)}"
        if acceptance_contract
        else ""
    )
    acceptance_base_line = (
        f'       acceptance_base_sha: "{task.get("baseline_sha", "")}"'
        if acceptance_contract
        else ""
    )
    return "\n".join(line for line in [
        "Drive ONE kitsoki bug-fix pipeline cell to completion via the kitsoki studio MCP.",
        f"The fix MUST be generated by the live worker model inside the session (profile "
        f"**{profile}**); you (orchestrator) only click studio tools — do NOT edit source.",
        "",
        f"1. {tool('studio.ping')}.",
        f"2. {tool('session.new')} EXACTLY:",
        f'   - story_path: "{getattr(args, "story", "") or BENCH_BUGFIX_STORY}"',
        '   - harness: "live"',
        f'   - profile: "{profile}"',
        f'   - trace: "{trace_ref}"',
        "   - initial_world:",
        f"       ticket_id: {json.dumps(str(task['id']))}",
        f"       thread: {json.dumps(str(thread))}",
        f"       ticket_title: {json.dumps(ticket_title)}",
        f"       ticket_body: {json.dumps(ticket_body)}",
        f'       workdir: "{tree}"',
        '       workspace_id: ""',
        '       workspace_prepared: true',
        f'       feature_branch: "{branch}"',
        f'       base_branch: "{branch}"',
        '       bugfix_mode: "full"',
        f'       implementation_mode: "{args.implementation_mode or "agent_task"}"',
        '       judge_mode: "llm"',
        f'       test_cmd: "{test_cmd}"',
        acceptance_line,
        acceptance_base_line,
        "       escalate_low_value: true",
        "   (workspace_prepared true + workspace_id EMPTY ⇒ implementer edits the prepared workdir directly + commits. Leave automatic start unset: the explicit full_pipeline submission below is the one lifecycle entry.)",
        "3. Submit `full_pipeline` ONCE with **session.submit** (not session.drive),",
        "   passing `async_after_ms: 900000` so a supervised live worker gets its full",
        "   declared 15-minute bound to settle before control returns.",
        "   control returns. Use the same async_after_ms value for EVERY later",
        "   session.submit in this cell. Then run this exact control loop: after EVERY",
        "   settled turn call session.status; if `running` is unexpectedly present,",
        "   wait 15 seconds without invoking another tool, then call session.status once and",
        "   compare `running.last_event_at_unix_micro`; use session.trace for the latest",
        "   agent.call.* events before deciding it is stalled. If the fresh trace has",
        "   an `agent.runtime.start` without a later `agent.runtime.end`, that supervised",
        "   runtime owns the wait: keep spaced status+trace checks until it emits its",
        "   terminal event or its declared timeout. Do NOT apply the three-check stall",
        "   rule while such a runtime is active. Never spin on bare status polls or",
        "   stop a live worker merely because its state is unchanged. If",
        "   `allowed_intents` contains a forward gate, call session.submit with that",
        "   exact intent. Prefer in order: `continue`, `accept`, `confirm`, `proceed`.",
        "   Example after the reproducer: session.status shows `continue`; call",
        "   session.submit {handle: HANDLE, intent: \"continue\"}. Repeat this loop",
        "   through proposing, implementing, testing, validating, and close-out.",
        "   `session.drive` accepts free text and is NOT the correct tool for a menu",
        "   action. Do NOT stop merely because a worker returned or a phase view says",
        "   `transitioned`; that is exactly when you must inspect allowed_intents and",
        "   submit the next gate. Do NOT re-submit `full_pipeline` after its first use.",
        "   Answer an awaiting-operator ask-gate affirmatively (\"looks correct, proceed\").",
        "4. STOP only at a terminal state, ~25 forward turns, a host_error bounce to idle,",
        "   or a genuine stall after no supervised runtime remains: three spaced (15s) checks",
        "   with no advancing activity and no agent.call.complete/error in the",
        "   fresh trace. On host_error, read",
        "   world.last_error and report it verbatim, then STOP.",
        "5. Then inspect the session status/world/trace through MCP. Do not use shell,",
        "   filesystem, git, GitHub, or non-kitsoki tools during the delegated drive.",
        "Report: final state; trace path; source modified (y/n) + fix SHA; 1-line fix;",
        "reproduction bug_verified (t/f); forward turns; last_error if any.",
    ] if line != "")


def real_trace_metrics(trace_ref: str, model: str) -> dict[str, Any]:
    """Cost/tokens off the REAL kitsoki session trace, read directly (NOT via
    bench.py's `cost` command / read_trace_metrics — that shared reader sums
    input_tokens but DISCARDS cached_input_tokens, then callers price every
    token at the full input rate).

    Verified live on two real kitsoki-codex-native cells: a codex-native
    worker's calls are 90-96% cache reads across a long multi-turn agent
    session (bf__reproducer/bf__implementer each re-read a large, mostly
    unchanged context every tool turn). Pricing that at the full input rate
    instead of pricing.py's cache_read rate (0.1x input) overstated real cost
    by >20x — a genuine cost-reporting bug, not a genuine kitsoki cost gap.
    codex-native runs on ChatGPT-subscription auth (unmetered: recorded
    cost_usd stays 0 even though token counts are real), so this always
    falls back to a cache-aware estimate computed directly from the trace's
    own per-call usage breakdown — the same pricing table method_cost() uses
    on Claude usage blocks, applied here to codex's usage shape."""
    if not os.path.exists(trace_ref):
        return {
            **zero_metrics(),
            "measurement_status": "incomplete",
            "cost_note": "no trace usage recorded",
        }
    total_in = total_cached = total_out = 0
    metered_cost = 0.0
    for line in Path(trace_ref).read_text(encoding="utf-8").splitlines():
        try:
            entry = json.loads(line)
        except (json.JSONDecodeError, ValueError):
            continue
        if entry.get("kind") != "agent.call.complete":
            continue
        meta = (entry.get("payload") or {}).get("meta") or {}
        usage = meta.get("usage") or {}
        total_in += usage.get("input_tokens", 0) or 0
        total_cached += usage.get("cached_input_tokens", 0) or 0
        total_out += usage.get("output_tokens", 0) or 0
        c = meta.get("cost_usd")
        if isinstance(c, (int, float)):
            metered_cost += c
    tokens = total_in + total_out
    if metered_cost > 0:
        return {
            "cost_usd": round(metered_cost, 6),
            "tokens": tokens,
            "input_tokens": total_in,
            "cached_input_tokens": total_cached,
            "output_tokens": total_out,
            "reasoning_output_tokens": 0,
            "cost_basis": "metered",
            "cost_note": "cost_usd from real recorded trace usage (metered)",
            "mcp_calls": trace_event_count(trace_ref, "mcp"),
            "codeact_eval_calls": trace_event_count(trace_ref, "codeact_eval"),
        }
    if tokens == 0:
        return {
            **zero_metrics(),
            "measurement_status": "incomplete",
            "cost_note": "no trace usage recorded",
        }
    return {
        "cost_usd": None,
        "tokens": tokens,
        "input_tokens": total_in,
        "cached_input_tokens": total_cached,
        "output_tokens": total_out,
        "reasoning_output_tokens": 0,
        "cost_basis": "subscription-unmetered",
        "mcp_calls": trace_event_count(trace_ref, "mcp"),
        "codeact_eval_calls": trace_event_count(trace_ref, "codeact_eval"),
        "cost_note": "subscription auth reports no authoritative USD cost; tokens are retained for comparison",
    }


def build_prompt(args: argparse.Namespace, task: dict[str, Any]) -> str:
    return "\n".join([
        "You are fixing one benchmark task in a checked-out repository baseline.",
        benchmark_isolation_instruction(),
        "Do not ask questions. Make the smallest correct change and run focused checks if obvious.",
        f"Task id: {task['id']}",
        f"Archetype: {task['archetype']}",
        f"Treatment: {args.treatment}",
        "",
        public_ticket_text(task),
    ])


def benchmark_isolation_instruction() -> str:
    return (
        "BENCHMARK ISOLATION: work only inside the provided checkout. Do not inspect "
        "operator homes, credentials, Codex/Claude state, memories, other repositories, "
        "or any path outside this checkout."
    )


def benchmark_ticket_title(task: dict[str, Any]) -> str:
    return f"{benchmark_isolation_instruction()}\n\n{task['id']} ({task['archetype']}): {task.get('ticket', '')}"


def benchmark_ticket_body(task: dict[str, Any]) -> str:
    """Return the optional public report supplied by a paired task.

    This is deliberately task data, not a lookup of a future fix or a hidden
    oracle. The raw and MCP treatments receive identical text, so a detailed
    filed issue can express its observable/API contract without biasing either
    arm or exposing the scorer's regression test.
    """
    return str(task.get("ticket_body") or "").strip()


def public_ticket_text(task: dict[str, Any]) -> str:
    title = str(task.get("ticket") or "").strip()
    body = benchmark_ticket_body(task)
    if title and body:
        return f"{title}\n\n## Public bug report\n\n{body}"
    return title or body


def score_tree(task: dict[str, Any], tree: Path) -> dict[str, str]:
    oracle = task.get("oracle") or {}
    if oracle.get("kind") == "github_content":
        target = tree / str(oracle["file"])
        text = target.read_text(encoding="utf-8", errors="replace") if target.exists() else ""
        green = str(oracle["required_text"]) in text
        return {
            "verdict": "solved" if green else "failed",
            "evidence": container_path(target),
            "notes": f"github_content oracle green={green}",
        }
    if oracle.get("kind") == "external_bakeoff":
        out = tree.parent / f"{tree.name}-score.json"
        cmd = [
            "python3",
            str(BENCH),
            "score",
            "--project",
            str(oracle["project"]),
            "--bug",
            str(oracle["bug"]),
            "--tree",
            str(tree),
            "--out",
            str(out),
            "--candidate",
            "paired-task",
            "--treatment",
            "paired-task",
        ]
        proc = subprocess.run(cmd, cwd=KITSOKI_ROOT, text=True, capture_output=True)
        verdict = "failed"
        notes = first_line(proc.stdout + "\n" + proc.stderr)
        if out.exists():
            cell = json.loads(out.read_text(encoding="utf-8"))
            verdict = str(((cell.get("outcome") or {}).get("quality")) or verdict)
            notes = notes or json.dumps(cell.get("outcome", {}), sort_keys=True)
        return {"verdict": verdict, "evidence": container_path(out), "notes": notes}
    if oracle.get("kind") == "bugswarm_fail_pass_pair":
        return score_bugswarm_tree(task, tree)
    return {"verdict": "blocked", "evidence": "", "notes": f"unknown oracle kind {oracle.get('kind')!r}"}


def score_bugswarm_tree(task: dict[str, Any], tree: Path) -> dict[str, str]:
    image = bugswarm_image(task)
    source_dir = bugswarm_source_dir(task)
    # The image runs its artifact script as the Travis user, while candidate
    # files originated on the host. Make the disposable mount writable first
    # without changing the script's expected user/environment.
    # The copied candidate includes a host-owned `.git/objects` store whose
    # hard-linked objects cannot be chmodded from Docker Desktop.  The oracle
    # only needs a writable working tree; deliberately prune Git metadata so a
    # successful candidate is not misclassified as an infrastructure block.
    prep_cmd = (
        f"find {shlex.quote(source_dir)} -path {shlex.quote(source_dir + '/.git')} -prune "
        "-o -exec chmod a+rwX {} +"
    )
    prep = ["docker", "run", "--rm", "--user", "root", "-v", f"{container_path(tree)}:{source_dir}", image, "bash", "-lc", prep_cmd]
    prepared = subprocess.run(prep, cwd=KITSOKI_ROOT, text=True, capture_output=True)
    if prepared.returncode != 0:
        return {"verdict": "blocked", "evidence": task_ref(task), "notes": f"bugswarm candidate permission preparation failed: {first_line(prepared.stdout + chr(10) + prepared.stderr)}"}
    cmd = [
        "docker",
        "run",
        "--rm",
        "-v",
            f"{container_path(tree)}:{source_dir}",
        image,
        "bash",
        "-lc",
        # BugSwarm images do not guarantee their default working directory.
        # Mounting the candidate alone is insufficient: run the artifact script
        # from the documented failed-source root, as corpus verification does.
        f"cd -- {shlex.quote(source_dir)} && run_failed.sh",
    ]
    try:
        proc = subprocess.run(
            cmd,
            cwd=KITSOKI_ROOT,
            text=True,
            capture_output=True,
            timeout=int(os.environ.get("ARENA_BUGSWARM_SCORE_TIMEOUT_S", "7200")),
        )
    except subprocess.TimeoutExpired as exc:
        return {
            "verdict": "blocked",
            "evidence": task_ref(task),
            "notes": f"bugswarm run_failed.sh timed out after {exc.timeout}s",
        }
    notes = f"bugswarm run_failed.sh exit={proc.returncode}: {first_line(proc.stdout + chr(10) + proc.stderr)}"
    if proc.returncode == 125:
        return {"verdict": "blocked", "evidence": task_ref(task), "notes": notes}
    return {
        "verdict": "solved" if proc.returncode == 0 else "failed",
        "evidence": task_ref(task),
        "notes": notes,
    }


def task_ref(task: dict[str, Any]) -> str:
    return str(task.get("_corpus_path") or DEFAULT_CORPUS) + "#" + str(task.get("id") or "")


def fetch_raw(repo: str, sha: str, file_path: str) -> str | None:
    url = f"https://raw.githubusercontent.com/{repo}/{sha}/{urllib.parse.quote(file_path)}"
    req = urllib.request.Request(url, headers={"User-Agent": "kitsoki-paired-task-runner"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read().decode("utf-8", errors="replace")
    except Exception:
        return None


def run(cmd: list[str], *, cwd: Path, capture: bool = False) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
    if proc.returncode != 0:
        sys.stderr.write(proc.stdout[-2000:] + proc.stderr[-2000:])
        raise SystemExit(f"command failed: {' '.join(cmd)}")
    return proc


def emit(
    *,
    verdict: str,
    cost_usd: float | None = None,
    tokens: int | None = None,
    wall_s: float | None = None,
    evidence_refs: list[str] | None = None,
    trace_ref: str = "",
    notes: str = "",
    metrics: dict[str, Any] | None = None,
    target: str = "",
    exit_code: int = 0,
) -> int:
    payload = {
        "verdict": verdict,
        "cost_usd": cost_usd,
        "tokens": tokens,
        "wall_s": wall_s,
        "evidence_refs": [ref for ref in (evidence_refs or []) if ref],
        "trace_ref": trace_ref,
        "notes": notes,
    }
    if metrics:
        payload["metrics"] = metrics
    serialized = json.dumps(payload, sort_keys=True)
    if target:
        output = Path(target)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(serialized + "\n", encoding="utf-8")
    print(serialized)
    return exit_code


def elapsed(started: float) -> float:
    return round(time.monotonic() - started, 3)


def first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:300]
    return ""


def parse_tokens(blob: str) -> int:
    lines = [line.strip() for line in blob.splitlines()]
    for idx, line in enumerate(lines):
        if line.lower() == "tokens used" and idx + 1 < len(lines):
            try:
                return int(lines[idx + 1].replace(",", ""))
            except ValueError:
                return 0
    return 0


def claude_output_metrics(blob: str, model: str) -> dict[str, Any]:
    tokens = parse_tokens(blob)
    cost_usd: float | None = None
    buckets: dict[str, int] = {}
    for line in blob.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            payload = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        usage = payload.get("usage") or (payload.get("message") or {}).get("usage")
        if isinstance(usage, dict):
            tokens = max(tokens, usage_token_count(usage))
            buckets = usage_token_buckets(usage)
        for key in ("total_cost_usd", "cost_usd"):
            value = payload.get(key)
            if isinstance(value, (int, float)):
                cost_usd = float(value)
                break
    if cost_usd is not None:
        return {
            "cost_usd": round(cost_usd, 6),
            "tokens": tokens,
            **buckets,
            "cost_basis": "metered",
            "cost_note": "cost_usd from claude JSON envelope",
        }
    cost, exact = blended_cost_usd(model, tokens)
    note = "blended estimate, not exact" if not exact else "priced from usage"
    return {"cost_usd": cost, "tokens": tokens, **buckets, "cost_basis": "blended-estimate", "cost_note": note}


def usage_token_count(usage: dict[str, Any]) -> int:
    total = 0
    for key in (
        "input_tokens",
        "output_tokens",
        "cache_read_input_tokens",
        "cache_creation_input_tokens",
        "cached_input_tokens",
    ):
        value = usage.get(key)
        if isinstance(value, int):
            total += value
    cache_creation = usage.get("cache_creation") or {}
    if isinstance(cache_creation, dict):
        for value in cache_creation.values():
            if isinstance(value, int):
                total += value
    return total


def usage_token_buckets(usage: dict[str, Any]) -> dict[str, int]:
    cache_creation = usage.get("cache_creation") or {}
    cache_creation_tokens = 0
    if isinstance(cache_creation, dict):
        cache_creation_tokens = sum(value for value in cache_creation.values() if isinstance(value, int))
    input_tokens = int_value(usage.get("input_tokens")) + int_value(usage.get("cache_creation_input_tokens")) + cache_creation_tokens
    cached_input_tokens = int_value(usage.get("cached_input_tokens")) + int_value(usage.get("cache_read_input_tokens"))
    output_tokens = int_value(usage.get("output_tokens"))
    reasoning_output_tokens = int_value(usage.get("reasoning_output_tokens"))
    return {
        "input_tokens": input_tokens,
        "cached_input_tokens": cached_input_tokens,
        "output_tokens": output_tokens,
        "reasoning_output_tokens": reasoning_output_tokens,
    }


def int_value(value: Any) -> int:
    return value if isinstance(value, int) else 0


_CODEX_LOG_FIELD = re.compile(r"(?:^|\s)([A-Za-z_][A-Za-z0-9_]*)=(\"[^\"]*\"|[^\s]+)")


def structured_turn_completed_usage(line: str) -> tuple[str, dict[str, int]] | None:
    """Extract one request's usage from Codex's retained key=value logs.

    Direct CodeAct can retain logger output rather than Codex's JSONL stream,
    e.g. ``type=turn.completed input_tokens=...``. Prefer a supplied request
    identity; absent one, the complete normalized line is the only honest
    duplicate key (we must not merge two different requests that happen to use
    the same number of tokens).
    """
    fields = {key: value.strip('"') for key, value in _CODEX_LOG_FIELD.findall(line)}
    if fields.get("type") != "turn.completed":
        return None
    usage = {key: int(value) for key, value in fields.items()
             if key in {"input_tokens", "output_tokens", "cached_input_tokens", "cache_read_input_tokens",
                        "cache_creation_input_tokens", "reasoning_output_tokens"} and value.isdigit()}
    if not usage:
        return None
    request_id = next((fields[key] for key in ("request_id", "turn_id", "id") if fields.get(key)), "")
    return ("log-id:" + request_id) if request_id else ("log:" + " ".join(line.split())), usage_token_buckets(usage)


def codex_output_metrics(blob: str, model: str) -> dict[str, Any]:
    # Codex JSONL reports each provider request as a ``turn.completed`` event.
    # A direct CodeAct process can emit several of those events, so the final
    # request is not a summary of the earlier ones.  Keep an event without an
    # id distinct (its line is the only available identity), but de-duplicate
    # retransmitted events that carry an explicit request/turn id.
    token_totals = {
        "input_tokens": 0,
        "cached_input_tokens": 0,
        "output_tokens": 0,
        "reasoning_output_tokens": 0,
    }
    seen_request_ids: set[str] = set()
    found_usage = False
    cost_usd: float | None = None
    for line in blob.splitlines():
        line = line.strip()
        logged_usage = structured_turn_completed_usage(line)
        if logged_usage is not None:
            request_id, buckets = logged_usage
            if request_id in seen_request_ids:
                continue
            seen_request_ids.add(request_id)
            for key in token_totals:
                token_totals[key] += buckets[key]
            found_usage = True
            continue
        if not line.startswith("{"):
            continue
        try:
            payload = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        usage = payload.get("usage") or (payload.get("message") or {}).get("usage")
        if isinstance(usage, dict):
            event_type = str(payload.get("type") or payload.get("kind") or "")
            request_id = next((str(value) for value in (
                payload.get("request_id"), payload.get("turn_id"), payload.get("id"), usage.get("request_id"),
            ) if value not in (None, "")), "")
            # A top-level ``id`` is only reliably a request identity for the
            # terminal turn event. Other JSON envelopes may reuse a message id.
            if event_type != "turn.completed" and not payload.get("request_id") and not payload.get("turn_id"):
                request_id = ""
            request_key = "json:" + request_id if request_id else ""
            if request_key and request_key in seen_request_ids:
                continue
            if request_key:
                seen_request_ids.add(request_key)
            buckets = usage_token_buckets(usage)
            for key in token_totals:
                token_totals[key] += buckets[key]
            found_usage = True
        for key in ("total_cost_usd", "cost_usd"):
            value = payload.get(key)
            if isinstance(value, (int, float)):
                cost_usd = (cost_usd or 0.0) + float(value)
                break
    tokens = sum(token_totals.values()) if found_usage else parse_tokens(blob)
    if cost_usd is not None:
        return {
            "cost_usd": round(cost_usd, 6),
            "tokens": tokens,
            **token_totals,
            "cost_basis": "metered",
            "cost_note": "cost_usd from codex JSON envelope",
        }
    return {
        "cost_usd": None,
        "tokens": tokens,
        **token_totals,
        "cost_basis": "subscription-unmetered",
        "cost_note": "Codex subscription output contains no authoritative USD cost; tokens are retained for comparison",
    }


def codeact_text_metrics(blob: str) -> dict[str, int]:
    lowered = blob.lower()
    return {
        "mcp_calls": lowered.count("codeact_eval"),
        "codeact_eval_calls": lowered.count("codeact_eval"),
        "codeact_domain_errors": lowered.count("codeact_eval:"),
        "codeact_done_rejections": lowered.count("schema") + lowered.count("done rejected"),
    }


def trace_event_count(trace_ref: str, needle: str) -> int:
    if not os.path.exists(trace_ref):
        return 0
    count = 0
    for line in Path(trace_ref).read_text(encoding="utf-8", errors="replace").splitlines():
        if needle in line:
            count += 1
    return count


def trace_has_unclosed_runtime(trace_ref: str) -> bool:
    """Return true when the shared supervised-runtime contract rejects a trace.

    The runner owns the temporary authentication home for the MCP orchestrator.
    Returning while such a worker is live tears that environment down and turns
    a transport/lifecycle defect into a misleading candidate score. The Go
    contract also rejects orphaned, duplicate, and missing-call-ID events so
    Arena cannot quietly score a malformed trace differently from CI.
    """
    if not os.path.exists(trace_ref):
        return False
    proc = subprocess.run(
        ["go", "run", "./cmd/kitsoki", "trace", "runtime-contract", trace_ref],
        cwd=KITSOKI_ROOT,
        text=True,
        capture_output=True,
    )
    return proc.returncode != 0


def trace_has_invalid_sequence(trace_ref: str) -> bool:
    """Return true when the shared EventSink sequence contract rejects a trace."""
    if not os.path.exists(trace_ref):
        return False
    proc = subprocess.run(
        ["go", "run", "./cmd/kitsoki", "trace", "sequence-contract", trace_ref],
        cwd=KITSOKI_ROOT,
        text=True,
        capture_output=True,
    )
    return proc.returncode != 0


def current_head(tree: Path) -> str:
    if not (tree / ".git").exists():
        return ""
    proc = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=tree,
        text=True,
        capture_output=True,
    )
    if proc.returncode != 0:
        return ""
    return proc.stdout.strip()


def diff_stats(tree: Path, baseline_ref: str = "HEAD") -> dict[str, int]:
    if not (tree / ".git").exists():
        return {"diff_files": 0, "diff_lines_added": 0, "diff_lines_deleted": 0}
    baseline = baseline_ref or "HEAD"
    proc = subprocess.run(
        ["git", "diff", "--numstat", baseline],
        cwd=tree,
        text=True,
        capture_output=True,
    )
    if proc.returncode != 0:
        return {"diff_files": 0, "diff_lines_added": 0, "diff_lines_deleted": 0}
    files = added = deleted = 0
    for line in proc.stdout.splitlines():
        parts = line.split("\t")
        if len(parts) < 3:
            continue
        files += 1
        if parts[0].isdigit():
            added += int(parts[0])
        if parts[1].isdigit():
            deleted += int(parts[1])
    return {"diff_files": files, "diff_lines_added": added, "diff_lines_deleted": deleted}


def safe_name(value: str) -> str:
    return "".join(ch if ch.isalnum() or ch in "._-" else "-" for ch in value)[:180]


def container_path(path: str | Path) -> str:
    """Translate a /workspace/kitsoki-rooted path to its host-visible path.

    Cell JSONs are read back on the host after the container exits; without
    this they'd carry a container-only prefix nothing on the host can open.
    ARENA_HOST_REPO_ROOT is the host side of the same bind mount (set by
    DockerBackend.run from the cell's own mounts, so it's exact per-run — no
    guessing at the mount source).
    """
    text = str(path)
    host_root = os.environ.get("ARENA_HOST_REPO_ROOT")
    if host_root and text.startswith(str(KITSOKI_ROOT)):
        return host_root.rstrip("/") + text[len(str(KITSOKI_ROOT)):]
    return text


def redact_cmd(cmd: list[str]) -> list[str]:
    # Prompt text can include task details; keep command shape without bloating trace.
    if cmd:
        return cmd[:-1] + ["<prompt>"]
    return cmd


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception:  # noqa: BLE001 - a cell runner must never leak a raw traceback to arena
        traceback_text = traceback.format_exc()
        raise SystemExit(emit(
            verdict="blocked",
            notes=f"runner exception: {first_line(traceback_text)}",
            metrics={"measurement_status": "incomplete", "runner_traceback": traceback_text},
            exit_code=0,
        ))
