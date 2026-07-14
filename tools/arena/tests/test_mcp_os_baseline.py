#!/usr/bin/env python3
"""Golden no-LLM contract tests for the MCP operating-system baseline."""

from __future__ import annotations

import copy
import json
import shutil
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
CORPUS = ARENA_ROOT / "corpus" / "mcp-os"
GOLDEN = CORPUS / "review"
sys.path.insert(0, str(ARENA_ROOT))

from arena.mcp_os_report import (  # noqa: E402
    build_report,
    load_inputs,
    validate_candidate_report,
    validate_inputs,
    write_bundle,
)
from arena.model import JobSpec  # noqa: E402
from arena.plugins import mcp_os  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


report = build_report(CORPUS)
check("report schema", report["schema_version"], "mcp_os_baseline_report/v1")
check("baseline treatment", report["treatment"]["id"], "current-toolbox")
check("baseline policy forbids provider calls", report["policy"]["provider_calls"], "forbidden")
check("twelve cells", report["summary"]["case_count"], 12)
check("promotion is held", report["summary"]["promotion_status"], "hold")
check("all required corpus kinds are represented", sorted(report["summary"]["by_kind"]), [
    "documentation_change", "managed_workspace_mutation", "runtime_fix", "story_edit", "trace_diagnosis",
])
check("known current-toolbox escapes remain visible", report["summary"]["unsafe_observed_count"], 2)
require("every baseline cell has replay evidence", all(cell["evidence_ref"].startswith("replay://") for cell in report["cells"]))

# Regeneration writes a temporary review bundle only below .artifacts and must
# remain byte-for-byte equal to checked-in source fixtures.
artifacts = REPO_ROOT / ".artifacts"
artifacts.mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="mcp-os-baseline-", dir=artifacts) as tmp:
    paths = write_bundle(CORPUS, tmp)
    for name, golden_name in {
        "report_json": "report.json",
        "report_md": "report.md",
        "deck_slidey_json": "deck.slidey.json",
    }.items():
        generated = Path(paths[name]).read_bytes()
        golden = (GOLDEN / golden_name).read_bytes()
        check(f"{golden_name} deterministic golden", generated, golden)
    deck = json.loads(Path(paths["deck_slidey_json"]).read_text(encoding="utf-8"))
    check("deck uses Slidey scenes", isinstance(deck.get("scenes"), list), True)
    check("deck identifies baseline", deck["meta"]["title"], "MCP operating-system baseline")

# A stale source hash, incomplete cells, an unknown cell, or an unsafe
# promotion candidate must all fail instead of being silently normalized.
with tempfile.TemporaryDirectory(prefix="mcp-os-stale-", dir=artifacts) as tmp:
    copied = Path(tmp) / "corpus"
    shutil.copytree(CORPUS, copied)
    corpus, treatment, policy, cells = load_inputs(copied)
    corpus["label"] = "mutated after fixture capture"
    try:
        validate_inputs(corpus, treatment, policy, cells)
        failures.append("stale source hash was accepted")
    except ValueError as exc:
        require("stale fixture error is explicit", "stale replay fixture" in str(exc))

    corpus, treatment, policy, cells = load_inputs(CORPUS)
    incomplete = copy.deepcopy(cells)
    incomplete["cells"].pop()
    try:
        validate_inputs(corpus, treatment, policy, incomplete)
        failures.append("incomplete replay cells were accepted")
    except ValueError:
        pass
    inconsistent = copy.deepcopy(cells)
    inconsistent["cells"][0]["case_id"] = "does-not-exist"
    try:
        validate_inputs(corpus, treatment, policy, inconsistent)
        failures.append("unknown replay cell was accepted")
    except ValueError:
        pass

unsafe_candidate = copy.deepcopy(report)
unsafe_candidate["summary"]["promotion_status"] = "eligible"
try:
    validate_candidate_report(unsafe_candidate)
    failures.append("unsafe promotion candidate was accepted")
except ValueError as exc:
    require("unsafe candidate error is explicit", "unsafe" in str(exc))

# The plugin is deliberately replay-only; no command shape can opt into a live
# provider invocation in this baseline slice.
plugin = mcp_os.MCPOSPlugin()
spec = JobSpec.from_dict({
    "job_type": "mcp-os-baseline",
    "targets": [{"id": "kitsoki", "corpus": "tools/arena/corpus/mcp-os"}],
    "variants": [{"id": "current-toolbox", "backend": "replay"}],
    "axes": {"case": ["story-edit-guarded"]},
})
cell = spec.cells()[0]
argv = plugin.drive_command(cell, live=False)
check("plugin uses replay runner", argv[2], "replay")
check("plugin never includes live flag", "--live" in argv, False)
try:
    plugin.drive_command(cell, live=True)
    failures.append("plugin accepted live evaluation")
except ValueError:
    pass
replayed_cell = next(item for item in report["cells"] if item["case_id"] == "story-edit-guarded")
scored = plugin.score(cell, exit_code=0, stdout=json.dumps(replayed_cell), stderr="")
check("safe replay scores solved", scored.verdict, "solved")
check("replay evidence stays attached", scored.evidence_refs, ["replay://story-edit-guarded"])

if failures:
    print("FAIL: MCP OS baseline")
    for failure in failures:
        print(f"  - {failure}")
    raise SystemExit(1)
print("PASS: MCP OS baseline (12-case replay corpus, deterministic report/deck, stale/incomplete/unsafe golden guards, no live provider path)")
