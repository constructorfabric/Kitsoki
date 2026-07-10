#!/usr/bin/env python3
"""Runner-level test for --file-local-findings: the default campaign finding
sink (AGENTS.md: local developer/dogfood findings stay local under
`.artifacts/issues/bugs` unless a caller explicitly opts into GitHub).

Run directly:  python3 tools/product-journey/local_findings_test.py

Covers:
  1. dry-run reports candidates and leaves the bundle untouched.
  2. filing writes local_ticket paths, is idempotent on re-run, and never
     touches gh-agent.
  3. review/validate treat local-artifact-filed findings as resolved: the
     `findings-filed`, `gh-agent-fixes`, and `autonomous-fix-report` gates
     pass without any GitHub filing or gh-agent drain having happened.
  4. `--stats` counts local-artifact tickets as filed.
  5. a mixed run (one local ticket, one still-open credible finding) still
     requires the GitHub gate for the unresolved finding.

Nothing here calls a live LLM, gh, or GitHub. A fake `kitsoki bug create`
(via KITSOKI_BIN) writes the same markdown-under-target-dir/.artifacts shape
the real Go CLI writes, so the bundle write-back contract is exercised
end-to-end without a `go run` dependency.
"""

import importlib.util
import os
import stat
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)

FAKE_KITSOKI = r'''#!/usr/bin/env python3
"""Fake `kitsoki bug create` used by local_findings_test.py.

Mirrors internal/bugfile.Create's local-artifact contract: write a markdown
file under <target-dir>/.artifacts/issues/bugs/<slug>.md and print the path
relative to <target-dir> (e.g. ".artifacts/issues/bugs/<slug>.md").
"""
import argparse, re, sys
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("verb1")
parser.add_argument("verb2")
parser.add_argument("--target")
parser.add_argument("--target-dir")
parser.add_argument("--sink")
parser.add_argument("--title")
parser.add_argument("--body")
parser.add_argument("--severity", default="")
parser.add_argument("--trace-ref", default="")
args = parser.parse_args()
assert (args.verb1, args.verb2) == ("bug", "create"), (args.verb1, args.verb2)
assert args.sink == "local-artifact", args.sink
assert args.target == "kitsoki", args.target
assert args.target_dir

slug = re.sub(r"[^a-z0-9]+", "-", args.title.lower()).strip("-") or "bug"
bugs_dir = Path(args.target_dir) / ".artifacts" / "issues" / "bugs"
bugs_dir.mkdir(parents=True, exist_ok=True)
path = bugs_dir / f"2026-07-09T000000Z-{slug}.md"
path.write_text(
    "---\n"
    f"title: \"{args.title}\"\n"
    f"severity: \"{args.severity}\"\n"
    "status: \"open\"\n"
    "---\n\n"
    f"# {args.title}\n\n{args.body}\n",
    encoding="utf-8",
)
print(f".artifacts/issues/bugs/{path.name}")
sys.exit(0)
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


def main():
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.ARTIFACT_ROOT.mkdir(parents=True)

        fake = tmp / "fake_kitsoki.py"
        fake.write_text(FAKE_KITSOKI, encoding="utf-8")
        fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
        os.environ["KITSOKI_BIN"] = f"{sys.executable} {fake}"

        target_dir = tmp / "fake-kitsoki-repo"
        target_dir.mkdir()

        catalog = run.load_catalog(run.CATALOG)
        personas = run.load_personas(run.PERSONAS)
        scenarios = run.load_scenarios(run.SCENARIOS)
        run_dir, run_json = run.build_run_bundle(
            catalog, run.load_github_targets(run.GITHUB_TARGETS),
            personas, scenarios, "vscode", "", "local-findings-test", "dry-run", None,
        )
        scenario_id = run_json["scenarios"][0]["id"]

        run.record_finding(run_dir, "issue", "credible one", "observed problem one",
                            scenario_id, "high", "", "open", None)
        run.record_finding(run_dir, "issue", "credible two", "observed problem two",
                            scenario_id, "medium", "", "open", None)
        run.record_finding(run_dir, "issue", "seeded demo issue", "harness-only",
                            scenario_id, "low", "", "open", None, origin="seeded")

        # 1. Dry-run: candidates reported, bundle untouched.
        before = (run_dir / "findings.json").read_text()
        result = run.file_local_findings(run_dir, True, None, "kitsoki", str(target_dir))
        _check("dry-run status", result["status"] == "findings_dry_run_local")
        _check("dry-run reports 2 candidates",
               sum(1 for o in result["outcomes"] if o["status"] == "dry-run") == 2)
        _check("dry-run leaves findings.json untouched",
               (run_dir / "findings.json").read_text() == before)

        reviewed_before = run.review_run_bundle(run_dir, None)
        _check("credible findings require filing before any sink runs",
               review_check(reviewed_before, "findings-filed")["status"] == "fail")

        # 2. Filing: local tickets written, findings.json updated.
        result = run.file_local_findings(run_dir, False, None, "kitsoki", str(target_dir))
        _check("filed 2 credible findings", result["findings_filed_count"] == 2)
        _check("no credible finding left unfiled", result["findings_unfiled_count"] == 0)
        _check("filing sink recorded", result["filing_sink"] == "local-artifact")

        findings = run.read_json(run_dir / "findings.json")
        credible = run.credible_issue_findings(findings)
        _check("both credible findings carry a local_ticket path",
               all(run.local_finding_ref(item).get("path", "").startswith(".artifacts/issues/bugs/")
                   for item in credible))
        for item in credible:
            ticket_path = target_dir / run.local_finding_ref(item)["path"]
            _check(f"local ticket file exists for {item['id']}", ticket_path.exists())
        seeded = [i for i in findings["items"] if i.get("origin") == "seeded"]
        _check("seeded finding not filed", not run.local_finding_ref(seeded[0]).get("path"))

        # 3. review/validate: local sink resolves the GitHub-only gate chain
        # without any gh-agent enqueue/drain having happened.
        reviewed = run.review_run_bundle(run_dir, None)
        _check("findings-filed passes for local-only filing",
               review_check(reviewed, "findings-filed")["status"] == "pass")
        _check("gh-agent-fixes passes without gh-agent when local sink resolved every credible finding",
               review_check(reviewed, "gh-agent-fixes")["status"] == "pass")
        _check("autonomous-fix-report passes without gh-agent when local sink resolved every credible finding",
               review_check(reviewed, "autonomous-fix-report")["status"] == "pass")
        _check("issue-closeout not required for local-only filing",
               review_check(reviewed, "issue-closeout")["status"] == "pass")
        validated = run.validate_run_bundle(run_dir)
        _check("validate has no findings-filed error for local-only filing",
               not any(i["id"] == "findings-filed" for i in validated["issues"]))
        _check("validate has no gh-agent error for local-only filing",
               not any(i["id"] in {"gh-agent-fixes", "autonomous-fix-report", "issue-closeout"}
                       for i in validated["issues"]))

        # 4. Idempotence: a re-run files nothing new.
        result = run.file_local_findings(run_dir, False, None, "kitsoki", str(target_dir))
        _check("re-run files nothing", result["findings_filed_count"] == 0)
        _check("re-run skips already-filed", result["findings_skipped_count"] == 2)

        # 5. --stats counts local-artifact tickets as filed.
        stats = run.derive_stats(run.ARTIFACT_ROOT, "", 0.82, 25, "")
        _check("stats counts local tickets as filed", stats["findings_filed_count"] == 2)
        _check("stats found matches credible count", stats["findings_found_count"] == 2)
        _check("stats has no fixed count for local-only tickets (no GitHub issue state)",
               stats["issues_fixed_count"] == 0)

        # 6. Mixed disposition: a third credible finding with no ticket at
        # all still trips the GitHub gate chain (local sink only resolves
        # the findings it actually filed).
        run.record_finding(run_dir, "issue", "credible three", "observed problem three",
                            scenario_id, "high", "", "open", None)
        reviewed_mixed = run.review_run_bundle(run_dir, None)
        _check("mixed run still fails findings-filed for the unresolved finding",
               review_check(reviewed_mixed, "findings-filed")["status"] == "fail")
        _check("mixed run still fails gh-agent-fixes for the unresolved finding",
               review_check(reviewed_mixed, "gh-agent-fixes")["status"] == "fail")

    print("PASS")


if __name__ == "__main__":
    main()
