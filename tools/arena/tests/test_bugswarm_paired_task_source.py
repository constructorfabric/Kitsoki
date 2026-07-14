#!/usr/bin/env python3
"""No-LLM/no-Docker tests for scheduling verified BugSwarm sources."""

from __future__ import annotations

import json
import hashlib
import os
import runpy
import subprocess
import sys
import tempfile
from types import SimpleNamespace
from pathlib import Path

import yaml  # type: ignore

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
CONVERT = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena.py"
APPLY = REPO_ROOT / "tools/arena/scripts/bugswarm_apply_verification.py"
GEN_SPEC = REPO_ROOT / "tools/arena/scripts/bugswarm_to_arena_spec.py"
RUNNER = REPO_ROOT / "tools/arena/lib/paired_task_runner.py"

sys.path.insert(0, str(ARENA_ROOT))
from arena.model import JobSpec  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


with tempfile.TemporaryDirectory() as tmp:
    tmpdir = Path(tmp)
    artifacts = tmpdir / "artifacts.json"
    source = tmpdir / "source.yaml"
    verification = tmpdir / "verification.json"
    verified_source = tmpdir / "verified.yaml"
    spec_path = tmpdir / "bugswarm-paired-task.yaml"

    artifacts.write_text(json.dumps({
        "artifacts": [
            {
                "image_tag": "square-okio-140452393",
                "repo": "square/okio",
                "failed_job_id": "140452393",
                "passed_job_id": "140452394",
            },
            {
                "image_tag": "square-okhttp-200000001",
                "repo": "square/okhttp",
                "failed_job_id": "200000001",
                "passed_job_id": "200000002",
            },
        ]
    }), encoding="utf-8")
    convert = subprocess.run(
        [sys.executable, str(CONVERT), "--in", str(artifacts), "--out", str(source)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("converter exits zero", convert.returncode, 0)
    verification.write_text(json.dumps({
        "kind": "arena_bugswarm_verification",
        "version": 1,
        "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
        "mode": "execute",
        "task_count": 2,
        "verified_count": 1,
        "results": [
            {
                "task_id": "bugswarm-square-okio-140452393",
                "image_tag": "square-okio-140452393",
                "verified_red": True,
                "verified_green": True,
                "failed_exit_code": 1,
                "passed_exit_code": 0,
            },
            {
                "task_id": "bugswarm-square-okhttp-200000001",
                "image_tag": "square-okhttp-200000001",
                "verified_red": False,
                "verified_green": False,
                "failed_exit_code": 0,
                "passed_exit_code": 0,
            },
        ],
    }), encoding="utf-8")
    apply = subprocess.run(
        [sys.executable, str(APPLY), "--source", str(source), "--verification", str(verification), "--out", str(verified_source)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("apply exits zero", apply.returncode, 0)

    generate = subprocess.run(
        [sys.executable, str(GEN_SPEC), "--source", str(verified_source), "--out", str(spec_path)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("spec generator exits zero", generate.returncode, 0)
    spec_payload = yaml.safe_load(spec_path.read_text(encoding="utf-8"))
    check("job type", spec_payload["job_type"], "paired-task")
    check("target corpus threaded", spec_payload["targets"][0]["corpus"], str(verified_source))
    check("verified-only task axis", spec_payload["axes"]["task"], ["bugswarm-square-okio-140452393"])
    check("variant treatments", [v["treatment"] for v in spec_payload["variants"]], ["kitsoki", "single-briefed"])
    check("default variant backends", [v["backend"] for v in spec_payload["variants"]], ["codex", "synthetic"])
    check("candidate model", spec_payload["variants"][0]["model"], "glm-5.2")
    validation = subprocess.run(
        [sys.executable, str(REPO_ROOT / "tools/arena/arena.py"), "validate", "--spec", str(spec_path)],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("default generated spec validates without live spend", validation.returncode, 0)
    live_spec_path = tmpdir / "bugswarm-paired-task-live.yaml"
    live_generate = subprocess.run(
        [
            sys.executable,
            str(GEN_SPEC),
            "--source",
            str(verified_source),
            "--out",
            str(live_spec_path),
            "--kitsoki-backend",
            "codex",
            "--raw-backend",
            "claude",
        ],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("live spec generator exits zero", live_generate.returncode, 0)
    live_spec_payload = yaml.safe_load(live_spec_path.read_text(encoding="utf-8"))
    check("live variant backends", [v["backend"] for v in live_spec_payload["variants"]], ["codex", "claude"])
    old_kitsoki_root = os.environ.get("KITSOKI_ROOT")
    os.environ["KITSOKI_ROOT"] = str(REPO_ROOT)
    try:
        runner_globals = runpy.run_path(str(RUNNER), run_name="paired_task_runner_test")
    finally:
        if old_kitsoki_root is None:
            os.environ.pop("KITSOKI_ROOT", None)
        else:
            os.environ["KITSOKI_ROOT"] = old_kitsoki_root
    check("glm profile mapping", runner_globals["MODEL_TO_PROFILE"].get("glm-5.2"), "synthetic-claude")
    check("glm raw claude mapping", runner_globals["MODEL_TO_RAW_CLAUDE_PROFILE"].get("glm-5.2"), "synthetic-claude")
    check("Kimi profile mapping", runner_globals["MODEL_TO_PROFILE"].get("kimi-k2.7-code"), "synthetic-kimi-k27")
    check("Kimi raw claude mapping", runner_globals["MODEL_TO_RAW_CLAUDE_PROFILE"].get("kimi-k2.7-code"), "synthetic-kimi-k27")
    check("Spark profile mapping", runner_globals["MODEL_TO_PROFILE"].get("gpt-5.3-codex-spark"), "codex-spark")
    check(
        "container runtime CLI stays off host checkout mount",
        runner_globals["kitsoki_runtime_binary_path"](kitsoki_root=Path("/workspace/kitsoki")),
        Path("/tmp/kitsoki-arena-bin/kitsoki"),
    )
    check(
        "container runtime CLI permits an explicit ephemeral cache",
        runner_globals["kitsoki_runtime_binary_path"](
            kitsoki_root=Path("/workspace/kitsoki"),
            env={"KITSOKI_ARENA_RUNTIME_BIN_DIR": "/tmp/arena-cli-cache"},
        ),
        Path("/tmp/arena-cli-cache/kitsoki"),
    )

    spec = JobSpec.load(spec_path)
    cells = spec.cells()
    check("cell count", len(cells), 2)
    plugin = plugins.get("paired-task")
    argv = plugin.drive_command(cells[0], live=False)
    require("plugin passes corpus flag", "--corpus" in argv)
    check("plugin corpus value", argv[argv.index("--corpus") + 1], str(verified_source))
    check("plugin arm-only remains last", argv[-1], "--arm-only")
    relative_payload = dict(spec_payload)
    relative_payload["targets"] = [dict(spec_payload["targets"][0], corpus=".artifacts/bugswarm/verified-source.yaml")]
    relative_cell = JobSpec.from_dict(relative_payload).cells()[0]
    relative_argv = plugin.drive_command(relative_cell, live=False)
    check(
        "plugin maps relative corpus to container repo",
        relative_argv[relative_argv.index("--corpus") + 1],
        "/workspace/kitsoki/.artifacts/bugswarm/verified-source.yaml",
    )

    env = dict(os.environ)
    env["KITSOKI_ROOT"] = str(REPO_ROOT)
    arm = subprocess.run(
        [
            sys.executable,
            str(RUNNER),
            "--task",
            "bugswarm-square-okio-140452393",
            "--treatment",
            "kitsoki",
            "--target",
            "bugswarm-verified",
            "--corpus",
            str(verified_source),
            "--arm-only",
        ],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("runner arm exits zero", arm.returncode, 0)
    payload = json.loads(arm.stdout.splitlines()[-1])
    check("runner verdict", payload["verdict"], "armed")
    check("runner tokens", payload["tokens"], 0)
    check("runner evidence", payload["evidence_refs"], [str(verified_source) + "#bugswarm-square-okio-140452393"])
    require("runner notes identify BugSwarm arm", "bugswarm_fail_pass_pair" in payload["notes"])

    unverified = subprocess.run(
        [
            sys.executable,
            str(RUNNER),
            "--task",
            "bugswarm-square-okhttp-200000001",
            "--treatment",
            "kitsoki",
            "--target",
            "bugswarm-verified",
            "--corpus",
            str(verified_source),
            "--arm-only",
        ],
        cwd=REPO_ROOT,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    check("unverified runner exits nonzero", unverified.returncode, 1)
    unverified_payload = json.loads(unverified.stdout.splitlines()[-1])
    check("unverified runner verdict", unverified_payload["verdict"], "failed")
    require("unverified notes include green=false", "green=False" in unverified_payload["notes"])

    bugswarm_task = yaml.safe_load(verified_source.read_text(encoding="utf-8"))["tasks"][0]
    check("default bugswarm source dir", runner_globals["bugswarm_source_dir"](bugswarm_task), "/home/travis/build/failed/square/okio")
    exported_checkout = tmpdir / "exported-checkout"
    exported_checkout.mkdir()
    (exported_checkout / "README.md").write_text("buggy checkout\n", encoding="utf-8")
    exported_task = dict(bugswarm_task)
    exported_task["meta"] = dict(exported_task.get("meta") or {}, bugswarm_checkout_path=str(exported_checkout))
    exported_tree = tmpdir / "exported-tree"
    runner_globals["materialize_baseline"](exported_task, exported_tree)
    check("exported checkout copied", (exported_tree / "README.md").read_text(encoding="utf-8"), "buggy checkout\n")

    docker_task = dict(bugswarm_task)
    docker_task["meta"] = dict(docker_task.get("meta") or {}, bugswarm_source_dir="/workspace/src", failed_commit_sha="0123456789abcdef0123456789abcdef01234567")
    docker_tree = tmpdir / "docker-tree"
    docker_commands: list[list[str]] = []
    runner_module_globals = runner_globals["materialize_baseline"].__globals__
    original_runner_run = runner_module_globals["run"]
    original_subprocess_run = runner_module_globals["subprocess"].run

    def fake_runner_run(cmd, *, cwd, capture=False):  # noqa: ANN001 - mirrors runner.run shape.
        docker_commands.append(cmd)
        if cmd[:2] == ["docker", "create"]:
            return subprocess.CompletedProcess(cmd, 0, stdout="container-123\n", stderr="")
        if cmd[:2] == ["docker", "cp"]:
            docker_tree.mkdir()
            (docker_tree / "build.gradle").write_text("copied from artifact\n", encoding="utf-8")
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")
        raise AssertionError(f"unexpected command: {cmd}")

    def fake_docker_subprocess(cmd, **kwargs):  # noqa: ANN001 - mirrors subprocess.run shape.
        docker_commands.append(cmd)
        if cmd[:3] == ["docker", "rm", "-f"]:
            return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")
        if cmd[:2] == ["docker", "run"]:
            return subprocess.CompletedProcess(cmd, 0, stdout="tests pass\n", stderr="")
        raise AssertionError(f"unexpected subprocess command: {cmd}")

    try:
        runner_module_globals["run"] = fake_runner_run
        runner_module_globals["subprocess"].run = fake_docker_subprocess
        runner_globals["materialize_baseline"](docker_task, docker_tree)
        docker_score = runner_globals["score_tree"](docker_task, docker_tree)
    finally:
        runner_module_globals["run"] = original_runner_run
        runner_module_globals["subprocess"].run = original_subprocess_run

    check("docker checkout copied", (docker_tree / "build.gradle").read_text(encoding="utf-8"), "copied from artifact\n")
    check("docker materializer create image", docker_commands[0][2], "bugswarm/cached-images:square-okio-140452393")
    check("docker materializer source dir", docker_commands[1][2], "container-123:/workspace/src/.")
    check("docker scorer verdict", docker_score["verdict"], "solved")
    score_cmd = next(cmd for cmd in docker_commands if cmd[:2] == ["docker", "run"] and "run_failed.sh" in cmd[-1])
    check("docker scorer mounts candidate tree", score_cmd[score_cmd.index("-v") + 1], f"{docker_tree}:/workspace/src")
    check("docker scorer command", score_cmd[-2:], ["-lc", "cd -- /workspace/src && run_failed.sh"])
    prep_cmd = next(cmd for cmd in docker_commands if cmd[:2] == ["docker", "run"] and "find /workspace/src" in cmd[-1])
    require("docker scorer permission prep prunes git metadata", "-path /workspace/src/.git -prune" in prep_cmd[-1])

    host_root = tmpdir / "host-kitsoki"
    container_tree = REPO_ROOT / ".artifacts/arena/paired-task-work/container-path"
    old_host_root = os.environ.get("ARENA_HOST_REPO_ROOT")
    try:
        os.environ["ARENA_HOST_REPO_ROOT"] = str(host_root)
        check(
            "container path translates for nested docker",
            runner_globals["container_path"](container_tree),
            str(host_root / ".artifacts/arena/paired-task-work/container-path"),
        )
    finally:
        if old_host_root is None:
            os.environ.pop("ARENA_HOST_REPO_ROOT", None)
        else:
            os.environ["ARENA_HOST_REPO_ROOT"] = old_host_root

    def fake_docker_infrastructure_failure(cmd, **kwargs):  # noqa: ANN001 - mirrors subprocess.run shape.
        if cmd[:2] == ["docker", "run"]:
            if "find /workspace/src" in cmd[-1]:
                return subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")
            return subprocess.CompletedProcess(cmd, 125, stdout="", stderr="docker daemon metadata I/O error\n")
        raise AssertionError(f"unexpected subprocess command: {cmd}")

    try:
        runner_module_globals["subprocess"].run = fake_docker_infrastructure_failure
        blocked_score = runner_globals["score_tree"](docker_task, docker_tree)
    finally:
        runner_module_globals["subprocess"].run = original_subprocess_run

    check("docker infrastructure scorer verdict", blocked_score["verdict"], "blocked")
    require("docker infrastructure scorer notes", "exit=125" in blocked_score["notes"])

    baseline_tree = tmpdir / "baseline-tree"
    baseline_tree.mkdir()
    subprocess.run(["git", "init", "-q"], cwd=baseline_tree, check=True)
    subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=baseline_tree, check=True)
    subprocess.run(["git", "config", "user.name", "Arena Test"], cwd=baseline_tree, check=True)
    (baseline_tree / "fixture.txt").write_text("fixture\n", encoding="utf-8")
    subprocess.run(["git", "add", "fixture.txt"], cwd=baseline_tree, check=True)
    subprocess.run(["git", "commit", "-qm", "fixture"], cwd=baseline_tree, check=True)
    baseline_sha = subprocess.run(["git", "rev-parse", "HEAD"], cwd=baseline_tree, text=True, capture_output=True, check=True).stdout.strip()
    pinned_task = dict(bugswarm_task)
    pinned_task["meta"] = dict(pinned_task.get("meta") or {}, failed_commit_sha=baseline_sha)
    check("materialized baseline pin", runner_globals["assert_materialized_baseline"](pinned_task, baseline_tree), baseline_sha)
    pinned_task["meta"]["failed_commit_sha"] = "0" * 40
    try:
        runner_globals["assert_materialized_baseline"](pinned_task, baseline_tree)
    except SystemExit as exc:
        require("baseline mismatch fails closed", "baseline mismatch" in str(exc))
    else:
        failures.append("baseline mismatch was accepted")

    old_key = os.environ.get("SYNTHETIC_API_KEY")
    old_budget = os.environ.get("ARENA_CLAUDE_MAX_BUDGET_USD")
    os.environ["SYNTHETIC_API_KEY"] = "test-synthetic-key"
    os.environ["ARENA_CLAUDE_MAX_BUDGET_USD"] = "0.50"
    calls: list[dict[str, object]] = []
    original_run = runner_globals["subprocess"].run

    def fake_claude_run(cmd, *, cwd, text, capture_output, timeout, env):  # noqa: ANN001 - mirrors subprocess.run shape.
        calls.append({"cmd": cmd, "cwd": cwd, "env": env, "timeout": timeout})
        return subprocess.CompletedProcess(
            cmd,
            0,
            stdout=json.dumps({
                "result": "patched",
                "total_cost_usd": 0.0123,
                "usage": {"input_tokens": 10, "output_tokens": 5},
            }),
            stderr="",
        )

    try:
        runner_globals["subprocess"].run = fake_claude_run
        trace = tmpdir / "raw-claude-trace.json"
        raw_result = runner_globals["dispatch_single_prompt"](
            SimpleNamespace(backend="claude", model="glm-5.2", treatment="single-briefed"),
            {"id": "fixture-task", "archetype": "unit", "ticket": "Fix the fixture."},
            tmpdir,
            str(trace),
        )
    finally:
        runner_globals["subprocess"].run = original_run
        if old_key is None:
            os.environ.pop("SYNTHETIC_API_KEY", None)
        else:
            os.environ["SYNTHETIC_API_KEY"] = old_key
        if old_budget is None:
            os.environ.pop("ARENA_CLAUDE_MAX_BUDGET_USD", None)
        else:
            os.environ["ARENA_CLAUDE_MAX_BUDGET_USD"] = old_budget

    check("raw claude call count", len(calls), 1)
    call = calls[0]
    cmd = call["cmd"]
    env = call["env"]
    check("raw claude executable", cmd[0], "claude")
    check("raw claude model", cmd[cmd.index("--model") + 1], "hf:zai-org/GLM-5.2")
    check("raw claude base url", env["ANTHROPIC_BASE_URL"], "https://api.synthetic.new/anthropic")
    check("raw claude token expanded", env["ANTHROPIC_AUTH_TOKEN"], "test-synthetic-key")
    check("raw claude blocked", raw_result.get("blocked"), False)
    check("raw claude omits subscription budget", "--max-budget-usd" in cmd, False)
    check("raw claude tokens", raw_result["metrics"]["tokens"], 15)
    check("raw claude cost", raw_result["metrics"]["cost_usd"], 0.0123)
    trace_payload = json.loads(trace.read_text(encoding="utf-8"))
    check("raw claude trace env keys", trace_payload["env_keys"], ["ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"])
    check("raw claude trace redacts secret", "test-synthetic-key" in trace.read_text(encoding="utf-8"), False)
    check("raw claude trace budget field", trace_payload["max_budget_usd"], "")

    timeout_call_trace = tmpdir / "raw-claude-timeout.json"
    timeout_calls: list[dict[str, object]] = []

    def fake_claude_timeout(cmd, *, cwd, text, capture_output, timeout, env):  # noqa: ANN001 - mirrors subprocess.run shape.
        timeout_calls.append({"cmd": cmd, "timeout": timeout, "env": env})
        raise subprocess.TimeoutExpired(cmd, timeout, output="", stderr="tool loop budget exceeded")

    old_key = os.environ.get("SYNTHETIC_API_KEY")
    try:
        os.environ["SYNTHETIC_API_KEY"] = "test-synthetic-key"
        runner_globals["subprocess"].run = fake_claude_timeout
        raw_timeout_result = runner_globals["dispatch_single_prompt"](
            SimpleNamespace(backend="claude", model="glm-5.2", treatment="single-briefed"),
            {"id": "fixture-task", "archetype": "unit", "ticket": "Fix the fixture."},
            tmpdir,
            str(timeout_call_trace),
        )
    finally:
        runner_globals["subprocess"].run = original_run
        if old_key is None:
            os.environ.pop("SYNTHETIC_API_KEY", None)
        else:
            os.environ["SYNTHETIC_API_KEY"] = old_key

    check("raw claude timeout is blocked", raw_timeout_result.get("blocked"), True)
    check("raw claude timeout budget call", timeout_calls[0]["timeout"], 900)
    trace_payload = json.loads(timeout_call_trace.read_text(encoding="utf-8"))
    check("raw claude timeout trace timeout", trace_payload["timeout_s"], 900)
    check("raw claude timeout returncode", trace_payload["returncode"], 124)

if failures:
    print("FAIL: BugSwarm paired-task source")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: BugSwarm paired-task source scheduling (no LLM, no Docker)")
