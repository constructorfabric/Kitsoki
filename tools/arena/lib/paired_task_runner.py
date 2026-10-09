#!/usr/bin/env python3
"""Run one frozen paired-task arena cell.

The runner is intentionally small: it materializes the pinned task baseline,
optionally dispatches a live worker, then scores the candidate tree with the
task's frozen deterministic oracle. `--arm-only` never calls a model; `--live`
only calls a live CLI when ARENA_PAIRED_TASK_ENABLE_CODEX=1 is set.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("paired_task_runner.py needs pyyaml")

KITSOKI_ROOT = Path(os.environ.get("KITSOKI_ROOT", "/workspace/kitsoki")).resolve()
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
    "gpt-5.5": "codex-native",
}

MODEL_TO_RAW_CLAUDE_PROFILE = {
    "glm-5.2": "synthetic-claude",
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
        ensure_kitsoki_binary=ensure_kitsoki_binary,
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
    parser.add_argument("--backend", default="")
    parser.add_argument("--model", default="")
    parser.add_argument("--effort", default="")
    parser.add_argument("--agent", default="")
    parser.add_argument("--worker-profile", default="")
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
        return arm_task(task)
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


def arm_task(task: dict[str, Any]) -> int:
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
            exit_code=0 if red and green else 1,
        )
    return emit(verdict="blocked", notes=f"unknown oracle kind {kind!r}", exit_code=2)


def run_live(args: argparse.Namespace, task: dict[str, Any]) -> int:
    started = time.monotonic()
    work_root = Path(args.work_root)
    work_root.mkdir(parents=True, exist_ok=True)
    cell_id = safe_name(f"{task['id']}--{args.treatment}--{args.backend or 'backend'}--{args.model or 'model'}")
    trace_ref = str(work_root / f"{cell_id}.jsonl")
    tree = work_root / cell_id
    if tree.exists():
        shutil.rmtree(tree)

    try:
        materialize_baseline(task, tree)
    except (Exception, SystemExit) as exc:  # noqa: BLE001 - report unsupported sources as cell results.
        return emit(
            verdict="blocked",
            cost_usd=0.0,
            tokens=0,
            wall_s=elapsed(started),
            evidence_refs=[task_ref(task)],
            trace_ref=container_path(trace_ref),
            notes=f"baseline materialization failed: {exc}",
            exit_code=0,
        )
    baseline_ref = current_head(tree)
    dispatch = dispatch_worker(args, task, tree, trace_ref)
    score = score_tree(task, tree)
    metrics = dict(dispatch.metrics or {})
    metrics.update(diff_stats(tree, baseline_ref))
    cost_usd = metrics.get("cost_usd")
    if not isinstance(cost_usd, (int, float)):
        cost_usd = 0.0
    tokens = metrics.get("tokens")
    if not isinstance(tokens, int):
        tokens = 0
    verdict = score["verdict"]
    if dispatch.blocked:
        verdict = "blocked"
    notes = "; ".join(part for part in [dispatch.notes, score.get("notes", "")] if part)
    evidence_refs = [task_ref(task), score.get("evidence", "")]
    if dispatch.launch_plan_ref:
        evidence_refs.append(dispatch.launch_plan_ref)
    if dispatch.transcript_ref and dispatch.transcript_ref != container_path(trace_ref):
        evidence_refs.append(dispatch.transcript_ref)
    if not args.keep_workdir:
        cleanup_cell_workdir(tree)
        notes = "; ".join(part for part in [notes, f"removed scratch workdir {container_path(str(tree))}"] if part)
    return emit(
        verdict=verdict,
        cost_usd=cost_usd,
        tokens=tokens,
        wall_s=elapsed(started),
        evidence_refs=evidence_refs,
        trace_ref=dispatch.trace_ref or container_path(trace_ref),
        notes=notes,
        metrics=metrics,
        exit_code=0,
    )


def cleanup_cell_workdir(tree: Path) -> None:
    if not tree.exists():
        return
    for root, dirs, files in os.walk(tree):
        for name in dirs:
            if (Path(root) / name).is_symlink():
                continue
            try:
                os.chmod(Path(root) / name, 0o700)
            except OSError:
                pass
        for name in files:
            if (Path(root) / name).is_symlink():
                continue
            try:
                os.chmod(Path(root) / name, 0o600)
            except OSError:
                pass
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
        repo = meta.get("repo") or "."
        if repo == ".":
            # --no-hardlinks: inside the paired-task container, KITSOKI_ROOT is a
            # read-only bind mount and `tree` lives on the container's own
            # filesystem (e.g. /tmp) — a different device, so --local's default
            # hardlinking fails with "Invalid cross-device link". Force copies.
            run(
                ["git", "clone", "--local", "--no-hardlinks", "--no-checkout", "-q", str(KITSOKI_ROOT), str(tree)],
                cwd=KITSOKI_ROOT,
            )
        else:
            run(["git", "clone", "-q", "--no-checkout", str(repo), str(tree)], cwd=KITSOKI_ROOT)
        run(["git", "checkout", "-q", str(meta["baseline_sha"])], cwd=tree)
        return
    raise SystemExit(f"cannot materialize oracle kind {oracle.get('kind')!r}")


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
        str(tree),
        "--skip-git-repo-check",
        "--dangerously-bypass-approvals-and-sandbox",
        "-s",
        "danger-full-access",
    ]
    if args.model:
        cmd.extend(["--model", args.model])
    cmd.append(prompt)
    proc = subprocess.run(cmd, cwd=tree, text=True, capture_output=True, timeout=int(os.environ.get("ARENA_CODEX_TIMEOUT_S", "900")))
    Path(trace_ref).write_text(json.dumps({
        "cmd": redact_cmd(cmd),
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
        bin_dir = ensure_kitsoki_binary()
        branch = f"paired-task-{safe_name(task['id'])}"
        run(["git", "checkout", "-q", "-B", branch], cwd=tree)
        test_cmd = test_cmd_for(task)
    except (Exception, SystemExit) as exc:  # noqa: BLE001 - report, don't crash the sweep
        return {
            "blocked": True,
            "notes": f"kitsoki dispatch setup failed: {exc}",
            "metrics": zero_metrics(action_surface="kitsoki-studio-mcp"),
        }

    orchestrator_model = os.environ.get("ARENA_PAIRED_TASK_ORCHESTRATOR_MODEL", "gpt-5.5")
    orchestrator_backend = os.environ.get("ARENA_PAIRED_TASK_ORCHESTRATOR_BACKEND") or _orchestrator_backend_for(orchestrator_model)

    thread = Path(trace_ref).with_suffix(".thread.md")
    prompt = build_kitsoki_prompt(args, task, tree, trace_ref, thread, profile, branch, test_cmd, orchestrator_backend)
    prompt_file = Path(trace_ref).with_suffix(".prompt.md")
    prompt_file.write_text(prompt, encoding="utf-8")

    env = dict(os.environ)
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

    drive_log = Path(trace_ref).with_suffix(".drive-log.json")
    drive_log.write_text(json.dumps({
        "cmd": redact_cmd(cmd),
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
    return {
        "blocked": proc.returncode != 0,
        "notes": notes,
        "metrics": metrics,
    }


def ensure_kitsoki_binary() -> Path:
    """Build the kitsoki CLI once and return its containing directory.

    The paired-task image is built ONCE (Dockerfile.paired-task), but the repo
    checkout is bind-mounted at container-RUN time — whatever's on the host at
    that moment, not what was baked into the image. So the binary has to be
    built at run time, not image-build time. `make build-bin` writes to the
    checkout's own (gitignored) bin/, which lives on the host side of the bind
    mount, so a build here is naturally cached across ephemeral `--rm`
    containers as long as they share the same mounted checkout — no cp of a
    locally-built binary (see MEMORY cp-binary-invalidates-codesign; that
    specific failure is macOS ad-hoc-signing, not applicable to this Linux
    image, but building fresh in-place sidesteps the whole class of problem).
    """
    which = shutil.which("kitsoki")
    if which:
        return Path(which).parent
    binary = KITSOKI_ROOT / "bin" / "kitsoki"
    if binary.exists():
        return binary.parent
    run(["make", "build-bin"], cwd=KITSOKI_ROOT)
    if not binary.exists():
        raise RuntimeError("make build-bin did not produce bin/kitsoki")
    return binary.parent


def test_cmd_for(task: dict[str, Any]) -> str:
    """The real test command for this task's tree, or a deliberate no-op.

    github_content tasks arm a repo-history content oracle (no test suite is
    part of the corpus for those repos); "true" is an explicit always-pass
    no-op, NOT an empty string — an empty test_cmd falls back to `go test
    ./...` in stories/bugfix (internal/host/local_ci.go), which is wrong (and
    slow/broken) for a non-Go tree. external_bakeoff tasks DO have a real
    project test_cmd (bench.py meta), the same one bugfix-bakeoff drives.
    """
    oracle = task.get("oracle") or {}
    if oracle.get("kind") == "external_bakeoff":
        meta = json.loads(
            run(
                ["python3", str(BENCH), "meta", "--project", str(oracle["project"]), "--bug", str(oracle["bug"])],
                cwd=KITSOKI_ROOT,
                capture=True,
            ).stdout
        )
        return meta.get("test_cmd") or "true"
    return "true"


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
    ticket_title = f"{task['id']} ({task['archetype']}): {task.get('ticket', '')}"
    return "\n".join([
        "Drive ONE kitsoki bug-fix pipeline cell to completion via the kitsoki studio MCP.",
        f"The fix MUST be generated by the live worker model inside the session (profile "
        f"**{profile}**); you (orchestrator) only click studio tools — do NOT edit source.",
        "",
        f"1. {tool('studio.ping')}.",
        f"2. {tool('session.new')} EXACTLY:",
        f'   - story_path: "{BENCH_BUGFIX_STORY}"',
        '   - harness: "live"',
        f'   - profile: "{profile}"',
        f'   - trace: "{trace_ref}"',
        "   - initial_world:",
        f'       ticket_id: "{task["id"]}"',
        f'       thread: "{thread}"',
        f'       ticket_title: "{ticket_title}"',
        f'       workdir: "{tree}"',
        '       workspace_id: ""',
        f'       feature_branch: "{branch}"',
        f'       base_branch: "{branch}"',
        '       bugfix_mode: "full"',
        f'       implementation_mode: "{args.implementation_mode or "agent_task"}"',
        '       judge_mode: "llm"',
        f'       test_cmd: "{test_cmd}"',
        "       bf_autostart_attempted: true",
        "       escalate_low_value: true",
        "   (workspace_id EMPTY ⇒ implementer edits the prepared workdir directly + commits.)",
        "3. Drive **full_pipeline** ONCE, then only advance explicit gates (accept/continue/",
        "   confirm/proceed) and answer ask-gates affirmatively (\"looks correct, proceed\").",
        "   Do NOT re-drive start — the LLM judge auto-emits accept/refine. Give each",
        "   on_enter step time (the worker profile does the real work there).",
        "4. STOP at a terminal state, ~25 forward turns, or a repeated stuck state. If a",
        "   host_error bounces you to idle, read world.last_error, report it verbatim, STOP.",
        "5. Then inspect the session status/world/trace through MCP. Do not use shell,",
        "   filesystem, git, GitHub, or non-kitsoki tools during the delegated drive.",
        "Report: final state; trace path; source modified (y/n) + fix SHA; 1-line fix;",
        "reproduction bug_verified (t/f); forward turns; last_error if any.",
    ])


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
        return {"cost_usd": 0.0, "tokens": 0, "cost_note": "no trace usage recorded"}
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
        return zero_metrics(cost_note="no trace usage recorded")
    price, _ = price_for(model)
    fresh_input = max(total_in - total_cached, 0)
    cost = (fresh_input * price.input + total_cached * price.cache_read + total_out * price.output) / 1e6
    cost = round(cost, 6)
    return {
        "cost_usd": cost,
        "tokens": tokens,
        "input_tokens": total_in,
        "cached_input_tokens": total_cached,
        "output_tokens": total_out,
        "reasoning_output_tokens": 0,
        "cost_basis": "cache-aware-estimate",
        "mcp_calls": trace_event_count(trace_ref, "mcp"),
        "codeact_eval_calls": trace_event_count(trace_ref, "codeact_eval"),
        "cost_note": (
            f"cost_usd={cost} (cache-aware estimate over REAL trace usage: "
            f"{fresh_input} fresh input + {total_cached} cache-read input + {total_out} output tokens; "
            "subscription auth reports no metered cost)"
        ),
    }


def build_prompt(args: argparse.Namespace, task: dict[str, Any]) -> str:
    return "\n".join([
        "You are fixing one benchmark task in a checked-out repository baseline.",
        "Do not ask questions. Make the smallest correct change and run focused checks if obvious.",
        f"Task id: {task['id']}",
        f"Archetype: {task['archetype']}",
        f"Treatment: {args.treatment}",
        "",
        str(task.get("ticket", "")),
    ])


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
    cmd = [
        "docker",
        "run",
        "--rm",
        "-v",
            f"{container_path(tree)}:{source_dir}",
        image,
        "bash",
        "-lc",
            "run_failed.sh",
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
    print(json.dumps(payload, sort_keys=True))
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


def codex_output_metrics(blob: str, model: str) -> dict[str, Any]:
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
            "cost_note": "cost_usd from codex JSON envelope",
        }
    cost, exact = blended_cost_usd(model, tokens)
    return {
        "cost_usd": cost,
        "tokens": tokens,
        **buckets,
        "cost_basis": "blended-estimate",
        "cost_note": "blended estimate, not exact" if not exact else "priced from usage",
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
    raise SystemExit(main())
