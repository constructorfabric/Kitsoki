#!/usr/bin/env python3
"""WB.2/WB.3 no-LLM arena gates.

Default mode exercises the paired-task plugin through the real arena pipeline
with a tiny built-in fake backend. `--fixture DIR` replays a committed pilot
fixture: spec load -> enumerate -> fake container stdout -> plugin score ->
rollup. No docker, no LLM, no network.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
sys.path.insert(0, str(ARENA_ROOT))

from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.model import JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402
from arena.rollup import write_rollup  # noqa: E402


class Checks:
    def __init__(self) -> None:
        self.failures: list[str] = []

    def check(self, label: str, got: Any, want: Any) -> None:
        if got != want:
            self.failures.append(f"{label}: got {got!r}, want {want!r}")

    def require(self, label: str, condition: bool) -> None:
        if not condition:
            self.failures.append(label)


def fixture_result(task: str, treatment: str) -> dict:
    verdict = "solved"
    if task == "flaky-test" and treatment == "single-naive":
        verdict = "failed"
    costs = {
        "kitsoki": 0.42,
        "single-briefed": 0.31,
        "single-naive": 0.12,
    }
    return {
        "verdict": verdict,
        "cost_usd": costs[treatment],
        "tokens": int(costs[treatment] * 100000),
        "wall_s": 7.5,
        "evidence_refs": [f"fixtures/{task}/{treatment}.json"],
        "trace_ref": f"traces/{task}/{treatment}.jsonl",
    }


def run_default() -> int:
    checks = Checks()
    lifecycle_test = subprocess.run(
        [sys.executable, str(HERE / "test_task_optimization_lifecycle.py")],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    checks.check("task optimization lifecycle gate", lifecycle_test.returncode, 0)
    if lifecycle_test.returncode:
        checks.failures.append((lifecycle_test.stdout + lifecycle_test.stderr).strip())
    scheduler_test = subprocess.run(
        [sys.executable, str(HERE / "test_task_optimization_runner.py")],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    checks.check("task optimization scheduler gate", scheduler_test.returncode, 0)
    if scheduler_test.returncode:
        checks.failures.append((scheduler_test.stdout + scheduler_test.stderr).strip())
    source_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_source.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("bugswarm source adapter gate", source_test.returncode, 0)
    if source_test.returncode:
        checks.failures.append((source_test.stdout + source_test.stderr).strip())
    enrich_source_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_enrich_source.py")],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    checks.check("bugswarm provenance enrichment gate", enrich_source_test.returncode, 0)
    if enrich_source_test.returncode:
        checks.failures.append((enrich_source_test.stdout + enrich_source_test.stderr).strip())
    verify_source_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_verify_source.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("bugswarm source verifier gate", verify_source_test.returncode, 0)
    if verify_source_test.returncode:
        checks.failures.append((verify_source_test.stdout + verify_source_test.stderr).strip())
    disk_budget_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_disk_budget.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("bugswarm disk budget gate", disk_budget_test.returncode, 0)
    if disk_budget_test.returncode:
        checks.failures.append((disk_budget_test.stdout + disk_budget_test.stderr).strip())
    apply_verification_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_apply_verification.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("bugswarm apply verification gate", apply_verification_test.returncode, 0)
    if apply_verification_test.returncode:
        checks.failures.append((apply_verification_test.stdout + apply_verification_test.stderr).strip())
    corpus_lock_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_lock_corpus.py")],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    checks.check("bugswarm corpus lock gate", corpus_lock_test.returncode, 0)
    if corpus_lock_test.returncode:
        checks.failures.append((corpus_lock_test.stdout + corpus_lock_test.stderr).strip())
    paired_task_source_test = subprocess.run(
        [sys.executable, str(HERE / "test_bugswarm_paired_task_source.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("bugswarm paired-task source gate", paired_task_source_test.returncode, 0)
    if paired_task_source_test.returncode:
        checks.failures.append((paired_task_source_test.stdout + paired_task_source_test.stderr).strip())
    treatments_library_test = subprocess.run(
        [sys.executable, str(HERE / "test_treatments_library.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("arena treatments library gate", treatments_library_test.returncode, 0)
    if treatments_library_test.returncode:
        checks.failures.append((treatments_library_test.stdout + treatments_library_test.stderr).strip())
    paired_task_codeact_test = subprocess.run(
        [sys.executable, str(HERE / "test_paired_task_codeact.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("paired-task CodeAct driver gate", paired_task_codeact_test.returncode, 0)
    if paired_task_codeact_test.returncode:
        checks.failures.append((paired_task_codeact_test.stdout + paired_task_codeact_test.stderr).strip())
    cli_ux_test = subprocess.run(
        [sys.executable, str(HERE / "test_arena_cli_ux.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("arena CLI UX gate", cli_ux_test.returncode, 0)
    if cli_ux_test.returncode:
        checks.failures.append((cli_ux_test.stdout + cli_ux_test.stderr).strip())
    scored_receipt_test = subprocess.run(
        [sys.executable, str(HERE / "test_task_optimization_scored_receipt.py")],
        cwd=REPO_ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    checks.check("task optimization scored receipt gate", scored_receipt_test.returncode, 0)
    if scored_receipt_test.returncode:
        checks.failures.append((scored_receipt_test.stdout + scored_receipt_test.stderr).strip())
    report_test = subprocess.run(
        [sys.executable, str(HERE / "test_glm52_bugswarm_report.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("glm52 bugswarm report gate", report_test.returncode, 0)
    if report_test.returncode:
        checks.failures.append((report_test.stdout + report_test.stderr).strip())
    report_gate_test = subprocess.run(
        [sys.executable, str(HERE / "test_glm52_report_gate.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("glm52 report claim gate", report_gate_test.returncode, 0)
    if report_gate_test.returncode:
        checks.failures.append((report_gate_test.stdout + report_gate_test.stderr).strip())
    gap_plan_test = subprocess.run(
        [sys.executable, str(HERE / "test_glm52_gap_plan.py")],
        cwd=REPO_ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    checks.check("glm52 gap execution packet gate", gap_plan_test.returncode, 0)
    if gap_plan_test.returncode:
        checks.failures.append((gap_plan_test.stdout + gap_plan_test.stderr).strip())
    spec_path = ARENA_ROOT / "specs" / "paired-task-fixture.yaml"
    spec = JobSpec.load(spec_path)
    cells = spec.cells()

    checks.check("registered plugins", "paired-task" in plugins.known(), True)
    checks.check("cell count", len(cells), 6)
    checks.check("first cell id", cells[0].id, "cost-bench-fixture--kitsoki--task:api-routing")

    plugin = plugins.get("paired-task")
    argv = plugin.drive_command(cells[0], live=False)
    checks.check("no-LLM arm mode", argv[-1], "--arm-only")
    checks.check("runner path", argv[1], "/workspace/kitsoki/tools/arena/lib/paired_task_runner.py")
    live_argv = plugin.drive_command(cells[0], live=True)
    checks.check("live is explicit", "--live" in live_argv, True)
    checks.check("live threads model", "--model" in live_argv and "gpt-5.5" in live_argv, True)

    def responder(cell, host, argv):
        checks.check(f"{cell.id} no live spend", "--live" in argv, False)
        task = cell.axis["task"]
        payload = fixture_result(task, cell.variant.id)
        return ContainerRun(exit_code=0, stdout=json.dumps(payload), stderr="", host=host)

    backend = FakeBackend(responder)
    executor = CellExecutor(backend, mounts_for=lambda cell, host: {"/repo": "/workspace/kitsoki"})
    results = run_sweep(spec, executor, live=False)

    checks.check("result count", len(results), 6)
    checks.check("container calls == cells", len(backend.calls), 6)
    checks.check("all fake calls arm only", all("--arm-only" in call["argv"] for call in backend.calls), True)
    solved = [r for r in results if r.verdict == "solved"]
    failed = [r for r in results if r.verdict == "failed"]
    checks.check("solved cells", len(solved), 5)
    checks.check("failed cells", len(failed), 1)
    checks.check("cost metric parsed", results[0].metrics["cost_usd"], 0.42)
    checks.check("evidence refs parsed", results[0].evidence_refs, ["fixtures/api-routing/kitsoki.json"])

    with tempfile.TemporaryDirectory(prefix="arena-no-llm-") as td:
        paths = write_rollup(results, td)
        rollup = json.loads(Path(paths["rollup"]).read_text(encoding="utf-8"))
        checks.check("rollup cells", rollup["summary"]["n"], 6)
        checks.check("rollup variants", sorted(rollup["by_variant"]), ["kitsoki", "single-briefed", "single-naive"])
        checks.check("single-naive win-rate", rollup["by_variant"]["single-naive"]["win_rate"], 0.5)
        checks.check("kitsoki avg cost", rollup["by_variant"]["kitsoki"]["avg_cost_usd"], 0.42)
        checks.check("markdown report exists", Path(paths["summary"]).exists(), True)

    return finish(checks, "paired-task arena gate (enumerate -> arm -> score -> rollup, no LLM)")


def run_fixture(fixture_dir: Path) -> int:
    checks = Checks()
    meta_path = fixture_dir / "fixture.json"
    if not meta_path.exists():
        checks.failures.append(f"fixture metadata missing: {meta_path}")
        return finish(checks, "arena fixture replay")
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    spec_path = (fixture_dir / meta["spec"]).resolve()
    cells_path = fixture_dir / meta["cells"]
    payloads = json.loads(cells_path.read_text(encoding="utf-8"))
    spec = JobSpec.load(spec_path)
    cells = spec.cells()

    checks.check("fixture cell count", len(cells), meta.get("expected_cell_count"))
    checks.check("fixture payload count", len(payloads), len(cells))
    checks.check("registered paired-task plugin", "paired-task" in plugins.known(), True)

    def responder(cell, host, argv):
        checks.check(f"{cell.id} replay is no-live", "--live" in argv, False)
        payload = payloads.get(cell.id)
        if payload is None:
            return ContainerRun(exit_code=2, stdout="", stderr=f"missing fixture payload for {cell.id}", host=host)
        return ContainerRun(exit_code=int(payload.get("exit_code", 0)), stdout=json.dumps(payload["stdout"]), stderr=payload.get("stderr", ""), host=host)

    backend = FakeBackend(responder)
    executor = CellExecutor(backend, mounts_for=lambda cell, host: {"/repo": "/workspace/kitsoki"})
    results = run_sweep(spec, executor, live=False)

    checks.check("fixture result count", len(results), len(cells))
    checks.require("fixture has no infra failures", all(not r.health.startswith("infra:") for r in results))
    checks.require("every pilot cell has cost", all(isinstance(r.metrics.get("cost_usd"), (int, float)) for r in results))
    checks.require("every pilot cell has token count", all(isinstance(r.metrics.get("tokens"), int) for r in results))
    checks.require("every pilot cell has evidence", all(r.evidence_refs for r in results))
    checks.require("every pilot cell has trace ref", all(r.trace_ref for r in results))
    checks.require("pilot has at least one failed oracle verdict", any(r.verdict == "failed" for r in results))
    checks.require("pilot has solved oracle verdicts", any(r.verdict == "solved" for r in results))

    with tempfile.TemporaryDirectory(prefix="arena-pilot-replay-") as td:
        paths = write_rollup(results, td)
        rollup = json.loads(Path(paths["rollup"]).read_text(encoding="utf-8"))
        checks.check("rollup cells", rollup["summary"]["n"], meta.get("expected_cell_count"))
        checks.check("rollup variants", sorted(rollup["by_variant"]), sorted(meta.get("expected_variants", [])))
        checks.check("rollup targets", sorted(rollup["by_target"]), sorted(meta.get("expected_targets", [])))
        checks.check("rollup markdown exists", Path(paths["summary"]).exists(), True)

    return finish(checks, f"arena fixture replay {fixture_dir}")


def finish(checks: Checks, label: str) -> int:
    if checks.failures:
        print(f"FAIL: {label}")
        for failure in checks.failures:
            print(f"  - {failure}")
        return 1
    print(f"PASS: {label}")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", help="replay a committed arena result fixture directory")
    args = parser.parse_args(argv)
    if args.fixture:
        return run_fixture(Path(args.fixture))
    return run_default()


if __name__ == "__main__":
    raise SystemExit(main())
