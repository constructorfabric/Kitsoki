#!/usr/bin/env python3
"""No-LLM, no-subprocess test for `run_live_gate.py`'s structural gating
(docs/proposals/usable-kitsoki-release-gate.md Task 3.3's LIVE half).

This is the counterpart to `tools/runstatus/tests/playwright/
swarm-cassette-users.spec.ts`'s "stubbed live-explorer dispatch contract"
describe block: it proves the gate refuses without `--live-gate`, and that
`main()` never reaches `subprocess.run` (a real agent spawn) when the flag
is absent -- WITHOUT ever actually spawning an agent or driving a session.
`subprocess.run` is monkeypatched to raise if called at all, so a
regression that moved the gate check after the real work would fail this
test loudly rather than silently spending tokens.

Never launches docker, Playwright, a real agent process, or an LLM.
"""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
GATE_TOOLS_DIR = HERE.parents[1] / "usable-kitsoki-gate"
sys.path.insert(0, str(GATE_TOOLS_DIR))

import run_live_gate  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_raises(label: str, fn, exc_type) -> None:
    try:
        fn()
    except exc_type:
        return
    except Exception as exc:  # noqa: BLE001
        failures.append(f"{label}: raised {type(exc).__name__} instead of {exc_type.__name__}")
        return
    failures.append(f"{label}: did not raise {exc_type.__name__}")


def check_does_not_raise(label: str, fn) -> None:
    try:
        fn()
    except Exception as exc:  # noqa: BLE001
        failures.append(f"{label}: raised {type(exc).__name__} unexpectedly: {exc}")


# ---- 1. parse_args: --live-gate is the ONLY thing that sets live_gate=True --

check("no flags -> live_gate is False", run_live_gate.parse_args([]).live_gate, False)
check("--live-gate literally present -> live_gate is True", run_live_gate.parse_args(["--live-gate"]).live_gate, True)
check(
    "other flags without --live-gate still leave live_gate False",
    run_live_gate.parse_args(["--agent-cmd", "claude"]).live_gate,
    False,
)
check(
    "--agent-cmd threads through independently of the gate",
    run_live_gate.parse_args(["--live-gate", "--agent-cmd", "codex"]).agent_cmd,
    "codex",
)
check(
    "--agent-cmd defaults to claude",
    run_live_gate.parse_args(["--live-gate"]).agent_cmd,
    run_live_gate.DEFAULT_AGENT_CMD,
)

# ---- 2. assert_live_gate_allowed: the ONE gate -----------------------------

check_raises(
    "refuses when live_gate is False",
    lambda: run_live_gate.assert_live_gate_allowed(live_gate=False),
    run_live_gate.LiveGateNotAllowedError,
)
check_does_not_raise(
    "allows when live_gate is True",
    lambda: run_live_gate.assert_live_gate_allowed(live_gate=True),
)

# ---- 3. main() never reaches subprocess.run without --live-gate -----------


class _MustNotBeCalled:
    def __call__(self, *args, **kwargs):
        raise AssertionError("subprocess.run was invoked without --live-gate -- gate ordering regressed")


_orig_subprocess_run = run_live_gate.subprocess.run
run_live_gate.subprocess.run = _MustNotBeCalled()  # type: ignore[assignment]
try:
    exit_code = run_live_gate.main([])
    check("main() with no flags returns the gate-refusal exit code", exit_code, 2)

    # Even with a fully-populated GATE_* env (as a real cell would set), the
    # gate check must still fire BEFORE any env is read or agent spawned.
    import os

    env_backup = {
        k: os.environ.get(k)
        for k in ("GATE_SURFACE", "GATE_SCENARIO_CORPUS", "GATE_SCENARIO_ID", "GATE_RUN_ID", "GATE_RESULTS_PATH")
    }
    os.environ["GATE_SCENARIO_ID"] = "scn-would-be-real"
    try:
        exit_code_with_env = run_live_gate.main([])
        check(
            "main() still refuses even with GATE_SCENARIO_ID set (gate precedes env reads)",
            exit_code_with_env,
            2,
        )
    finally:
        for k, v in env_backup.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
finally:
    run_live_gate.subprocess.run = _orig_subprocess_run  # type: ignore[assignment]

if failures:
    print(f"FAIL ({len(failures)}):")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("PASS: run_live_gate.py structural gating (Task 3.3 live half) -- "
      "no agent ever spawned without a literal --live-gate flag")
