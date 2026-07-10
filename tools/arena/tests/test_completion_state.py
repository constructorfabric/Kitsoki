#!/usr/bin/env python3
"""Deterministic tests for the unified arena/persona-QA completion-state contract.

Covers (no docker, no LLM — FakeBackend/fixtures only, per AGENTS.md):

  1. schema conformance BOTH directions: a persona-qa CompletionState and a
     bugfix-shaped completion-state dict (as bench.py's write_completion_state
     emits) both validate against schemas/completion-state.schema.json.
  2. the 19-check review-gate mapping: ready->solved, needs_evidence->partial,
     blocker(s)->blocked/failed, harness signals->blocked/infra:*.
  3. malformed-file rejection: the arena bugfix plugin's score() must not crash
     on a missing/invalid/incomplete completion-state file — it must report a
     clear infra:* health instead of guessing a model verdict.
"""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "tools" / "arena"))

from tools.persona_qa import from_product_journey_report  # noqa: E402
from arena.artifact_adapters import adapt_artifact  # noqa: E402
from arena.model import Cell, Target, Variant  # noqa: E402
from arena.plugins.bugfix import BugfixPlugin  # noqa: E402

SCHEMA_PATH = ROOT / "schemas" / "completion-state.schema.json"
BENCH_PATH = ROOT / "tools" / "bugfix-bakeoff" / "external" / "bench.py"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def check_true(label: str, cond: bool, detail: str = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


# ---- load bench.py's write_completion_state without a hyphen-safe import path ----
spec = importlib.util.spec_from_file_location("bakeoff_bench", BENCH_PATH)
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)  # type: ignore[union-attr]


# ---------------------------------------------------------------------------
# 1. Schema conformance, both directions.
# ---------------------------------------------------------------------------
schema = json.loads(SCHEMA_PATH.read_text())

try:
    import jsonschema

    validator = jsonschema.Draft7Validator(schema)
except ImportError:
    jsonschema = None
    validator = None


def assert_conforms(label: str, instance: dict) -> None:
    if validator is None:
        return  # jsonschema not installed in this environment; skip (advisory).
    errors = sorted(validator.iter_errors(instance), key=lambda e: e.path)
    if errors:
        failures.append(f"{label}: schema violations: {[e.message for e in errors]}")


# 1a. persona-qa direction.
ready_state = from_product_journey_report({
    "review_status": "ready",
    "validation_status": "valid",
    "passed": 19, "warnings": 0, "failed": 0, "total": 19,
    "run_dir": ".artifacts/product-journey/ready",
})
assert_conforms("persona-qa 'ready' CompletionState conforms", ready_state.to_dict())

# 1b. bugfix direction — the exact shape bench.py's write_completion_state emits.
with tempfile.TemporaryDirectory(prefix="arena-completion-state-") as td:
    bugfix_tmp = Path(td) / "completion-state-schema-check.json"
    payload = bench.write_completion_state(
        bugfix_tmp,
        verdict="armed",
        health="model:result",
        metrics={"cost_usd": 0.0, "total_tokens": None},
        evidence_refs=["trace.jsonl"],
        cell_id="query-string-qs1-kitsoki",
        job_type="bugfix",
        target_id="query-string",
        variant_id="kitsoki",
        axis={"bug": "qs1"},
        notes="RED@baseline/GREEN@fix arming proof",
    )
    assert_conforms("bugfix completion-state (write_completion_state output) conforms", payload)
    assert_conforms("bugfix completion-state (file round-trip) conforms",
                    json.loads(bugfix_tmp.read_text()))

if jsonschema is None:
    print("NOTE: jsonschema not installed — schema-conformance checks were skipped, "
          "not failed (pip install jsonschema to enforce them).")


# 1c. Artifact adapter registry: every supported kind normalizes to the same
# completion-state schema shape.
with tempfile.TemporaryDirectory(prefix="arena-completion-adapters-") as td:
    adapter_root = Path(td)
    completion_path = adapter_root / "completion.json"
    bench.write_completion_state(
        completion_path,
        verdict="solved",
        health="model:result",
        metrics={"cost_usd": 0.0},
        evidence_refs=["trace.jsonl"],
    )
    assert_conforms(
        "adapter completion-state conforms",
        adapt_artifact("completion-state", completion_path),
    )

    swarm_path = adapter_root / "swarm-results.json"
    swarm_path.write_text(json.dumps({
        "run_id": "swarm-1",
        "started_at": "",
        "ended_at": "",
        "server": {"addr": "", "flow": ""},
        "user_count": 1,
        "users": [{
            "index": 0,
            "persona_id": "p",
            "session_id": "s",
            "marker": "m",
            "completed": True,
            "states_visited": [],
            "console_errors": 0,
            "console_error_samples": [],
            "audit_error_count": 0,
            "audit_error_samples": [],
            "audit_a11y_advisory_count": 0,
            "audit_a11y_advisory_samples": [],
            "isolation_ok": True,
            "isolation_leaked": [],
            "duration_ms": 1,
        }],
        "all_completed": True,
        "all_isolated": True,
        "all_console_clean": True,
        "all_audit_clean": True,
        "rss": {},
        "negative_control": {
            "description": "",
            "shared_session_id": "",
            "injected_marker": "",
            "detected": False,
            "leaked": [],
        },
    }))
    swarm_payload = adapt_artifact("swarm-results", swarm_path)
    check("adapter swarm-results verdict", swarm_payload["verdict"], "solved")
    assert_conforms("adapter swarm-results conforms", swarm_payload)

    ui_data = ROOT / "tools" / "persona_qa" / "tests" / "testdata"
    ui_qa_payload = adapt_artifact("ui-qa-verdict", ui_data / "ui_qa_verdict_pass.json")
    check("adapter ui-qa check_type", ui_qa_payload["check_type"], "journey-verdict")
    assert_conforms("adapter ui-qa conforms", ui_qa_payload)

    ui_review_payload = adapt_artifact("ui-review-verdict", ui_data / "ui_review_verdict_pass.json")
    check("adapter ui-review check_type", ui_review_payload["check_type"], "ux-heuristic")
    assert_conforms("adapter ui-review conforms", ui_review_payload)

    run_dir = adapter_root / "product-journey-run"
    run_dir.mkdir()
    (run_dir / "review.json").write_text(json.dumps({
        "status": "ready",
        "summary_counts": {"passed": 19, "warned": 0, "failed": 0, "total": 19},
    }))
    product_payload = adapt_artifact("product-journey-review", run_dir)
    check("adapter product-journey verdict", product_payload["verdict"], "solved")
    assert_conforms("adapter product-journey conforms", product_payload)


# ---------------------------------------------------------------------------
# 2. The 19-check review-gate mapping.
# ---------------------------------------------------------------------------
ready = from_product_journey_report({
    "review_status": "ready",
    "validation_status": "valid",
    "passed": 19, "warnings": 0, "failed": 0, "total": 19,
})
check("ready -> solved", ready.verdict, "solved")
check("ready health", ready.health, "model:result")

needs_evidence_partial = from_product_journey_report({
    "review": {"status": "needs_evidence",
               "summary_counts": {"passed": 15, "warned": 3, "failed": 1, "total": 19}},
    "validation": {"status": "valid"},
    "attached_evidence": [{"path": "cassette://run/onboarding/tui"}],
})
check("needs_evidence (with proof) -> partial", needs_evidence_partial.verdict, "partial")

all_failed = from_product_journey_report({
    "review": {"status": "needs_evidence",
               "summary_counts": {"passed": 0, "warned": 0, "failed": 19, "total": 19}},
    "validation": {"status": "valid"},
})
check("all-checks-failed (no evidence, no blocker) -> failed", all_failed.verdict, "failed")

blocked = from_product_journey_report({
    "review": {"status": "needs_evidence",
               "summary_counts": {"passed": 10, "warned": 0, "failed": 0, "total": 19}},
    "validation": {"status": "valid"},
    "scenario_outcomes_summary": {"scenarios": 3, "started": 1, "blocked": 1},
    "driver_handoff": {"missing_proof_evidence": [{"scenario": "onboarding", "kind": "tui-cast"}]},
})
check("blocker + scenario blocked, no failed checks -> blocked", blocked.verdict, "blocked")
check_true("blocked carries the named blocker",
           any("onboarding" in b for b in blocked.blockers), blocked.blockers)

harness_error = from_product_journey_report({
    "status": "failed",
    "validation_status": "error",
})
check("harness/validation error -> blocked", harness_error.verdict, "blocked")
check("harness/validation error -> infra:harness", harness_error.health, "infra:harness")


# ---------------------------------------------------------------------------
# 3. Malformed-file rejection (the arena bugfix plugin's score()).
# ---------------------------------------------------------------------------
plugin = BugfixPlugin()
target = Target(id="query-string", label="qs")
variant = Variant(id="kitsoki")
cell = Cell(id="query-string--kitsoki--bug:qs9", job_type="bugfix", target=target,
            variant=variant, axis={"bug": "qs9"})
state_path = Path(plugin.completion_state_path(cell))


def reset_state_file() -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    state_path.unlink(missing_ok=True)


try:
    # 3a. No file at all, no recognizable infra text -> explicit missing-contract signal.
    reset_state_file()
    r = plugin.score(cell, exit_code=1, stdout="some unrelated tool output", stderr="")
    check("missing file, no infra text -> blocked", r.verdict, "blocked")
    check("missing file -> infra:missing-completion-state", r.health, "infra:missing-completion-state")

    # 3b. No file, but stdout/stderr carries a recognized infra signature -> infra:harness.
    r = plugin.score(cell, exit_code=1, stdout="", stderr="connection refused by host")
    check("missing file + infra text -> blocked", r.verdict, "blocked")
    check("missing file + infra text -> infra:harness", r.health, "infra:harness")

    # 3c. File present but not valid JSON.
    state_path.write_text("{not json")
    r = plugin.score(cell, exit_code=0, stdout="", stderr="")
    check("invalid JSON -> blocked", r.verdict, "blocked")
    check("invalid JSON -> infra:completion-state-malformed", r.health, "infra:completion-state-malformed")

    # 3d. File present, valid JSON, but missing required fields.
    state_path.write_text(json.dumps({"verdict": "solved"}))
    r = plugin.score(cell, exit_code=0, stdout="", stderr="")
    check("missing required fields -> blocked", r.verdict, "blocked")
    check("missing required fields -> infra:completion-state-malformed", r.health,
          "infra:completion-state-malformed")

    # 3e. File present, all required fields, but an unknown verdict value.
    state_path.write_text(json.dumps({
        "schema_version": "1.0.0", "verdict": "nonsense", "health": "model:result",
        "metrics": {}, "evidence_refs": [],
    }))
    r = plugin.score(cell, exit_code=0, stdout="", stderr="")
    check("unknown verdict -> blocked", r.verdict, "blocked")
    check("unknown verdict -> infra:completion-state-malformed", r.health,
          "infra:completion-state-malformed")

    # 3f. A well-formed file is read through cleanly (happy path, for contrast).
    state_path.write_text(json.dumps({
        "schema_version": "1.0.0", "verdict": "solved", "health": "model:result",
        "metrics": {"cost_usd": 0.42}, "evidence_refs": ["trace.jsonl"],
        "notes": "oracle GREEN",
    }))
    r = plugin.score(cell, exit_code=0, stdout="", stderr="")
    check("well-formed file -> verdict", r.verdict, "solved")
    check("well-formed file -> health", r.health, "model:result")
    check("well-formed file -> metrics", r.metrics, {"cost_usd": 0.42})
    check("well-formed file -> evidence_refs", r.evidence_refs, ["trace.jsonl"])
    assert_conforms("well-formed fixture itself conforms to the schema", json.loads(state_path.read_text()))
finally:
    state_path.unlink(missing_ok=True)


if failures:
    print("FAIL: completion-state contract")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: completion-state contract (schema conformance + review-gate mapping + malformed rejection)")
