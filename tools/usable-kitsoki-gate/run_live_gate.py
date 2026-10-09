#!/usr/bin/env python3
"""run_live_gate.py — Task 3.3's LIVE half (docs/proposals/
usable-kitsoki-release-gate.md). The gated, cost-bearing counterpart to
`flow_gate_runner.py`'s no-LLM flow-replay path: instead of replaying a
compiled flow fixture through `stories/scenario-foundry-harness`'s stub
`ask` room, this drives a mined scenario's turns into a REAL `workbench:`
room via a REAL live kitsoki session, closing the "honest gap"
`flow_gate_runner.py`'s docstring documents for the no-LLM path.

## Which real workbench room (epic-finalization: multi-target)

Any of `flow_gate_runner.WORKBENCH_TARGETS` (`dev-story` / `pets-dev` /
`slidey-dev` -- the three real `workbench:` rooms this project ships, see
`tools/session-mining/flow_fixture_compiler.py`'s registry) may be driven,
selected by the `GATE_TARGET` env var -- the SAME optional env var
`_gate_cli.py` already reads for the no-LLM path (`GATE_TARGET` unset
defaults to `dev-story`, this module's original single-target behavior).
The app path and session slug are both derived from the registry entry
(`app_id`), never hardcoded to `dev-story` alone -- driving `pets-dev` or
`slidey-dev` used to silently look for the new trace file in `dev-story`'s
`~/.kitsoki/sessions/dev-story/` directory regardless of which app was
actually driven (a real bug fixed at epic-finalization time; see git
history for the single-target constant this replaced).

## Mechanism: `tools/mcp-drive/drive.sh`, not a bespoke `claude -p` spawn

Earlier revisions of this module spawned a raw `claude -p <prompt>`
subprocess with no `--mcp-config`, no `--permission-mode`, and no
`--allowedTools` -- i.e. a headless agent that had no reliable way to reach
the kitsoki studio MCP at all (an in-process/ad-hoc `claude -p` does not
attach any MCP server unless told to) and would have hung or silently
no-opped on the very first `session.new` tool call. `tools/mcp-drive/
drive.sh` is this repo's own already-battle-tested primitive for exactly
this shape of delegation (`--mcp-config tools/mcp-drive/kitsoki-mcp.json
--strict-mcp-config --permission-mode acceptEdits --allowedTools <studio
tools>`, plus retry/backoff over transient provider errors) -- reusing it
here means this module inherits a real, working MCP attachment instead of
re-deriving (and re-breaking) one.

Direct-API `--harness live` (`kitsoki drive --harness live`,
`cmd/kitsoki/credential.go`) was considered and rejected for THIS role: it
requires an `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`/on-disk primary-key
credential, which an ambient Claude Code subscription (the auth `kitsoki
doctor` reports ready) does not expose as a portable key. The studio MCP's
own `session.new {harness: "live"}` resolves live differently
(`cmd/kitsoki/mcp.go`'s `studioHarnessBuilder` builds the CLAUDE-CLI-backed
harness -- subscription auth, no direct API key -- the same one `kitsoki
web`/`kitsoki run` use), which is why driving through the MCP (via
`drive.sh`'s spawned orchestrator agent calling `session.new`/
`session.drive`) is the mechanism that actually works with only an ambient
`claude` CLI login, not a second, incompatible live path.

## Trace path is explicit, not inferred

An earlier revision of this module snapshotted the mtimes of every file
under `~/.kitsoki/sessions/<app_id>/` before spawning the agent and diffed
them afterward to find "the trace file the run just wrote" -- mirroring
`kitsoki run`/`kitsoki drive`'s on-disk convention
(`internal/store/trace_path.go`'s `DefaultTracePath`). That convention does
NOT apply to a studio-MCP-driven session: `internal/mcp/studio/
session_tools.go`'s `resolveTracePath` writes to a **fresh
`os.CreateTemp("", "kitsoki-studio-*.jsonl")` file** whenever `session_new`'s
`trace` argument is omitted -- i.e. every prior invocation of this module
would have looked in the wrong directory entirely and always reported "no
new session trace file was found", never actually joining a real trace. The
fix is not a better heuristic: `session_new` accepts an explicit `trace`
path (`internal/mcp/studio/session_runtime.go`'s `newSessionRuntime` calls
`os.MkdirAll` on its directory and `store.OpenJSONL` on the path directly,
no different from the CLI's own `--trace`), so this module picks the path
itself, tells the agent to pass it verbatim, and reads it back from the
exact location it named -- no directory-diffing, no ambiguity, no race with
any of this machine's many OTHER concurrent kitsoki sessions.

## Structural gating (mirrors tools/swarm/tiers/liveExplorerCli.ts exactly)

This module is NEVER executed by any test in this repo -- grep
`tools/arena/tests/` and `tools/usable-kitsoki-gate/` for this file's name;
it will only turn up in docstrings/README references and in
`usable_kitsoki_gate.py`'s `drive_command(cell, live=True)` branch (which
itself is only reachable when an operator passes `arena run --live`
explicitly -- see `tools/arena/arena.py`'s `--live` flag and README's
"Live... gated behind --live, cost-bearing, never run in CI").

The ONE gate lives in `assert_live_gate_allowed`: `live_gate` must be
`is True`, and the only way this module's own `main()` ever sets that to
`True` is a LITERAL `--live-gate` flag in `sys.argv` -- not an env var, not
a config default (mirrors `liveExplorerCli.ts`'s `parseArgs().liveExplorers`
contract, and `bugfix.py`'s `--live`-gated `drive_cell.sh` path). Deleting
the flag from the invocation makes `parse_args` return `live_gate=False`,
which makes `main` refuse before spawning any agent or touching a real
session -- tested directly in
`tools/arena/tests/test_usable_kitsoki_gate_live_gate.py` by calling
`parse_args`/`assert_live_gate_allowed` themselves, never by invoking this
file's `main()`.

## What ONE live cell does (only reachable via `--live-gate`, manual only)

1. Loads the scenario IR document (`GATE_SCENARIO_CORPUS`/`GATE_SCENARIO_ID`,
   the identical env contract `_gate_cli.py` reads for the no-LLM path) and
   resolves the target workbench room (`GATE_TARGET`, default `dev-story`).
2. Picks a KNOWN, deterministic trace path under `evidence_dir` (never a
   sessions-directory mtime-diff heuristic -- see "Trace path is explicit,
   not inferred" below for why an earlier revision's approach was broken)
   and threads it into the briefing prompt as `session_new`'s own `trace`
   argument, so the orchestrator agent writes the session's JSONL exactly
   where this script already knows to read it back from.
3. Builds a briefing prompt (mirrors `liveExplorerCli.ts`'s `buildPrompt`)
   instructing the agent to call the kitsoki studio MCP's `session_new`
   (`story_path`, `harness: "live"`, `profile: "claude-native"`, `trace`)
   then `session_drive` once per mined turn, IN ORDER, as free text -- real
   routing, real dispatch, real LLM spend. `AskUserQuestion` is hard-denied
   for headless agents (AGENTS.md); the prompt tells the agent to proceed on
   its own judgment rather than stall.
4. Spawns that agent via `tools/mcp-drive/drive.sh` (`--agent-cmd` selects
   the ORCHESTRATOR backend, `claude` (default) or `codex`, via
   `MCP_DRIVE_BACKEND`) -- real LLM spend happens here, exactly once per
   cell for the orchestrator, plus once more per turn for the workbench's
   own `host.agent.task` dispatch (a SEPARATE real agent the kitsoki engine
   itself spawns -- `internal/host/agent_backend.go`'s default `claude`
   backend -- when the room's `on_enter` fires).
5. Reads that same known trace path back (once the agent process exits),
   parses it as the SAME raw `store.Event` JSONL
   `flow_gate_runner.run_scenario_flow`'s `--trace-out` produces (one line
   per event, `{"kind": "turn.end", "payload": {...}}` for the ones that
   matter), and re-uses `usable_kitsoki_gate.py`'s `extract_turn_signals` /
   `build_parity_record` verbatim -- the SAME S1 x S4 join the no-LLM path
   performs, so a live and a no-LLM record can never silently drift onto
   different join logic.
6. Writes `{"records": [<record>]}` to `GATE_RESULTS_PATH` and prints the
   `[usable-kitsoki-gate] wrote <path> (N records)` pointer line
   `usable_kitsoki_gate.py`'s `score()` scans for -- the identical contract
   the no-LLM harnesses honor, so the plugin's `score()` needs no
   live-vs-no-LLM branch of its own.

## Why this is not run in CI, ever

Every invocation costs real LLM tokens (an orchestrator agent, PLUS one real
workbench dispatch per turn) and depends on an operator's own `claude`/agent
credentials being present in the environment -- exactly the two properties
AGENTS.md's "Automated testing should never use a real LLM or incur costs"
rule forbids in any CI job. The release-candidate workflow (`.github/
workflows/usable-kitsoki-gate.yml`'s `release-candidate-live-gate` job)
documents the intended cadence (an explicit tag or `workflow_dispatch`,
never `pull_request`/`push: main`) but still never actually invokes this
file with `--live-gate` from within the repo -- that remains an operator's
own `arena run --live` invocation (or a direct, deliberate manual run) per
`tools/arena/README.md`'s existing "Live... never run in CI" convention.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import flow_gate_runner as runner  # noqa: E402

_ARENA_ROOT = runner.REPO_ROOT / "tools" / "arena"
if str(_ARENA_ROOT) not in sys.path:
    sys.path.insert(0, str(_ARENA_ROOT))

from arena.plugins import usable_kitsoki_gate as gate_plugin  # noqa: E402

# The real `workbench:` rooms this live path can drive -- reuses the SAME
# registry `flow_gate_runner.py` (no-LLM path) and `flow_fixture_compiler.py`
# already treat as the single source of truth, so a live and a no-LLM record
# for the same `target` can never silently disagree on which app/app_id they
# mean. See flow_fixture_compiler.WORKBENCH_TARGETS for the full contract.
WORKBENCH_TARGETS = runner.WORKBENCH_TARGETS
DEFAULT_TARGET = "dev-story"

# tools/mcp-drive/drive.sh — this repo's own headless kitsoki-MCP delegation
# primitive (see module docstring's "Mechanism" section for why this, not a
# bespoke claude -p spawn or `kitsoki drive --harness live`).
DRIVE_SH = runner.REPO_ROOT / "tools" / "mcp-drive" / "drive.sh"

DEFAULT_AGENT_CMD = "claude"
LIVE_AGENT_TIMEOUT_SECONDS = 1800  # 30 minutes -- a real multi-turn agent drive, not a flow replay

# The orchestrator agent `drive.sh` spawns only ever needs to click these four
# studio tools for this module's job (mint a session, submit turns, poll,
# close) -- never Bash/Read/Glob/Grep, which `drive.sh`'s own default
# MCP_DRIVE_TOOLS allowlist otherwise grants (useful for its OTHER callers
# that stage/verify a worktree between turns, not needed or wanted here).
# Narrowing the allowlist means a wandering orchestrator turn cannot touch
# this repo's files itself; the workbench's own real tool use happens in a
# SEPARATE agent process the kitsoki engine spawns for host.agent.task,
# governed by that room's own WS toolbox + write_mode gate, not by this list.
ORCHESTRATOR_TOOLS = (
    "mcp__kitsoki__session_new,"
    "mcp__kitsoki__session_drive,"
    "mcp__kitsoki__session_status,"
    "mcp__kitsoki__session_close"
)


class LiveGateNotAllowedError(Exception):
    """Raised by `assert_live_gate_allowed` when `--live-gate` was not passed
    literally on the command line. The ONE gate this module has -- see the
    module docstring's "Structural gating" section."""


def assert_live_gate_allowed(*, live_gate: bool) -> None:
    if live_gate is not True:
        raise LiveGateNotAllowedError(
            "run_live_gate.py refuses to spend real LLM tokens without an explicit "
            "--live-gate flag on the command line (no env var fallback, no default) -- "
            "see this module's docstring and tools/swarm/tiers/liveExplorerCli.ts for the "
            "identical pattern this mirrors"
        )


def parse_args(argv: list[str]) -> argparse.Namespace:
    """Parses argv. `live_gate` is `True` ONLY if `--live-gate` is literally
    present -- no env var fallback, no implicit default (mirrors
    `liveExplorerCli.ts`'s `parseArgs().liveExplorers` contract exactly).
    """
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--live-gate", action="store_true", dest="live_gate",
        help="required: opt in to real LLM spend for this cell (never set this from CI)",
    )
    parser.add_argument(
        "--agent-cmd", default=DEFAULT_AGENT_CMD,
        help="orchestrator backend drive.sh spawns to drive the MCP: claude|codex (default: claude)",
    )
    return parser.parse_args(argv)


def build_live_prompt(scenario: dict[str, Any], target_app: Path, trace_path: Path) -> str:
    """Mirrors `liveExplorerCli.ts`'s `buildPrompt` -- a persona-briefed,
    scenario-grounded prompt telling the spawned orchestrator agent exactly
    which kitsoki studio MCP tool calls to make and in what order, so driving
    the session is a mechanical transcription of the scenario's own mined
    turns rather than something requiring the orchestrator's own judgment
    (the workbench's real judgment is exercised separately, inside the
    engine's own `host.agent.task` dispatch). `trace_path` is threaded into
    `session_new`'s own `trace` argument so this module reads the session
    back from a path IT chose, never a directory-mtime guess (see the module
    docstring's "Trace path is explicit, not inferred" section)."""
    turns = scenario.get("turns") or []
    turn_lines = "\n".join(
        f"  {i + 1}. {t.get('text', '')}" for i, t in enumerate(turns) if isinstance(t, dict)
    )
    return "\n".join([
        "You are driving ONE real kitsoki session as part of the usable-kitsoki-gate",
        "live parity harness (docs/tracing/usable-kitsoki-gate.md). Use the kitsoki",
        "studio MCP tools ONLY, in exactly this order, and do nothing else:",
        "",
        f"1. Call session_new with story_path=\"{target_app}\", harness=\"live\", "
        f"profile=\"claude-native\", trace=\"{trace_path}\". Remember the returned handle.",
        "2. For each numbered turn below, IN ORDER, call session_drive with that",
        "   handle and input set to the turn's verbatim text -- do not skip, reorder,",
        "   paraphrase, or combine turns. If a call returns {running: true}, call",
        "   session_status with the same handle repeatedly (a few seconds apart) until",
        "   running clears before moving to the next turn.",
        "3. After the last turn has been submitted and settled, call session_close",
        "   with the handle. Do not delete or otherwise touch the session before that.",
        "",
        f"Persona: {scenario.get('persona', '')}",
        f"Goal: {scenario.get('goal', '')}",
        "",
        "Turns:",
        turn_lines,
        "",
        "AskUserQuestion is not available to you headless -- proceed on your own",
        "judgment for anything ambiguous rather than stopping to ask.",
    ])


def run_live_cell(
    scenario_id: str,
    corpus_dir: Path,
    surface: str,
    *,
    target: str,
    agent_cmd: str,
    evidence_dir: Path,
) -> dict[str, Any]:
    """The real work `main()` performs once `--live-gate` has been checked.
    Never called from anywhere else in this repo (see module docstring)."""
    if target not in WORKBENCH_TARGETS:
        raise ValueError(
            f"run_live_gate: unknown target {target!r} (known: {', '.join(sorted(WORKBENCH_TARGETS))})"
        )
    target_app = runner.REPO_ROOT / "stories" / target / "app.yaml"

    scenario = runner.load_scenario(corpus_dir, scenario_id)

    # A known, deterministic path THIS process chose -- never a directory-
    # mtime guess (see module docstring's "Trace path is explicit, not
    # inferred" section). session_new's own `trace` argument (threaded via
    # the prompt below) writes here; newSessionRuntime creates the parent
    # dir + file itself, so this script does not need to pre-create it.
    trace_path = evidence_dir / f"{scenario_id}.{target}.{surface}.live.session-trace.jsonl"

    prompt = build_live_prompt(scenario, target_app, trace_path)

    env = dict(os.environ)
    env["MCP_DRIVE_BACKEND"] = agent_cmd if agent_cmd in ("claude", "codex") else "claude"
    env["MCP_DRIVE_TOOLS"] = ORCHESTRATOR_TOOLS
    proc = subprocess.run(
        [str(DRIVE_SH), prompt],
        cwd=str(runner.REPO_ROOT),
        capture_output=True,
        text=True,
        timeout=LIVE_AGENT_TIMEOUT_SECONDS,
        env=env,
    )

    # Give the trace writer a moment to flush after the agent process exits
    # (best-effort; a real operator run is not latency-sensitive here).
    time.sleep(0.5)

    # ALWAYS persist the orchestrator's own stdout/stderr, regardless of exit
    # code -- an earlier revision only did this on a non-zero exit, which left
    # a "the orchestrator exited 0 but the trace shows no turns were ever
    # driven" outcome completely undebuggable after the fact (the spawned
    # `claude -p` process, and any explanation it gave for stopping short, is
    # gone by the time this function inspects the result). A zero-exit,
    # zero-turn cell is exactly the kind of gap the live gate exists to
    # surface, so it must be at least as inspectable as a hard failure.
    evidence_dir.mkdir(parents=True, exist_ok=True)
    orchestrator_log_path = evidence_dir / f"{scenario_id}.{target}.{surface}.live.orchestrator.log"
    orchestrator_log_path.write_text(
        f"[exit code {proc.returncode}]\n--- stdout ---\n{proc.stdout}\n--- stderr ---\n{proc.stderr}\n",
        encoding="utf-8",
    )

    trace_events: list[dict[str, Any]] = []
    notes_parts: list[str] = []
    if proc.returncode != 0:
        blob = (proc.stdout + "\n" + proc.stderr).strip()
        notes_parts.append(f"live agent exited {proc.returncode}: {blob[:300]}")
    if not trace_path.is_file():
        notes_parts.append(
            f"no session trace was found at {trace_path} after the live agent run "
            "(the agent may not have called session_new with the requested trace path) "
            "-- treating as no signal captured, not fabricating one"
        )
    else:
        for line in trace_path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line:
                trace_events.append(json.loads(line))
        if not trace_events or not any(e.get("kind") == "turn.end" for e in trace_events):
            notes_parts.append(
                f"orchestrator exited {proc.returncode} but the trace at {trace_path} shows "
                "no turn.end event -- session_new likely succeeded (a trace file/header exists) "
                "but no turn was ever driven to completion; see "
                f"{orchestrator_log_path} for the orchestrator's own stdout/stderr"
            )

    turn_signals = gate_plugin.extract_turn_signals(trace_events)

    evidence_refs = [str(orchestrator_log_path)]
    if trace_path.is_file():
        evidence_refs.append(str(trace_path))

    return gate_plugin.build_parity_record(
        scenario=scenario,
        surface=surface,
        turn_signals=turn_signals,
        evidence_refs=evidence_refs,
        notes="; ".join(notes_parts),
    )


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        assert_live_gate_allowed(live_gate=args.live_gate)
    except LiveGateNotAllowedError as exc:
        print(f"[usable-kitsoki-gate] {exc}", file=sys.stderr)
        return 2

    scenario_corpus = os.environ.get("GATE_SCENARIO_CORPUS") or str(runner.DEFAULT_CORPUS)
    corpus_dir = Path(scenario_corpus)
    if not corpus_dir.is_absolute():
        corpus_dir = runner.REPO_ROOT / corpus_dir

    scenario_id = os.environ.get("GATE_SCENARIO_ID")
    if not scenario_id:
        print("[usable-kitsoki-gate] GATE_SCENARIO_ID is required (one cell = one scenario)", file=sys.stderr)
        return 2

    surface = os.environ.get("GATE_SURFACE") or gate_plugin.DEFAULT_SURFACE
    target = os.environ.get("GATE_TARGET") or DEFAULT_TARGET
    run_id = os.environ.get("GATE_RUN_ID") or "local-live"
    results_path_env = os.environ.get("GATE_RESULTS_PATH")
    if results_path_env:
        results_path = Path(results_path_env)
        if not results_path.is_absolute():
            results_path = runner.REPO_ROOT / results_path
    else:
        results_path = runner.REPO_ROOT / ".artifacts" / "usable-kitsoki-gate" / run_id / "parity-records.json"

    evidence_dir = results_path.parent / "evidence"

    try:
        record = run_live_cell(
            scenario_id, corpus_dir, surface,
            target=target, agent_cmd=args.agent_cmd, evidence_dir=evidence_dir,
        )
    except runner.ScenarioNotFound as exc:
        print(f"[usable-kitsoki-gate] {exc}", file=sys.stderr)
        return 2
    except ValueError as exc:
        print(f"[usable-kitsoki-gate] {exc}", file=sys.stderr)
        return 2

    results_path.parent.mkdir(parents=True, exist_ok=True)
    results_path.write_text(json.dumps({"run_id": run_id, "records": [record]}, indent=2), encoding="utf-8")
    print(f"[usable-kitsoki-gate] wrote {results_path} (1 record)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
