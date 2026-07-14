#!/usr/bin/env python3
"""Runner-level test for --autonomous-marathon.

The fake KITSOKI_BIN comes from file_findings_test.py, so this test does not
call GitHub or a real LLM. It proves the marathon wrapper creates a bounded run
and later finalizes that run through the native issue filing and gh-agent gate.
"""

import importlib.util
import http.server
import os
import stat
import sys
import tempfile
import threading
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)

_filing_spec = importlib.util.spec_from_file_location(
    "file_findings_test", str(Path(__file__).with_name("file_findings_test.py"))
)
filing_test = importlib.util.module_from_spec(_filing_spec)
_filing_spec.loader.exec_module(filing_test)


def check(label: str, condition: bool, failures: list[str]) -> None:
    if condition:
        print(f"ok: {label}")
    else:
        print(f"not ok: {label}")
        failures.append(label)


class HealthHandler(http.server.BaseHTTPRequestHandler):
    body = b"ok"
    status = 200
    ready_body = b'{"status":"ready","repo":"o/r","public_base_url":"","worker":"test-worker","drain_enabled":true}'
    ready_status = 200

    def do_GET(self) -> None:
        if self.path == "/api/ready":
            self.send_response(self.ready_status)
            self.end_headers()
            self.wfile.write(self.ready_body)
            return
        if self.path != "/healthz":
            self.send_response(404)
            self.end_headers()
            return
        self.send_response(self.status)
        self.end_headers()
        self.wfile.write(self.body)

    def log_message(self, _format: str, *_args: object) -> None:
        return


def start_health_server(body: bytes = b"ok", status: int = 200, ready_body=None, ready_status: int = 200):
    attrs = {"body": body, "status": status, "ready_status": ready_status}
    if ready_body is not None:
        attrs["ready_body"] = ready_body
    handler = type("TestHealthHandler", (HealthHandler,), attrs)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, f"http://127.0.0.1:{server.server_port}"


def main() -> int:
    failures: list[str] = []
    with tempfile.TemporaryDirectory() as tmp_name:
        tmp = Path(tmp_name)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)

        fake = tmp / "fake_kitsoki.py"
        fake.write_text(filing_test.FAKE_KITSOKI, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR)

        old_env = {
            "KITSOKI_BIN": os.environ.get("KITSOKI_BIN"),
            "KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE": os.environ.get("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE"),
            "KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND": os.environ.get("KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND"),
        }
        os.environ["KITSOKI_BIN"] = f"{sys.executable} {fake}"
        os.environ["KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE"] = "1"
        os.environ["KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND"] = "1"
        try:
            catalog = run.load_catalog(run.CATALOG)
            github_targets = run.load_github_targets(run.GITHUB_TARGETS)
            personas = run.load_personas(run.PERSONAS)
            scenarios = run.load_scenarios(run.SCENARIOS)
            healthy_server, healthy_url = start_health_server()
            unhealthy_server, unhealthy_url = start_health_server(b"not-ok")
            wrong_repo_server, wrong_repo_url = start_health_server(
                ready_body=b'{"status":"ready","repo":"other/repo","public_base_url":"","worker":"test-worker","drain_enabled":true}'
            )

            created = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-test",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            run_dir = Path(created["run_dir"])
            run_json = run.read_json(run_dir / "run.json")
            scenario_id = run_json["scenarios"][0]["id"]
            control_path = Path(created["autonomous_control_path"])
            control_markdown_path = Path(created["autonomous_control_markdown_path"])
            control = run.read_json(control_path)
            expected_final_gates = [
                "autonomous_watchdog",
                "autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<public-gh-agent-url>",
                "review",
                "validate",
            ]
            execution_plan = run.read_json(run_dir / "execution-plan.json")
            driver_plan = run.read_json(run_dir / "driver-plan.json")
            agent_brief = run.read_json(run_dir / "agent-brief.json")
            driver_handoff = run.read_json(run_dir / "driver-handoff.json")
            loaded_summary = run.summarize_run_bundle(run_dir)

            check("autonomous marathon creates a bounded run",
                  created["autonomous_marathon_status"] == "autonomous_marathon_ready_for_driver"
                  and run_json["mode"] == "autonomous-marathon"
                  and len(run_json["scenarios"]) == 1
                  and created["live_budget_minutes"] == 7
                  and created["gh_agent_health_status"] == "pass"
                  and created["gh_agent_readiness_status"] == "pass"
                  and "/api/ready" in created["gh_agent_readiness_summary"],
                  failures)
            check("creation writes standing-loop control metadata",
                  created["autonomous_control_status"] == "ready_for_driver"
                  and control_path.exists()
                  and control_markdown_path.exists()
                  and "Autonomous Marathon Control" in control_markdown_path.read_text(encoding="utf-8")
                  and control["cadence"]["hours"] == 24
                  and control["budget"]["per_scenario_live_minutes"] == 7
                  and control["budget"]["manual_glue_steps_target"] == 0
                  and control["watchdog"]["heartbeat_minutes"] == 15
                  and control["watchdog"]["watchdog_minutes"] == 45
                  and control["gh_agent"]["public_base_url"] == healthy_url
                  and control["final_gates"][:2] == ["autonomous_watchdog", "autonomous_fix"],
                  failures)
            check("creation writes a human-reviewable marathon report",
                  Path(created["autonomous_marathon_report_path"]).exists()
                  and "Autonomous Marathon Report" in Path(created["autonomous_marathon_report_path"]).read_text(encoding="utf-8"),
                  failures)
            check("generated driver contract advertises watchdog before autonomous fix",
                  execution_plan["finalize_commands"][-4:] == expected_final_gates
                  and driver_plan["final_gates"] == expected_final_gates
                  and agent_brief["finalize_commands"][-4:] == expected_final_gates
                  and driver_handoff["finalize_commands"] == expected_final_gates
                  and loaded_summary["driver_final_gates"] == expected_final_gates
                  and loaded_summary["gh_agent_public_base_url"] == healthy_url
                  and "4 final gates" in loaded_summary["driver_contract_summary"],
                  failures)

            missing_live_profile = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-live-profile",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
            )
            check("live driver mode refuses missing explicit live profile before dispatch",
                  missing_live_profile["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_live_profile["validation_issue_summary"] == "live-profile-required"
                  and missing_live_profile["autonomous_driver_status"] == "invalid"
                  and missing_live_profile["autonomous_control_status"] == "not_run",
                  failures)

            live_ready = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-live-driver",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            check("live driver mode creates a story-dispatchable bundle",
                  live_ready["autonomous_marathon_status"] == "autonomous_marathon_ready_for_driver"
                  and live_ready["autonomous_driver_mode"] == "live"
                  and live_ready["autonomous_driver_live_profile"] == "test-live"
                  and live_ready["autonomous_driver_status"] == "ready_for_dispatch"
                  and live_ready["autonomous_control_status"] == "ready_for_driver"
                  and "driver=ready" in live_ready["autonomous_gate_summary"]
                  and Path(live_ready["autonomous_marathon_report_path"]).exists(),
                  failures)
            live_dispatch = run.record_autonomous_driver_dispatch(
                Path(live_ready["run_dir"]),
                "live",
                "captured",
                "Story-owned live driver captured proof and recorded a credible issue.",
                5,
                1,
                str(Path(live_ready["run_dir"]) / "driver-trace.jsonl"),
                "",
            )
            live_summary = run.run_story_summary(Path(live_ready["run_dir"]))
            live_receipt_text = Path(live_summary["autonomous_driver_dispatch_markdown_path"]).read_text(encoding="utf-8")
            check("live driver dispatch writes durable review receipt",
                  live_dispatch["schema"] == "kitsoki/product-journey-autonomous-driver-dispatch/v1"
                  and Path(live_summary["autonomous_driver_dispatch_path"]).exists()
                  and Path(live_summary["autonomous_driver_dispatch_markdown_path"]).exists()
                  and live_summary["autonomous_driver_dispatch_status"] == "captured"
                  and live_summary["autonomous_driver_dispatch_trace"].endswith("driver-trace.jsonl")
                  and "Autonomous Driver Dispatch" in live_receipt_text,
                  failures)

            missing_ticket = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-ticket",
                "bugfix",
                7,
                "",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("live pending marathon refuses missing ticket repo before handoff",
                  missing_ticket["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_ticket["validation_issue_summary"] == "ticket-repo-required"
                  and missing_ticket["autonomous_control_status"] == "not_run",
                  failures)

            unhealthy = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-unhealthy-gh-agent",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                unhealthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("live pending marathon refuses unhealthy gh-agent before handoff",
                  unhealthy["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and unhealthy["validation_issue_summary"] == "gh-agent-health"
                  and unhealthy["gh_agent_health_status"] == "fail"
                  and unhealthy["autonomous_control_status"] == "not_run",
                  failures)

            wrong_repo = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-wrong-gh-agent",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                wrong_repo_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("live pending marathon refuses gh-agent for another repo before handoff",
                  wrong_repo["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and wrong_repo["validation_issue_summary"] == "gh-agent-readiness"
                  and wrong_repo["gh_agent_health_status"] == "pass"
                  and wrong_repo["gh_agent_readiness_status"] == "fail"
                  and "/api/ready" in wrong_repo["gh_agent_readiness_summary"]
                  and wrong_repo["autonomous_control_status"] == "not_run",
                  failures)

            filing_test.attach_bugfix_proof(run_dir, scenario_id)
            run.record_driver_event(
                run_dir,
                scenario_id,
                "replay",
                "captured",
                "Autonomous marathon test replay captured proof for the bugfix journey.",
                "session.open,session.trace,render.tui,visual.observe",
                str(run_dir / "test-evidence" / "trace-replay.md"),
                "",
                None,
            )
            run.record_finding(
                run_dir,
                "weakness",
                "Scoped marathon still needs wider cadence",
                "A single bounded scenario proves the gate while broader cadence expands coverage.",
                scenario_id,
                "medium",
                str(run_dir / "driver-plan.md"),
                "open",
                None,
            )
            run.record_finding(
                run_dir,
                "issue",
                "Autonomous marathon should file and fix issues",
                "Credible persona QA issues should be filed, queued for gh-agent, verified, and included in stats.",
                scenario_id,
                "high",
                str(run_dir / "test-evidence" / "trace-replay.md"),
                "open",
                None,
            )

            finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                run_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(run_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            report = Path(finalized["autonomous_marathon_report_path"])
            report_text = report.read_text(encoding="utf-8") if report.exists() else ""

            check("finalization runs native autonomous fix gate",
                  finalized["autonomous_marathon_status"] == "autonomous_marathon_valid"
                  and finalized["autonomous_fix_status"] == "autonomous_fix_valid"
                  and finalized["autonomous_gate_summary"] == "filing=pass, gh_agent=pass, independent_verify=pass, review=pass, validation=pass",
                  failures)
            check("marathon report links fix evidence and stats",
                  "autonomous-fix-report.md" in report_text
                  and "autonomous-marathon-stats.json" in report_text
                  and "Stats gate: `pass` - pass" in report_text
                  and "Stats current run scanned: `yes`" in report_text
                  and finalized["stats_found_count"] == 1
                  and finalized["stats_filed_count"] == 1,
                  failures)
            check("weaknesses route to PRD/design during finalization",
                  finalized["weakness_route_count"] == 1
                  and "finding-1->stories/prd" in finalized["weakness_route_summary"]
                  and finalized["prd_design_intake_count"] == 1
                  and "finding-1->stories/prd start" in finalized["prd_design_intake_summary"]
                  and Path(finalized["prd_design_intake_path"]).exists(),
                  failures)

            no_issue = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-no-issue",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            no_issue_dir = Path(no_issue["run_dir"])
            no_issue_run_json = run.read_json(no_issue_dir / "run.json")
            no_issue_scenario = no_issue_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(no_issue_dir, no_issue_scenario)
            run.record_driver_event(
                no_issue_dir,
                no_issue_scenario,
                "replay",
                "captured",
                "Autonomous marathon test replay captured proof without a credible issue.",
                "session.open,session.trace,render.tui,visual.observe",
                str(no_issue_dir / "test-evidence" / "trace-replay.md"),
                "",
                None,
            )
            run.record_finding(
                no_issue_dir,
                "strength",
                "Autonomous marathon can complete without issue filing",
                "Some persona runs produce reviewable proof and no credible bug to file.",
                no_issue_scenario,
                "low",
                str(no_issue_dir / "test-evidence" / "oracle_result.md"),
                "observed",
                None,
            )
            run.record_finding(
                no_issue_dir,
                "weakness",
                "No-issue marathon still routes design feedback",
                "Observed weaknesses should route to PRD/design while issue filing remains not required.",
                no_issue_scenario,
                "medium",
                str(no_issue_dir / "driver-plan.md"),
                "open",
                None,
            )
            no_issue_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                no_issue_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-no-issue.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(no_issue_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("no-issue marathon reports final review and validation gates",
                  no_issue_finalized["autonomous_marathon_status"] == "autonomous_marathon_valid"
                  and no_issue_finalized["autonomous_fix_status"] == "not_required"
                  and no_issue_finalized["review_status"] == "ready"
                  and no_issue_finalized["validation_status"] == "valid"
                  and no_issue_finalized["autonomous_gate_summary"] == "filing=not_required, gh_agent=not_required, independent_verify=not_required, review=pass, validation=pass"
                  and no_issue_finalized["stats_gate_status"] == "pass"
                  and no_issue_finalized["stats_current_run_scanned"] == "yes",
                  failures)

            failed_driver = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-failed-live-driver",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            failed_driver_dir = Path(failed_driver["run_dir"])
            failed_run_json = run.read_json(failed_driver_dir / "run.json")
            failed_scenario = failed_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(failed_driver_dir, failed_scenario)
            run.record_finding(
                failed_driver_dir,
                "issue",
                "Failed live driver must not trigger autonomous fixing",
                "A failed driver dispatch should stop before issue filing even when a credible issue exists.",
                failed_scenario,
                "high",
                str(failed_driver_dir / "test-evidence" / "trace-replay.md"),
                "open",
                None,
            )
            run.record_autonomous_driver_dispatch(
                failed_driver_dir,
                "live",
                "failed",
                "Live driver failed before it could capture trustworthy proof.",
                0,
                1,
                str(failed_driver_dir / "failed-driver-trace.jsonl"),
                "driver session failed",
            )
            failed_driver_db = tmp / "gh-agent-failed-driver.json"
            failed_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                failed_driver_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(failed_driver_db),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(failed_driver_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            failed_report_text = Path(failed_finalized["autonomous_marathon_report_path"]).read_text(encoding="utf-8")
            check("failed live driver stops before autonomous final gates",
                  failed_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and failed_finalized["autonomous_driver_status"] == "failed"
                  and failed_finalized["autonomous_fix_status"] == "not_run"
                  and failed_finalized["autonomous_watchdog_status"] == "not_run"
                  and failed_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and failed_finalized["stats_status"] == "not_run"
                  and "driver=fail" in failed_finalized["autonomous_gate_summary"]
                  and "autonomous-driver-dispatch.md" in failed_report_text
                  and not failed_driver_db.exists()
                  and not run.autonomous_marathon_watchdog_path(failed_driver_dir).exists(),
                  failures)

            missing_blocker = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-driver-blocker",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            missing_blocker_dir = Path(missing_blocker["run_dir"])
            missing_blocker_run_json = run.read_json(missing_blocker_dir / "run.json")
            missing_blocker_scenario = missing_blocker_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(missing_blocker_dir, missing_blocker_scenario)
            run.record_finding(
                missing_blocker_dir,
                "issue",
                "Failed live driver must explain blocker",
                "A failed driver dispatch needs structured blocker evidence before the story can stop cleanly.",
                missing_blocker_scenario,
                "high",
                str(missing_blocker_dir / "test-evidence" / "trace-replay.md"),
                "open",
                None,
            )
            run.record_autonomous_driver_dispatch(
                missing_blocker_dir,
                "live",
                "failed",
                "Live driver failed but only returned prose.",
                0,
                1,
                "trace://driver/missing-blocker",
                "",
            )
            missing_blocker_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                missing_blocker_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-missing-driver-blocker.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(missing_blocker_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("failed live driver without structured blocker stops with reviewable blocker error",
                  missing_blocker_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_blocker_finalized["autonomous_driver_status"] == "missing-blocker"
                  and missing_blocker_finalized["autonomous_driver_dispatch_status"] == "missing-blocker"
                  and missing_blocker_finalized["autonomous_fix_status"] == "not_run"
                  and missing_blocker_finalized["autonomous_watchdog_status"] == "not_run"
                  and missing_blocker_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and missing_blocker_finalized["stats_status"] == "not_run"
                  and "structured blocker" in missing_blocker_finalized["autonomous_driver_summary"]
                  and not run.autonomous_marathon_watchdog_path(missing_blocker_dir).exists(),
                  failures)

            missing_receipt = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-driver-receipt",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            missing_receipt_dir = Path(missing_receipt["run_dir"])
            missing_receipt_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                missing_receipt_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-missing-driver-receipt.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(missing_receipt_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("missing live driver receipt stops before autonomous final gates",
                  missing_receipt_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_receipt_finalized["autonomous_driver_status"] == "missing"
                  and missing_receipt_finalized["autonomous_fix_status"] == "not_run"
                  and missing_receipt_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and missing_receipt_finalized["stats_status"] == "not_run"
                  and not run.autonomous_marathon_watchdog_path(missing_receipt_dir).exists(),
                  failures)

            empty_journal = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-empty-driver-journal",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            empty_journal_dir = Path(empty_journal["run_dir"])
            run.record_autonomous_driver_dispatch(
                empty_journal_dir,
                "live",
                "captured",
                "Dispatch receipt claims captured proof, but no driver-journal event was persisted.",
                1,
                1,
                str(empty_journal_dir / "driver-trace.jsonl"),
                "",
            )
            empty_journal_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                empty_journal_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-empty-driver-journal.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(empty_journal_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("captured live driver receipt without driver journal stops before autonomous final gates",
                  empty_journal_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and empty_journal_finalized["autonomous_driver_status"] == "missing-heartbeat"
                  and empty_journal_finalized["autonomous_driver_dispatch_status"] == "captured"
                  and empty_journal_finalized["autonomous_fix_status"] == "not_run"
                  and empty_journal_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and empty_journal_finalized["stats_status"] == "not_run"
                  and not run.autonomous_marathon_watchdog_path(empty_journal_dir).exists(),
                  failures)

            missing_issue = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-issue-finding",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            missing_issue_dir = Path(missing_issue["run_dir"])
            missing_issue_run_json = run.read_json(missing_issue_dir / "run.json")
            missing_issue_scenario = missing_issue_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(missing_issue_dir, missing_issue_scenario)
            run.record_driver_event(
                missing_issue_dir,
                missing_issue_scenario,
                "live",
                "captured",
                "Live driver captured proof but failed to persist the claimed issue finding.",
                "session.open,session.trace,visual.observe",
                str(missing_issue_dir / "test-evidence" / "trace-replay.md"),
                "",
                None,
            )
            run.record_finding(
                missing_issue_dir,
                "strength",
                "Live driver preserved proof before issue recording",
                "This run intentionally omits the claimed issue finding to prove finalization fails closed.",
                missing_issue_scenario,
                "low",
                str(missing_issue_dir / "test-evidence" / "oracle_result.md"),
                "observed",
                None,
            )
            run.record_autonomous_driver_dispatch(
                missing_issue_dir,
                "live",
                "captured",
                "Dispatch receipt claims one issue but no credible issue finding was persisted.",
                1,
                1,
                str(missing_issue_dir / "driver-trace.jsonl"),
                "",
            )
            missing_issue_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                missing_issue_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-missing-issue-finding.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(missing_issue_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("captured live driver receipt claiming missing issue stops before no-issue finalization",
                  missing_issue_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_issue_finalized["autonomous_driver_status"] == "inconsistent-counts"
                  and missing_issue_finalized["autonomous_driver_dispatch_status"] == "captured"
                  and missing_issue_finalized["autonomous_fix_status"] == "not_run"
                  and missing_issue_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and missing_issue_finalized["stats_status"] == "not_run"
                  and "issues=0/1" in missing_issue_finalized["autonomous_driver_summary"]
                  and not run.autonomous_marathon_watchdog_path(missing_issue_dir).exists(),
                  failures)

            demo_proof = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-demo-proof",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            demo_proof_dir = Path(demo_proof["run_dir"])
            demo_proof_run_json = run.read_json(demo_proof_dir / "run.json")
            demo_proof_scenario = demo_proof_run_json["scenarios"][0]["id"]
            demo_artifact = demo_proof_dir / "test-evidence" / "demo-placeholder.md"
            demo_artifact.parent.mkdir(parents=True, exist_ok=True)
            demo_artifact.write_text("deterministic placeholder; not product proof\n", encoding="utf-8")
            run.attach_evidence(
                demo_proof_dir,
                demo_proof_scenario,
                "trace-replay",
                str(demo_artifact),
                "captured",
                "demo",
                "demo placeholder evidence for receipt consistency",
                None,
            )
            run.record_driver_event(
                demo_proof_dir,
                demo_proof_scenario,
                "live",
                "captured",
                "Live driver attached only demo evidence, which cannot satisfy autonomous proof.",
                "session.open,session.trace,visual.observe",
                str(demo_artifact),
                "",
                None,
            )
            run.record_finding(
                demo_proof_dir,
                "strength",
                "Demo evidence is visible but not product proof",
                "This run intentionally uses demo evidence to prove finalization fails closed.",
                demo_proof_scenario,
                "low",
                str(demo_artifact),
                "observed",
                None,
            )
            run.record_autonomous_driver_dispatch(
                demo_proof_dir,
                "live",
                "captured",
                "Dispatch receipt claims one captured evidence artifact, but only demo evidence was persisted.",
                1,
                0,
                str(demo_proof_dir / "driver-trace.jsonl"),
                "",
            )
            demo_proof_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                demo_proof_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-demo-proof.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(demo_proof_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("captured live driver receipt backed only by demo evidence stops before final gates",
                  demo_proof_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and demo_proof_finalized["autonomous_driver_status"] == "inconsistent-counts"
                  and demo_proof_finalized["autonomous_driver_dispatch_status"] == "captured"
                  and demo_proof_finalized["autonomous_fix_status"] == "not_run"
                  and demo_proof_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and demo_proof_finalized["stats_status"] == "not_run"
                  and "evidence=0/1" in demo_proof_finalized["autonomous_driver_summary"]
                  and not run.autonomous_marathon_watchdog_path(demo_proof_dir).exists(),
                  failures)

            missing_trace = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-driver-trace",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            missing_trace_dir = Path(missing_trace["run_dir"])
            missing_trace_run_json = run.read_json(missing_trace_dir / "run.json")
            missing_trace_scenario = missing_trace_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(missing_trace_dir, missing_trace_scenario)
            run.record_driver_event(
                missing_trace_dir,
                missing_trace_scenario,
                "live",
                "captured",
                "Live driver captured proof but omitted the reviewable driver task trace.",
                "session.open,session.trace,visual.observe",
                str(missing_trace_dir / "test-evidence" / "trace-replay.md"),
                "",
                None,
            )
            run.record_finding(
                missing_trace_dir,
                "strength",
                "Live driver preserved proof before trace recording",
                "This run intentionally omits the dispatch trace to prove finalization fails closed.",
                missing_trace_scenario,
                "low",
                str(missing_trace_dir / "test-evidence" / "oracle_result.md"),
                "observed",
                None,
            )
            run.record_autonomous_driver_dispatch(
                missing_trace_dir,
                "live",
                "captured",
                "Dispatch receipt counts match persisted proof, but no driver trace was reported.",
                1,
                0,
                "",
                "",
            )
            missing_trace_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                missing_trace_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-missing-driver-trace.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(missing_trace_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("captured live driver receipt without reviewable trace stops before final gates",
                  missing_trace_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_trace_finalized["autonomous_driver_status"] == "missing-trace"
                  and missing_trace_finalized["autonomous_driver_dispatch_status"] == "captured"
                  and missing_trace_finalized["autonomous_fix_status"] == "not_run"
                  and missing_trace_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and missing_trace_finalized["stats_status"] == "not_run"
                  and "reviewable driver trace" in missing_trace_finalized["autonomous_driver_summary"]
                  and not run.autonomous_marathon_watchdog_path(missing_trace_dir).exists(),
                  failures)

            captured_blocker = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-captured-with-blocker",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "live",
                24,
                15,
                45,
                None,
                autonomous_driver_live_profile="test-live",
            )
            captured_blocker_dir = Path(captured_blocker["run_dir"])
            captured_blocker_run_json = run.read_json(captured_blocker_dir / "run.json")
            captured_blocker_scenario = captured_blocker_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(captured_blocker_dir, captured_blocker_scenario)
            run.record_driver_event(
                captured_blocker_dir,
                captured_blocker_scenario,
                "live",
                "captured",
                "Live driver captured proof but also reported a blocker in the dispatch receipt.",
                "session.open,session.trace,visual.observe",
                str(captured_blocker_dir / "test-evidence" / "trace-replay.md"),
                "",
                None,
            )
            run.record_finding(
                captured_blocker_dir,
                "strength",
                "Captured blocker receipt should not pass",
                "This run intentionally reports a blocker with captured status to prove finalization fails closed.",
                captured_blocker_scenario,
                "low",
                str(captured_blocker_dir / "test-evidence" / "oracle_result.md"),
                "observed",
                None,
            )
            run.record_autonomous_driver_dispatch(
                captured_blocker_dir,
                "live",
                "captured",
                "Dispatch receipt has proof but also reports an unresolved blocker.",
                1,
                0,
                "trace://driver/captured-with-blocker",
                "operator intervention required",
            )
            captured_blocker_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                captured_blocker_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(tmp / "gh-agent-captured-with-blocker.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(captured_blocker_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            check("captured live driver receipt with blocker stops before final gates",
                  captured_blocker_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and captured_blocker_finalized["autonomous_driver_status"] == "captured-with-blockers"
                  and captured_blocker_finalized["autonomous_driver_dispatch_status"] == "captured"
                  and captured_blocker_finalized["autonomous_fix_status"] == "not_run"
                  and captured_blocker_finalized["validation_issue_summary"] == "autonomous-driver-dispatch"
                  and captured_blocker_finalized["stats_status"] == "not_run"
                  and "blocker" in captured_blocker_finalized["autonomous_driver_summary"]
                  and not run.autonomous_marathon_watchdog_path(captured_blocker_dir).exists(),
                  failures)

            stale = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-stale-watchdog",
                "bugfix",
                7,
                "o/r",
                "",
                "stories/bugfix",
                healthy_url,
                "",
                "",
                "",
                "none",
                "",
                "",
                "",
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
            )
            stale_dir = Path(stale["run_dir"])
            stale_run_json = run.read_json(stale_dir / "run.json")
            stale_scenario = stale_run_json["scenarios"][0]["id"]
            filing_test.attach_bugfix_proof(stale_dir, stale_scenario)
            run.record_finding(
                stale_dir,
                "issue",
                "Stale marathon must stop before autonomous fixing",
                "The marathon has a credible issue but no driver heartbeat inside the watchdog window.",
                stale_scenario,
                "high",
                str(stale_dir / "test-evidence" / "trace-replay.md"),
                "open",
                None,
            )
            stale_control = run.read_json(run.autonomous_marathon_control_path(stale_dir))
            stale_baseline = stale_control.get("cadence", {}).get("created_at") or stale_run_json["created_at"]
            stale_checked_at = (
                run.parse_iso_datetime(stale_baseline) + run.datetime.timedelta(minutes=47)
            ).isoformat(timespec="seconds")
            stale_db = tmp / "gh-agent-stale-watchdog.json"
            stale_finalized = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                stale_dir,
                "vscode",
                "core-maintainer",
                "ignored",
                "",
                7,
                "o/r",
                str(stale_db),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                str(stale_dir / "autonomous-marathon-stats.json"),
                0.82,
                25,
                "pending",
                24,
                15,
                45,
                None,
                stale_checked_at,
            )
            stale_report_text = Path(stale_finalized["autonomous_marathon_report_path"]).read_text(encoding="utf-8")
            check("marathon finalization enforces watchdog before autonomous fix spend",
                  stale_finalized["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and stale_finalized["autonomous_fix_status"] == "not_run"
                  and stale_finalized["autonomous_watchdog_status"] == "autonomous_watchdog_blocked"
                  and stale_finalized["validation_issue_summary"] == "autonomous-watchdog"
                  and stale_finalized["stats_status"] == "not_run"
                  and "watchdog=fail" in stale_finalized["autonomous_marathon_summary"]
                  and "autonomous-marathon-watchdog.md" in stale_report_text
                  and not stale_db.exists(),
                  failures)

            missing_stats = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-missing-stats",
                "bugfix",
                7,
                "o/r",
                str(tmp / "gh-agent-missing-stats.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(tmp / "empty-stats-root"),
                "",
                0.82,
                25,
                "replay",
                24,
                15,
                45,
                None,
            )
            check("marathon fails closed when derived stats do not cover the run",
                  missing_stats["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and missing_stats["autonomous_fix_status"] == "autonomous_fix_valid"
                  and missing_stats["stats_found_count"] == 0
                  and missing_stats["stats_gate_status"] == "fail"
                  and missing_stats["stats_current_run_scanned"] == "no"
                  and "stats=fail" in missing_stats["autonomous_marathon_summary"],
                  failures)

            unrelated_stats_root = tmp / "unrelated-stats-root"
            unrelated_run = unrelated_stats_root / "run-unrelated"
            unrelated_run.mkdir(parents=True)
            run.write_json(unrelated_run / "findings.json", {
                "items": [
                    {
                        "id": "finding-unrelated",
                        "kind": "issue",
                        "title": "Unrelated fixed persona QA issue",
                        "summary": "This fixed issue belongs to a different run and must not satisfy the current marathon stats gate.",
                        "scenario": "bugfix",
                        "severity": "high",
                        "origin": "observed",
                        "status": "fixed",
                        "github_issue": {
                            "url": "https://github.com/o/r/issues/999",
                            "repo": "o/r",
                            "number": "999",
                            "state": "closed",
                            "comments": [{"body": "kitsoki-fixed-in: unrelated"}],
                        },
                    }
                ]
            })
            unrelated_stats = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-unrelated-stats",
                "bugfix",
                7,
                "o/r",
                str(tmp / "gh-agent-unrelated-stats.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(unrelated_stats_root),
                "",
                0.82,
                25,
                "replay",
                24,
                15,
                45,
                None,
            )
            check("marathon fails closed when aggregate stats omit the current run",
                  unrelated_stats["autonomous_marathon_status"] == "autonomous_marathon_invalid"
                  and unrelated_stats["autonomous_fix_status"] == "autonomous_fix_valid"
                  and unrelated_stats["stats_found_count"] == 1
                  and unrelated_stats["stats_fixed_count"] == 1
                  and unrelated_stats["stats_gate_status"] == "fail"
                  and unrelated_stats["stats_current_run_scanned"] == "no"
                  and "current_run_scanned=no" in unrelated_stats["autonomous_marathon_summary"],
                  failures)

            replay = run.autonomous_marathon(
                catalog,
                github_targets,
                personas,
                scenarios,
                None,
                "vscode",
                "core-maintainer",
                "autonomous-marathon-replay",
                "bugfix",
                7,
                "o/r",
                str(tmp / "gh-agent-replay.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                "",
                str(run.ARTIFACT_ROOT),
                "",
                0.82,
                25,
                "replay",
                24,
                15,
                45,
                None,
            )
            replay_dir = Path(replay["run_dir"])
            replay_report = Path(replay["autonomous_marathon_report_path"])
            replay_report_text = replay_report.read_text(encoding="utf-8") if replay_report.exists() else ""
            check("replay mode creates, captures, and finalizes without external attachment",
                  replay["autonomous_marathon_status"] == "autonomous_marathon_valid"
                  and replay["autonomous_driver_status"] == "captured"
                  and replay["autonomous_driver_evidence_count"] >= 5
                  and replay["autonomous_fix_status"] == "autonomous_fix_valid"
                  and replay["stats_filed_count"] >= 1,
                  failures)
            check("replay mode records armed marathon control",
                  replay["autonomous_control_status"] == "armed"
                  and Path(replay["autonomous_control_path"]).exists()
                  and "manual_glue_steps_target" in Path(replay["autonomous_control_path"]).read_text(encoding="utf-8"),
                  failures)
            check("replay mode leaves human-reviewable driver and fix artifacts",
                  (replay_dir / "driver-journal.md").exists()
                  and "autonomous-replay-evidence" in (replay_dir / "driver-journal.md").read_text(encoding="utf-8")
                  and "autonomous-fix-report.md" in replay_report_text
                  and "Autonomous driver: `replay` / `captured`" in replay_report_text,
                  failures)
        finally:
            if "healthy_server" in locals():
                healthy_server.shutdown()
            if "unhealthy_server" in locals():
                unhealthy_server.shutdown()
            if "wrong_repo_server" in locals():
                wrong_repo_server.shutdown()
            for key, value in old_env.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value

    if failures:
        print("FAIL")
        for failure in failures:
            print(f"- {failure}")
        return 1
    print("PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
