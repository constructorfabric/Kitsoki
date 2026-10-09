#!/usr/bin/env python3
"""No-LLM integration for product-journey -> native gh-agent queue/drain.

This intentionally does not file GitHub issues. It starts with a run bundle
whose credible finding already has a github_issue URL, then drives the real
`kitsoki gh-agent enqueue` and `kitsoki gh-agent drain` commands through the
runner helpers. The drain uses the replay harness and --comment-mode none, so
it exercises native SQLite job state, story dispatch, and asset persistence
without GitHub credentials or LLM cost.
"""

import importlib.util
import json
import os
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def attach_bugfix_proof(run_dir, scenario_id):
    evidence_dir = run_dir / "native-ghagent-evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    refs = []
    for kind in [
        "session_trace",
        "candidate_diff",
        "oracle_result",
        "full_suite_result",
        "key_interaction_video",
        "trace-replay",
    ]:
        suffix = ".mp4" if kind == "key_interaction_video" else ".md"
        artifact = evidence_dir / f"{kind}{suffix}"
        artifact.write_text(f"{kind} proof\n", encoding="utf-8")
        run.attach_evidence(
            run_dir,
            scenario_id,
            kind,
            str(artifact),
            "validated",
            "cassette",
            f"{kind} proof for native gh-agent integration",
            None,
        )
        refs.append(str(artifact))
    run.record_driver_event(
        run_dir,
        scenario_id,
        "replay",
        "validated",
        "Cassette replay produced the bugfix proof artifacts.",
        "story.driver_event,visual.observe",
        ",".join(refs),
        "",
        None,
    )


def mark_filed_issue(run_dir, repo, number):
    findings_path = run_dir / "findings.json"
    findings = run.read_json(findings_path)
    issue_url = f"https://github.com/{repo}/issues/{number}"
    for item in findings["items"]:
        if item.get("kind") == "issue" and item.get("origin", "observed") != "seeded":
            item["github_issue"] = {
                "url": issue_url,
                "number": str(number),
                "repo": repo,
                "filed_at": "2026-07-05T00:00:00+00:00",
            }
            break
    else:
        raise AssertionError("no credible issue finding to mark filed")
    findings["filing"] = {
        "requested": True,
        "ticket_repo": repo,
        "updated_at": "2026-07-05T00:00:00+00:00",
        "filed": 1,
        "skipped": 0,
        "failed": 0,
    }
    run.write_json(findings_path, findings)
    run.update_derived_artifacts(run_dir, None)
    return issue_url


def attach_closeout(run_dir):
    findings = run.read_json(run_dir / "findings.json")
    jobs = {
        job.get("origin_ref"): job
        for job in findings.get("gh_agent", {}).get("drained_jobs", [])
        if job.get("state") == "done"
    }
    items = []
    for item in findings.get("items", []):
        issue = item.get("github_issue", {})
        if item.get("kind") != "issue" or item.get("origin") == "seeded" or not issue.get("url"):
            continue
        origin = f"github:{issue.get('repo')}/issue/{issue.get('number')}"
        job = jobs.get(origin, {})
        comment_url = f"{issue['url']}#issuecomment-kitsoki-fixed-in"
        issue["state"] = "closed"
        issue["status"] = "closed"
        issue["closed_by"] = "kitsoki gitops autonomous-fix"
        issue["closeout_comment_url"] = comment_url
        issue.setdefault("comments", []).append({
            "body": "kitsoki-fixed-in\nindependent-verify.md",
            "url": comment_url,
        })
        item["status"] = "fixed"
        items.append({
            "finding_id": item.get("id", ""),
            "issue_url": issue["url"],
            "repo": issue.get("repo", ""),
            "number": issue.get("number", ""),
            "comment_url": comment_url,
            "run_url": job.get("run_url", ""),
            "job_id": job.get("job_id", ""),
            "closed": True,
        })
    findings["issue_closeout"] = {
        "status": "closed",
        "count": len(items),
        "summary": f"Closed {len(items)} fixed GitHub issue(s).",
        "items": items,
        "errors": [],
    }
    run.write_json(run_dir / "findings.json", findings)
    run.update_derived_artifacts(run_dir, None)


def deck_scene(deck, eyebrow):
    for scene in deck.get("scenes", []):
        if scene.get("eyebrow") == eyebrow:
            return scene
    return {}


def review_check(review, check_id):
    for check in review.get("checks", []):
        if check.get("id") == check_id:
            return check
    return {}


def main():
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)
        os.environ.pop("KITSOKI_BIN", None)

        catalog = run.load_catalog(run.CATALOG)
        personas = run.load_personas(run.PERSONAS)
        scenarios = [
            scenario for scenario in run.load_scenarios(run.SCENARIOS)
            if scenario.get("id") == "bugfix"
        ]
        run_dir, run_json = run.build_run_bundle(
            catalog,
            run.load_github_targets(run.GITHUB_TARGETS),
            personas,
            scenarios,
            "vscode",
            "",
            "native-ghagent-test",
            "dry-run",
            None,
        )
        scenario_id = run_json["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir, scenario_id)
        run.record_finding(
            run_dir,
            "issue",
            "native gh-agent credible",
            "observed problem for native gh-agent integration",
            scenario_id,
            "high",
            "",
            "open",
            None,
        )
        issue_url = mark_filed_issue(run_dir, "o/r", "501")

        db_path = tmp / "gh-agent.sqlite"
        asset_dir = tmp / "gh-agent-assets"
        enqueued = run.enqueue_gh_agent_fixes(run_dir, "o/r", str(db_path), "stories/bugfix")
        check("native enqueue queued one job",
              enqueued["gh_agent_enqueue_status"] == "queued"
              and enqueued["gh_agent_enqueued_count"] == 1)
        drained = run.drain_gh_agent_fixes(
            str(db_path),
            "o/r",
            "https://agent.example",
            "",
            "",
            str(asset_dir),
            "none",
        )
        combined = {**enqueued, **drained}
        combined.update({
            "status": "native_ghagent_smoke",
            "ticket_repo": "o/r",
            "autonomous_gate_summary": "filing=pass, gh_agent=pass, independent_verify=pass, review=pending, validation=pending",
        })
        run.record_gh_agent_findings_status(run_dir, combined)
        attach_closeout(run_dir)
        report = run.write_autonomous_fix_report(run_dir, combined)
        run.update_derived_artifacts(run_dir, None)
        reviewed = run.review_run_bundle(run_dir, None)
        validated = run.validate_run_bundle(run_dir)

        findings = run.read_json(run_dir / "findings.json")
        gh_agent = findings.get("gh_agent", {})
        links = run.gh_agent_fix_evidence_links(gh_agent)
        triage_links = run.gh_agent_triage_evidence_links(gh_agent)
        check("native drain completed",
              drained["gh_agent_drain_status"] == "drained"
              and drained["gh_agent_done_count"] == 1
              and drained["gh_agent_failed_count"] == 0)
        check("native drain exposed fix artifacts",
              any(link.endswith("/fix-report.md") for link in links))
        check("native drain exposed triage verdict",
              any(link.endswith("/triage-verdict.md") for link in triage_links))
        check("native drain exposed independent verification",
              any(link.endswith("/independent-verify.md") for link in links))
        check("native drain exposed integration landing proof",
              all(
                  str(job.get("integration_branch", "")).startswith("integration/")
                  and str(job.get("commit_sha", "")).strip()
                  for job in gh_agent.get("drained_jobs", [])
                  if isinstance(job, dict) and job.get("state") == "done"
              ))
        check("native review passes gh-agent evidence checks",
              review_check(reviewed, "gh-agent-fix-evidence").get("status") == "pass"
              and review_check(reviewed, "gh-agent-triage-evidence").get("status") == "pass"
              and review_check(reviewed, "gh-agent-independent-verify").get("status") == "pass"
              and review_check(reviewed, "gh-agent-integration-landing").get("status") == "pass")
        check("native review carries close-out evidence",
              review_check(reviewed, "issue-closeout").get("status") == "pass")
        report_text = report.read_text()
        check("native report contains filed issue and fix evidence",
              issue_url in report_text
              and "https://agent.example/run/" in report_text
              and any(link in report_text for link in triage_links)
              and any(link in report_text for link in links))
        deck = run.read_json(run_dir / "deck.slidey.json")
        gh_scene = deck_scene(deck, "GH-agent fixes")
        check("deck contains filed issue and native artifact links",
              issue_url in gh_scene.get("body", "")
              and any(link in gh_scene.get("body", "") for link in links))

    print("PASS")


if __name__ == "__main__":
    main()
