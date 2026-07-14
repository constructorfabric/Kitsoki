#!/usr/bin/env python3
"""No-LLM adversarial tests for strict scored task-optimization receipts."""

from __future__ import annotations

import copy
import hashlib
import json
import os
import sys
import tempfile
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.task_optimization_receipt import validate_scored_attempt_receipt  # noqa: E402


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory(prefix="task-opt-score-") as tmp:
        root = Path(tmp)
        attempt_id = "attempt-1"
        started = datetime.now(timezone.utc).replace(microsecond=0)
        finished = started + timedelta(seconds=60)
        produced_at = (started + timedelta(seconds=1)).isoformat().replace("+00:00", "Z")
        started_at = started.isoformat().replace("+00:00", "Z")
        finished_at = finished.isoformat().replace("+00:00", "Z")
        trace = root / "trace.jsonl"; trace.write_text('{"event":"done"}\n', encoding="utf-8")
        oracle = root / "oracle.json"; oracle.write_text('{"passed":true}\n', encoding="utf-8")
        suite = root / "suite.json"; suite.write_text('{"passed":true}\n', encoding="utf-8")
        report = root / "agentbench.json"
        report.write_text(json.dumps({"trace": str(trace), "passed": True, "outcome": "solved", "metrics": {
            "input_tokens": 10, "output_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 3,
            "total_tokens": 15, "cost_usd": 0.1, "wall_seconds": 2.0, "accounting_status": "complete",
            "agent_calls_started": 2, "agent_calls_finished": 2, "agent_calls_errored": 0, "agent_calls_in_flight": 0,
        }}), encoding="utf-8")

        def artifact(path: Path, **extra: object) -> dict[str, object]:
            return {"path": str(path), "sha256": digest(path), "attempt_id": attempt_id,
                    "produced_at": produced_at, **extra}

        receipt = {
            "attempt_id": attempt_id, "status": "scored", "verdict": "solved",
            "runtime": {"status": "exited", "exit_code": 0, "runner_commit": "abc", "image_digest": "sha256:image",
                        "started_at": started_at, "finished_at": finished_at},
            "boundary": {"profile_hash": "profile", "launch_plan_hash": "launch", "capability_hash": "sha256:caps",
                         "sandbox_kind": "docker", "sandbox_identity": "container-1"},
            "leakage": {"verdict": "clean", "checker": "corpusproof", "policy_hash": "sha256:policy"},
            "artifacts": {"agentbench_report": artifact(report), "trace": artifact(trace),
                          "oracle": artifact(oracle, passed=True), "suite": artifact(suite, passed=True)},
            "score": {"schema": "task-optimization/score/v1", "agentbench_report_sha256": digest(report), "trace_sha256": digest(trace)},
            "metrics": {"input_tokens": 10, "output_tokens": 2, "cache_creation_input_tokens": 0,
                        "cache_read_input_tokens": 3, "total_tokens": 15, "cost_usd": 0.1, "wall_s": 2.0},
        }
        preflight = {"profile_hash": "profile", "launch_plan_hash": "launch"}
        validate_scored_attempt_receipt(receipt, receipt_path=root / "receipt.json", preflight_candidate=preflight)

        try:
            validate_scored_attempt_receipt(
                copy.deepcopy(receipt), receipt_path=root / "receipt.json", preflight_candidate=preflight,
                requires_codeact_runtime=True,
            )
        except ValueError as exc:
            if "runtime receipts" not in str(exc):
                failures.append("CodeAct runtime omission returned an unrelated error: " + str(exc))
        else:
            failures.append("CodeAct receipt without runtime accounting was accepted")

        codeact_report = json.loads(report.read_text(encoding="utf-8"))
        codeact_report["metrics"]["runtime_accounting_status"] = "complete"
        codeact_report_path = root / "codeact-agentbench.json"
        codeact_report_path.write_text(json.dumps(codeact_report), encoding="utf-8")
        codeact_receipt = copy.deepcopy(receipt)
        codeact_receipt["artifacts"]["agentbench_report"] = artifact(codeact_report_path)
        codeact_receipt["score"]["agentbench_report_sha256"] = digest(codeact_report_path)
        validate_scored_attempt_receipt(
            codeact_receipt, receipt_path=root / "receipt.json", preflight_candidate=preflight,
            requires_codeact_runtime=True,
        )

        cases = {
            "missing oracle": lambda value: value["artifacts"].pop("oracle"),
            "failed suite": lambda value: value["artifacts"]["suite"].update({"passed": False}),
            "nonterminal calls": lambda value: value["artifacts"]["agentbench_report"].update({"sha256": "0" * 64}),
            "missing usage": lambda value: value["metrics"].pop("total_tokens"),
            "boundary mismatch": lambda value: value["boundary"].update({"launch_plan_hash": "wrong"}),
            "leakage not clean": lambda value: value["leakage"].update({"verdict": "unknown"}),
            "stale artifact time": lambda value: value["artifacts"]["trace"].update({"produced_at": "2000-01-01T00:00:00Z"}),
        }
        # The lifecycle fixture needs a report whose valid digest still names
        # in-flight calls, so it proves contract validation rather than hashing.
        lifecycle_report = json.loads(report.read_text(encoding="utf-8"))
        lifecycle_report["metrics"]["agent_calls_in_flight"] = 1
        lifecycle = root / "inflight-agentbench.json"; lifecycle.write_text(json.dumps(lifecycle_report), encoding="utf-8")
        def nonterminal(value: dict) -> None:
            value["artifacts"]["agentbench_report"] = artifact(lifecycle)
            value["score"]["agentbench_report_sha256"] = digest(lifecycle)
        cases["nonterminal calls"] = nonterminal
        for label, mutate in cases.items():
            invalid = copy.deepcopy(receipt)
            mutate(invalid)
            try:
                validate_scored_attempt_receipt(invalid, receipt_path=root / "receipt.json", preflight_candidate=preflight)
            except ValueError:
                continue
            failures.append(label + " was accepted")
        os.utime(trace, (1, 1))
        try:
            validate_scored_attempt_receipt(copy.deepcopy(receipt), receipt_path=root / "receipt.json", preflight_candidate=preflight)
        except ValueError:
            pass
        else:
            failures.append("stale artifact mtime was accepted")
    if failures:
        print("FAIL: task-optimization scored receipt")
        print("\n".join("  - " + failure for failure in failures))
        return 1
    print("PASS: task-optimization scored receipt (AgentBench, artifacts, terminal lifecycle, boundary, leakage; no LLM)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
