#!/usr/bin/env python3
"""No-LLM, no-docker test for the persona-qa arena plugin.

Covers: plugin registration alongside bugfix, drive_command argv shape
(non-live driver-replay-smoke + gated live agent dispatch), score() reading a
REAL run bundle's review.json through the unify-contract completion.py bridge
(never stdout regex), a 2-concurrent FakeBackend sweep over a persona axis, and
axis coords carrying persona/scenario values end to end into CellResult/rollup.
"""

from __future__ import annotations

import json
import sys
import tempfile
import threading
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.model import JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402
from arena.rollup import build_rollup  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


# ---- 1. Registration --------------------------------------------------------

check("persona-qa is registered", "persona-qa" in plugins.known(), True)
check("bugfix still registered alongside persona-qa", "bugfix" in plugins.known(), True)
plugin = plugins.get("persona-qa")

# ---- 2. drive_command argv shape -------------------------------------------

SPEC = {
    "job_type": "persona-qa",
    "targets": [{"id": "vscode", "label": "microsoft/vscode", "stack": "typescript"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {"persona": ["core-maintainer"], "scenario": ["project-onboarding"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
}

spec = JobSpec.from_dict(SPEC)
cell = spec.cells()[0]

argv = plugin.drive_command(cell, live=False)
check("non-live runner is python3", argv[0], "python3")
check("non-live path targets kit.py", argv[1].endswith("tools/persona_qa/kit.py"), True)
check("non-live uses replay-smoke", "replay-smoke" in argv, True)
check("project threaded", argv[argv.index("--project") + 1], "vscode")
check("persona threaded", argv[argv.index("--persona") + 1], "core-maintainer")
check("scenario threaded", argv[argv.index("--scenario") + 1], "project-onboarding")
check("json-output requested", "--json-output" in argv, True)
check("non-live never mentions claude dispatch", "claude" in " ".join(argv), False)

live_argv = plugin.drive_command(cell, live=True)
live_cmd = " ".join(live_argv)
check("live path emits a run bundle first", "emit-run" in live_cmd, True)
check("live path dispatches the product-journey-qa-driver agent", "product-journey-qa-driver" in live_cmd, True)
check("live path reviews the bundle afterward", " review " in live_cmd, True)

# ---- 3. score() reads a REAL run bundle's review.json via the bridge -------
#
# Build an actual on-disk run-bundle shape (review.json + deck path) so score()
# genuinely exercises tools.persona_qa.load_product_journey_run reading files
# off disk -- not a hand-built CompletionState and not stdout parsing beyond
# locating the run_dir pointer.

with tempfile.TemporaryDirectory() as tmp:
    run_dir = Path(tmp) / "run-vscode-core-maintainer"
    run_dir.mkdir()
    (run_dir / "review.json").write_text(json.dumps({
        "status": "needs_evidence",
        "summary": "needs_evidence: 16/19 checks passed, 0 warnings, 3 failures",
        "summary_counts": {"passed": 16, "warned": 0, "failed": 3, "total": 19},
        "scenario_outcomes_summary": {"scenarios": 6, "started": 1, "blocked": 0},
    }), encoding="utf-8")

    stdout_payload = json.dumps({
        "status": "passed",
        "run_dir": str(run_dir),
        "review_status": "needs_evidence",
    })

    real_bundle_result = plugin.score(cell, exit_code=0, stdout=stdout_payload, stderr="")
    check("real bundle verdict is partial (16/19, not ready)", real_bundle_result.verdict, "partial")
    check("real bundle health is model:result", real_bundle_result.health, "model:result")
    check("real bundle metrics carry checks_passed", real_bundle_result.metrics["checks_passed"], 16)
    check("real bundle trace_ref is the run_dir", real_bundle_result.trace_ref, str(run_dir))

    # A fully-ready bundle scores solved.
    (run_dir / "review.json").write_text(json.dumps({
        "status": "ready",
        "summary": "ready: 19/19 checks passed",
        "summary_counts": {"passed": 19, "warned": 0, "failed": 0, "total": 19},
    }), encoding="utf-8")
    ready_result = plugin.score(cell, exit_code=0, stdout=stdout_payload, stderr="")
    check("ready bundle verdict is solved", ready_result.verdict, "solved")

# score() with no run_dir pointer at all is an infra failure, not a guessed verdict.
missing = plugin.score(cell, exit_code=0, stdout="not json", stderr="")
check("missing run_dir pointer is infra", missing.health, "infra:missing-run-dir")
check("missing run_dir pointer is blocked, not a model verdict", missing.verdict, "blocked")

crash = plugin.score(cell, exit_code=1, stdout="", stderr="Traceback (most recent call last):\n  boom")
check("crash before any JSON is infra:harness", crash.health, "infra:harness")

# ---- 4. A 2-concurrent FakeBackend sweep over the persona axis ------------

sweep_spec = JobSpec.from_dict({
    "job_type": "persona-qa",
    "targets": [{"id": "vscode", "label": "microsoft/vscode", "stack": "typescript"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {
        "persona": ["core-maintainer", "dependency-debugger", "ide-first-engineer"],
        "scenario": ["project-onboarding"],
    },
    "placement": {"hosts": ["local"], "concurrency": 2, "retry": 0},
})
sweep_cells = sweep_spec.cells()
check("sweep enumerates one cell per persona", len(sweep_cells), 3)

lock = threading.Lock()
release_first = threading.Event()
in_flight = 0
max_in_flight = 0
paused_once = False


def responder(sweep_cell, host, sweep_argv):
    global in_flight, max_in_flight, paused_once
    should_pause = False
    with lock:
        in_flight += 1
        max_in_flight = max(max_in_flight, in_flight)
        if not paused_once:
            paused_once = True
            should_pause = True
        elif not release_first.is_set():
            release_first.set()
    try:
        if should_pause:
            release_first.wait(timeout=5)
        persona = sweep_cell.axis["persona"]
        bundle_dir = Path(tmp2) / persona
        bundle_dir.mkdir(parents=True, exist_ok=True)
        (bundle_dir / "review.json").write_text(json.dumps({
            "status": "ready",
            "summary_counts": {"passed": 19, "warned": 0, "failed": 0, "total": 19},
        }), encoding="utf-8")
        payload = json.dumps({"status": "passed", "run_dir": str(bundle_dir)})
        return ContainerRun(exit_code=0, stdout=payload + "\n", stderr="", host=host)
    finally:
        with lock:
            in_flight -= 1


with tempfile.TemporaryDirectory() as tmp2:
    backend = FakeBackend(responder)
    executor = CellExecutor(backend, mounts_for=lambda c, h: {"/repo": "/workspace/kitsoki"})
    sweep_results = run_sweep(sweep_spec, executor, live=False)

check("sweep completed every cell", len(sweep_results), 3)
check("sweep proved real concurrency (>=2 in flight at once)", max_in_flight >= 2, True)
check("sweep all solved", {r.verdict for r in sweep_results}, {"solved"})

rollup = build_rollup(sweep_results)
check("rollup counts every sweep cell", sum(rollup["summary"]["verdicts"].values()), 3)

# ---- 5. Axis coords carry persona/scenario values onto CellResult ---------

for r in sweep_results:
    check(f"{r.cell_id} axis carries persona", "persona" in r.axis, True)
    check(f"{r.cell_id} axis carries scenario", r.axis.get("scenario"), "project-onboarding")
persona_values = {r.axis["persona"] for r in sweep_results}
check(
    "axis persona values match the sweep's personas",
    persona_values,
    {"core-maintainer", "dependency-debugger", "ide-first-engineer"},
)

if failures:
    print("FAIL: persona-qa arena plugin")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: persona-qa arena plugin (registration, drive_command, review.json bridge scoring, 2-concurrent sweep)")
