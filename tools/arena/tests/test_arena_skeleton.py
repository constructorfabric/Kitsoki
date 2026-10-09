#!/usr/bin/env python3
"""No-LLM, no-docker end-to-end test of the arena walking skeleton.

Drives the full pipeline — JobSpec → cell enumeration → container execution
(FakeBackend) → bugfix plugin scoring → rollup — with a fake container backend,
so it proves the plumbing deterministically with zero docker and zero LLM. The
real DockerBackend satisfies the same `ContainerBackend` interface.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

# Import the arena package from the tools/arena dir (sibling of tests/).
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.executor import CellExecutor, ContainerRun, DockerBackend, FakeBackend  # noqa: E402
from arena.model import JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.rollup import build_rollup  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


paired_task_dockerfile = (HERE.parent / "docker" / "Dockerfile.paired-task").read_text(encoding="utf-8")
check("paired task image has disposable Git author", "git config --system user.name \"Kitsoki Arena\"" in paired_task_dockerfile, True)
check("paired task image has disposable Git email", "git config --system user.email \"arena@kitsoki.local\"" in paired_task_dockerfile, True)

REPO_ROOT = HERE.parent.parent.parent
drive_cell_source = (REPO_ROOT / "tools" / "bugfix-bakeoff" / "external" / "drive_cell.sh").read_text(encoding="utf-8")
check("drive_cell.sh accepts --story", '--story) story="$2"; shift 2;;' in drive_cell_source, True)
check("drive_cell.sh defaults --story to bench-bugfix", 'story="stories/bench-bugfix/app.yaml"' in drive_cell_source, True)
check("drive_cell.sh interpolates $story into story_path (not a hardcoded literal)", 'story_path: "$REPO_ROOT/$story"' in drive_cell_source, True)
check("drive_cell.sh no longer hardcodes the story literal", 'story_path: "$REPO_ROOT/stories/bench-bugfix/app.yaml"' in drive_cell_source, False)


SPEC = {
    "job_type": "bugfix",
    "targets": [{"id": "query-string", "label": "qs", "stack": "javascript"}],
    "variants": [
        {"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"},
        {"id": "single-gpt-5.5", "backend": "codex", "model": "gpt-5.5"},
    ],
    "axes": {"bug": ["qs1", "qs2", "qs3"]},
    "placement": {"hosts": ["local"], "concurrency": 2, "retry": 1},
}

spec = JobSpec.from_dict(SPEC)

# 1. Enumeration: 1 target × 2 variants × 3 bugs = 6 cells, deterministic ids.
cells = spec.cells()
check("cell count", len(cells), 6)
check("cell id shape", cells[0].id, "query-string--kitsoki-gpt-5.5--bug:qs1")

# 2. Plugin selects the verify (no-LLM arming) command + the repo-runtime image.
from arena.plugins import base as plugins  # noqa: E402

bugfix = plugins.get("bugfix")
argv = bugfix.drive_command(cells[0], live=False)
check("non-live uses bench verify", argv[1:4], ["/workspace/kitsoki/tools/bugfix-bakeoff/external/bench.py", "verify", "--project"])
check("image per project", bugfix.image(cells[0]), "kitsoki-arena-repo/query-string:latest")
live_argv = bugfix.drive_command(cells[0], live=True)
check("live uses drive_cell", live_argv[0:2], ["bash", "/workspace/kitsoki/tools/bugfix-bakeoff/external/drive_cell.sh"])
check("live threads completion-state path", "--completion-state" in live_argv, True)
check("live completion-state points inside mounted repo", live_argv[live_argv.index("--completion-state") + 1].startswith("/workspace/kitsoki/.artifacts/arena/completion-state/"), True)
check("no target.meta.story -> no --story flag threaded (default preserved)", "--story" in live_argv, False)

STORY_SPEC = {
    "job_type": "bugfix",
    "targets": [{"id": "query-string", "label": "qs", "stack": "javascript", "story": "stories/bugfix/app.yaml"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {"bug": ["qs1"]},
}
story_cell = JobSpec.from_dict(STORY_SPEC).cells()[0]
story_live_argv = bugfix.drive_command(story_cell, live=True)
check("target.meta.story threads --story into drive_cell.sh", "--story" in story_live_argv, True)
check("threaded --story value", story_live_argv[story_live_argv.index("--story") + 1] if "--story" in story_live_argv else None, "stories/bugfix/app.yaml")


def write_completion_state(cell, *, verdict, health, notes=""):
    """Simulate bench.py's --completion-state write from inside the container —
    the plugin now scores from this file (schemas/completion-state.schema.json),
    not from stdout. Host-side path == what the plugin reads after the run."""
    path = Path(bugfix.completion_state_path(cell))
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({
        "schema_version": "1.0.0",
        "verdict": verdict,
        "health": health,
        "metrics": {},
        "evidence_refs": [],
        "notes": notes,
    }))
    return path


written_state_paths: list[Path] = []

# 3. Fake container backend: qs2 fails the oracle once then nothing (no retry
#    config here is exercised separately); everything else arms GREEN.
seen_hosts: set[str] = set()


def responder(cell, host, argv):
    seen_hosts.add(host)
    bug = cell.axis["bug"]
    if bug == "qs2":
        written_state_paths.append(write_completion_state(
            cell, verdict="failed", health="model:result",
            notes="RED stayed RED: oracle did not go GREEN"))
        return ContainerRun(exit_code=1, stdout="RED stayed RED: oracle did not go GREEN", stderr="", host=host)
    written_state_paths.append(write_completion_state(
        cell, verdict="armed", health="model:result",
        notes="verify OK: baseline RED, fix GREEN (armed)"))
    return ContainerRun(exit_code=0, stdout="verify OK: baseline RED, fix GREEN (armed)", stderr="", host=host)


backend = FakeBackend(responder)
executor = CellExecutor(backend, mounts_for=lambda c, h: {"/repo": "/workspace/kitsoki"})
results = run_sweep(spec, executor, live=False)

check("result count", len(results), 6)
check("all placed local", seen_hosts, {"local"})
armed = [r for r in results if r.verdict == "armed"]
failed = [r for r in results if r.verdict == "failed"]
check("armed cells (qs1+qs3 × 2 variants)", len(armed), 4)
check("failed cells (qs2 × 2 variants)", len(failed), 2)
check("armed is model health", armed[0].health, "model:result")
# FakeBackend recorded one container call per cell.
check("container calls == cells", len(backend.calls), 6)
check("mounts threaded through", backend.calls[0]["mounts"], {"/repo": "/workspace/kitsoki"})

# 3a. DockerBackend command construction remains no-docker-testable via an
# injected runner. Docker socket mounting is opt-in for nested Docker workloads
# such as BugSwarm materialization/scoring.
docker_commands: list[list[str]] = []


def fake_docker_runner(cmd, *, capture_output, text):  # noqa: ANN001 - mirrors subprocess.run.
    docker_commands.append(cmd)
    return subprocess.CompletedProcess(cmd, 0, stdout="{}", stderr="")


old_sock = os.environ.get("ARENA_DOCKER_SOCK_SRC")
old_synthetic = os.environ.get("SYNTHETIC_API_KEY")
old_budget = os.environ.get("ARENA_CLAUDE_MAX_BUDGET_USD")
old_npm_cache = os.environ.get("ARENA_NPM_CACHE_SRC")
old_npm_offline = os.environ.get("NPM_CONFIG_PREFER_OFFLINE")
try:
    os.environ.pop("ARENA_DOCKER_SOCK_SRC", None)
    os.environ["SYNTHETIC_API_KEY"] = "test-synthetic-key"
    os.environ["ARENA_CLAUDE_MAX_BUDGET_USD"] = "0.50"
    docker_backend = DockerBackend(runner=fake_docker_runner)
    docker_backend.run(cell=cells[0], host="local", image="image:test", argv=["echo", "ok"], mounts={"/repo": "/workspace/kitsoki"})
    check("docker socket not mounted by default", any("docker.sock" in part for part in docker_commands[-1]), False)
    check("synthetic key forwarded", "SYNTHETIC_API_KEY=test-synthetic-key" in docker_commands[-1], True)
    check("claude budget forwarded", "ARENA_CLAUDE_MAX_BUDGET_USD=0.50" in docker_commands[-1], True)

    os.environ["ARENA_NPM_CACHE_SRC"] = "/tmp/npm-cache"
    os.environ["NPM_CONFIG_PREFER_OFFLINE"] = "true"
    docker_backend.run(cell=cells[0], host="local", image="image:test", argv=["echo", "ok"], mounts={"/repo": "/workspace/kitsoki"})
    check("npm cache mounted when requested", "/tmp/npm-cache:/workspace/npm-cache" in docker_commands[-1], True)
    check("npm cache env configured", "NPM_CONFIG_CACHE=/workspace/npm-cache" in docker_commands[-1], True)
    check("npm offline preference forwarded", "NPM_CONFIG_PREFER_OFFLINE=true" in docker_commands[-1], True)

    os.environ["ARENA_DOCKER_SOCK_SRC"] = "/var/run/docker.sock"
    docker_backend.run(cell=cells[0], host="local", image="image:test", argv=["echo", "ok"], mounts={"/repo": "/workspace/kitsoki"})
    check("docker socket mounted when requested", "-v" in docker_commands[-1] and "/var/run/docker.sock:/var/run/docker.sock" in docker_commands[-1], True)
    check("docker host env forwarded", "DOCKER_HOST=unix:///var/run/docker.sock" in docker_commands[-1], True)
    check("host repo mirror mounted for nested docker", "/repo:/repo" in docker_commands[-1], True)
finally:
    if old_sock is None:
        os.environ.pop("ARENA_DOCKER_SOCK_SRC", None)
    else:
        os.environ["ARENA_DOCKER_SOCK_SRC"] = old_sock
    if old_synthetic is None:
        os.environ.pop("SYNTHETIC_API_KEY", None)
    else:
        os.environ["SYNTHETIC_API_KEY"] = old_synthetic
    if old_budget is None:
        os.environ.pop("ARENA_CLAUDE_MAX_BUDGET_USD", None)
    else:
        os.environ["ARENA_CLAUDE_MAX_BUDGET_USD"] = old_budget
    if old_npm_cache is None:
        os.environ.pop("ARENA_NPM_CACHE_SRC", None)
    else:
        os.environ["ARENA_NPM_CACHE_SRC"] = old_npm_cache
    if old_npm_offline is None:
        os.environ.pop("NPM_CONFIG_PREFER_OFFLINE", None)
    else:
        os.environ["NPM_CONFIG_PREFER_OFFLINE"] = old_npm_offline

# 3b. Done with block 3's cell results — clear the fixture files so block 4's
#     reused cell id ("qs1" × the same variant) doesn't pick up a stale file.
for p in written_state_paths:
    p.unlink(missing_ok=True)

# 4. Retry: an infra failure is retried up to placement.retry, a model verdict is final.
#    The infra branch writes NO completion-state file (a real harness failure
#    dies before bench.py can write one) so the plugin falls back to the
#    stdout infra-signal detector; the eventual success branch writes the file.
infra_calls = {"n": 0}


def flaky(cell, host, argv):
    if cell.axis["bug"] == "qs1" and infra_calls["n"] == 0:
        infra_calls["n"] += 1
        return ContainerRun(exit_code=1, stdout="connection refused", stderr="", host=host)
    write_completion_state(cell, verdict="armed", health="model:result", notes="armed")
    return ContainerRun(exit_code=0, stdout="armed", stderr="", host=host)


spec_one = JobSpec.from_dict({**SPEC, "variants": [SPEC["variants"][0]], "axes": {"bug": ["qs1"]}, "placement": {"hosts": ["local"], "concurrency": 1, "retry": 1}})
backend2 = FakeBackend(flaky)
executor2 = CellExecutor(backend2, mounts_for=lambda c, h: {})
retry_results = run_sweep(spec_one, executor2, live=False)
check("infra retried then armed", retry_results[0].verdict, "armed")
check("retry note recorded", "infra" in retry_results[0].notes, True)
Path(bugfix.completion_state_path(spec_one.cells()[0])).unlink(missing_ok=True)

# 5. Rollup buckets by variant + target with a win-rate.
rollup = build_rollup(results)
check("summary n", rollup["summary"]["n"], 6)
check("two variant buckets", sorted(rollup["by_variant"]), ["kitsoki-gpt-5.5", "single-gpt-5.5"])
check("variant win-rate", rollup["by_variant"]["kitsoki-gpt-5.5"]["win_rate"], 0.6667)

if failures:
    print("FAIL: arena skeleton")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: arena skeleton (enumerate → container(fake) → score → rollup, no LLM)")
