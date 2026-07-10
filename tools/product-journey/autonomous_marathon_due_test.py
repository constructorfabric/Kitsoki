#!/usr/bin/env python3
"""Runner-level test for autonomous marathon due-run discovery.

The test builds local run/control artifacts only. It never calls GitHub or a
live LLM, and it proves the cadence scanner can reconstruct the next native
story/gitops command from persisted control state.
"""

import importlib.util
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def check(label: str, condition: bool, failures: list[str]) -> None:
    if condition:
        print(f"ok: {label}")
    else:
        print(f"not ok: {label}")
        failures.append(label)


def create_controlled_run(
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    seed: str,
    slug: str,
    created_at: str,
    driver_mode: str,
    ticket_repo: str,
    gh_agent_public_base_url: str,
    cadence_hours: int = 24,
):
    original_now = run.now_utc
    original_slug = run.slug_timestamp
    run.now_utc = lambda: created_at
    run.slug_timestamp = lambda: slug
    try:
        run_dir, run_json = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            run.select_scenarios(scenarios, "bugfix"),
            "vscode",
            "core-maintainer",
            seed,
            "autonomous-marathon",
            None,
            live_budget_minutes=6,
        )
        control = run.write_autonomous_marathon_control(
            run_dir,
            run_json,
            driver_mode,
            cadence_hours,
            15,
            45,
            gh_agent_public_base_url,
            ticket_repo,
        )
        return run_dir, run_json, control
    finally:
        run.now_utc = original_now
        run.slug_timestamp = original_slug


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory() as tmp_name:
        tmp = Path(tmp_name)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)

        catalog = run.load_catalog(run.CATALOG)
        github_targets = run.load_github_targets(run.GITHUB_TARGETS)
        personas = run.load_personas(run.PERSONAS)
        scenarios = run.load_scenarios(run.SCENARIOS)

        due_dir, _due_json, _due_control = create_controlled_run(
            catalog,
            github_targets,
            personas,
            scenarios,
            "due-seed",
            "20260701T000000Z",
            "2026-07-01T00:00:00+00:00",
            "replay",
            "owner/repo",
            "https://agent.example",
        )
        create_controlled_run(
            catalog,
            github_targets,
            personas,
            scenarios,
            "blocked-seed",
            "20260701T010000Z",
            "2026-07-01T01:00:00+00:00",
            "pending",
            "owner/repo",
            "https://agent.example",
        )
        create_controlled_run(
            catalog,
            github_targets,
            personas,
            scenarios,
            "upcoming-seed",
            "20260702T000000Z",
            "2026-07-02T00:00:00+00:00",
            "replay",
            "owner/repo",
            "https://agent.example",
        )
        ignored_dir, _ignored_json, ignored_control = create_controlled_run(
            catalog,
            github_targets,
            personas,
            scenarios,
            "ignored-seed",
            "20260701T020000Z",
            "2026-07-01T02:00:00+00:00",
            "replay",
            "owner/repo",
            "https://agent.example",
        )
        ignored_control["status"] = "retired"
        run.write_json(ignored_dir / "autonomous-marathon-control.json", ignored_control)

        checked_at = "2026-07-02T01:00:00+00:00"
        result = run.autonomous_marathon_due(run.ARTIFACT_ROOT, checked_at, 10)
        control_markdown = (due_dir / "autonomous-marathon-control.md").read_text(encoding="utf-8")

        check(
            "due scanner classifies due, upcoming, and blocked controls",
            result["status"] == "autonomous_marathon_due_checked"
            and result["due_count"] == 1
            and result["upcoming_count"] == 1
            and result["blocked_count"] == 1,
            failures,
        )
        check(
            "due scanner ignores non-active controls",
            result["ignored_count"] == 1
            and result["ignored_runs"][0]["run_id"].endswith("ignored-seed")
            and "not an active standing marathon" in result["ignored_runs"][0]["ignored_reason"],
            failures,
        )
        check(
            "due scanner reconstructs next native marathon command with gitops config",
            result["next_due_run_dir"] == str(due_dir)
            and "--autonomous-marathon" in result["next_due_command"]
            and "--ticket-repo owner/repo" in result["next_due_command"]
            and "--gh-agent-public-base-url https://agent.example" in result["next_due_command"]
            and "--seed due-seed-cycle-20260702T010000Z" in result["next_due_command"],
            failures,
        )
        check(
            "due scanner exposes story intent for the next fresh cadence cycle",
            result["next_due_story_intent"].startswith("autonomous_marathon ")
            and "seed=due-seed-cycle-20260702T010000Z" in result["next_due_story_intent"]
            and "ticket_repo=owner/repo" in result["next_due_story_intent"],
            failures,
        )
        check(
            "pending driver mode fails closed instead of pretending to be autonomous",
            result["blocked_runs"]
            and "pending driver mode" in result["blocked_runs"][0]["blocked_reason"],
            failures,
        )
        check(
            "control artifact persists ticket repo for future cadence cycles",
            "Ticket repo: `owner/repo`" in control_markdown
            and '"ticket_repo": "owner/repo"' in (due_dir / "autonomous-marathon-control.json").read_text(encoding="utf-8"),
            failures,
        )

        calls = []
        original_autonomous_marathon = run.autonomous_marathon

        def fake_autonomous_marathon(
            _catalog,
            _github_targets,
            _personas,
            _scenarios,
            run_dir,
            project,
            persona,
            seed,
            scenario_filter,
            live_budget_minutes,
            ticket_repo,
            gh_agent_db,
            gh_agent_story,
            gh_agent_public_base_url,
            *_args,
            **_kwargs,
        ):
            calls.append({
                "run_dir": run_dir,
                "project": project,
                "persona": persona,
                "seed": seed,
                "scenario_filter": scenario_filter,
                "live_budget_minutes": live_budget_minutes,
                "ticket_repo": ticket_repo,
                "gh_agent_db": gh_agent_db,
                "gh_agent_story": gh_agent_story,
                "gh_agent_public_base_url": gh_agent_public_base_url,
            })
            return {
                "status": "autonomous_marathon_valid",
                "autonomous_marathon_status": "autonomous_marathon_valid",
                "autonomous_marathon_summary": "credible_issues=1, fix=autonomous_fix_valid, review=ready, validation=valid, stats=pass",
                "run_id": "advanced-run",
                "run_dir": str(run.ARTIFACT_ROOT / "advanced-run"),
                "autonomous_marathon_report_path": str(run.ARTIFACT_ROOT / "advanced-run" / "autonomous-marathon-report.md"),
            }

        run.autonomous_marathon = fake_autonomous_marathon
        try:
            advanced = run.autonomous_marathon_advance_due(
                catalog,
                github_targets,
                personas,
                scenarios,
                run.ARTIFACT_ROOT,
                checked_at,
                10,
                "jobs.sqlite",
                "stories/bugfix",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                "",
                0.82,
                25,
                None,
            )
        finally:
            run.autonomous_marathon = original_autonomous_marathon
        check(
            "advance_due invokes the native autonomous marathon path for the next due cycle",
            advanced["status"] == "autonomous_marathon_valid"
            and advanced["autonomous_due_advance_status"] == "autonomous_marathon_advanced"
            and advanced["source_run_dir"] == str(due_dir)
            and calls
            and calls[0]["run_dir"] is None
            and calls[0]["project"] == "vscode"
            and calls[0]["persona"] == "core-maintainer"
            and calls[0]["seed"] == "due-seed-cycle-20260702T010000Z"
            and calls[0]["scenario_filter"] == "bugfix"
            and calls[0]["ticket_repo"] == "owner/repo"
            and calls[0]["gh_agent_db"] == "jobs.sqlite"
            and calls[0]["gh_agent_public_base_url"] == "https://agent.example",
            failures,
        )

        empty_root = tmp / "empty-product-journey"
        empty_root.mkdir()
        not_due = run.autonomous_marathon_advance_due(
            catalog,
            github_targets,
            personas,
            scenarios,
            empty_root,
            checked_at,
            10,
            "",
            "stories/bugfix",
            "",
            "",
            "",
            "none",
            "",
            str(empty_root),
            "",
            0.82,
            25,
            None,
        )
        check(
            "advance_due reports no-op when no standing marathon is due",
            not_due["status"] == "autonomous_marathon_not_due"
            and not_due["autonomous_due_advance_status"] == "autonomous_marathon_not_due"
            and not_due["autonomous_due_count"] == 0,
            failures,
        )

    if failures:
        print("\nFailures:")
        for failure in failures:
            print(f"- {failure}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
