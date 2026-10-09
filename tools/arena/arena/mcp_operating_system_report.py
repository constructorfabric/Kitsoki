#!/usr/bin/env python3
"""Offline decision bundle for the MCP operating-system replay matrix.

This module intentionally has no provider client.  It expands stored replay
cells, evaluates safety and correctness before considering cost or latency, and
emits a deterministic review bundle.  The optional live-calibration command is
an authorization record only; dispatch remains a separately approved operation.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from collections import defaultdict
from pathlib import Path
from typing import Any


SCHEMA = "mcp_operating_system_decision/v1"
REQUIRED_PROFILES = ("strict", "escape", "legacy")
REQUIRED_CASE_COUNT = 12
LIVE_HARD_CAP_USD = 25.0


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


def sha256(value: Any) -> str:
    return hashlib.sha256(canonical_bytes(value)).hexdigest()


def load_spec(path: str | Path) -> dict[str, Any]:
    value = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("MCP operating-system spec must be an object")
    validate_spec(value)
    return value


def validate_spec(spec: dict[str, Any]) -> None:
    if spec.get("job_type") != "mcp-operating-system":
        raise ValueError("wrong MCP operating-system job type")
    targets = spec.get("targets")
    variants = spec.get("variants")
    cases = spec.get("axes", {}).get("case") if isinstance(spec.get("axes"), dict) else None
    if not isinstance(targets, list) or len(targets) != 1 or not isinstance(targets[0], dict):
        raise ValueError("MCP operating-system spec requires one replay target")
    if not isinstance(cases, list) or len(cases) != REQUIRED_CASE_COUNT or len(set(cases)) != len(cases):
        raise ValueError("MCP operating-system spec requires exactly twelve unique cases")
    if not isinstance(variants, list) or [item.get("id") for item in variants if isinstance(item, dict)] != list(REQUIRED_PROFILES):
        raise ValueError("MCP operating-system profiles must be strict, escape, legacy in review order")
    target = targets[0]
    if not isinstance(target.get("corpus_version"), str) or not target["corpus_version"]:
        raise ValueError("replay target needs a corpus version")
    if not isinstance(target.get("policy_hash"), str) or len(target["policy_hash"]) != 64:
        raise ValueError("replay target needs the compiled policy hash")
    matrix = target.get("replay_matrix")
    if not isinstance(matrix, dict) or set(matrix) != set(REQUIRED_PROFILES):
        raise ValueError("replay matrix must contain every operating profile")
    for profile in REQUIRED_PROFILES:
        rows = matrix[profile]
        if not isinstance(rows, dict) or set(rows) != set(cases):
            raise ValueError(f"replay matrix for {profile} must cover every case once")
        for case_id, cell in rows.items():
            if not isinstance(cell, dict):
                raise ValueError(f"replay cell {profile}/{case_id} must be an object")
            if cell.get("safety") not in {"pass", "fail"} or cell.get("correctness") not in {"pass", "fail"}:
                raise ValueError(f"replay cell {profile}/{case_id} has an invalid hard-gate result")
            if not isinstance(cell.get("cost_usd"), (int, float)) or not isinstance(cell.get("latency_s"), (int, float)):
                raise ValueError(f"replay cell {profile}/{case_id} lacks cost/latency metrics")
    options = spec.get("options")
    if not isinstance(options, dict) or options.get("promotion_candidate") != "strict":
        raise ValueError("strict must be the only promotion candidate in this replay matrix")
    live = options.get("live_calibration")
    if not isinstance(live, dict) or live.get("tests_forbidden") is not True or not live.get("operator_authorization"):
        raise ValueError("live calibration must require authorization and be forbidden in tests")


def replay_cell(spec: dict[str, Any], profile: str, case_id: str) -> dict[str, Any]:
    validate_spec(spec)
    target = spec["targets"][0]
    try:
        cell = target["replay_matrix"][profile][case_id]
    except KeyError as exc:
        raise ValueError(f"unknown replay coordinate {profile}/{case_id}") from exc
    return {
        "profile": profile,
        "case_id": case_id,
        "safety": cell["safety"],
        "correctness": cell["correctness"],
        "cost_usd": cell["cost_usd"],
        "latency_s": cell["latency_s"],
        "evidence_ref": f"replay://mcp-os/{profile}/{case_id}",
    }


def _cells(spec: dict[str, Any]) -> list[dict[str, Any]]:
    cases = spec["axes"]["case"]
    return [replay_cell(spec, profile, case_id) for profile in REQUIRED_PROFILES for case_id in cases]


def _hard_gate_summary(cells: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    summary: dict[str, dict[str, Any]] = {}
    by_profile: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for cell in cells:
        by_profile[cell["profile"]].append(cell)
    for profile in REQUIRED_PROFILES:
        rows = by_profile[profile]
        safety_failures = [row["case_id"] for row in rows if row["safety"] != "pass"]
        correctness_failures = [row["case_id"] for row in rows if row["correctness"] != "pass"]
        summary[profile] = {
            "case_count": len(rows),
            "safety_pass": not safety_failures,
            "correctness_pass": not correctness_failures,
            "safety_failures": safety_failures,
            "correctness_failures": correctness_failures,
            "hard_gate_pass": not safety_failures and not correctness_failures,
        }
    return summary


def _comparison(summary: dict[str, dict[str, Any]], cells: list[dict[str, Any]]) -> dict[str, Any]:
    eligible = [profile for profile in REQUIRED_PROFILES if summary[profile]["hard_gate_pass"]]
    # Cost and latency are intentionally absent unless a profile first clears
    # both non-negotiable gates. This prevents a cheap unsafe profile winning.
    rows = []
    for profile in eligible:
        profile_cells = [cell for cell in cells if cell["profile"] == profile]
        rows.append({
            "profile": profile,
            "average_cost_usd": round(sum(cell["cost_usd"] for cell in profile_cells) / len(profile_cells), 4),
            "average_latency_s": round(sum(cell["latency_s"] for cell in profile_cells) / len(profile_cells), 4),
        })
    return {"eligible_profiles": eligible, "cost_latency_considered": bool(rows), "profiles": rows}


def build_report(spec_path: str | Path) -> dict[str, Any]:
    spec = load_spec(spec_path)
    target = spec["targets"][0]
    cells = _cells(spec)
    gates = _hard_gate_summary(cells)
    comparison = _comparison(gates, cells)
    candidate = spec["options"]["promotion_candidate"]
    candidate_gate = gates[candidate]
    status = "eligible" if candidate_gate["hard_gate_pass"] else "hold"
    reason = (
        "Strict candidate cleared every replay safety and correctness gate."
        if status == "eligible"
        else "Strict candidate remains held: "
        + "; ".join(
            ([f"safety failed for {', '.join(candidate_gate['safety_failures'])}"] if candidate_gate["safety_failures"] else [])
            + ([f"correctness failed for {', '.join(candidate_gate['correctness_failures'])}"] if candidate_gate["correctness_failures"] else [])
        )
        + ". Cost and latency were not considered."
    )
    return {
        "schema_version": SCHEMA,
        "provenance": {
            "spec_sha256": sha256(spec),
            "corpus_version": target["corpus_version"],
            "policy_hash": target["policy_hash"],
            "replay_only": True,
        },
        "review_order": ["safety", "correctness", "cost", "latency"],
        "promotion_candidate": candidate,
        "promotion_status": status,
        "promotion_reason": reason,
        "hard_gates": gates,
        "comparison": comparison,
        "cells": cells,
        "live_calibration": {
            "status": "not-requested",
            "operator_authorization_required": True,
            "minimum_budget_usd": spec["options"]["live_calibration"]["minimum_budget_usd"],
            "tests_forbidden": True,
            "note": "No provider call is available through replay generation or tests.",
        },
    }


def build_decision(report: dict[str, Any]) -> dict[str, Any]:
    validate_report(report)
    status = report["promotion_status"]
    return {
        "schema_version": SCHEMA,
        "decision": status if status in {"eligible", "hold", "reject"} else "hold",
        "candidate": report["promotion_candidate"],
        "reason": report["promotion_reason"],
        "hard_gates": report["hard_gates"][report["promotion_candidate"]],
        "cost_latency_considered": report["comparison"]["cost_latency_considered"],
        "live_calibration": report["live_calibration"],
    }


def render_markdown(report: dict[str, Any], decision: dict[str, Any]) -> str:
    lines = [
        "# MCP operating-system replay decision",
        "",
        f"- Candidate: `{report['promotion_candidate']}`",
        f"- Decision: **{decision['decision']}** — {decision['reason']}",
        "- Review order: safety → correctness → cost → latency.",
        f"- Live calibration: `{report['live_calibration']['status']}`; operator authorization and a budget are required.",
        "",
        "## Hard-gate matrix",
        "",
        "| profile | safety | correctness | status |",
        "|---|---|---|---|",
    ]
    for profile in REQUIRED_PROFILES:
        gate = report["hard_gates"][profile]
        lines.append(f"| {profile} | {'pass' if gate['safety_pass'] else 'fail'} | {'pass' if gate['correctness_pass'] else 'fail'} | {'eligible' if gate['hard_gate_pass'] else 'held'} |")
    lines.extend([
        "",
        "## Interpretation",
        "",
        "A profile must clear all replay safety and correctness cells before cost or latency are compared. This bundle is replay-only and cannot authorize, trigger, or account for a provider call.",
        "",
    ])
    return "\n".join(lines)


def render_deck(report: dict[str, Any], decision: dict[str, Any]) -> dict[str, Any]:
    cards = []
    for profile in REQUIRED_PROFILES:
        gate = report["hard_gates"][profile]
        cards.append({
            "label": profile,
            "sub": f"safety {'pass' if gate['safety_pass'] else 'fail'} · correctness {'pass' if gate['correctness_pass'] else 'fail'}",
            "style": "primary" if gate["hard_gate_pass"] else "secondary",
        })
    held = report["hard_gates"][report["promotion_candidate"]]["correctness_failures"]
    return {
        "_comment": "Generated by mcp_operating_system_report.py; replay data is canonical and this Slidey deck is derived.",
        "meta": {"title": "MCP operating-system promotion decision", "resolution": {"width": 1920, "height": 1080}, "theme": "rose-pine-moon"},
        "scenes": [
            {"type": "title", "eyebrow": "MCP operating system", "title": "Replay promotion decision", "subtitle": f"candidate {decision['candidate']} · {decision['decision']}"},
            {"type": "cards", "variant": "grid", "title": "Hard gates before efficiency", "cards": cards},
            {"type": "cards", "variant": "grid", "title": "Candidate hold evidence", "cards": [
                {"label": item, "sub": "correctness failed", "style": "secondary"} for item in held
            ] or [{"label": "All strict hard gates passed", "sub": "no hold evidence", "style": "primary"}]},
            {"type": "cards", "variant": "grid", "title": "Live boundary", "cards": [
                {"label": "Not requested", "sub": "replay bundle only", "style": "default"},
                {"label": "Operator authorization", "sub": "explicit token required", "style": "default"},
                {"label": "Budget", "sub": "positive amount required", "style": "default"},
                {"label": "Tests", "sub": "never dispatch live calibration", "style": "secondary"},
            ]},
        ],
    }


def visual_review_input(report: dict[str, Any], deck: dict[str, Any]) -> dict[str, Any]:
    validate_slidey(deck)
    return {
        "schema_version": "mcp_operating_system_visual_review/v1",
        "deck_sha256": sha256(deck),
        "promotion_status": report["promotion_status"],
        "scene_titles": [str(scene.get("title", "")) for scene in deck["scenes"]],
        "required_claims": ["hard gates before efficiency", "live calibration is not requested", "candidate decision"],
    }


def validate_slidey(deck: dict[str, Any]) -> None:
    meta = deck.get("meta")
    scenes = deck.get("scenes")
    if not isinstance(meta, dict) or meta.get("title") != "MCP operating-system promotion decision":
        raise ValueError("Slidey deck has an invalid title")
    if not isinstance(scenes, list) or len(scenes) != 4:
        raise ValueError("Slidey deck must have exactly four deterministic scenes")
    expected = ["title", "cards", "cards", "cards"]
    if [scene.get("type") if isinstance(scene, dict) else None for scene in scenes] != expected:
        raise ValueError("Slidey deck scene sequence is invalid")
    if not all(isinstance(scene.get("title", ""), str) or scene.get("type") == "title" for scene in scenes if isinstance(scene, dict)):
        raise ValueError("Slidey deck has non-text scene labels")


def validate_report(report: dict[str, Any]) -> None:
    if report.get("schema_version") != SCHEMA:
        raise ValueError("unsupported MCP operating-system decision schema")
    if report.get("promotion_candidate") != "strict":
        raise ValueError("only strict may be promoted")
    cells = report.get("cells")
    if not isinstance(cells, list) or len(cells) != REQUIRED_CASE_COUNT * len(REQUIRED_PROFILES):
        raise ValueError("decision report has an incomplete replay matrix")
    gate = report.get("hard_gates", {}).get("strict") if isinstance(report.get("hard_gates"), dict) else None
    if not isinstance(gate, dict):
        raise ValueError("decision report is missing strict hard gates")
    status = report.get("promotion_status")
    if status == "eligible" and not gate.get("hard_gate_pass"):
        raise ValueError("eligible decision bypassed a strict hard gate")
    if status not in {"eligible", "hold", "reject"}:
        raise ValueError("decision must be eligible, hold, or reject")
    live = report.get("live_calibration")
    if not isinstance(live, dict) or live.get("tests_forbidden") is not True:
        raise ValueError("decision report must retain the no-test live boundary")


def authorize_live_calibration(
    spec_path: str | Path, *, authorization: str, budget_usd: float, provider: str, model: str
) -> dict[str, Any]:
    spec = load_spec(spec_path)
    policy = spec["options"]["live_calibration"]
    if authorization != policy["operator_authorization"]:
        raise ValueError("live calibration requires the explicit operator authorization token")
    if not isinstance(budget_usd, (int, float)) or isinstance(budget_usd, bool) or float(budget_usd) != LIVE_HARD_CAP_USD:
        raise ValueError(f"live calibration requires the exact USD {LIVE_HARD_CAP_USD:.0f} hard cap")
    if provider not in {"claude-cli", "generic-command"}:
        raise ValueError("live calibration requires a supported explicit provider identity")
    if not isinstance(model, str) or not model.strip():
        raise ValueError("live calibration requires an explicit selected model")
    return {
        "schema_version": "mcp_operating_system_live_calibration_request/v1",
        "status": "authorized-not-dispatched",
        "budget_usd": budget_usd,
        "provider": provider,
        "model": model,
        "corpus_version": spec["targets"][0]["corpus_version"],
        "policy_hash": spec["targets"][0]["policy_hash"],
        "note": "Authorization is recorded separately; no provider call is made by this command.",
    }


def write_bundle(spec_path: str | Path, out_dir: str | Path) -> dict[str, str]:
    report = build_report(spec_path)
    decision = build_decision(report)
    deck = render_deck(report, decision)
    visual = visual_review_input(report, deck)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    paths = {
        "report_json": out / "report.json",
        "report_md": out / "report.md",
        "decision_json": out / "decision.json",
        "deck_slidey_json": out / "deck.slidey.json",
        "visual_review_input_json": out / "visual-review-input.json",
    }
    paths["report_json"].write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    paths["report_md"].write_text(render_markdown(report, decision), encoding="utf-8")
    paths["decision_json"].write_text(json.dumps(decision, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    paths["deck_slidey_json"].write_text(json.dumps(deck, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    paths["visual_review_input_json"].write_text(json.dumps(visual, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return {name: str(path) for name, path in paths.items()}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    report_parser = sub.add_parser("report", help="regenerate a no-LLM review bundle")
    report_parser.add_argument("--spec", required=True)
    report_parser.add_argument("--out", required=True)
    replay_parser = sub.add_parser("replay", help="print one stored replay cell")
    replay_parser.add_argument("--spec", required=True)
    replay_parser.add_argument("--profile", required=True, choices=REQUIRED_PROFILES)
    replay_parser.add_argument("--case", dest="case_id", required=True)
    live_parser = sub.add_parser("live-calibration", help="record explicit live-calibration authorization without dispatching")
    live_parser.add_argument("--spec", required=True)
    live_parser.add_argument("--operator-authorization", required=True)
    live_parser.add_argument("--budget-usd", required=True, type=float)
    live_parser.add_argument("--provider", required=True, choices=("claude-cli", "generic-command"))
    live_parser.add_argument("--model", required=True)
    args = parser.parse_args(argv)
    if args.command == "report":
        print(json.dumps(write_bundle(args.spec, args.out), sort_keys=True))
    elif args.command == "replay":
        print(json.dumps(replay_cell(load_spec(args.spec), args.profile, args.case_id), sort_keys=True))
    else:
        print(json.dumps(authorize_live_calibration(
            args.spec,
            authorization=args.operator_authorization,
            budget_usd=args.budget_usd,
            provider=args.provider,
            model=args.model,
        ), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
