#!/usr/bin/env python3
"""No-LLM persona replay -> issue -> gh-agent fix smoke.

This smoke starts from the same product-journey run bundle shape the reusable
persona driver uses, records an observed issue finding with local proof
artifacts, then runs the native `kitsoki gitops autonomous-fix` facade with a
fake Kitsoki backend. It proves the persona-QA output can enter the story-owned
issue-to-fix gate without raw gh calls, live GitHub, or live LLM work.
"""

import importlib.util
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


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory() as tmp_name:
        tmp = Path(tmp_name)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)

        fake = tmp / "fake_kitsoki.py"
        fake.write_text(filing_test.FAKE_KITSOKI, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR)

        catalog = run.load_catalog(run.CATALOG)
        personas = run.load_personas(run.PERSONAS)
        scenarios = [
            scenario
            for scenario in run.load_scenarios(run.SCENARIOS)
            if scenario.get("id") == "bugfix"
        ]
        run_dir, run_json = run.build_run_bundle(
            catalog,
            run.load_github_targets(run.GITHUB_TARGETS),
            personas,
            scenarios,
            "vscode",
            "core-maintainer",
            "persona-autofix-smoke",
            "dry-run",
            None,
        )
        scenario_id = run_json["scenarios"][0]["id"]
        filing_test.attach_bugfix_proof(run_dir, scenario_id)
        run.record_finding(
            run_dir,
            "strength",
            "Persona replay collected bugfix proof",
            "The persona replay bundle attached the local bugfix proof artifacts needed by the review gate.",
            scenario_id,
            "low",
            str(run_dir / "test-evidence" / "oracle_result.md"),
            "observed",
            None,
        )
        run.record_finding(
            run_dir,
            "weakness",
            "Persona replay surfaced a repairable product issue",
            "The run keeps the issue visible as a finding instead of stopping at evidence capture.",
            scenario_id,
            "medium",
            str(run_dir / "driver-journal.md"),
            "open",
            None,
        )
        run.record_finding(
            run_dir,
            "issue",
            "Persona replay issue should be filed and fixed",
            "A credible persona-QA issue finding should become a filed GitHub issue and a completed gh-agent fix with review artifacts.",
            scenario_id,
            "high",
            str(run_dir / "test-evidence" / "trace-replay.md"),
            "open",
            None,
        )

        proc = subprocess.run(
            [
                "go", "run", "./cmd/kitsoki", "gitops", "autonomous-fix",
                "--json",
                "--report-invalid-autonomous-fix",
                "--allow-test-backend",
                "--run-dir", str(run_dir),
                "--ticket-repo", "o/r",
                "--agent-db", str(tmp / "persona-autofix-gh-agent.json"),
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
        check("gitops autonomous-fix facade exits cleanly", proc.returncode == 0, failures)
        if proc.returncode != 0:
            print(proc.stdout)
            print(proc.stderr)
            return 1
        result = json.loads(proc.stdout)
        findings = run.read_json(run_dir / "findings.json")
        issue_items = [
            item for item in findings.get("items", [])
            if item.get("kind") == "issue" and item.get("origin", "observed") != "seeded"
        ]
        report_path = Path(result.get("autonomous_fix_report_path", ""))
        report_text = report_path.read_text(encoding="utf-8") if report_path.exists() else ""
        claim_comment_url = result.get("gh_agent_claims", [{}])[0].get("comment_url", "")
        closeout_comment_url = result.get("issue_closeouts", [{}])[0].get("comment_url", "")
        deck = run.read_json(run_dir / "deck.slidey.json")
        gh_scene = next(
            (scene for scene in deck.get("scenes", []) if scene.get("eyebrow") == "GH-agent fixes"),
            {},
        )
        gh_scene_body = gh_scene.get("body", "")

        check("persona replay filed exactly one observed issue",
              result.get("findings_filed_count") == 1
              and len(issue_items) == 1
              and bool(issue_items[0].get("github_issue", {}).get("url")),
              failures)
        check("gh-agent drained one completed fix",
              result.get("gh_agent_drain_status") == "drained"
              and result.get("gh_agent_done_count") == 1
              and result.get("gh_agent_failed_count") == 0,
              failures)
        check("autonomous gate is valid",
              result.get("autonomous_fix_status") == "autonomous_fix_valid"
              and result.get("autonomous_gate_summary") == "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass",
              failures)
        check("review and validation are clean",
              result.get("review_status") == "ready"
              and result.get("validation_status") == "valid"
              and result.get("validation_errors") == 0,
              failures)
        check("independent verification artifact is required and present",
              result.get("gh_agent_independent_verify_count") == 1
              and result.get("gh_agent_missing_verify_count") == 0
              and "independent-verify.md" in result.get("gh_agent_independent_verify_summary", ""),
              failures)
        check("triage verdict artifact is required and present",
              result.get("gh_agent_triage_evidence_count") == 1
              and result.get("gh_agent_missing_triage_count") == 0
              and "triage-verdict.md" in result.get("gh_agent_triage_evidence_summary", ""),
              failures)
        check("human report links issue, run, fix report, and independent verification",
              "https://github.com/o/r/issues/" in report_text
              and "https://agent.example/run/job-" in report_text
              and "fix-report.md" in report_text
              and "triage-verdict.md" in report_text
              and "independent-verify.md" in report_text,
              failures)
        check("human report links gh-agent claim evidence",
              result.get("gh_agent_claim_status") == "claimed"
              and claim_comment_url
              and claim_comment_url in report_text
              and "Claims: `claimed`" in report_text,
              failures)
        check("review deck links issue, run, report, fix evidence, and independent verification",
              "https://github.com/o/r/issues/" in gh_scene_body
              and "https://agent.example/run/job-" in gh_scene_body
              and "autonomous-fix-report.md" in gh_scene_body
              and "fix-report.md" in gh_scene_body
              and "triage=" in gh_scene_body
              and "triage-verdict.md" in gh_scene_body
              and "independent_verify=" in gh_scene_body
              and "independent-verify.md" in gh_scene_body,
              failures)
        check("review deck links gh-agent claim evidence",
              result.get("gh_agent_claim_status") == "claimed"
              and claim_comment_url
              and claim_comment_url in gh_scene_body
              and "Claims: claimed" in gh_scene_body,
              failures)
        check("review deck links issue close-out evidence",
              result.get("issue_closeout_status") == "closed"
              and closeout_comment_url
              and closeout_comment_url in gh_scene_body
              and "Issue close-out: closed" in gh_scene_body,
              failures)

        output = {
            "status": "passed" if not failures else "failed",
            "run_id": run_json["run_id"],
            "run_dir": str(run_dir),
            "deck_path": str(run_dir / "deck.slidey.json"),
            "driver_journal_path": str(run_dir / "driver-journal.md"),
            "autonomous_fix_report_path": str(report_path),
            "filed_issue_count": result.get("findings_filed_count", 0),
            "gh_agent_done_count": result.get("gh_agent_done_count", 0),
            "gh_agent_claim_comment_url": claim_comment_url,
            "gh_agent_independent_verify_count": result.get("gh_agent_independent_verify_count", 0),
            "issue_closeout_status": result.get("issue_closeout_status", ""),
            "issue_closeout_comment_url": closeout_comment_url,
            "review_status": result.get("review_status", ""),
            "validation_status": result.get("validation_status", ""),
            "autonomous_gate_summary": result.get("autonomous_gate_summary", ""),
            "failures": failures,
        }
        (tmp / "persona-autofix-smoke.json").write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        if failures:
            print(json.dumps(output, sort_keys=True))
            return 1
        print("PASS")
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
