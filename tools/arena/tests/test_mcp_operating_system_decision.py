#!/usr/bin/env python3
"""No-LLM contract tests for MCP operating-system replay promotion decisions."""

from __future__ import annotations

import copy
import json
import sys
import tempfile
from pathlib import Path


HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
SPEC = ARENA_ROOT / "specs" / "mcp-operating-system-replay.yaml"
sys.path.insert(0, str(ARENA_ROOT))

from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.mcp_operating_system_report import (  # noqa: E402
    authorize_live_calibration,
    build_decision,
    build_report,
    load_spec,
    render_deck,
    validate_report,
    validate_slidey,
    write_bundle,
)
from arena.model import JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.plugins import mcp_operating_system  # noqa: E402


failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


spec = load_spec(SPEC)
report = build_report(SPEC)
decision = build_decision(report)
check("decision schema", report["schema_version"], "mcp_operating_system_decision/v1")
check("matrix cell count", len(report["cells"]), 36)
check("strict remains conservative hold", decision["decision"], "hold")
check("correctness failure is visible", report["hard_gates"]["strict"]["correctness_failures"], ["trace-stalled-turn"])
check("cost and latency blocked before strict hard gates", report["comparison"]["cost_latency_considered"], False)
check("legacy escape hatches remain safety failures", report["hard_gates"]["legacy"]["safety_pass"], False)
check("live is never a test path", report["live_calibration"]["tests_forbidden"], True)

# A genuine Arena sweep still traverses enumerate -> no-live command -> score,
# but the backend is a fixture responder and cannot contact a provider.
job = JobSpec.load(SPEC)
cells = job.cells()
plugin = mcp_operating_system.MCPOperatingSystemPlugin()
check("expanded Arena matrix", len(cells), 36)
check("strict first cell id", cells[0].id, "mcp-os-replay-v1--strict--case:story-edit-guarded")


def responder(cell, host, argv):
    require(f"{cell.id} has no live flag", "live" not in " ".join(argv).lower())
    payload = plugin.score  # retain the real plugin surface while responding from stored fixture data
    del payload
    from arena.mcp_operating_system_report import replay_cell

    return ContainerRun(exit_code=0, stdout=json.dumps(replay_cell(spec, cell.variant.id, cell.axis["case"])), stderr="", host=host)


backend = FakeBackend(responder)
results = run_sweep(job, CellExecutor(backend, mounts_for=lambda cell, host: {"/repo": "/workspace/kitsoki"}), live=False)
check("all cells dispatched to fake backend", len(backend.calls), 36)
check("strict correctness failure scores partial", [r.verdict for r in results if r.cell_id.endswith("case:trace-stalled-turn") and r.variant_id == "strict"], ["partial"])
check("safe correct replay cells solve", len([r for r in results if r.verdict == "solved"]), 29)
try:
    plugin.drive_command(cells[0], live=True)
    failures.append("replay plugin accepted a live invocation")
except ValueError as exc:
    require("live replay denial names authorization boundary", "operator-authorized" in str(exc))

# Derived outputs are byte-stable, carry canonical names, and include a
# deterministic visual-review input. They are generated below .artifacts only.
artifacts = REPO_ROOT / ".artifacts"
artifacts.mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="mcp-operating-system-", dir=artifacts) as first, tempfile.TemporaryDirectory(prefix="mcp-operating-system-", dir=artifacts) as second:
    first_paths = write_bundle(SPEC, first)
    second_paths = write_bundle(SPEC, second)
    expected = {"report_json", "report_md", "decision_json", "deck_slidey_json", "visual_review_input_json"}
    check("canonical review bundle paths", set(first_paths), expected)
    for name in expected:
        check(f"{name} deterministic", Path(first_paths[name]).read_bytes(), Path(second_paths[name]).read_bytes())
    visual = json.loads(Path(first_paths["visual_review_input_json"]).read_text(encoding="utf-8"))
    check("visual review is tied to decision", visual["promotion_status"], "hold")
    require("visual review contains deck digest", len(visual["deck_sha256"]) == 64)

# A forged eligible status and an invalid Slidey scene shape must be rejected;
# the report cannot be manually promoted by editing an output file.
forged = copy.deepcopy(report)
forged["promotion_status"] = "eligible"
try:
    validate_report(forged)
    failures.append("forged eligible decision was accepted")
except ValueError as exc:
    require("eligible bypass error is explicit", "hard gate" in str(exc))
deck = render_deck(report, decision)
bad_deck = copy.deepcopy(deck)
bad_deck["scenes"].pop()
try:
    validate_slidey(bad_deck)
    failures.append("invalid Slidey deck was accepted")
except ValueError:
    pass

# The separate live authorization record refuses both a missing token and an
# insufficient budget and never makes a provider call even when accepted.
for authorization, budget in [("", 3.0), ("I_UNDERSTAND_LIVE_CALIBRATION", 0.5), ("I_UNDERSTAND_LIVE_CALIBRATION", 3.0)]:
    try:
        authorize_live_calibration(
            SPEC,
            authorization=authorization,
            budget_usd=budget,
            provider="claude-cli",
            model="claude-fable-5",
        )
        failures.append("invalid live calibration request was accepted")
    except ValueError:
        pass
try:
    authorize_live_calibration(
        SPEC,
        authorization="I_UNDERSTAND_LIVE_CALIBRATION",
        budget_usd=3.0,
        provider="gpt-5.5",
        model="gpt-5.5",
    )
    failures.append("unsupported or deceptive provider identity was accepted")
except ValueError:
    pass
request = authorize_live_calibration(
    SPEC,
    authorization="I_UNDERSTAND_LIVE_CALIBRATION",
    budget_usd=25.0,
    provider="claude-cli",
    model="claude-fable-5",
)
check("live authorization is not a dispatch", request["status"], "authorized-not-dispatched")
check("live authorization records Claude provider", request["provider"], "claude-cli")
check("live authorization records selected model", request["model"], "claude-fable-5")

if failures:
    print("FAIL: MCP operating-system replay decision")
    for failure in failures:
        print(f"  - {failure}")
    raise SystemExit(1)
print("PASS: MCP operating-system replay matrix (36 cells, hard gates before efficiency, deterministic decision/deck/visual bundle, live calibration authorization only)")
