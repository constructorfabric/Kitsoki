#!/usr/bin/env python3
"""No-LLM, no-subprocess test for `run_live_calibration.py` (epic-finalization
live sweep over `run_live_gate.py`). Proves the sweep's OWN plumbing --
argument parsing, target validation, and (critically) that it never reaches
a live agent spawn without `--live-gate` -- without ever actually driving a
session or spending a token. Mirrors `test_usable_kitsoki_gate_live_gate.py`'s
shape for the single-cell script this one loops over.

Never launches docker, Playwright, a real agent process, or an LLM.
"""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
GATE_TOOLS_DIR = HERE.parents[1] / "usable-kitsoki-gate"
sys.path.insert(0, str(GATE_TOOLS_DIR))

import run_live_calibration  # noqa: E402
import run_live_gate  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


# ---- 1. parse_args: targets default to all three shipped workbench rooms --

ns = run_live_calibration.parse_args(["--scenarios", "scn-a,scn-b", "--out", "/tmp/x.json"])
check("targets default to all three WORKBENCH_TARGETS", sorted(ns.targets.split(",")), sorted(run_live_gate.WORKBENCH_TARGETS))
check("surface defaults to mcp (the real surface these cells drive)", ns.surface, "mcp")
check("live_gate defaults False", ns.live_gate, False)
check("--scenarios splits on comma", ns.scenarios, "scn-a,scn-b")

ns2 = run_live_calibration.parse_args(["--live-gate", "--scenarios", "scn-a", "--targets", "dev-story", "--out", "/tmp/x.json"])
check("--live-gate is forwardable", ns2.live_gate, True)

# ---- 2. main(): an unknown target is rejected before ANY cell runs ---------


class _MustNotBeCalled:
    def __call__(self, *args, **kwargs):
        raise AssertionError("run_live_gate.subprocess.run was invoked -- an unknown --targets entry "
                              "should be rejected before any cell is attempted")


_orig_subprocess_run = run_live_gate.subprocess.run
run_live_gate.subprocess.run = _MustNotBeCalled()  # type: ignore[assignment]
try:
    rc = run_live_calibration.main([
        "--scenarios", "scn-a", "--targets", "not-a-real-target", "--out", "/tmp/x.json",
    ])
    check("unknown --targets entry returns exit code 2", rc, 2)
finally:
    run_live_gate.subprocess.run = _orig_subprocess_run  # type: ignore[assignment]

# ---- 3. main() WITHOUT --live-gate never reaches a live agent spawn --------
#
# sweep() calls run_live_gate.main() per cell, whose own structural gate
# (test_usable_kitsoki_gate_live_gate.py) refuses before subprocess.run when
# --live-gate is absent. This proves that refusal propagates all the way
# through run_live_calibration.py's loop as a hard RuntimeError, not a
# silently-skipped cell that would let a caller believe a real run happened.

run_live_gate.subprocess.run = _MustNotBeCalled()  # type: ignore[assignment]
try:
    raised = False
    try:
        run_live_calibration.main([
            "--scenarios", "scn-does-not-need-to-exist",
            "--targets", "dev-story",
            "--out", "/tmp/x.json",
        ])
    except RuntimeError as exc:
        raised = True
        check("refusal RuntimeError mentions the gate", "returned 2" in str(exc), True)
    check("main() without --live-gate raises (never silently proceeds)", raised, True)
finally:
    run_live_gate.subprocess.run = _orig_subprocess_run  # type: ignore[assignment]

if failures:
    print(f"FAIL ({len(failures)}):")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("PASS: run_live_calibration.py structural gating + plumbing (epic-finalization live sweep) -- "
      "no agent ever spawned without a literal --live-gate flag propagated to every cell")
