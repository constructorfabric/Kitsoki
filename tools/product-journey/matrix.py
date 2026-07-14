#!/usr/bin/env python3
"""Extracted from run.py: matrix module (see tools/product-journey/README.md)."""


def render_matrix_summary(matrix: dict) -> str:
    proof = matrix.get("target_proof", {})
    proof_summary = proof.get("summary", {})
    strict_ready = bool(proof) and proof_summary.get("failed", 0) == 0 and proof_summary.get("errors", 0) == 0
    lines = [
        "# Product journey GitHub matrix",
        "",
        f"- Matrix: `{matrix['matrix_id']}`",
        f"- Seed: `{matrix['seed']}`",
        f"- Targets: {matrix['target_count']}",
        f"- Assignments: {matrix['assignment_count']}",
        f"- Scenarios per assignment: {matrix['scenario_count']}",
        "",
        "## Selection Contract",
        "",
        f"- Host: {matrix['selection_contract']['host']}",
        f"- License: {matrix['selection_contract'].get('license', 'not set')}",
        f"- Open bug floor: {matrix['selection_contract']['open_bug_floor']}",
        f"- Stargazer floor: {matrix['selection_contract'].get('stargazer_floor', 'not set')}",
        f"- Refresh: {matrix['selection_contract']['refresh_note']}",
        f"- Target proof: {proof.get('proof_id', 'not refreshed')}",
        f"- Target proof checked: {proof.get('created_at', '')}",
        f"- Strict sweep ready: {'yes' if strict_ready else 'no - run refresh-github-targets and validate with --strict-target-proof'}",
        "",
        "## Targets",
        "",
    ]
    for target in matrix["targets"]:
        selection_proof = target.get("selection_proof", {})
        if selection_proof:
            proof_line = (
                f"{selection_proof.get('status')} - "
                f"{selection_proof.get('open_bug_count')} open bugs "
                f"(floor {selection_proof.get('open_bug_floor')}), "
                f"{selection_proof.get('stargazers_count', 'unknown')} stars "
                f"(floor {selection_proof.get('stargazer_floor', matrix['selection_contract'].get('stargazer_floor', 'unknown'))}, "
                f"license {selection_proof.get('license', 'unknown')} via {selection_proof.get('license_source', 'unknown')}, "
                f"checked {selection_proof.get('checked_at')})"
            )
        else:
            proof_line = "not refreshed"
        lines.extend([
            f"### {target['label']}",
            "",
            f"- Repo: {target['repo']}",
            f"- Stack: {target['stack']}",
            f"- Bug query: {target['bug_query']}",
            f"- Selection proof: {proof_line}",
            f"- Status: {target['status']}",
            f"- Notes: {target['notes']}",
            "",
        ])
    lines.extend([
        "## Assignments",
        "",
    ])
    for assignment in matrix["assignments"]:
        lines.append(
            f"- `{assignment['id']}`: {assignment['target']['label']} as "
            f"{assignment['persona']['label']} ({len(assignment['scenarios'])} scenarios) - "
            f"`{assignment['emit_run_command']}`"
        )
        for task in assignment.get("scenario_tasks", [])[:2]:
            lines.append(f"  - `{task['scenario']}`: {task['task_prompt']}")
    lines.extend([
        "",
        "## Execution Loop",
        "",
        "1. Refresh each target's open bug count from its `bug_query` before a live scored sweep.",
        "2. Create one product-journey run per assignment.",
        "3. Drive scenarios through Kitsoki and visual MCP using the assigned persona.",
        "4. Attach evidence, record findings, and run the review gate.",
        "5. Review the per-run Slidey deck plus this matrix deck.",
    ])
    return "\n".join(lines) + "\n"


def render_matrix_deck(matrix: dict) -> dict:
    target_lines = [
        (
            f"{target['label']} - {target['stack']} - "
            f"{target.get('selection_proof', {}).get('open_bug_count', 'unrefreshed')} bugs / floor {target['open_bug_floor']} - "
            f"{target.get('selection_proof', {}).get('stargazers_count', 'unrefreshed')} stars / floor {matrix['selection_contract'].get('stargazer_floor', 'n/a')}"
        )
        for target in matrix["targets"]
    ]
    proof = matrix.get("target_proof", {})
    proof_summary = proof.get("summary", {})
    strict_ready = bool(proof) and proof_summary.get("failed", 0) == 0 and proof_summary.get("errors", 0) == 0
    proof_lines = [
        f"Proof: {proof.get('proof_id', 'not refreshed')}",
        f"Checked: {proof.get('created_at', '')}",
        f"Passed: {proof_summary.get('passed', 0)} / {proof_summary.get('targets', 0)}",
        f"Failed: {proof_summary.get('failed', 0)}",
        f"Errors: {proof_summary.get('errors', 0)}",
        f"Bug floor: {proof_summary.get('open_bug_floor', matrix['selection_contract'].get('open_bug_floor', 'n/a'))}",
        f"Star floor: {proof_summary.get('stargazer_floor', matrix['selection_contract'].get('stargazer_floor', 'n/a'))}",
        f"Strict sweep ready: {'yes' if strict_ready else 'no - validate with --strict-target-proof before live scoring'}",
    ]
    assignment_lines = [
        f"{assignment['target']['label']} / {assignment['persona']['label']}"
        for assignment in matrix["assignments"][:16]
    ]
    scenario_lines = [
        f"{scenario['label']}: {', '.join(scenario['required_mcp'])}"
        for scenario in matrix["scenarios"]
    ]
    task_lines = []
    for assignment in matrix["assignments"][:5]:
        first_task = assignment.get("scenario_tasks", [{}])[0]
        if first_task:
            task_lines.append(f"{assignment['target']['label']} / {assignment['persona']['label']}: {first_task.get('task_prompt', '')}")
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey GitHub Matrix",
            "phase": "planning",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "GitHub Product Journey Matrix",
                "subtitle": f"{matrix['target_count']} repos · {matrix['assignment_count']} assignments",
                "narration": "A repeatable no-LLM plan for natural product journey QA across popular GitHub projects.",
            },
            {
                "type": "narrative",
                "eyebrow": "Selection",
                "title": "Popular GitHub repos with large bug queues",
                "body": "\n".join(target_lines),
                "narration": "Each target is selected for public GitHub usage, popularity, and a large bug-labeled issue corpus.",
            },
            {
                "type": "narrative",
                "eyebrow": "Target proof",
                "title": "Bug corpus and popularity evidence",
                "body": "\n".join(proof_lines),
                "narration": "Current GitHub proof is optional for no-LLM planning, but required before claiming the live matrix satisfies the bug-count and popularity floors.",
            },
            {
                "type": "narrative",
                "eyebrow": "Personas",
                "title": matrix["persona_mode"],
                "body": "\n".join(assignment_lines),
                "narration": "The matrix assigns personas deterministically so results are repeatable across reruns.",
            },
            {
                "type": "narrative",
                "eyebrow": "Scenarios",
                "title": "MCP evidence contract",
                "body": "\n".join(scenario_lines),
                "narration": "Every assignment uses the same scenario set and evidence contract.",
            },
            {
                "type": "narrative",
                "eyebrow": "Task prompts",
                "title": "Natural-use seeds",
                "body": "\n".join(task_lines) if task_lines else "No assignment task prompts generated.",
                "narration": "Each matrix assignment includes deterministic task prompts so natural-use runs are repeatable.",
            },
            {
                "type": "narrative",
                "eyebrow": "Execution",
                "title": "From matrix to reviewable deck",
                "body": "Create runs\nDrive Kitsoki and visual MCP\nAttach evidence\nRecord findings\nRun review gate\nReview per-run and matrix Slidey decks",
                "narration": "The matrix is a planning artifact; each assignment still produces its own evidence-backed bundle.",
            },
        ],
    }


def aggregate_scenario_outcomes(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for outcome in run.get("scenario_outcomes", []):
            scenario_id = outcome.get("scenario", "")
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "label": outcome.get("label", scenario_id),
                "runs": 0,
                "present_evidence_count": 0,
                "required_evidence_count": 0,
                "findings_count": 0,
                "strength_count": 0,
                "weakness_count": 0,
                "issue_count": 0,
                "fix_count": 0,
                "blocked_count": 0,
                "outcomes": {},
            })
            finding_counts = outcome.get("finding_counts", {})
            row["runs"] += 1
            row["present_evidence_count"] += outcome.get("present_evidence_count", 0)
            row["required_evidence_count"] += outcome.get("required_evidence_count", 0)
            row["strength_count"] += finding_counts.get("strength", 0)
            row["weakness_count"] += finding_counts.get("weakness", 0)
            row["issue_count"] += finding_counts.get("issue", 0)
            row["fix_count"] += finding_counts.get("fix", 0)
            row["blocked_count"] += finding_counts.get("blocked", 0)
            row["findings_count"] += sum(finding_counts.get(kind, 0) for kind in ["strength", "weakness", "issue", "fix"])
            outcome_name = outcome.get("outcome", "unknown")
            row["outcomes"][outcome_name] = row["outcomes"].get(outcome_name, 0) + 1
    return [by_scenario[key] for key in sorted(by_scenario)]


def aggregate_persona_outcomes(runs: list[dict]) -> list[dict]:
    by_persona: dict[str, dict] = {}
    for run in runs:
        persona = run.get("persona", {})
        persona_id = persona.get("id", "unknown")
        row = by_persona.setdefault(persona_id, {
            "persona": persona_id,
            "label": persona.get("label", persona_id),
            "runs": 0,
            "reviewed_runs": 0,
            "ready_runs": 0,
            "present_evidence_count": 0,
            "required_evidence_count": 0,
            "findings_count": 0,
            "strength_count": 0,
            "weakness_count": 0,
            "issue_count": 0,
            "fix_count": 0,
            "blocked_count": 0,
            "quality_gate_satisfied_runs": 0,
            "quality_gate_total_runs": 0,
            "quality_gate_blocked_runs": 0,
            "proof_minimum_evidence_count": 0,
            "minimum_evidence_count": 0,
            "review_statuses": {},
        })
        row["runs"] += 1
        row["reviewed_runs"] += 1 if run.get("review_status") != "not_reviewed" else 0
        row["ready_runs"] += 1 if run.get("review_status") == "ready" else 0
        row["present_evidence_count"] += run.get("present_evidence_count", 0)
        row["required_evidence_count"] += run.get("required_evidence_count", 0)
        row["findings_count"] += run.get("findings_count", 0)
        row["strength_count"] += run.get("strength_count", 0)
        row["weakness_count"] += run.get("weakness_count", 0)
        row["issue_count"] += run.get("issue_count", 0)
        row["fix_count"] += run.get("fix_count", 0)
        row["blocked_count"] += run.get("blocked_count", 0)
        status = run.get("review_status", "not_reviewed")
        row["review_statuses"][status] = row["review_statuses"].get(status, 0) + 1
        for gate in run.get("quality_gates", []):
            row["quality_gate_total_runs"] += 1
            row["quality_gate_satisfied_runs"] += 1 if gate.get("satisfied") else 0
            row["quality_gate_blocked_runs"] += 1 if gate.get("blocked") else 0
            row["proof_minimum_evidence_count"] += gate.get("proof_minimum_evidence_count", 0)
            row["minimum_evidence_count"] += gate.get("minimum_evidence_count", 0)
    return [by_persona[key] for key in sorted(by_persona)]


def aggregate_quality_gates(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for gate in run.get("quality_gates", []):
            scenario_id = gate.get("scenario", "")
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "label": gate.get("label", scenario_id),
                "runs": 0,
                "satisfied_runs": 0,
                "blocked_runs": 0,
                "present_minimum_evidence_count": 0,
                "proof_minimum_evidence_count": 0,
                "minimum_evidence_count": 0,
                "missing_minimum_evidence": {},
                "missing_proof_minimum_evidence": {},
                "outcomes": {},
            })
            row["runs"] += 1
            row["satisfied_runs"] += 1 if gate.get("satisfied") else 0
            row["blocked_runs"] += 1 if gate.get("blocked") else 0
            row["present_minimum_evidence_count"] += gate.get("present_minimum_evidence_count", 0)
            row["proof_minimum_evidence_count"] += gate.get("proof_minimum_evidence_count", 0)
            row["minimum_evidence_count"] += gate.get("minimum_evidence_count", 0)
            outcome = gate.get("outcome", "not_started")
            row["outcomes"][outcome] = row["outcomes"].get(outcome, 0) + 1
            for evidence_kind in gate.get("missing_minimum_evidence", []):
                row["missing_minimum_evidence"][evidence_kind] = row["missing_minimum_evidence"].get(evidence_kind, 0) + 1
            for evidence_kind in gate.get("missing_proof_minimum_evidence", []):
                row["missing_proof_minimum_evidence"][evidence_kind] = row["missing_proof_minimum_evidence"].get(evidence_kind, 0) + 1
    return [by_scenario[key] for key in sorted(by_scenario)]


def aggregate_driver_journal(runs: list[dict]) -> list[dict]:
    by_scenario: dict[str, dict] = {}
    for run in runs:
        for event in run.get("driver_journal_events", []):
            scenario_id = event.get("scenario", "")
            if not scenario_id:
                continue
            row = by_scenario.setdefault(scenario_id, {
                "scenario": scenario_id,
                "events": 0,
                "runs": set(),
                "statuses": {},
                "dispatch_modes": {},
                "mcp_tools": {},
                "evidence_refs": 0,
                "blocked_events": 0,
            })
            row["events"] += 1
            row["runs"].add(run.get("run_id", ""))
            status = event.get("status", "attempted")
            row["statuses"][status] = row["statuses"].get(status, 0) + 1
            mode = event.get("dispatch_mode", "")
            if mode:
                row["dispatch_modes"][mode] = row["dispatch_modes"].get(mode, 0) + 1
            for tool in event.get("mcp_tools", []):
                row["mcp_tools"][tool] = row["mcp_tools"].get(tool, 0) + 1
            row["evidence_refs"] += len(event.get("evidence_refs", []))
            if status == "blocked" or event.get("blockers"):
                row["blocked_events"] += 1
    return [
        {**row, "runs": len(row["runs"])}
        for _, row in sorted(by_scenario.items())
    ]


def aggregate_missing_proof_evidence(quality_gates: list[dict], runs: list[dict]) -> list[dict]:
    rows_by_key: dict[tuple[str, str], dict] = {}
    for gate in quality_gates:
        for evidence_kind, count in gate.get("missing_proof_minimum_evidence", {}).items():
            rows_by_key[(gate.get("scenario", ""), evidence_kind)] = {
                "scenario": gate.get("scenario", ""),
                "label": gate.get("label", gate.get("scenario", "")),
                "evidence_kind": evidence_kind,
                "missing_runs": count,
                "runs": gate.get("runs", 0),
                "affected_runs": [],
            }

    for run in runs:
        for gate in run.get("quality_gates", []):
            scenario_id = gate.get("scenario", "")
            for evidence_kind in gate.get("missing_proof_minimum_evidence", []):
                row = rows_by_key.get((scenario_id, evidence_kind))
                if row is None:
                    continue
                row["affected_runs"].append({
                    "run_id": run.get("run_id", ""),
                    "project": run.get("project", {}).get("id", ""),
                    "persona": run.get("persona", {}).get("id", ""),
                    "run_dir": run.get("run_dir", ""),
                    "driver_handoff_path": run.get("driver_handoff_path", ""),
                })

    return sorted(rows_by_key.values(), key=lambda row: (-row["missing_runs"], row["scenario"], row["evidence_kind"]))


def render_rollup_summary(rollup: dict) -> str:
    summary = rollup["summary"]
    lines = [
        "# Product journey matrix rollup",
        "",
        f"- Matrix: `{rollup['matrix_id']}`",
        f"- Runs found: {summary['runs_found']} / {summary['assignments']}",
        f"- Reviewed runs: {summary['reviewed_runs']}",
        f"- Ready runs: {summary['ready_runs']}",
        f"- Evidence present: {summary['present_evidence_count']} / {summary['required_evidence_count']}",
        f"- Findings: {summary['findings_count']} (strengths {summary['strength_count']}, weaknesses {summary['weakness_count']}, issues {summary['issue_count']}, fixes {summary['fix_count']}, blocked {summary.get('blocked_count', 0)})",
        f"- Persona outcome rows: {summary.get('persona_outcomes', 0)}",
        f"- Scenario outcome rows: {summary['scenario_outcomes']} ({summary['scenario_outcomes_with_findings']} with findings)",
        f"- Driver journal: {summary.get('driver_journal_events', 0)} events across {summary.get('driver_journal_rows', 0)} scenarios ({summary.get('driver_journal_blocked_events', 0)} blocked, {summary.get('driver_journal_evidence_refs', 0)} evidence refs)",
        f"- Quality gates: {summary.get('quality_gate_satisfied_runs', 0)} / {summary.get('quality_gate_total_runs', 0)} satisfied, {summary.get('quality_gate_blocked_runs', 0)} blocked, proof evidence {summary.get('quality_gate_proof_minimum_evidence_count', 0)} / {summary.get('quality_gate_minimum_evidence_count', 0)} (captured {summary.get('quality_gate_present_minimum_evidence_count', 0)})",
        f"- Missing proof evidence rows: {summary.get('missing_proof_evidence_rows', 0)} ({summary.get('quality_gate_missing_proof_evidence_count', 0)} missing run-slots)",
        "",
        "## Runs",
        "",
    ]
    for run in rollup["runs"]:
        lines.extend([
            f"### {run['project'].get('label', run['project'].get('id', 'unknown'))} / {run['persona'].get('label', run['persona'].get('id', 'unknown'))}",
            "",
            f"- Run: `{run['run_id']}`",
            f"- Review: {run['review_status']} - {run['review_summary']}",
            f"- Evidence: {run['present_evidence_count']} / {run['required_evidence_count']}",
            f"- Quality gates: {sum(1 for gate in run.get('quality_gates', []) if gate.get('satisfied'))} / {len(run.get('quality_gates', []))} satisfied",
            f"- Findings: {run['findings_count']}",
            f"- Deck: `{run['deck_path']}`",
            f"- Execution plan: `{run['execution_plan_path']}`",
            "",
        ])
    if not rollup["runs"]:
        lines.append("- (no run bundles matched this matrix)")
    lines.extend(["", "## Persona Outcomes", ""])
    if rollup.get("persona_outcomes"):
        for row in rollup["persona_outcomes"]:
            status_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["review_statuses"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Persona: `{row['persona']}`",
                f"- Runs: {row['runs']} (reviewed {row['reviewed_runs']}, ready {row['ready_runs']})",
                f"- Evidence: {row['present_evidence_count']} / {row['required_evidence_count']}",
                f"- Findings: {row['findings_count']} (strengths {row['strength_count']}, weaknesses {row['weakness_count']}, issues {row['issue_count']}, fixes {row['fix_count']}, blocked {row.get('blocked_count', 0)})",
                f"- Quality gates: {row['quality_gate_satisfied_runs']} / {row['quality_gate_total_runs']} satisfied, {row['quality_gate_blocked_runs']} blocked",
                f"- Proof evidence: {row['proof_minimum_evidence_count']} / {row['minimum_evidence_count']}",
                f"- Review statuses: {status_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no persona outcomes found in matched runs)")
    lines.extend(["", "## Scenario Outcomes", ""])
    if rollup["scenario_outcomes"]:
        for row in rollup["scenario_outcomes"]:
            outcome_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["outcomes"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Scenario: `{row['scenario']}`",
                f"- Runs: {row['runs']}",
                f"- Evidence: {row['present_evidence_count']} / {row['required_evidence_count']}",
                f"- Findings: {row['findings_count']} (strengths {row['strength_count']}, weaknesses {row['weakness_count']}, issues {row['issue_count']}, fixes {row['fix_count']}, blocked {row.get('blocked_count', 0)})",
                f"- Outcomes: {outcome_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no scenario outcomes found in matched runs)")
    lines.extend(["", "## Driver Journal", ""])
    if rollup.get("driver_journal"):
        for row in rollup["driver_journal"]:
            status_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["statuses"].items()))
            mode_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["dispatch_modes"].items()))
            tool_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["mcp_tools"].items()))
            lines.extend([
                f"### {row['scenario']}",
                "",
                f"- Runs: {row['runs']}",
                f"- Events: {row['events']}",
                f"- Statuses: {status_counts or '(none)'}",
                f"- Dispatch modes: {mode_counts or '(none)'}",
                f"- Evidence refs: {row['evidence_refs']}",
                f"- Blocked events: {row['blocked_events']}",
                f"- MCP tools: {tool_counts or '(none recorded)'}",
                "",
            ])
    else:
        lines.append("- (no driver journal events found in matched runs)")
    lines.extend(["", "## Quality Gates", ""])
    if rollup.get("quality_gates"):
        for row in rollup["quality_gates"]:
            missing = ", ".join(f"{name}={count}" for name, count in sorted(row["missing_proof_minimum_evidence"].items()))
            outcome_counts = ", ".join(f"{name}={count}" for name, count in sorted(row["outcomes"].items()))
            lines.extend([
                f"### {row['label']}",
                "",
                f"- Scenario: `{row['scenario']}`",
                f"- Runs: {row['runs']}",
                f"- Satisfied: {row['satisfied_runs']} / {row['runs']}",
                f"- Blocked: {row['blocked_runs']}",
                f"- Minimum proof evidence: {row['proof_minimum_evidence_count']} / {row['minimum_evidence_count']} (captured {row['present_minimum_evidence_count']})",
                f"- Missing proof evidence: {missing or '(none)'}",
                f"- Outcomes: {outcome_counts or '(none)'}",
                "",
            ])
    else:
        lines.append("- (no quality gate rows found in matched runs)")
    lines.extend(["", "## Missing Proof Evidence", ""])
    if rollup.get("missing_proof_evidence"):
        for row in rollup["missing_proof_evidence"]:
            lines.append(
                f"- `{row['scenario']}` / `{row['evidence_kind']}`: missing in {row['missing_runs']} / {row['runs']} runs"
            )
            for run in row.get("affected_runs", [])[:5]:
                lines.append(
                    f"  - `{run.get('project', '')}` / `{run.get('persona', '')}`: "
                    f"`{run.get('run_id', '')}`; handoff `{run.get('driver_handoff_path', '')}`"
                )
            if len(row.get("affected_runs", [])) > 5:
                lines.append(f"  - +{len(row.get('affected_runs', [])) - 5} more runs")
    else:
        lines.append("- (none)")
    return "\n".join(lines) + "\n"


def render_rollup_deck(rollup: dict) -> dict:
    summary = rollup["summary"]
    run_lines = [
        f"{run['project'].get('label', run['project'].get('id', 'unknown'))} / {run['persona'].get('label', run['persona'].get('id', 'unknown'))}: {run['review_status']} ({run['present_evidence_count']}/{run['required_evidence_count']} evidence)"
        for run in rollup["runs"][:16]
    ]
    findings_body = (
        f"Strengths: {summary['strength_count']}\n"
        f"Weaknesses: {summary['weakness_count']}\n"
        f"Issues: {summary['issue_count']}\n"
        f"Fixes: {summary['fix_count']}\n"
        f"Blocked: {summary.get('blocked_count', 0)}"
    )
    scenario_lines = [
        f"{row['scenario']}: evidence {row['present_evidence_count']}/{row['required_evidence_count']}, findings {row['findings_count']}, outcomes {', '.join(f'{name}={count}' for name, count in sorted(row['outcomes'].items()))}"
        for row in rollup["scenario_outcomes"][:12]
    ]
    persona_lines = [
        f"{row['persona']}: runs {row['runs']}, ready {row['ready_runs']}, evidence {row['present_evidence_count']}/{row['required_evidence_count']}, proof {row['proof_minimum_evidence_count']}/{row['minimum_evidence_count']}, findings {row['findings_count']}"
        for row in rollup.get("persona_outcomes", [])[:12]
    ]
    driver_lines = [
        f"{row['scenario']}: events {row['events']}, runs {row['runs']}, statuses {', '.join(f'{name}={count}' for name, count in sorted(row['statuses'].items()))}, refs {row['evidence_refs']}, blocked {row['blocked_events']}"
        for row in rollup.get("driver_journal", [])[:12]
    ]
    quality_gate_lines = [
        f"{row['scenario']}: satisfied {row['satisfied_runs']}/{row['runs']}, proof evidence {row['proof_minimum_evidence_count']}/{row['minimum_evidence_count']}, blocked {row['blocked_runs']}"
        for row in rollup.get("quality_gates", [])[:12]
    ]
    missing_proof_lines = [
        (
            f"{row['scenario']} / {row['evidence_kind']}: missing {row['missing_runs']}/{row['runs']} runs"
            + (
                f" - start {row.get('affected_runs', [{}])[0].get('project', '')}/"
                f"{row.get('affected_runs', [{}])[0].get('persona', '')}"
                if row.get("affected_runs") else ""
            )
        )
        for row in rollup.get("missing_proof_evidence", [])[:16]
    ]
    return {
        "meta": {
            "mode": "pitch",
            "title": "Product Journey Matrix Rollup",
            "phase": "rollup",
            "resolution": {"width": 1920, "height": 1080},
        },
        "scenes": [
            {
                "type": "title",
                "title": "Product Journey Matrix Rollup",
                "subtitle": f"{summary['runs_found']} / {summary['assignments']} runs",
                "narration": "Aggregated product-journey evidence and findings across matrix assignments.",
            },
            {
                "type": "narrative",
                "eyebrow": "Coverage",
                "title": "Evidence and readiness",
                "body": f"Reviewed runs: {summary['reviewed_runs']}\nReady runs: {summary['ready_runs']}\nEvidence present: {summary['present_evidence_count']} / {summary['required_evidence_count']}\nProof evidence: {summary.get('quality_gate_proof_minimum_evidence_count', 0)} / {summary.get('quality_gate_minimum_evidence_count', 0)}\nQuality gates satisfied: {summary.get('quality_gate_satisfied_runs', 0)} / {summary.get('quality_gate_total_runs', 0)}\nMissing proof rows: {summary.get('missing_proof_evidence_rows', 0)}\nMissing assignments: {rollup['missing_assignment_count']}",
                "narration": "This rollup shows whether the matrix has enough completed runs to review.",
            },
            {
                "type": "narrative",
                "eyebrow": "Runs",
                "title": "Assignment status",
                "body": "\n".join(run_lines) if run_lines else "No run bundles matched this matrix yet.",
                "narration": "Each run links back to its own deck and execution plan in the rollup markdown.",
            },
            {
                "type": "narrative",
                "eyebrow": "Findings",
                "title": "Strengths, weaknesses, issues, fixes",
                "body": findings_body,
                "narration": "Finding counts are aggregated from the per-run findings files.",
            },
            {
                "type": "narrative",
                "eyebrow": "Persona outcomes",
                "title": "Cross-persona signals",
                "body": "\n".join(persona_lines) if persona_lines else "No persona outcomes found in matched runs.",
                "narration": "Persona outcome rollups show whether different natural-use lenses are producing different evidence, findings, and proof coverage.",
            },
            {
                "type": "narrative",
                "eyebrow": "Scenario outcomes",
                "title": "Cross-run scenario signals",
                "body": "\n".join(scenario_lines) if scenario_lines else "No scenario outcomes found in matched runs.",
                "narration": "Scenario-level rollups show which journeys are repeatedly weak across natural-use assignments.",
            },
            {
                "type": "narrative",
                "eyebrow": "Driver journal",
                "title": "Reusable driver attempts",
                "body": "\n".join(driver_lines) if driver_lines else "No driver journal events found in matched runs.",
                "narration": "Driver journal rollups show which scenarios the reusable driver actually attempted, captured, blocked, or validated.",
            },
            {
                "type": "narrative",
                "eyebrow": "Quality gates",
                "title": "Cross-run proof coverage",
                "body": "\n".join(quality_gate_lines) if quality_gate_lines else "No quality gate rows found in matched runs.",
                "narration": "Quality gate rollups show which scenarios have enough proof-source minimum evidence to count as completed across the matrix.",
            },
            {
                "type": "narrative",
                "eyebrow": "Missing proof",
                "title": "Evidence backlog",
                "body": "\n".join(missing_proof_lines) if missing_proof_lines else "No missing proof evidence across reviewed runs.",
                "narration": "The missing proof scene shows which evidence kinds still need live visual MCP or cassette-backed capture.",
            },
        ],
    }


def rollup_handoff_backlog_summary(rollup: dict, limit: int = 3) -> str:
    lines = []
    for row in rollup.get("missing_proof_evidence", [])[:limit]:
        affected = row.get("affected_runs", [])
        if affected:
            first = affected[0]
            lines.append(
                f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: "
                f"{first.get('project', '')}/{first.get('persona', '')} -> {first.get('driver_handoff_path', '')}"
            )
        else:
            lines.append(f"{row.get('scenario', '')}/{row.get('evidence_kind', '')}: no affected run link")
    remaining = len(rollup.get("missing_proof_evidence", [])) - limit
    if remaining > 0:
        lines.append(f"+{remaining} more proof rows in rollup.md")
    return "; ".join(lines)
