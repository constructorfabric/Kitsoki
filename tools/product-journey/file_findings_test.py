#!/usr/bin/env python3
"""Runner-level test for --file-findings: wiring, idempotence, and gates.

Run directly:  python3 tools/product-journey/file_findings_test.py

Body assembly and the real GitHub orchestration live in Go
(host.GitHubFileFindings, unit-tested in internal/host/github_findings_test.go
with a stubbed gh runner). This test covers the runner side with a fake
KITSOKI_BIN so nothing calls gh, GitHub, or an LLM:

  1. dry-run leaves the bundle untouched, reports candidates, and keeps
     credible issue findings blocked from final review,
  2. filing records issue URLs + the filing block and refreshes derived
     artifacts,
  3. a re-run skips already-filed findings (idempotent),
  4. once filing was requested, review gains native filing and gh-agent gates
     and validate errors on credible-but-unfiled findings or missing fix
     evidence.
"""

import importlib.util
import json
import os
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)

FAKE_KITSOKI = r'''#!/usr/bin/env python3
"""Fake `kitsoki bug file-findings` / `gh-agent enqueue` used by file_findings_test.py.

Mirrors the Go orchestration's bundle contract: file each credible unfiled
issue finding (record github_issue), stamp findings.filing, print the JSON
result. --dry-run touches nothing.
"""
import argparse, json, os, sys
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("verb1")
parser.add_argument("verb2")
parser.add_argument("--run-dir")
parser.add_argument("--repo")
parser.add_argument("--dry-run", action="store_true")
parser.add_argument("--db")
parser.add_argument("--issue")
parser.add_argument("--kind", default="issue")
parser.add_argument("--story", default="stories/bugfix")
parser.add_argument("--public-base-url", default="")
parser.add_argument("--project-root", default="")
parser.add_argument("--incident-repo", default="")
parser.add_argument("--asset-dir", default="")
parser.add_argument("--comment-mode", default="")
parser.add_argument("--json", action="store_true")
args = parser.parse_args()
if (args.verb1, args.verb2) == ("gh-agent", "drain"):
    assert args.db
    assert args.asset_dir, "product-journey must pass --asset-dir to gh-agent drain"
    assert args.comment_mode == "none", "product-journey drain tests must stay offline with --comment-mode none"
    db_path = Path(args.db)
    rows = json.loads(db_path.read_text()) if db_path.exists() else []
    jobs = []
    for i, row in enumerate(rows, start=1):
        row["state"] = "done"
        if os.environ.get("KITSOKI_FAKE_GH_AGENT_NO_RUN_URL"):
            row["run_url"] = ""
        else:
            row["run_url"] = f"https://agent.example/run/job-{i}"
        row.setdefault("job_id", f"job-{i}")
        integration_branch = f"integration/kitsoki-autofix-job-{i}"
        commit_sha = f"abc123{i}"
        jobs.append({
            "job_id": row["job_id"],
            "origin_ref": row["origin_ref"],
            "repo": row["origin_ref"].split("/issue/")[0].removeprefix("github:"),
            "object_kind": "issue",
            "object_number": row["origin_ref"].split("/")[-1],
            "story": row["story"],
            "state": row["state"],
            "run_url": row["run_url"],
            "integration_branch": integration_branch,
            "commit_sha": commit_sha,
            "commit_url": f"https://github.com/{row['origin_ref'].split('/issue/')[0].removeprefix('github:')}/commit/{commit_sha}",
            "incident_url": "",
            "err_msg": "",
            "assets": [] if os.environ.get("KITSOKI_FAKE_GH_AGENT_NO_ASSETS") else [
                {
                    "name": "fix-report.md",
                    "mime_type": "text/markdown",
                    "size_bytes": 128,
                    "url": f"https://agent.example/run/job-{i}/artifacts/fix-report.md",
                },
                *([] if os.environ.get("KITSOKI_FAKE_GH_AGENT_NO_TRIAGE") else [{
                    "name": "triage-verdict.md",
                    "mime_type": "text/markdown",
                    "size_bytes": 160,
                    "url": f"https://agent.example/run/job-{i}/artifacts/triage-verdict.md",
                }]),
                *([] if os.environ.get("KITSOKI_FAKE_GH_AGENT_NO_VERIFY") else [{
                    "name": "independent-verify.md",
                    "mime_type": "text/markdown",
                    "size_bytes": 192,
                    "url": f"https://agent.example/run/job-{i}/artifacts/independent-verify.md",
                }]),
                {
                    "name": "fix.patch",
                    "mime_type": "text/x-diff",
                    "size_bytes": 256,
                    "url": f"https://agent.example/run/job-{i}/artifacts/fix.patch",
                },
            ],
        })
    db_path.write_text(json.dumps(rows, indent=2, sort_keys=True) + "\n")
    print(json.dumps({
        "status": "drained",
        "drained_count": len(jobs),
        "done_count": len(jobs),
        "failed_count": 0,
        "active_count": 0,
        "jobs": jobs,
    }))
    sys.exit(0)
if (args.verb1, args.verb2) == ("gh-agent", "enqueue"):
    assert args.db and args.repo and args.issue
    origin = f"github:{args.repo}/{args.kind}/{args.issue}"
    db_path = Path(args.db)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    rows = []
    if db_path.exists():
        rows = json.loads(db_path.read_text())
    created = origin not in [row["origin_ref"] for row in rows]
    if created:
        rows.append({"job_id": "job-" + args.issue, "origin_ref": origin, "story": args.story, "state": "queued"})
        db_path.write_text(json.dumps(rows, indent=2, sort_keys=True) + "\n")
    print(json.dumps({
        "status": "queued",
        "created": created,
        "job_id": "job-" + args.issue,
        "origin_ref": origin,
        "repo": args.repo,
        "object_kind": args.kind,
        "object_number": args.issue,
        "story": args.story,
        "state": "queued",
    }))
    sys.exit(0)
assert (args.verb1, args.verb2) == ("bug", "file-findings"), (args.verb1, args.verb2)
assert args.run_dir and args.repo

path = Path(args.run_dir) / "findings.json"
findings = json.loads(path.read_text())
outcomes, filed, skipped = [], 0, 0
for i, item in enumerate(findings.get("items", []), start=1):
    if item.get("kind") != "issue" or item.get("origin", "observed") == "seeded":
        continue
    if item.get("github_issue", {}).get("url"):
        skipped += 1
        outcomes.append({"finding_id": item.get("id", ""), "status": "skipped",
                         "issue_url": item["github_issue"]["url"]})
        continue
    if args.dry_run:
        outcomes.append({"finding_id": item.get("id", ""), "status": "dry-run",
                         "body": "## Expected\n...\n## Actual\n...\n## Reproduction\n..."})
        continue
    filed += 1
    url = f"https://github.com/{args.repo}/issues/{100 + i}"
    item["github_issue"] = {"url": url, "number": str(100 + i), "repo": args.repo,
                            "filed_at": "2026-07-05T00:00:00+00:00",
                            "evidence_assets": [
                                {
                                    "name": "trace-replay.md",
                                    "url": f"https://github.com/{args.repo}/releases/download/kitsoki-artifacts/finding-{i}-trace-replay.md",
                                }
                            ]}
    outcomes.append({"finding_id": item.get("id", ""), "status": "filed", "issue_url": url})
if not args.dry_run:
    findings["filing"] = {"requested": True, "ticket_repo": args.repo,
                          "updated_at": "2026-07-05T00:00:00+00:00",
                          "filed": filed, "skipped": skipped, "failed": 0}
    path.write_text(json.dumps(findings, indent=2, sort_keys=True) + "\n")
print(json.dumps({
    "status": "findings_dry_run" if args.dry_run else "findings_filed",
    "ticket_repo": args.repo, "run_dir": args.run_dir,
    "dry_run": args.dry_run,
    "filed": filed, "skipped": skipped, "failed": 0,
    "outcomes": outcomes,
}))
'''


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def review_check(review_result, check_id):
    for check in review_result["checks"]:
        if check["id"] == check_id:
            return check
    return None


def deck_scene(deck, eyebrow):
    for scene in deck.get("scenes", []):
        if scene.get("eyebrow") == eyebrow:
            return scene
    return {}


def attach_bugfix_proof(run_dir, scenario_id, record_driver=True):
    evidence_dir = run_dir / "test-evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    evidence_kinds = [
        "session_trace",
        "candidate_diff",
        "oracle_result",
        "full_suite_result",
        "key_interaction_video",
        "trace-replay",
    ]
    refs = []
    for kind in evidence_kinds:
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
            f"{kind} proof for autonomous fix test",
            None,
        )
        refs.append(str(artifact))
    if record_driver:
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
    return refs


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


def main():
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        # Keep every artifact the runner writes inside the tempdir.
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)

        fake = tmp / "fake_kitsoki.py"
        fake.write_text(FAKE_KITSOKI, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
        os.environ["KITSOKI_BIN"] = f"{sys.executable} {fake}"

        catalog = run.load_catalog(run.CATALOG)
        personas = run.load_personas(run.PERSONAS)
        scenarios = run.load_scenarios(run.SCENARIOS)
        run_dir, run_json = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, scenarios, "vscode", "", "file-findings-test", "dry-run", None,
        )
        scenario_id = run_json["scenarios"][0]["id"]

        # Two credible issue findings + one seeded issue + one strength +
        # one weakness routed to PRD/design instead of the bugfix queue.
        run.record_finding(run_dir, "issue", "credible one", "observed problem one",
                           scenario_id, "high", "", "open", None)
        run.record_finding(run_dir, "issue", "credible two", "observed problem two",
                           scenario_id, "medium", "", "open", None)
        run.record_finding(run_dir, "issue", "seeded demo issue", "harness-only",
                           scenario_id, "low", "", "open", None, origin="seeded")
        run.record_finding(run_dir, "strength", "nice deck", "deck renders",
                           scenario_id, "low", "", "observed", None)
        run.record_finding(run_dir, "weakness", "confusing design prompt", "The persona could not tell which PRD path to use.",
                           scenario_id, "medium", "driver-plan.md", "open", None)

        # 1. Dry-run: candidates reported, bundle untouched, gates fail closed.
        before = (run_dir / "findings.json").read_text()
        result = run.file_findings(run_dir, "o/r", True, None)
        _check("dry-run status", result["status"] == "findings_dry_run")
        _check("dry-run reports 2 candidates",
               sum(1 for o in result["outcomes"] if o["status"] == "dry-run") == 2)
        _check("dry-run leaves findings.json untouched",
               (run_dir / "findings.json").read_text() == before)
        reviewed = run.review_run_bundle(run_dir, None)
        _check("credible findings require filing before review",
               review_check(reviewed, "findings-filed")["status"] == "fail"
               and "unfiled:" in review_check(reviewed, "findings-filed")["detail"])
        _check("credible findings require autonomous fixes before review",
               review_check(reviewed, "gh-agent-fixes")["status"] == "fail"
               and "require autonomous_fix" in review_check(reviewed, "gh-agent-fixes")["detail"])
        _check("credible findings require autonomous report before review",
               review_check(reviewed, "autonomous-fix-report")["status"] == "fail"
               and "require autonomous_fix" in review_check(reviewed, "autonomous-fix-report")["detail"])
        validated = run.validate_run_bundle(run_dir)
        _check("validate blocks unfiled credible findings",
               any(i["id"] == "findings-filed" and i["severity"] == "error"
                   for i in validated["issues"]))
        _check("validate blocks missing autonomous fixes",
               any(i["id"] == "gh-agent-fixes" and i["severity"] == "error"
                   for i in validated["issues"]))

        no_debug_proc = subprocess.run(
            [
                sys.executable, "tools/product-journey/run.py",
                "--file-findings",
                "--json-output",
                "--run-dir", str(run_dir),
                "--ticket-repo", "o/r",
                "--filing-mode", "file",
            ],
            cwd=run.ROOT,
            env=os.environ.copy(),
            text=True,
            capture_output=True,
            check=False,
        )
        _check("direct real file_findings requires debug opt-in",
               no_debug_proc.returncode != 0
               and "--debug-file" in (no_debug_proc.stderr + no_debug_proc.stdout))

        # 2. Filing: URLs + filing block recorded, derived artifacts refreshed.
        gh_agent_db = tmp / "gh-agent-jobs.json"
        result = run.file_findings(run_dir, "o/r", False, None, str(gh_agent_db), "stories/bugfix", True, "https://agent.example", "", "")
        _check("filed 2 credible findings", result["findings_filed_count"] == 2)
        _check("no credible finding left unfiled", result["findings_unfiled_count"] == 0)
        _check("filed urls surface", len(result["filed_issue_urls"]) == 2
               and all(u.startswith("https://github.com/o/r/issues/") for u in result["filed_issue_urls"]))
        _check("filed findings queued for gh-agent fixes",
               result["gh_agent_enqueue_status"] == "queued"
               and result["gh_agent_enqueued_count"] == 2
               and result["gh_agent_skipped_count"] == 0)
        _check("queued fixes drained by gh-agent",
               result["gh_agent_drain_status"] == "drained"
               and result["gh_agent_done_count"] == 2
               and result["gh_agent_failed_count"] == 0)
        _check("gh-agent run summary surfaces review links",
               "https://agent.example/run/job-1" in result["gh_agent_run_summary"])
        _check("gh-agent fix evidence fields are reported",
               result["gh_agent_fix_evidence_count"] == 8
               and result["gh_agent_missing_evidence_count"] == 0
               and "https://agent.example/run/job-1/artifacts/fix-report.md" in result["gh_agent_fix_evidence_summary"]
               and result["gh_agent_triage_evidence_count"] == 2
               and result["gh_agent_missing_triage_count"] == 0
               and "https://agent.example/run/job-1/artifacts/triage-verdict.md" in result["gh_agent_triage_evidence_summary"]
               and result["gh_agent_independent_verify_count"] == 2
               and result["gh_agent_missing_verify_count"] == 0
               and "https://agent.example/run/job-1/artifacts/independent-verify.md" in result["gh_agent_independent_verify_summary"])
        queued_rows = json.loads(gh_agent_db.read_text())
        _check("gh-agent queue uses issue origin refs",
               sorted(row["origin_ref"] for row in queued_rows)
               == ["github:o/r/issue/101", "github:o/r/issue/102"])
        findings = run.read_json(run_dir / "findings.json")
        _check("filing block recorded", findings["filing"]["requested"] is True
               and findings["filing"]["ticket_repo"] == "o/r")
        first_issue_assets = findings["items"][0]["github_issue"].get("evidence_assets", [])
        _check("filed issue evidence assets are retained for review",
               first_issue_assets
               and first_issue_assets[0]["name"] == "trace-replay.md"
               and first_issue_assets[0]["url"].startswith("https://github.com/o/r/releases/download/kitsoki-artifacts/"))
        deck = run.read_json(run_dir / "deck.slidey.json")
        gh_scene = deck_scene(deck, "GH-agent fixes")
        _check("deck has gh-agent fix review scene", bool(gh_scene))
        _check("deck includes filed issues and fix run URLs",
               "https://github.com/o/r/issues/101" in gh_scene.get("body", "")
               and "https://agent.example/run/job-1" in gh_scene.get("body", ""))
        _check("deck includes filed issue evidence assets",
               "issue_evidence=" in gh_scene.get("body", "")
               and "https://github.com/o/r/releases/download/kitsoki-artifacts/finding-1-trace-replay.md" in gh_scene.get("body", ""))
        _check("deck includes autonomous report and independent verification links",
               "autonomous-fix-report.md" in gh_scene.get("body", "")
               and "independent_verify=" in gh_scene.get("body", "")
               and "https://agent.example/run/job-1/artifacts/independent-verify.md" in gh_scene.get("body", ""))
        _check("deck includes gh-agent triage evidence links",
               "triage=" in gh_scene.get("body", "")
               and "https://agent.example/run/job-1/artifacts/triage-verdict.md" in gh_scene.get("body", ""))
        _check("deck includes gh-agent fix evidence links",
               "https://agent.example/run/job-1/artifacts/fix-report.md" in gh_scene.get("body", "")
               and "https://agent.example/run/job-1/artifacts/fix.patch" in gh_scene.get("body", ""))
        routes = run.read_json(run_dir / "weakness-routes.json")
        intake = run.read_json(run_dir / "prd-design-intake.json")
        _check("weakness finding routes to PRD/design",
               routes["summary"]["routed"] == 1
               and routes["items"][0]["target_story"] == "stories/prd"
               and routes["items"][0]["target_pipeline"] == "prd-design")
        _check("weakness route has PRD/design intake",
               intake["summary"]["intake_count"] == 1
               and intake["items"][0]["target_story"] == "stories/prd"
               and intake["items"][0]["story_intent"] == "start"
               and "confusing design prompt" in intake["items"][0]["story_slots"]["idea"]
               and "weakness-routes.md" in intake["items"][0]["story_slots"]["upstream_paths"]
               and intake["items"][0]["persona_lens"]["starting_surface"],
               )
        route_scene = deck_scene(deck, "PRD/design routes")
        _check("deck includes PRD/design route scene",
               "confusing design prompt" in route_scene.get("body", "")
               and "stories/prd" in route_scene.get("body", "")
               and "prd-design-intake.md" in route_scene.get("body", ""))
        seeded = [i for i in findings["items"] if i.get("origin") == "seeded"]
        _check("seeded finding not filed", not seeded[0].get("github_issue"))

        # 3. Idempotence: a re-run files nothing new.
        result = run.file_findings(run_dir, "o/r", False, None, str(gh_agent_db), "stories/bugfix", True, "https://agent.example", "", "")
        _check("re-run files nothing", result["findings_filed_count"] == 0)
        _check("re-run skips already-filed", result["findings_skipped_count"] == 2)
        _check("re-run attaches to queued fix jobs",
               result["gh_agent_enqueued_count"] == 2
               and not any(job["created"] for job in result["gh_agent_jobs"]))
        findings2 = run.read_json(run_dir / "findings.json")
        urls = sorted(i["github_issue"]["url"] for i in findings2["items"] if i.get("github_issue"))
        _check("urls stable across re-runs", len(urls) == 2 and len(set(urls)) == 2)

        # 4. Gates: review counts filing, gh-agent, and close-out checks; a
        # direct file_findings run is not complete until native gitops closes
        # the fixed issues.
        reviewed = run.review_run_bundle(run_dir, None)
        _check("review has 33 checks", reviewed["total"] == 33)
        _check("direct filing requires issue close-out before final review",
               review_check(reviewed, "issue-closeout")["status"] == "fail"
               and "status=(missing)" in review_check(reviewed, "issue-closeout")["detail"])
        validated = run.validate_run_bundle(run_dir)
        _check("validate blocks missing issue close-out",
               any(i["id"] == "issue-closeout" and i["severity"] == "error"
                   for i in validated["issues"]))
        attach_closeout(run_dir)

        # 5. Gates: a new credible finding
        # after filing trips review + validate until re-filed.
        reviewed = run.review_run_bundle(run_dir, None)
        _check("review has 33 checks after close-out", reviewed["total"] == 33)
        _check("weakness-routing passes when weakness has PRD route",
               review_check(reviewed, "weakness-routing")["status"] == "pass")
        _check("prd-design-intake passes when weakness has PRD intake",
               review_check(reviewed, "prd-design-intake")["status"] == "pass")
        _check("findings-filed passes when fully filed",
               review_check(reviewed, "findings-filed")["status"] == "pass")
        _check("issue-closeout passes when fixed issues are closed",
               review_check(reviewed, "issue-closeout")["status"] == "pass")
        _check("gh-agent-fixes passes when drained",
               review_check(reviewed, "gh-agent-fixes")["status"] == "pass")
        _check("gh-agent-fix-evidence passes when assets are present",
               review_check(reviewed, "gh-agent-fix-evidence")["status"] == "pass")
        _check("gh-agent-triage-evidence passes when triage artifacts are present",
               review_check(reviewed, "gh-agent-triage-evidence")["status"] == "pass")
        _check("gh-agent-independent-verify passes when verify artifacts are present",
               review_check(reviewed, "gh-agent-independent-verify")["status"] == "pass")
        _check("gh-agent-run-url passes when run URLs are present",
               review_check(reviewed, "gh-agent-run-url")["status"] == "pass")
        _check("gh-agent-integration-landing passes when branch and commit are present",
               review_check(reviewed, "gh-agent-integration-landing")["status"] == "pass")
        validated = run.validate_run_bundle(run_dir)
        _check("validate has no findings-filed error",
               not any(i["id"] == "findings-filed" for i in validated["issues"]))
        _check("validate has no gh-agent evidence error",
               not any(i["id"] in {"gh-agent-fixes", "gh-agent-fix-evidence", "gh-agent-triage-evidence", "gh-agent-independent-verify", "gh-agent-run-url", "gh-agent-integration-landing", "issue-closeout", "gh-agent-fix-deck"} for i in validated["issues"]))
        saved_findings = run.read_json(run_dir / "findings.json")
        missing_asset_findings = json.loads(json.dumps(saved_findings))
        for job in missing_asset_findings["gh_agent"]["drained_jobs"]:
            job["assets"] = []
        run.write_json(run_dir / "findings.json", missing_asset_findings)
        reviewed_missing_assets = run.review_run_bundle(run_dir, None)
        _check("review fails done gh-agent jobs without evidence assets",
               review_check(reviewed_missing_assets, "gh-agent-fix-evidence")["status"] == "fail")
        missing_asset_validated = run.validate_run_bundle(run_dir)
        _check("validate catches done gh-agent jobs without evidence assets",
               any(i["id"] == "gh-agent-fix-evidence" and i["severity"] == "error"
                   for i in missing_asset_validated["issues"]))
        missing_triage_findings = json.loads(json.dumps(saved_findings))
        for job in missing_triage_findings["gh_agent"]["drained_jobs"]:
            job["assets"] = [
                asset for asset in job.get("assets", [])
                if asset.get("name") != "triage-verdict.md"
            ]
        run.write_json(run_dir / "findings.json", missing_triage_findings)
        reviewed_missing_triage = run.review_run_bundle(run_dir, None)
        _check("review fails done gh-agent jobs without triage evidence",
               review_check(reviewed_missing_triage, "gh-agent-triage-evidence")["status"] == "fail")
        missing_triage_validated = run.validate_run_bundle(run_dir)
        _check("validate catches done gh-agent jobs without triage evidence",
               any(i["id"] == "gh-agent-triage-evidence" and i["severity"] == "error"
                   for i in missing_triage_validated["issues"]))
        missing_verify_findings = json.loads(json.dumps(saved_findings))
        for job in missing_verify_findings["gh_agent"]["drained_jobs"]:
            job["assets"] = [
                asset for asset in job.get("assets", [])
                if asset.get("name") != "independent-verify.md"
            ]
        run.write_json(run_dir / "findings.json", missing_verify_findings)
        reviewed_missing_verify = run.review_run_bundle(run_dir, None)
        _check("review fails done gh-agent jobs without independent verification",
               review_check(reviewed_missing_verify, "gh-agent-independent-verify")["status"] == "fail")
        missing_verify_validated = run.validate_run_bundle(run_dir)
        _check("validate catches done gh-agent jobs without independent verification",
               any(i["id"] == "gh-agent-independent-verify" and i["severity"] == "error"
                   for i in missing_verify_validated["issues"]))
        missing_run_url_findings = json.loads(json.dumps(saved_findings))
        for job in missing_run_url_findings["gh_agent"]["drained_jobs"]:
            job["run_url"] = ""
        run.write_json(run_dir / "findings.json", missing_run_url_findings)
        reviewed_missing_run_url = run.review_run_bundle(run_dir, None)
        _check("review fails done gh-agent jobs without run URLs",
               review_check(reviewed_missing_run_url, "gh-agent-run-url")["status"] == "fail")
        missing_run_url_validated = run.validate_run_bundle(run_dir)
        _check("validate catches done gh-agent jobs without run URLs",
               any(i["id"] == "gh-agent-run-url" and i["severity"] == "error"
                   for i in missing_run_url_validated["issues"]))
        missing_landing_findings = json.loads(json.dumps(saved_findings))
        for job in missing_landing_findings["gh_agent"]["drained_jobs"]:
            job.pop("integration_branch", None)
            job.pop("commit_sha", None)
            job.pop("commit_url", None)
        run.write_json(run_dir / "findings.json", missing_landing_findings)
        reviewed_missing_landing = run.review_run_bundle(run_dir, None)
        _check("review fails done gh-agent jobs without integration landing proof",
               review_check(reviewed_missing_landing, "gh-agent-integration-landing")["status"] == "fail")
        missing_landing_validated = run.validate_run_bundle(run_dir)
        _check("validate catches done gh-agent jobs without integration landing proof",
               any(i["id"] == "gh-agent-integration-landing" and i["severity"] == "error"
                   for i in missing_landing_validated["issues"]))
        run.write_json(run_dir / "findings.json", saved_findings)
        run.update_derived_artifacts(run_dir, None)
        deck_path = run_dir / "deck.slidey.json"
        original_issue_evidence_url = saved_findings["items"][0]["github_issue"]["evidence_assets"][0]["url"]
        stale_deck = run.read_json(deck_path)
        for scene in stale_deck["scenes"]:
            if scene.get("eyebrow") == "GH-agent fixes":
                scene["body"] = scene.get("body", "").replace("https://agent.example/run/job-1/artifacts/fix-report.md", "")
                break
        run.write_json(deck_path, stale_deck)
        stale_validated = run.validate_run_bundle(run_dir)
        _check("validate catches missing gh-agent evidence URL in deck",
               any(i["id"] == "gh-agent-fix-deck" and i["severity"] == "error"
                   for i in stale_validated["issues"]))
        run.update_derived_artifacts(run_dir, None)
        stale_deck = run.read_json(deck_path)
        for scene in stale_deck["scenes"]:
            if scene.get("eyebrow") == "GH-agent fixes":
                scene["body"] = scene.get("body", "").replace(original_issue_evidence_url, "")
                break
        run.write_json(deck_path, stale_deck)
        stale_validated = run.validate_run_bundle(run_dir)
        _check("validate catches missing filed issue evidence URL in deck",
               any(i["id"] == "gh-agent-fix-deck" and i["severity"] == "error"
                   for i in stale_validated["issues"]))
        run.update_derived_artifacts(run_dir, None)
        stale_deck = run.read_json(deck_path)
        for scene in stale_deck["scenes"]:
            if scene.get("eyebrow") == "GH-agent fixes":
                scene["body"] = scene.get("body", "").replace("autonomous-fix-report.md", "")
                break
        run.write_json(deck_path, stale_deck)
        stale_validated = run.validate_run_bundle(run_dir)
        _check("validate catches missing autonomous report link in deck",
               any(i["id"] == "gh-agent-fix-deck" and i["severity"] == "error"
                   for i in stale_validated["issues"]))
        run.update_derived_artifacts(run_dir, None)
        stale_deck = run.read_json(deck_path)
        for scene in stale_deck["scenes"]:
            if scene.get("eyebrow") == "GH-agent fixes":
                scene["body"] = scene.get("body", "").replace("independent_verify=", "")
                break
        run.write_json(deck_path, stale_deck)
        stale_validated = run.validate_run_bundle(run_dir)
        _check("validate catches missing independent verification label in deck",
               any(i["id"] == "gh-agent-fix-deck" and i["severity"] == "error"
                   for i in stale_validated["issues"]))
        run.update_derived_artifacts(run_dir, None)
        closeout_url = saved_findings["issue_closeout"]["items"][0]["comment_url"]
        stale_deck = run.read_json(deck_path)
        for scene in stale_deck["scenes"]:
            if scene.get("eyebrow") == "GH-agent fixes":
                scene["body"] = scene.get("body", "").replace(closeout_url, "")
                break
        run.write_json(deck_path, stale_deck)
        stale_validated = run.validate_run_bundle(run_dir)
        _check("validate catches missing issue close-out URL in deck",
               any(i["id"] == "gh-agent-fix-deck" and i["severity"] == "error"
                   for i in stale_validated["issues"]))
        run.update_derived_artifacts(run_dir, None)

        run.record_finding(run_dir, "issue", "late credible", "found after filing",
                           scenario_id, "high", "", "open", None)
        reviewed = run.review_run_bundle(run_dir, None)
        _check("late finding fails the review gate",
               review_check(reviewed, "findings-filed")["status"] == "fail")
        validated = run.validate_run_bundle(run_dir)
        _check("late finding is a validate error",
               any(i["id"] == "findings-filed" and i["severity"] == "error"
                   for i in validated["issues"]))

        # Re-file closes the gate again.
        run.file_findings(run_dir, "o/r", False, None, str(gh_agent_db), "stories/bugfix", True, "https://agent.example", "", "")
        reviewed = run.review_run_bundle(run_dir, None)
        _check("re-filing closes the gate",
               review_check(reviewed, "findings-filed")["status"] == "pass")

        # 6. Credible issue findings require a story-owned driver receipt
        # before the autonomous issue-to-fix gate can be trusted.
        stable_scenarios = [scenario for scenario in scenarios if scenario.get("id") == "bugfix"]
        run_dir_receipt, run_json_receipt = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "driver-receipt-test", "dry-run", None,
        )
        scenario_receipt = run_json_receipt["scenarios"][0]["id"]
        receipt_refs = attach_bugfix_proof(run_dir_receipt, scenario_receipt, record_driver=False)
        run.record_finding(run_dir_receipt, "issue", "receipt credible", "observed problem",
                           scenario_receipt, "high", "", "open", None)
        reviewed_receipt_gap = run.review_run_bundle(run_dir_receipt, None)
        _check("credible issue review requires driver receipt",
               review_check(reviewed_receipt_gap, "credible-issue-driver-receipts")["status"] == "fail"
               and "missing captured-or-validated driver event" in review_check(reviewed_receipt_gap, "credible-issue-driver-receipts")["detail"])
        validated_receipt_gap = run.validate_run_bundle(run_dir_receipt)
        _check("validate catches credible issue without driver receipt",
               any(i["id"] == "credible-issue-driver-receipts" and i["severity"] == "error"
                   for i in validated_receipt_gap["issues"]))
        run.record_driver_event(
            run_dir_receipt,
            scenario_receipt,
            "replay",
            "validated",
            "Cassette replay produced the credible issue proof artifacts.",
            "story.driver_event,visual.observe",
            ",".join(receipt_refs),
            "",
            None,
        )
        reviewed_receipt_ok = run.review_run_bundle(run_dir_receipt, None)
        _check("credible issue driver receipt gate passes with proof refs",
               review_check(reviewed_receipt_ok, "credible-issue-driver-receipts")["status"] == "pass")
        validated_receipt_ok = run.validate_run_bundle(run_dir_receipt)
        _check("validate accepts credible issue driver receipt",
               not any(i["id"] == "credible-issue-driver-receipts" and i["severity"] == "error"
                       for i in validated_receipt_ok["issues"]))

        # 7. The legacy Python composite loop owns filing and gh-agent drain,
        # but native gitops is required for final issue close-out.
        run_dir2, run_json2 = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "autonomous-fix-test", "dry-run", None,
        )
        scenario2 = run_json2["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir2, scenario2)
        run.record_finding(run_dir2, "issue", "autonomous credible", "observed problem",
                           scenario2, "high", "", "open", None)
        result = run.autonomous_fix_loop(
            run_dir2,
            "o/r",
            str(tmp / "gh-agent-autonomous.json"),
            "stories/bugfix",
            "https://agent.example",
            "",
            "",
            "",
            "none",
            None,
        )
        _check("legacy autonomous loop requires native close-out",
               result["autonomous_fix_status"] == "autonomous_fix_invalid")
        _check("legacy autonomous loop reports missing close-out gates",
               result["autonomous_gate_summary"] == "filing=pass, gh_agent=pass, independent_verify=pass, review=fail, validation=fail"
               and result["independent_verify_status"] == "pass"
               and result["independent_verify_summary"] == "verified=1/1"
               and any(i["id"] == "issue-closeout" for i in result["validation_issues"]))
        _check("autonomous loop preserves filing status", result["filing_status"] == "findings_filed")
        _check("autonomous loop drained gh-agent", result["gh_agent_drain_status"] == "drained" and result["gh_agent_done_count"] == 1)
        _check("autonomous loop exposes fix evidence assets",
               result["gh_agent_fix_evidence_count"] == 4
               and result["gh_agent_missing_evidence_count"] == 0
               and result["gh_agent_triage_evidence_count"] == 1
               and result["gh_agent_missing_triage_count"] == 0
               and result["gh_agent_independent_verify_count"] == 1
               and result["gh_agent_missing_verify_count"] == 0)
        report = Path(result["autonomous_fix_report_path"])
        report_text = report.read_text()
        _check("autonomous loop writes complete review report",
               "https://github.com/o/r/issues/101" in report_text
               and "evidence `trace-replay.md`" in report_text
               and "https://github.com/o/r/releases/download/kitsoki-artifacts/" in report_text
               and "https://agent.example/run/job-1" in report_text
               and "integration/kitsoki-autofix-job-1" in report_text
               and "abc1231" in report_text
               and "https://agent.example/run/job-1/artifacts/fix-report.md" in report_text
               and "https://agent.example/run/job-1/artifacts/triage-verdict.md" in report_text
               and "https://agent.example/run/job-1/artifacts/independent-verify.md" in report_text)
        _check("legacy autonomous loop reviewed close-out gate",
               result["review_total_count"] == 33 and result["validation_status"] == "invalid")

        run_dir_facade, run_json_facade = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "gitops-facade-test", "dry-run", None,
        )
        scenario_facade = run_json_facade["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir_facade, scenario_facade)
        run.write_autonomous_marathon_control(run_dir_facade, run_json_facade, "pending", 24, 15, 45)
        run.record_finding(run_dir_facade, "issue", "gitops facade credible", "observed problem",
                           scenario_facade, "high", "", "open", None)
        facade_proc = subprocess.run(
            [
                "go", "run", "./cmd/kitsoki", "gitops", "autonomous-fix",
                "--json",
                "--report-invalid-autonomous-fix",
                "--allow-test-backend",
                "--run-dir", str(run_dir_facade),
                "--ticket-repo", "o/r",
                "--agent-db", str(tmp / "gh-agent-autonomous-facade.json"),
                "--public-base-url", "https://agent.example",
            ],
            cwd=run.ROOT,
            env={
                **os.environ,
                "KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE": "1",
                "KITSOKI_GITOPS_AUTOFIX_ALLOW_TEST_BACKEND": "1",
            },
            text=True,
            capture_output=True,
            check=False,
        )
        _check("gitops autonomous-fix facade exits cleanly",
               facade_proc.returncode == 0)
        facade_result = json.loads(facade_proc.stdout)
        _check("gitops autonomous-fix facade validates bundle",
               facade_result["autonomous_fix_status"] == "autonomous_fix_valid")
        _check("gitops autonomous-fix facade writes report",
               Path(facade_result["autonomous_fix_report_path"]).exists())
        _check("gitops autonomous-fix facade closes filed issues",
               facade_result["issue_closeout_status"] == "closed"
               and facade_result["issue_closeout_count"] == 1)
        facade_report = Path(facade_result["autonomous_fix_report_path"])
        facade_report_text = facade_report.read_text()
        _check("gitops autonomous-fix facade report includes watchdog and hosted-agent proof",
               "## Autonomous Watchdog" in facade_report_text
               and "autonomous_watchdog_ok" in facade_report_text
               and "## Hosted GH-agent" in facade_report_text
               and "Health: `pass`" in facade_report_text
               and "Readiness: `pass`" in facade_report_text
               and "/healthz" in facade_report_text
               and "/api/ready" in facade_report_text)
        facade_findings = run.read_json(run_dir_facade / "findings.json")
        facade_issue_evidence_url = facade_findings["items"][0]["github_issue"]["evidence_assets"][0]["url"]
        facade_report.write_text(facade_report_text.replace(facade_issue_evidence_url, "", 1))
        validated_missing_issue_evidence = run.validate_run_bundle(run_dir_facade)
        _check("validate catches missing filed issue evidence in autonomous report",
               any(i["id"] == "autonomous-fix-report" and i["severity"] == "error"
                   for i in validated_missing_issue_evidence["issues"]))
        facade_report.write_text(facade_report_text)
        facade_report.write_text(facade_report_text.replace("## Autonomous Watchdog", "## Watchdog", 1))
        validated_missing_watchdog_proof = run.validate_run_bundle(run_dir_facade)
        _check("validate catches missing autonomous watchdog proof in report",
               any(i["id"] == "autonomous-fix-report" and i["severity"] == "error"
                   for i in validated_missing_watchdog_proof["issues"]))
        facade_report.write_text(
            facade_report_text
            .replace("/healthz", "/health-check")
            .replace("/api/ready", "/api/status")
        )
        validated_missing_readiness_proof = run.validate_run_bundle(run_dir_facade)
        _check("validate catches missing hosted gh-agent proof in report",
               any(i["id"] == "autonomous-fix-report" and i["severity"] == "error"
                   for i in validated_missing_readiness_proof["issues"]))
        facade_report.write_text(facade_report_text)
        report.unlink()
        reviewed_missing_report = run.review_run_bundle(run_dir2, None)
        _check("review fails missing autonomous report",
               review_check(reviewed_missing_report, "autonomous-fix-report")["status"] == "fail")
        validated_missing_report = run.validate_run_bundle(run_dir2)
        _check("validate catches missing autonomous report",
               any(i["id"] == "autonomous-fix-report" and i["severity"] == "error"
                   for i in validated_missing_report["issues"]))

        run_dir3, run_json3 = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "autonomous-missing-evidence-test", "dry-run", None,
        )
        scenario3 = run_json3["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir3, scenario3)
        run.record_finding(run_dir3, "issue", "autonomous missing evidence", "observed problem",
                           scenario3, "high", "", "open", None)
        os.environ["KITSOKI_FAKE_GH_AGENT_NO_ASSETS"] = "1"
        try:
            result = run.autonomous_fix_loop(
                run_dir3,
                "o/r",
                str(tmp / "gh-agent-autonomous-no-assets.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                None,
            )
        finally:
            os.environ.pop("KITSOKI_FAKE_GH_AGENT_NO_ASSETS", None)
        _check("autonomous loop rejects done fixes without evidence",
               result["autonomous_fix_status"] == "autonomous_fix_invalid"
               and result["gh_agent_missing_evidence_count"] == 1)
        _check("autonomous loop reports failing gates for missing evidence",
               result["autonomous_gate_summary"] == "filing=pass, gh_agent=fail, independent_verify=fail, review=fail, validation=fail"
               and any(i["id"] == "gh-agent-fix-evidence" for i in result["validation_issues"]))

        run_dir6, run_json6 = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "autonomous-missing-verify-test", "dry-run", None,
        )
        scenario6 = run_json6["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir6, scenario6)
        run.record_finding(run_dir6, "issue", "autonomous missing verify", "observed problem",
                           scenario6, "high", "", "open", None)
        os.environ["KITSOKI_FAKE_GH_AGENT_NO_VERIFY"] = "1"
        try:
            result = run.autonomous_fix_loop(
                run_dir6,
                "o/r",
                str(tmp / "gh-agent-autonomous-no-verify.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                None,
            )
        finally:
            os.environ.pop("KITSOKI_FAKE_GH_AGENT_NO_VERIFY", None)
        _check("autonomous loop rejects done fixes without independent verification",
               result["autonomous_fix_status"] == "autonomous_fix_invalid"
               and result["gh_agent_fix_evidence_count"] == 3
               and result["gh_agent_missing_verify_count"] == 1)
        _check("autonomous loop reports failing gates for missing independent verification",
               result["autonomous_gate_summary"] == "filing=pass, gh_agent=pass, independent_verify=fail, review=fail, validation=fail"
               and result["independent_verify_status"] == "fail"
               and result["independent_verify_summary"] == "missing=1, verified=0/1"
               and any(i["id"] == "gh-agent-independent-verify" for i in result["validation_issues"]))

        run_dir5, run_json5 = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "autonomous-missing-run-url-test", "dry-run", None,
        )
        scenario5 = run_json5["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir5, scenario5)
        run.record_finding(run_dir5, "issue", "autonomous missing run URL", "observed problem",
                           scenario5, "high", "", "open", None)
        os.environ["KITSOKI_FAKE_GH_AGENT_NO_RUN_URL"] = "1"
        try:
            result = run.autonomous_fix_loop(
                run_dir5,
                "o/r",
                str(tmp / "gh-agent-autonomous-no-run-url.json"),
                "stories/bugfix",
                "https://agent.example",
                "",
                "",
                "",
                "none",
                None,
            )
        finally:
            os.environ.pop("KITSOKI_FAKE_GH_AGENT_NO_RUN_URL", None)
        _check("autonomous loop rejects done fixes without run URLs",
               result["autonomous_fix_status"] == "autonomous_fix_invalid"
               and result["gh_agent_missing_run_url_count"] == 1)
        _check("autonomous loop reports failing gates for missing run URLs",
               result["autonomous_gate_summary"] == "filing=pass, gh_agent=fail, independent_verify=pass, review=fail, validation=fail"
               and any(i["id"] == "gh-agent-run-url" for i in result["validation_issues"]))

        run_dir4, run_json4 = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, stable_scenarios, "vscode", "", "autonomous-no-issue-test", "dry-run", None,
        )
        scenario4 = run_json4["scenarios"][0]["id"]
        attach_bugfix_proof(run_dir4, scenario4)
        run.record_finding(run_dir4, "strength", "autonomous strength only", "proof exists but no issue was found",
                           scenario4, "low", "", "observed", None)
        result = run.autonomous_fix_loop(
            run_dir4,
            "o/r",
            str(tmp / "gh-agent-autonomous-no-issue.json"),
            "stories/bugfix",
            "https://agent.example",
            "",
            "",
            "",
            "none",
            None,
        )
        _check("autonomous loop rejects zero issue findings",
               result["autonomous_fix_status"] == "autonomous_fix_invalid"
               and result["findings_filed_count"] == 0
               and result["gh_agent_enqueued_count"] == 0)
        _check("autonomous loop requires at least one drained fix job",
               result["autonomous_gate_summary"] == "filing=fail, gh_agent=fail, independent_verify=fail, review=fail, validation=fail"
               and any(i["id"] == "gh-agent-fixes" for i in result["validation_issues"]))

    print("PASS")


if __name__ == "__main__":
    main()
