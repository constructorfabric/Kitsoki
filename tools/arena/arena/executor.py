"""Cell execution + the container-backend seam (dependency-injected).

A cell runs in a container. `ContainerBackend` is the seam: `DockerBackend` runs
a real `docker --context <host> run …`; `FakeBackend` records calls and returns
canned results so the whole pipeline is unit-testable with no docker and no LLM.
Placement is just *which host (docker context)* the container lands on — `local`
is the local daemon, `vm-N` a remote VM's daemon (a later phase registers those).
"""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass
from typing import Callable, Protocol

from .model import Cell, CellResult
from .plugins import base as plugins


@dataclass
class ContainerRun:
    """Result of running one command in one container."""

    exit_code: int
    stdout: str
    stderr: str
    host: str


class ContainerBackend(Protocol):
    """The execution seam: place+run a container on a host, return its output."""

    def run(self, *, cell: Cell, host: str, image: str, argv: list[str], mounts: dict[str, str]) -> ContainerRun:
        ...


class DockerBackend:
    """Runs cells as real containers via `docker --context <host> run`.

    `host` maps to a docker context — `local` to the default daemon, any other
    name to that context (a VM registered as a remote docker host). The VM
    *executes the container*; placement never forks a separate code path.
    """

    def __init__(self, *, context_for: Callable[[str], str | None] | None = None, runner=subprocess.run) -> None:
        # `local` → the operator's ACTIVE context (omit --context, least surprise
        # with Docker Desktop etc.); any other host → its own named context (a VM
        # registered as a remote docker host).
        self._context_for = context_for or (lambda host: None if host == "local" else host)
        self._runner = runner

    def run(self, *, cell: Cell, host: str, image: str, argv: list[str], mounts: dict[str, str]) -> ContainerRun:
        del cell
        context = self._context_for(host)
        cmd = ["docker"]
        if context:
            cmd += ["--context", context]
        cmd += ["run", "--rm"]
        docker_sock = os.environ.get("ARENA_DOCKER_SOCK_SRC")
        for src, dst in mounts.items():
            cmd += ["-v", f"{src}:{dst}"]
            if dst == "/workspace/kitsoki":
                cmd += ["-e", f"ARENA_HOST_REPO_ROOT={src}"]
                if docker_sock and src != dst:
                    cmd += ["-v", f"{src}:{src}"]
        if docker_sock:
            cmd += ["-v", f"{docker_sock}:/var/run/docker.sock"]
            cmd += ["-e", "DOCKER_HOST=unix:///var/run/docker.sock"]
        if os.environ.get("ARENA_CODEX_HOME_SRC"):
            cmd += ["-e", "CODEX_HOME=/workspace/codex-home"]
        npm_cache = os.environ.get("ARENA_NPM_CACHE_SRC")
        if npm_cache:
            cmd += ["-v", f"{npm_cache}:/workspace/npm-cache", "-e", "NPM_CONFIG_CACHE=/workspace/npm-cache"]
        for name in (
            "ARENA_PAIRED_TASK_ENABLE_CODEX",
            "ARENA_CODEX_TIMEOUT_S",
            "ARENA_CLAUDE_TIMEOUT_S",
            "ARENA_CLAUDE_MAX_BUDGET_USD",
            "ARENA_PAIRED_TASK_WORK_ROOT",
            "ARENA_PAIRED_TASK_PREWARM_TIMEOUT_S",
            "ARENA_PAIRED_TASK_ORCHESTRATOR_MODEL",
            "ARENA_PAIRED_TASK_ORCHESTRATOR_BACKEND",
            "NPM_CONFIG_PREFER_OFFLINE",
            "NPM_CONFIG_FETCH_RETRIES",
            "CLAUDE_CODE_OAUTH_TOKEN",
            "SYNTHETIC_API_KEY",
            "ANTHROPIC_AUTH_TOKEN",
            "ANTHROPIC_BASE_URL",
        ):
            value = os.environ.get(name)
            if value is not None:
                cmd += ["-e", f"{name}={value}"]
        cmd += [image, *argv]
        proc = self._runner(cmd, capture_output=True, text=True)
        return ContainerRun(
            exit_code=proc.returncode,
            stdout=proc.stdout or "",
            stderr=proc.stderr or "",
            host=host,
        )


class FakeBackend:
    """No-docker backend for tests: returns scripted runs and records every call."""

    def __init__(self, responder: Callable[[Cell, str, list[str]], ContainerRun]) -> None:
        self._responder = responder
        self.calls: list[dict] = []

    def run(self, *, cell: Cell, host: str, image: str, argv: list[str], mounts: dict[str, str]) -> ContainerRun:
        self.calls.append({"host": host, "image": image, "argv": argv, "mounts": mounts})
        return self._responder(cell, host, argv)


class CellExecutor:
    """Runs one cell: pick image → drive in container → plugin scores the output."""

    def __init__(self, backend: ContainerBackend, *, mounts_for: Callable[[Cell, str], dict[str, str]] | None = None) -> None:
        self._backend = backend
        # mounts_for is (cell, host) — a remote host's `-v` paths resolve on that
        # host's daemon, so the source side must be the checkout path ON the host.
        self._mounts_for = mounts_for or (lambda cell, host: {})

    def execute(self, cell: Cell, *, host: str = "local", live: bool = False) -> CellResult:
        plugin = plugins.get(cell.job_type)
        image = plugin.image(cell)
        argv = plugin.drive_command(cell, live=live)
        run = self._backend.run(
            cell=cell,
            host=host,
            image=image,
            argv=argv,
            mounts=self._mounts_for(cell, host),
        )
        result = plugin.score(cell, exit_code=run.exit_code, stdout=run.stdout, stderr=run.stderr)
        result.metrics.setdefault("host", run.host)
        return result
