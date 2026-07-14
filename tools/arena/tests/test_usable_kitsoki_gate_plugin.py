#!/usr/bin/env python3
"""No-LLM, no-docker test for the usable-kitsoki-gate arena plugin (Task 2).

Covers: plugin registration alongside bugfix/persona-qa/swarm, image()
selection (browser image for web/tui, non-browser for mcp, plus
target/variant meta override), drive_command argv/env composition per
surface (web dispatches into the swarm-harness-style Playwright spec; tui/mcp
dispatch into their own stub runner scripts, each with GATE_* env threaded
through), an empty scenario corpus (no targets/variants/axes at all) yielding
ZERO CELLS rather than an error, and score() reading a REAL on-disk parity
records bundle via the stdout `[usable-kitsoki-gate] wrote <path>` pointer --
solved/failed/schema-violation/infra cases. Never launches docker or
Playwright.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.model import JobSpec  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402
from arena.plugins import usable_kitsoki_gate_constants as gate_constants  # noqa: E402
from arena.plugins.usable_kitsoki_gate import KITSOKI_MNT, REPO_ROOT  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


# ---- 1. Registration --------------------------------------------------------

check("usable-kitsoki-gate is registered", "usable-kitsoki-gate" in plugins.known(), True)
check("bugfix still registered alongside it", "bugfix" in plugins.known(), True)
check("persona-qa still registered alongside it", "persona-qa" in plugins.known(), True)
check("swarm still registered alongside it", "swarm" in plugins.known(), True)
plugin = plugins.get("usable-kitsoki-gate")


def make_cell(surface: str, *, persona: str = "core-maintainer", extra_axes: dict | None = None, target_meta=None, variant_meta=None):
    axes = {"surface": [surface], "persona": [persona]}
    if extra_axes:
        axes.update(extra_axes)
    spec = JobSpec.from_dict({
        "job_type": "usable-kitsoki-gate",
        "targets": [{"id": "kitsoki", "label": "kitsoki workbench", **(target_meta or {})}],
        "variants": [{"id": "gate-v1", "backend": "replay", **(variant_meta or {})}],
        "axes": axes,
        "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
    })
    return spec.cells()[0]


# ---- 2. image() selection ---------------------------------------------------

web_cell = make_cell("web")
check("web surface gets the browser image", plugin.image(web_cell), "kitsoki-arena-repo-runtime-browser:latest")

tui_cell = make_cell("tui")
check("tui surface gets the browser image", plugin.image(tui_cell), "kitsoki-arena-repo-runtime-browser:latest")

mcp_cell = make_cell("mcp")
check("mcp surface gets the non-browser image", plugin.image(mcp_cell), "kitsoki-arena-repo-runtime:latest")

override_cell = make_cell("mcp", target_meta={"image": "kitsoki-arena-repo-runtime:pinned"})
check("target.meta.image overrides the default", plugin.image(override_cell), "kitsoki-arena-repo-runtime:pinned")

override_variant_cell = make_cell("web", variant_meta={"image": "kitsoki-arena-repo-runtime-browser:pinned"})
check("variant.meta.image overrides the default", plugin.image(override_variant_cell), "kitsoki-arena-repo-runtime-browser:pinned")

# ---- 3. drive_command argv/env composition (per surface) -------------------

web_argv = plugin.drive_command(web_cell, live=False)
check("drive_command shells out via bash -lc", web_argv[0:2], ["bash", "-lc"])
web_script = web_argv[2]
check("web: cd's into the runstatus dir inside the container", f"cd {KITSOKI_MNT}/tools/runstatus" in web_script, True)
check("web: runs the usable-kitsoki-gate web spec", "tests/playwright/usable-kitsoki-gate-web.spec.ts" in web_script, True)
check("web: runs it via npx playwright test", "npx playwright test" in web_script, True)
check("web: GATE_SURFACE=web", "GATE_SURFACE=web" in web_script, True)
check("web: GATE_PERSONA carries the persona axis", "GATE_PERSONA=core-maintainer" in web_script, True)
check("web: GATE_RESULTS_PATH points under .artifacts/usable-kitsoki-gate", ".artifacts/usable-kitsoki-gate" in web_script, True)
check("web: GATE_SCENARIO_CORPUS defaults when no axis given", "GATE_SCENARIO_CORPUS=" in web_script, True)

tui_argv = plugin.drive_command(tui_cell, live=False)
tui_script = tui_argv[2]
check("tui: cd's into the kitsoki mount root", f"cd {KITSOKI_MNT}" in tui_script, True)
check("tui: runs the tui gate runner", "tools/usable-kitsoki-gate/run_tui_gate.py" in tui_script, True)
check("tui: GATE_SURFACE=tui", "GATE_SURFACE=tui" in tui_script, True)
check("tui: does not run playwright", "npx playwright" in tui_script, False)

mcp_argv = plugin.drive_command(mcp_cell, live=False)
mcp_script = mcp_argv[2]
check("mcp: runs the mcp gate runner", "tools/usable-kitsoki-gate/run_mcp_gate.py" in mcp_script, True)
check("mcp: GATE_SURFACE=mcp", "GATE_SURFACE=mcp" in mcp_script, True)

live_argv = plugin.drive_command(web_cell, live=True)
check("live composes via bash -lc same as non-live", live_argv[0:2], ["bash", "-lc"])
live_script = live_argv[2]
check("live: still cd's into the kitsoki mount root", f"cd {KITSOKI_MNT}" in live_script, True)
check("live: dispatches into run_live_gate.py", "tools/usable-kitsoki-gate/run_live_gate.py" in live_script, True)
check("live: passes --live-gate", "run_live_gate.py --live-gate" in live_script, True)
check("live: still threads GATE_SURFACE=web", "GATE_SURFACE=web" in live_script, True)
check("live: still threads GATE_PERSONA", "GATE_PERSONA=core-maintainer" in live_script, True)
check("live: does NOT run playwright (surface-agnostic live runner)", "npx playwright" in live_script, False)
check("live != non-live command (they now genuinely differ)", live_argv == web_argv, False)

scenario_corpus_cell = make_cell("web", extra_axes={"scenario_corpus": ["docs/proposals/pinned-corpus.json"], "run_id": ["run-42"]})
scenario_script = plugin.drive_command(scenario_corpus_cell, live=False)[2]
check("explicit scenario_corpus axis threads through", "GATE_SCENARIO_CORPUS=docs/proposals/pinned-corpus.json" in scenario_script, True)
check("explicit run_id axis threads through", "GATE_RUN_ID=run-42" in scenario_script, True)
check("results path uses the explicit run_id", ".artifacts/usable-kitsoki-gate/run-42/parity-records.json" in scenario_script, True)

no_persona_spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [{"id": "kitsoki", "label": "kitsoki workbench"}],
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {"surface": ["web"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
no_persona_cell = no_persona_spec.cells()[0]
no_persona_script = plugin.drive_command(no_persona_cell, live=False)[2]
check("no persona axis omits GATE_PERSONA entirely", "GATE_PERSONA=" in no_persona_script, False)

no_surface_axis_spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [{"id": "kitsoki", "label": "kitsoki workbench"}],
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
default_surface_cell = no_surface_axis_spec.cells()[0]
default_surface_script = plugin.drive_command(default_surface_cell, live=False)[2]
check("falls back to the web default with no surface axis at all", "GATE_SURFACE=web" in default_surface_script, True)

# ---- 4. empty scenario corpus -> ZERO cells, not an error -------------------

empty_spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [],
    "variants": [],
    "axes": {},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
check("empty targets/variants/axes yields zero cells, not an error", empty_spec.cells(), [])

empty_axes_only_spec = JobSpec.from_dict({
    "job_type": "usable-kitsoki-gate",
    "targets": [{"id": "kitsoki", "label": "kitsoki workbench"}],
    "variants": [{"id": "gate-v1", "backend": "replay"}],
    "axes": {"surface": [], "persona": []},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
check(
    "an axis present but with an empty value list also yields zero cells",
    empty_axes_only_spec.cells(),
    [],
)

# ---- 5. score() reads a REAL parity-records bundle via the stdout pointer --

with tempfile.TemporaryDirectory() as tmp:
    results_dir = Path(tmp) / "usable-kitsoki-gate"
    results_dir.mkdir()
    results_path = results_dir / "parity-records.json"

    def make_record(**overrides):
        record = {
            "schema_version": "1.0.0",
            "scenario_id": "scn-git-ops-0007",
            "persona": "core-maintainer",
            "surface": "web",
            "source_completed": True,
            "candidate_completed": True,
            "silent_bounce": False,
            "misroute_adjacent": False,
            "evidence_refs": [".artifacts/usable-kitsoki-gate/run-1/scn-git-ops-0007/trace.jsonl"],
            "notes": "",
        }
        record.update(overrides)
        return record

    def write_records(records):
        results_path.write_text(json.dumps({"run_id": "run-1", "records": records}), encoding="utf-8")

    stdout_ok = f"[usable-kitsoki-gate] wrote {results_path} (1 record)\n"

    # All clean -> solved.
    write_records([make_record()])
    solved = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("all-clean bundle verdict is solved", solved.verdict, "solved")
    check("all-clean bundle health is model:result", solved.health, "model:result")
    check("metrics carry record_count", solved.metrics["record_count"], 1)
    check("metrics carry parity_percent", solved.metrics["parity_percent"], 100.0)
    check("trace_ref is the results path", solved.trace_ref, str(results_path))
    check_true("evidence_refs include the results path", str(results_path) in solved.evidence_refs)

    # A silent bounce fails the cell even with perfect parity otherwise.
    write_records([make_record(), make_record(scenario_id="scn-git-ops-0008", silent_bounce=True)])
    bounced = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("a silent bounce fails the cell", bounced.verdict, "failed")
    check("silent_bounce_count is reflected in metrics", bounced.metrics["silent_bounce_count"], 1)

    # A misroute-adjacent record fails the cell.
    write_records([make_record(), make_record(scenario_id="scn-git-ops-0009", misroute_adjacent=True)])
    misrouted = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("a misroute-adjacent record fails the cell", misrouted.verdict, "failed")

    # Parity below threshold fails even with zero bounce/misroute.
    below_threshold_records = [make_record(scenario_id=f"scn-{i}", candidate_completed=(i < 8)) for i in range(10)]
    write_records(below_threshold_records)
    below_threshold = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("parity below the 90% threshold fails the cell", below_threshold.verdict, "failed")
    check("parity_percent reflects 8/10 = 80.0", below_threshold.metrics["parity_percent"], 80.0)

    # Parity exactly at the threshold passes (>=, not >).
    at_threshold_records = [make_record(scenario_id=f"scn-{i}", candidate_completed=(i < 9)) for i in range(10)]
    write_records(at_threshold_records)
    at_threshold = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("parity exactly at the 90% threshold solves the cell", at_threshold.verdict, "solved")

    # Empty records list: empty-denominator convention -> solved (nothing to
    # regress on), not silently failed.
    write_records([])
    empty_records = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("zero records is solved (empty-denominator convention), not failed", empty_records.verdict, "solved")
    check("zero records reports zero record_count", empty_records.metrics["record_count"], 0)

    # A record that violates the schema (missing required field) blocks the
    # cell rather than silently scoring it.
    bad_record = make_record()
    del bad_record["evidence_refs"]
    write_records([bad_record])
    schema_violation = plugin.score(web_cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("a schema-violating record blocks the cell", schema_violation.verdict, "blocked")
    check("a schema-violating record reports infra:results-malformed", schema_violation.health, "infra:results-malformed")

# score() with no results-path pointer at all is an infra failure, not a guessed verdict.
missing = plugin.score(web_cell, exit_code=0, stdout="not the expected line", stderr="")
check("missing results pointer is infra", missing.health, "infra:missing-results-path")
check("missing results pointer is blocked, not a model verdict", missing.verdict, "blocked")

crash = plugin.score(web_cell, exit_code=1, stdout="", stderr="Traceback (most recent call last):\n  boom")
check("crash before any results file is infra:harness", crash.health, "infra:harness")

# ---- 6. host<->container path mapping --------------------------------------

with tempfile.TemporaryDirectory():
    rel = Path(".artifacts") / "usable-kitsoki-gate" / "mapping-test" / "parity-records.json"
    host_path = REPO_ROOT / rel
    host_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        host_path.write_text(json.dumps({
            "run_id": "mapping-test",
            "records": [
                {
                    "schema_version": "1.0.0",
                    "scenario_id": "scn-git-ops-0007",
                    "persona": "core-maintainer",
                    "surface": "web",
                    "source_completed": True,
                    "candidate_completed": True,
                    "silent_bounce": False,
                    "misroute_adjacent": False,
                    "evidence_refs": ["x"],
                }
            ],
        }), encoding="utf-8")
        container_stdout = f"[usable-kitsoki-gate] wrote {KITSOKI_MNT}/{rel} (1 record)\n"
        mapped = plugin.score(web_cell, exit_code=0, stdout=container_stdout, stderr="")
        check("container path maps back to the real host path", mapped.trace_ref, str(host_path))
        check("mapped read scores solved", mapped.verdict, "solved")
    finally:
        host_path.unlink(missing_ok=True)

# ---- 7. gate constants sanity re-check (belt + suspenders on the boundary) --

check(
    "gate_passes matches the plugin's own boundary behavior",
    gate_constants.gate_passes(silent_bounce_count=0, misroute_adjacent_count=0, worst_surface_parity_percent=90.0),
    True,
)

if failures:
    print("FAIL: usable-kitsoki-gate arena plugin")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: usable-kitsoki-gate arena plugin (registration, image selection, per-surface argv/env composition, empty-corpus, parity-records scoring)")
