#!/usr/bin/env python3
"""WS-G G1 — check-type plugin architecture (schema + arena plumbing), no LLM/docker.

Covers:

  1. Schema: the new optional `check_type` discriminator on
     schemas/completion-state.schema.json — present-and-valid, absent
     (backward compatible: every pre-1.1.0 payload stays valid), and a bad
     value rejected. (jsonschema-enforced when installed; advisory otherwise,
     matching test_completion_state.py.)
  2. Spec parsing: default check suite == [replay]; `checks:` accepts bare
     strings and mappings; an unknown check_type and a duplicated check_type
     are rejected at load time.
  3. Aggregation: a multi-check cell yields one CellResult PER check_type —
     `replay` graded from the (fake) container run, the three declared-but-
     unimplemented types reporting honest PENDING, with exactly one container
     call per cell (unimplemented checks never spend).
  4. Back-compat: CellResult.to_dict() omits the default `check_type` so the
     golden rollup bytes and pre-check-suite consumers are unchanged; the
     rollup cells/ dir gets one file per cell per check_type.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE.parent))

from arena.checks import run_cell_checks, unimplemented_check_result  # noqa: E402
from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.model import CHECK_TYPES, CheckSpec, JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.rollup import build_rollup, write_rollup  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402

SCHEMA_PATH = ROOT / "schemas" / "completion-state.schema.json"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


# ---------------------------------------------------------------------------
# 1. Schema: check_type present / absent / bad value.
# ---------------------------------------------------------------------------
schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))

check("schema declares check_type", "check_type" in schema["properties"], True)
check("schema check_type enum", schema["properties"]["check_type"]["enum"], list(CHECK_TYPES))
check("check_type stays OPTIONAL (backward compatible)",
      "check_type" in schema.get("required", []), False)

try:
    import jsonschema

    validator = jsonschema.Draft7Validator(schema)
except ImportError:
    jsonschema = None
    validator = None

BASE_PAYLOAD = {
    "schema_version": "1.1.0",
    "verdict": "solved",
    "health": "model:result",
    "metrics": {},
    "evidence_refs": [],
}

if validator is not None:
    def n_errors(instance: dict) -> int:
        return len(list(validator.iter_errors(instance)))

    check("absent check_type conforms (pre-1.1.0 payloads stay valid)",
          n_errors(BASE_PAYLOAD), 0)
    for ct in CHECK_TYPES:
        check(f"check_type={ct!r} conforms", n_errors({**BASE_PAYLOAD, "check_type": ct}), 0)
    check_true("bad check_type rejected",
               n_errors({**BASE_PAYLOAD, "check_type": "vibes"}) > 0)
    check_true("non-string check_type rejected",
               n_errors({**BASE_PAYLOAD, "check_type": 7}) > 0)
else:
    print("NOTE: jsonschema not installed — schema-conformance checks were skipped, "
          "not failed (pip install jsonschema to enforce them).")


# ---------------------------------------------------------------------------
# 2. Spec parsing: default suite, string/mapping entries, bad values rejected.
# ---------------------------------------------------------------------------
BASE_SPEC = {
    "job_type": "bugfix",
    "targets": [{"id": "query-string", "label": "qs", "stack": "javascript"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {"bug": ["qs1", "qs2"]},
    "placement": {"hosts": ["local"], "concurrency": 2, "retry": 0},
}

default_spec = JobSpec.from_dict(BASE_SPEC)
check("default check suite is [replay]",
      [c.check_type for c in default_spec.checks], ["replay"])

multi_spec = JobSpec.from_dict({
    **BASE_SPEC,
    "checks": [
        "replay",
        {"check_type": "docs-fidelity", "docs": "docs/project-onboarding.md"},
        {"type": "ux-heuristic"},
        {"check_type": "journey-verdict", "options": {"floor": 2}},
    ],
})
check("multi-check suite parsed",
      [c.check_type for c in multi_spec.checks],
      ["replay", "docs-fidelity", "ux-heuristic", "journey-verdict"])
check("mapping extras fold into options",
      multi_spec.checks[1].options, {"docs": "docs/project-onboarding.md"})
check("explicit options mapping honored", multi_spec.checks[3].options, {"floor": 2})

try:
    JobSpec.from_dict({**BASE_SPEC, "checks": ["replay", "vibes"]})
    failures.append("unknown check_type accepted: expected ValueError")
except ValueError:
    pass

try:
    CheckSpec(check_type="vibes")
    failures.append("CheckSpec accepted unknown check_type: expected ValueError")
except ValueError:
    pass

try:
    JobSpec.from_dict({**BASE_SPEC, "checks": ["replay", "replay"]})
    failures.append("duplicate check_type accepted: expected ValueError")
except ValueError:
    pass


# ---------------------------------------------------------------------------
# 3. Aggregation: multi-check sweep → per-cell verdict per check_type;
#    unimplemented types report honest PENDING and never touch a container.
# ---------------------------------------------------------------------------
bugfix = plugins.get("bugfix")
state_paths: list[Path] = []


def write_completion_state(cell, *, verdict, health):
    path = Path(bugfix.completion_state_path(cell))
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({
        "schema_version": "1.1.0",
        "verdict": verdict,
        "health": health,
        "metrics": {},
        "evidence_refs": [],
    }))
    state_paths.append(path)


def responder(cell, host, argv):
    verdict = "failed" if cell.axis["bug"] == "qs2" else "armed"
    write_completion_state(cell, verdict=verdict, health="model:result")
    return ContainerRun(exit_code=0, stdout="", stderr="", host=host)


try:
    backend = FakeBackend(responder)
    executor = CellExecutor(backend, mounts_for=lambda c, h: {})
    results = run_sweep(multi_spec, executor, live=False)

    # 2 cells × 4 checks = 8 results; only the 2 replay checks ran a container.
    check("results = cells × checks", len(results), 8)
    check("one container call per cell (replay only)", len(backend.calls), 2)

    by_cell: dict[str, dict[str, object]] = {}
    for r in results:
        by_cell.setdefault(r.cell_id, {})[r.check_type] = r
    check("two cells aggregated", len(by_cell), 2)
    for cell_id, per_type in by_cell.items():
        check(f"{cell_id}: one verdict per check_type",
              sorted(per_type), sorted(CHECK_TYPES))
        for ct in ("docs-fidelity", "ux-heuristic", "journey-verdict"):
            r = per_type[ct]
            check(f"{cell_id}/{ct}: honest PENDING", r.verdict, "pending")
            check(f"{cell_id}/{ct}: health incomplete (never scored)", r.health, "incomplete")
        # docs-fidelity is declared-but-not-implemented at all (no runner exists
        # yet); journey-verdict/ux-heuristic (WS-G G6) ARE implemented as a
        # file-adapter check, but this bugfix-shaped spec never configures a
        # `verdict_path`, so they honestly PENDING for a different reason — no
        # verdict.json to grade, not "unimplemented".
        check_true("docs-fidelity: notes say unimplemented",
                   "not implemented" in per_type["docs-fidelity"].notes, per_type["docs-fidelity"].notes)
        for ct in ("ux-heuristic", "journey-verdict"):
            check_true(f"{ct}: notes say no verdict_path configured",
                       "no verdict_path configured" in per_type[ct].notes, per_type[ct].notes)
    check("replay verdict graded (qs1 armed)",
          by_cell["query-string--kitsoki-gpt-5.5--bug:qs1"]["replay"].verdict, "armed")
    check("replay verdict graded (qs2 failed)",
          by_cell["query-string--kitsoki-gpt-5.5--bug:qs2"]["replay"].verdict, "failed")

    # Rollup: pending checks are counted but never boost the win-rate
    # (2 armed... no: 1 armed + 1 failed + 6 pending → 1 win / 8).
    rollup = build_rollup(results)
    check("rollup n", rollup["summary"]["n"], 8)
    check("rollup win_rate excludes pending wins", rollup["summary"]["win_rate"], 0.125)
    check("rollup verdict counts", rollup["summary"]["verdicts"],
          {"armed": 1, "failed": 1, "pending": 6})

    # 4. Back-compat + per-check cell files.
    replay_dict = by_cell["query-string--kitsoki-gpt-5.5--bug:qs1"]["replay"].to_dict()
    check("to_dict omits default check_type (byte-compat)",
          "check_type" in replay_dict, False)
    docs_dict = by_cell["query-string--kitsoki-gpt-5.5--bug:qs1"]["docs-fidelity"].to_dict()
    check("to_dict keeps non-default check_type", docs_dict.get("check_type"), "docs-fidelity")
    if validator is not None:
        payload = {**BASE_PAYLOAD, **{k: docs_dict[k] for k in
                                      ("verdict", "health", "metrics", "evidence_refs", "check_type")}}
        check("pending check payload conforms to schema",
              len(list(validator.iter_errors(payload))), 0)

    with tempfile.TemporaryDirectory(prefix="arena-check-types-") as td:
        write_rollup(results, td)
        cell_files = sorted(p.name for p in (Path(td) / "cells").glob("*.json"))
        check("one cell file per cell per check_type", len(cell_files), 8)
        check_true("replay file keeps historical name",
                   "query-string--kitsoki-gpt-5.5--bug-qs1.json" in cell_files, str(cell_files))
        check_true("non-replay file carries --check-<type> suffix",
                   "query-string--kitsoki-gpt-5.5--bug-qs1--check-docs-fidelity.json" in cell_files,
                   str(cell_files))
finally:
    for p in state_paths:
        p.unlink(missing_ok=True)


# ---------------------------------------------------------------------------
# 3b. run_cell_checks (single-cell composition) mirrors the sweep behavior.
# ---------------------------------------------------------------------------
cells = multi_spec.cells()
try:
    backend2 = FakeBackend(responder)
    executor2 = CellExecutor(backend2, mounts_for=lambda c, h: {})
    per_cell = run_cell_checks(cells[0], executor2, multi_spec.checks)
    check("run_cell_checks: one result per check", len(per_cell), 4)
    check("run_cell_checks: replay tagged", per_cell[0].check_type, "replay")
    check("run_cell_checks: pending tail",
          [r.verdict for r in per_cell[1:]], ["pending"] * 3)
finally:
    for p in state_paths:
        p.unlink(missing_ok=True)

pend = unimplemented_check_result(cells[0], "docs-fidelity")
check("unimplemented_check_result carries cell coords", pend.cell_id, cells[0].id)
check("unimplemented_check_result axis copied", pend.axis, {"bug": "qs1"})


if failures:
    print("FAIL: check-type architecture")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: check-type architecture (schema discriminator + multi-check aggregation, "
      "unimplemented types honest-PENDING, no LLM/docker)")
