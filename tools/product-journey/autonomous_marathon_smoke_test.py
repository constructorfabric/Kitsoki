#!/usr/bin/env python3
"""No-LLM autonomous product-QA marathon persona sweep.

This is the deterministic version of the standing marathon loop for Kitsoki's
core gears-rust use cases: create a scoped run for each active curated persona,
attach cassette/local driver proof, record credible issues, run the native
gitops autonomous-fix facade, and derive stats from the filed issue state. It
uses the same fake Kitsoki backend as the runner-level filing tests, so it never
calls a real LLM or GitHub.
"""

import importlib.util
import argparse
import json
import os
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

_run_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_run_spec)
_run_spec.loader.exec_module(run)

_filing_spec = importlib.util.spec_from_file_location(
    "pj_file_findings_test", str(Path(__file__).with_name("file_findings_test.py"))
)
filing_test = importlib.util.module_from_spec(_filing_spec)
_filing_spec.loader.exec_module(filing_test)


def check(name: str, cond: bool, failures: list[str]) -> None:
    if cond:
        print(f"ok: {name}")
        return
    print(f"FAIL: {name}")
    failures.append(name)


def write_issue_state(path: Path, issue_urls: list[str]) -> None:
    payload = [
        {
            "url": issue_url,
            "state": "closed",
            "comments": [
                {
                    "body": (
                        "Fixed in:\n\n"
                        "- `abc1234` Autonomous marathon smoke\n\n"
                        "<!-- kitsoki-fixed-in\n"
                        "commits:\n"
                        "  - abc1234\n"
                        "verified:\n"
                        "  - independent verify passed\n"
                        "-->"
                    )
                }
            ],
        }
        for issue_url in issue_urls
    ]
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def display_path(value: str) -> str:
    if not value:
        return ""
    path = Path(value)
    try:
        return str(path.relative_to(Path.cwd()))
    except ValueError:
        return value


def render_smoke_markdown(output: dict) -> str:
    lines = [
        "# Autonomous Marathon Smoke Ledger",
        "",
        f"- Status: `{output.get('status', '')}`",
        f"- Project: `{output.get('project', '')}`",
        f"- Cycles: {output.get('cycle_count', 1)}",
        f"- Personas: {output.get('persona_count', 0)}",
        f"- Scenarios: {output.get('scenario_count', 0)}",
        f"- Runs: {output.get('run_count', 0)}",
        f"- Filed issues: {output.get('filed_issue_count', 0)} / {output.get('expected_issue_count', 0)}",
        f"- Fixed issues: {output.get('gh_agent_done_count', 0)} / {output.get('expected_issue_count', 0)}",
        f"- Flawless runs: {output.get('flawless_run_count', 0)} / {output.get('run_count', 0)}",
        f"- Success rate: {output.get('success_rate', '')}",
        "",
        "## Coverage",
        "",
        "- Persona ids: " + ", ".join(output.get("persona_ids", [])),
        "- Scenario ids: " + ", ".join(output.get("scenario_ids", [])),
        "",
        "## Runs",
        "",
    ]
    for item in output.get("runs", []):
        cycle = int(item.get("cycle", 1) or 1)
        lines.extend([
            f"### Cycle {cycle}: {item.get('persona', '')}",
            "",
            f"- Cycle: {cycle}",
            f"- Status: `{item.get('status', '')}`",
            f"- Run id: `{item.get('run_id', '')}`",
            f"- Scenarios: {', '.join(item.get('scenario_ids', []))}",
            f"- Filed issues: {item.get('filed_issue_count', 0)}",
            f"- Fixed issues: {item.get('gh_agent_done_count', 0)}",
            f"- Independent verification artifacts: {item.get('gh_agent_independent_verify_count', 0)}",
            f"- Integration landing proofs: {item.get('gh_agent_integration_landing_count', 0)}",
            f"- Gate: `{item.get('autonomous_gate_summary', '')}`",
            f"- Report: `{display_path(item.get('autonomous_fix_report_path', ''))}`",
            f"- Deck: `{display_path(item.get('deck_path', ''))}`",
            "",
        ])
    if output.get("failures"):
        lines.extend(["## Failures", ""])
        lines.extend(f"- {failure}" for failure in output.get("failures", []))
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def write_smoke_reports(output: dict, report_dir: Path) -> tuple[Path, Path]:
    report_dir.mkdir(parents=True, exist_ok=True)
    json_path = report_dir / "autonomous-marathon-smoke.json"
    markdown_path = report_dir / "autonomous-marathon-smoke.md"
    json_path.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    markdown_path.write_text(render_smoke_markdown(output), encoding="utf-8")
    return json_path, markdown_path


def run_persona(
    tmp: Path,
    fake: Path,
    catalog: dict,
    github_targets: dict,
    personas: list[dict],
    scenarios: list[dict],
    persona_id: str,
    cycle: int,
    failures: list[str],
) -> dict:
    run_dir, run_json = run.build_run_bundle(
        catalog,
        github_targets,
        personas,
        scenarios,
        "gears-rust",
        persona_id,
        f"autonomous-marathon-smoke-cycle-{cycle}-{persona_id}",
        "dry-run",
        None,
    )
    driver_result = run.attach_autonomous_marathon_replay_driver(run_dir, run_json, None)

    proc = subprocess.run(
        [
            "go", "run", "./cmd/kitsoki", "gitops", "autonomous-fix",
            "--json",
            "--report-invalid-autonomous-fix",
            "--allow-test-backend",
            "--run-dir", str(run_dir),
            "--ticket-repo", "o/r",
            "--agent-db", str(tmp / f"autonomous-marathon-cycle-{cycle}-{persona_id}-gh-agent.json"),
            "--public-base-url", "https://agent.example",
        ],
        cwd=run.ROOT,
        env={
            **os.environ,
            "KITSOKI_BIN": f"{sys.executable} {fake}",
            "KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE": "1",
            "KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND": "1",
        },
        text=True,
        capture_output=True,
        check=False,
    )
    prefix = f"cycle {cycle} {persona_id}: "
    check(prefix + "native gitops autonomous-fix facade exits cleanly", proc.returncode == 0, failures)
    if proc.returncode != 0:
        print(proc.stdout)
        print(proc.stderr)
        return {"persona": persona_id, "status": "failed", "run_dir": str(run_dir)}
    result = json.loads(proc.stdout)

    findings = run.read_json(run_dir / "findings.json")
    issue_urls = [
        item.get("github_issue", {}).get("url", "")
        for item in findings.get("items", [])
        if item.get("kind") == "issue" and item.get("origin", "observed") != "seeded"
    ]
    issue_urls = [url for url in issue_urls if url]
    issue_state = tmp / f"cycle-{cycle}-{persona_id}-issue-state.json"
    if issue_urls:
        write_issue_state(issue_state, issue_urls)
    stats_output = tmp / f"cycle-{cycle}-{persona_id}-stats.json"
    stats = run.derive_stats(run_dir, str(issue_state), 0.82, 25, str(stats_output))
    report_path = Path(result.get("autonomous_fix_report_path", ""))
    report_text = report_path.read_text(encoding="utf-8") if report_path.exists() else ""
    claim_comment_url = result.get("gh_agent_claims", [{}])[0].get("comment_url", "")
    closeout_comment_url = result.get("issue_closeouts", [{}])[0].get("comment_url", "")
    weakness_routes = run.read_json(run_dir / "weakness-routes.json")
    prd_intake = run.read_json(run_dir / "prd-design-intake.json")
    deck_text = (run_dir / "deck.slidey.json").read_text(encoding="utf-8")
    driver_plan = run.read_json(run_dir / "driver-plan.json")
    agent_brief = run.read_json(run_dir / "agent-brief.json")
    expected_lens = run.persona_lens(run_json["persona"])
    driver_lenses = [item.get("persona_lens", {}) for item in driver_plan.get("scenarios", [])]
    brief_lens = agent_brief.get("persona_contract", {}).get("lens", {})
    expected_utterances = {
        scenario["id"]: scenario.get("natural_utterances", [])
        for scenario in run_json.get("scenarios", [])
    }
    driver_utterances = {
        item.get("scenario"): item.get("natural_utterances", [])
        for item in driver_plan.get("scenarios", [])
    }
    driver_action_utterances = {
        item.get("scenario"): next(
            (
                action.get("natural_utterances", [])
                for action in item.get("driver_actions", [])
                if action.get("id") == "act_as_persona"
            ),
            [],
        )
        for item in driver_plan.get("scenarios", [])
    }
    brief_utterances = {
        item.get("id"): item.get("natural_utterances", [])
        for item in agent_brief.get("scenario_order", [])
    }
    drained_jobs = [
        job for job in findings.get("gh_agent", {}).get("drained_jobs", [])
        if isinstance(job, dict) and job.get("state") == "done"
    ]
    integration_landing_count = sum(
        1
        for job in drained_jobs
        if str(job.get("integration_branch", "")).startswith("integration/")
        and str(job.get("commit_sha", "")).strip()
    )

    scenario_ids = [scenario.get("id") for scenario in run_json.get("scenarios", [])]
    check(prefix + "run targets gears-rust with requested persona",
          run_json.get("project", {}).get("id") == "gears-rust"
          and run_json.get("persona", {}).get("id") == persona_id
          and driver_plan.get("project", {}).get("id") == "gears-rust"
          and driver_plan.get("persona", {}).get("id") == persona_id
          and agent_brief.get("project", {}).get("id") == "gears-rust"
          and agent_brief.get("persona", {}).get("id") == persona_id,
          failures)
    check(prefix + "persona lens is preserved in driver and brief contracts",
          driver_lenses
          and all(lens == expected_lens for lens in driver_lenses)
          and brief_lens == expected_lens,
          failures)
    check(prefix + "marathon scoped run covers core use cases",
          scenario_ids == ["project-onboarding", "prd-design", "bugfix"],
          failures)
    check(prefix + "core use cases carry transcript-derived natural utterances",
          expected_utterances
          and all(
              utterances
              and driver_utterances.get(scenario_id) == utterances
              and driver_action_utterances.get(scenario_id) == utterances
              and brief_utterances.get(scenario_id) == utterances
              and all(item.get("source") == "session-transcript" and item.get("source_ref") for item in utterances)
              for scenario_id, utterances in expected_utterances.items()
          ),
          failures)
    check(prefix + "autonomous replay captured every core use case",
          driver_result.get("autonomous_driver_status") == "captured"
          and driver_result.get("autonomous_driver_issue_count") == 3
          and driver_result.get("autonomous_driver_evidence_count", 0) >= 9,
          failures)
    check(prefix + "credible issues were filed",
          result.get("findings_filed_count") == 3 and len(issue_urls) == 3,
          failures)
    check(prefix + "gh-agent fixes drained with independent verification",
          result.get("gh_agent_drain_status") == "drained"
          and result.get("gh_agent_done_count") == 3
          and result.get("gh_agent_missing_verify_count") == 0
          and result.get("gh_agent_independent_verify_count") == 3
          and result.get("gh_agent_missing_triage_count") == 0
          and result.get("gh_agent_triage_evidence_count") == 3,
          failures)
    check(prefix + "gh-agent fixes carry integration landing proof",
          integration_landing_count == 3
          and all(
              job.get("integration_branch", "") in report_text
              and job.get("commit_sha", "") in report_text
              and job.get("integration_branch", "") in deck_text
              and job.get("commit_sha", "") in deck_text
              for job in drained_jobs
          ),
          failures)
    check(prefix + "autonomous gates are all green",
          result.get("autonomous_fix_status") == "autonomous_fix_valid"
          and result.get("autonomous_gate_summary") == "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass",
          failures)
    check(prefix + "human review artifacts link every issue, run, fix, and verify",
          issue_urls
          and all(url in report_text for url in issue_urls)
          and "https://agent.example/run/job-" in report_text
          and "fix-report.md" in report_text
          and "triage-verdict.md" in report_text
          and "independent-verify.md" in report_text,
          failures)
    check(prefix + "human review artifacts link gh-agent claim evidence",
          result.get("gh_agent_claim_status") == "claimed"
          and claim_comment_url
          and claim_comment_url in report_text
          and claim_comment_url in deck_text
          and "Claims: claimed" in deck_text,
          failures)
    check(prefix + "review deck links issue close-out evidence",
          result.get("issue_closeout_status") == "closed"
          and closeout_comment_url
          and closeout_comment_url in deck_text
          and "Issue close-out: closed" in deck_text,
          failures)
    check(prefix + "weakness routed to PRD/design review artifact",
          weakness_routes.get("summary", {}).get("routed") == 1
          and weakness_routes.get("items", [{}])[0].get("target_story") == "stories/prd"
          and prd_intake.get("summary", {}).get("intake_count") == 1
          and prd_intake.get("items", [{}])[0].get("story_intent") == "start"
          and prd_intake.get("items", [{}])[0].get("persona_lens", {}) == expected_lens
          and "weakness-routes.md" in prd_intake.get("items", [{}])[0].get("story_slots", {}).get("upstream_paths", "")
          and "prd-design-intake.md" in deck_text
          and "PRD/design routes" in deck_text,
          failures)
    check(prefix + "mechanical stats count found/filed/fixed",
          stats.get("findings_found_count") == 3
          and stats.get("findings_filed_count") == 3
          and stats.get("issues_fixed_count") == 3
          and stats.get("issues_reopened_count") == 0
          and stats.get("manual_stats_replaced") == "yes",
          failures)

    return {
        "cycle": cycle,
        "persona": persona_id,
        "status": "passed",
        "run_id": run_json["run_id"],
        "run_dir": str(run_dir),
        "deck_path": str(run_dir / "deck.slidey.json"),
        "scenario_ids": scenario_ids,
        "persona_lens": expected_lens,
        "driver_journal_path": str(run_dir / "driver-journal.md"),
        "autonomous_fix_report_path": str(report_path),
        "issue_state_file": str(issue_state),
        "stats_output": str(stats_output),
        "stats_summary": stats.get("stats_summary", ""),
        "filed_issue_count": result.get("findings_filed_count", 0),
        "gh_agent_done_count": result.get("gh_agent_done_count", 0),
        "gh_agent_claim_comment_url": claim_comment_url,
        "gh_agent_independent_verify_count": result.get("gh_agent_independent_verify_count", 0),
        "gh_agent_integration_landing_count": integration_landing_count,
        "issue_closeout_status": result.get("issue_closeout_status", ""),
        "issue_closeout_comment_url": closeout_comment_url,
        "autonomous_gate_summary": result.get("autonomous_gate_summary", ""),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report-dir", default="", help="Directory for retained JSON/Markdown smoke ledger")
    parser.add_argument("--repeats", type=int, default=1, help="Number of full active-persona cycles to run")
    args = parser.parse_args()
    if args.repeats < 1:
        raise SystemExit("--repeats must be >= 1")
    failures: list[str] = []
    with tempfile.TemporaryDirectory() as tmp_name:
        tmp = Path(tmp_name)
        retained_root = Path(args.report_dir) if args.report_dir else None
        scratch = retained_root / "support" if retained_root else tmp
        scratch.mkdir(parents=True, exist_ok=True)
        run.ARTIFACT_ROOT = retained_root / "runs" if retained_root else tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True, exist_ok=True)

        fake = scratch / "fake_kitsoki.py"
        fake.write_text(filing_test.FAKE_KITSOKI, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR)

        catalog = run.load_catalog(run.CATALOG)
        github_targets = run.load_github_targets(run.GITHUB_TARGETS)
        personas = run.load_personas(run.PERSONAS)
        active_persona_ids = [persona["id"] for persona in run.active_personas(personas)]
        scenarios = run.select_scenarios(
            run.active_scenarios(run.load_scenarios(run.SCENARIOS)),
            "core-use-cases",
        )
        runs = []
        for cycle in range(1, args.repeats + 1):
            for persona_id in active_persona_ids:
                runs.append(run_persona(scratch, fake, catalog, github_targets, personas, scenarios, persona_id, cycle, failures))
        total_filed = sum(int(item.get("filed_issue_count", 0) or 0) for item in runs)
        total_done = sum(int(item.get("gh_agent_done_count", 0) or 0) for item in runs)
        total_landing = sum(int(item.get("gh_agent_integration_landing_count", 0) or 0) for item in runs)
        expected_issue_count = args.repeats * len(active_persona_ids) * len(scenarios)
        flawless_runs = [
            item for item in runs
            if item.get("status") == "passed"
            and int(item.get("filed_issue_count", 0) or 0) == len(scenarios)
            and int(item.get("gh_agent_done_count", 0) or 0) == len(scenarios)
            and int(item.get("gh_agent_integration_landing_count", 0) or 0) == len(scenarios)
            and item.get("autonomous_gate_summary") == "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass"
        ]
        output = {
            "status": "passed" if not failures else "failed",
            "summary": (
                "core use-case autonomous product-QA marathon persona sweep passed"
                if not failures
                else "core use-case autonomous product-QA marathon persona sweep failed"
            ),
            "project": "gears-rust",
            "cycle_count": args.repeats,
            "persona_ids": active_persona_ids,
            "persona_count": len(active_persona_ids),
            "scenario_ids": [scenario.get("id") for scenario in scenarios],
            "scenario_count": len(scenarios),
            "run_count": len(runs),
            "expected_issue_count": expected_issue_count,
            "filed_issue_count": total_filed,
            "gh_agent_done_count": total_done,
            "gh_agent_integration_landing_count": total_landing,
            "flawless_run_count": len(flawless_runs),
            "success_rate": f"{len(flawless_runs)}/{len(runs)}",
            "runs": runs,
            "failures": failures,
        }
        if args.report_dir:
            report_json, report_markdown = write_smoke_reports(output, Path(args.report_dir))
            output["report_path"] = str(report_json)
            output["report_markdown_path"] = str(report_markdown)
        else:
            report_json, report_markdown = write_smoke_reports(output, tmp)
            output["report_path"] = str(report_json)
            output["report_markdown_path"] = str(report_markdown)
        check("sweep covered multiple persona runs", len(active_persona_ids) >= 5 and len(runs) >= len(active_persona_ids), failures)
        check("sweep completed every requested repeat cycle",
              args.repeats >= 1 and len(runs) == args.repeats * len(active_persona_ids),
              failures)
        check("sweep filed and fixed every core scenario issue",
              total_filed == expected_issue_count
              and total_done == expected_issue_count,
              failures)
        check("sweep recorded integration landing proof for every fix",
              total_landing == expected_issue_count,
              failures)
        check("sweep has a flawless ledger entry for every persona in every cycle",
              len(flawless_runs) == args.repeats * len(active_persona_ids),
              failures)
        output["status"] = "passed" if not failures else "failed"
        output["failures"] = failures
        write_smoke_reports(output, Path(args.report_dir) if args.report_dir else tmp)
        if failures:
            output["summary"] = "core use-case autonomous product-QA marathon persona sweep failed"
            print(json.dumps(output, sort_keys=True))
            return 1
        print("SUMMARY_JSON: " + json.dumps({
            "project": output["project"],
            "cycle_count": output["cycle_count"],
            "persona_count": output["persona_count"],
            "scenario_count": output["scenario_count"],
            "run_count": output["run_count"],
            "filed_issue_count": output["filed_issue_count"],
            "gh_agent_done_count": output["gh_agent_done_count"],
            "gh_agent_integration_landing_count": output["gh_agent_integration_landing_count"],
            "flawless_run_count": output["flawless_run_count"],
            "expected_issue_count": output["expected_issue_count"],
            "success_rate": output["success_rate"],
            "report_path": output["report_path"],
            "report_markdown_path": output["report_markdown_path"],
        }, sort_keys=True))
        print("PASS")
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
