#!/usr/bin/env python3
"""No-LLM lifecycle checks for task-optimization campaign projections."""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.model import (  # noqa: E402
    analyze_task_optimization,
    receipt_sha256,
    select_task_optimization_champion,
    task_optimization_status,
)

failures: list[str] = []


def check(label: str, got: object, want: object) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


plan = {
    "schema": "task-optimization/v1",
    "study_id": "lifecycle",
    "promotion_policy": {"min_scored": 2, "min_solved": 2, "min_solve_rate": 1.0},
    "retry_policy": {"max_infra_retries": 2},
    "cells": [
        {"id": f"{task}--{candidate}--{treatment}", "task_id": task, "split": split,
         "candidate_id": candidate, "treatment": treatment}
        for split, task in (("learning", "t1"), ("learning", "t2"), ("confirmation", "t3"))
        for candidate in ("a", "b")
        for treatment in ("raw", "codeact")
    ],
}
digest = receipt_sha256(plan)


def attempt(identifier: str, cell_id: str, *, status: str, verdict: str = "", health: str = "", retryable: bool | None = None) -> dict:
    value = {"schema": "task-optimization/attempt/v1", "attempt_id": identifier, "cell_id": cell_id,
             "candidate_id": next(cell["candidate_id"] for cell in plan["cells"] if cell["id"] == cell_id),
             "status": status, "plan_sha256": digest, "preflight_sha256": "preflight", "metrics": {"total_tokens": 10}}
    if verdict:
        value["verdict"] = verdict
    if health:
        value["health"] = health
    if retryable is not None:
        value["retryable"] = retryable
    return value


# A transient Docker/harness outage is durable evidence but must not make a
# model arm terminal or eligible for promotion.
retry_cell = "t1--a--codeact"
infra = attempt("infra-1", retry_cell, status="blocked", health="infra:harness")
retry_status = task_optimization_status(plan, [infra])
retry_row = next(cell for cell in retry_status["cells"] if cell["id"] == retry_cell)
check("infra state is retryable", retry_row["status"], "retryable_infra")
check("infra appears in resume", retry_cell in retry_status["resume_cell_ids"], True)
check("infra does not complete learning", retry_status["learning_complete"], False)

receipts: list[dict] = [infra]
for cell in plan["cells"]:
    if cell["split"] != "learning":
        continue
    # Only a/codeact clears the declared two-task promotion bar.  This also
    # proves selection cannot collapse raw and codeact into candidate `a`.
    solved = cell["candidate_id"] == "a" and cell["treatment"] == "codeact"
    receipts.append(attempt("result-" + cell["id"], cell["id"], status="scored", verdict="solved" if solved else "failed"))

analysis = analyze_task_optimization(plan, receipts)
arms = {(row["candidate_id"], row["treatment"]): row for row in analysis["arms"]}
check("only treatment-aware winner eligible", [(row["candidate_id"], row["treatment"]) for row in analysis["arms"] if row["promotion_eligible"]], [("a", "codeact")])
check("winner solve set pinned", arms[("a", "codeact")]["solve_task_ids"], ["t1", "t2"])
check("raw arm is not merged into winner", arms[("a", "raw")]["solved"], 0)

champion = select_task_optimization_champion(plan, receipts)
check("champion candidate", champion["candidate_id"], "a")
check("champion treatment", champion["treatment"], "codeact")
status = task_optimization_status(plan, receipts, champion=champion)
check("confirmation phase starts after freeze", status["phase"], "confirmation")
check("only champion confirmation resumes", status["resume_cell_ids"], ["t3--a--codeact"])

if failures:
    print("FAIL: task optimization lifecycle")
    for failure in failures:
        print("  -", failure)
    raise SystemExit(1)
print("PASS: task optimization lifecycle (no LLM)")
