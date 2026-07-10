#!/usr/bin/env python3
"""agent_dispatch.py — the injected agent-dispatch seam WS-G G1's runners share.

Both `docs_fidelity.py` and `ux_heuristic.py` need to hand a prompt (plus, for
ux-heuristic, a set of captured frame paths) to a persona/vision-critique
agent and get back its verdict as text. Neither runner talks to a process
directly — they take a `DispatchFn` parameter (dependency injection, per
AGENTS.md) so:

  - production callers pass `claude_cli_dispatch` (or nothing — it's the
    default), which shells out to the kitsoki headless-agent harness
    (`claude -p ... --output-format json`, the same CLI the repo's other
    headless dispatch points use — see `tools/usable-kitsoki-gate/run_live_gate.py`);
  - tests pass a scripted fake that returns canned JSON text with zero
    subprocess, zero network, zero LLM cost.

`extract_json_object` centralizes pulling the agent's structured answer out of
whatever the harness wrapped it in (the `claude --output-format json`
envelope's `result` field, or a bare JSON blob an agent printed directly) so
both runners parse responses identically.
"""
from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path
from typing import Callable, Protocol

DEFAULT_TIMEOUT_S = 900


class DispatchFn(Protocol):
    """`(prompt, cwd) -> raw text response`. Production or fake, same shape."""

    def __call__(self, prompt: str, cwd: Path) -> str: ...


class AgentDispatchError(RuntimeError):
    """Raised when a dispatch call fails or its output can't be parsed."""


def claude_cli_dispatch(
    prompt: str,
    cwd: Path,
    *,
    agent: str | None = None,
    model: str | None = None,
    timeout_s: int = DEFAULT_TIMEOUT_S,
    tools: tuple[str, ...] | None = None,
) -> str:
    """Production dispatch: one headless `claude -p` turn, no session state.

    This is the same class of call as the repo's other headless-agent
    dispatch points (`claude -p --agent ... --dangerously-skip-permissions`,
    see `tools/arena/arena/plugins/persona_qa.py`) — a single print-mode
    invocation whose whole job is to answer the prompt and stop, never a
    conversational MCP-driven session. Callers that need the full kitsoki
    studio MCP toolchain (a persona actually driving the app, not just
    critiquing captured artifacts) should inject a different `DispatchFn` that
    wraps `tools/mcp-drive/drive.sh` instead — this default is intentionally
    the simplest thing that answers a bounded judging prompt.
    """
    command = ["claude", "-p", prompt, "--output-format", "json"]
    if tools is not None:
        # A bounded judging prompt must not wander: pin the tool list (empty
        # tuple = tool-less single-shot answer) and skip project MCP startup
        # entirely — the first live docs-fidelity run hung for the full 900s
        # because the agent tried to *execute* the doc under judgment instead
        # of scoring the embedded text.
        command += ["--tools", *tools] if tools else ["--tools", ""]
        command += ["--strict-mcp-config"]
    if agent:
        command += ["--agent", agent]
    if model:
        command += ["--model", model]
    try:
        proc = subprocess.run(
            command,
            cwd=cwd,
            capture_output=True,
            text=True,
            check=False,
            timeout=timeout_s,
        )
    except (OSError, subprocess.TimeoutExpired) as err:
        raise AgentDispatchError(f"claude dispatch failed to start/finish: {err}") from err
    if proc.returncode != 0:
        tail = (proc.stderr or proc.stdout or "").strip()[-500:]
        raise AgentDispatchError(f"claude dispatch exited {proc.returncode}: {tail}")
    return proc.stdout


def extract_json_object(text: str) -> dict:
    """Pull the agent's structured verdict out of raw dispatch output.

    Handles two shapes: the `claude --output-format json` envelope
    (`{"type":"result","result":"<agent's final text>",...}` — unwrap
    `result` and re-scan it), and a bare JSON object an agent printed
    directly. Raises `AgentDispatchError` (never a bare parse exception) if
    neither yields a JSON object — an honest, typed failure the caller can
    fold into a `blocked`/`infra:*` verdict rather than crashing the runner.
    """
    candidate = text
    try:
        envelope = json.loads(text)
        if isinstance(envelope, dict) and isinstance(envelope.get("result"), str):
            candidate = envelope["result"]
    except json.JSONDecodeError:
        pass

    match = re.search(r"\{.*\}", candidate, re.DOTALL)
    if not match:
        raise AgentDispatchError(f"no JSON object found in agent output: {text[:300]!r}")
    try:
        parsed = json.loads(match.group(0))
    except json.JSONDecodeError as err:
        raise AgentDispatchError(f"agent output was not valid JSON: {err}") from err
    if not isinstance(parsed, dict):
        raise AgentDispatchError(f"agent output JSON was not an object: {parsed!r}")
    return parsed
