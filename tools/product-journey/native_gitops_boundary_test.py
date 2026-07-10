#!/usr/bin/env python3
"""Regression for product-journey native gitops boundary validation."""

import importlib.util
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def check(name, condition, failures):
    if condition:
        print(f"ok: {name}")
    else:
        print(f"FAIL: {name}")
        failures.append(name)


def main():
    failures = []
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        good = root / "good.md"
        readme = root / "readme.md"
        story_readme = root / "story-readme.md"
        prompt = root / "autonomous-driver.md"
        good.write_text(
            "Never file findings with raw `gh issue create`; use kitsoki gitops autonomous-fix.\n"
            "Do not run `gh issue comment` or `gh issue close`; use kitsoki gitops issue-closeout "
            "for story-owned fixed-issue close-out.\n",
            encoding="utf-8",
        )
        bad_create = root / "bad-create.md"
        bad_comment = root / "bad-comment.md"
        bad_close = root / "bad-close.md"
        bad_intent = root / "bad-intent.md"
        bad_create.write_text("File the finding with `gh issue create` after capture.\n", encoding="utf-8")
        bad_comment.write_text("Post the closeout with `gh issue comment` after the fix lands.\n", encoding="utf-8")
        bad_close.write_text("Close the issue with `gh issue close` after verification.\n", encoding="utf-8")
        bad_intent.write_text("Use issue_comment and issue_transition directly from the driver.\n", encoding="utf-8")
        readme.write_text(
            "Use kitsoki gitops autonomous-fix for issue-to-fix gates, or kitsoki gitops issue-closeout "
            "to replay fixed-issue close-out from the story bundle.\n",
            encoding="utf-8",
        )
        story_readme.write_text("Use the product-journey story autonomous_fix gate.\n", encoding="utf-8")
        prompt.write_text(
            "Capture proof, record findings, and return JSON. The outer product-journey story has already "
            "queued the autonomous finalizer; that finalizer owns `autonomous_watchdog`, `autonomous_fix`, "
            "review, validation, stats, issue close-out, and gh-agent draining.\n",
            encoding="utf-8",
        )
        bad_prompt = root / "bad-autonomous-driver.md"
        bad_prompt.write_text(
            "After capture, submit `autonomous_fix ticket_repo=<owner/repo>` and submit `review`.\n",
            encoding="utf-8",
        )
        story_root = root / "story-root"
        room_path = story_root / "stories" / "product-journey-qa" / "rooms" / "run_created.yaml"
        app_path = story_root / "stories" / "product-journey-qa" / "app.yaml"
        room_path.parent.mkdir(parents=True)
        docs_body = (
            "Use `autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<url>` "
            "as the story-owned full loop through `kitsoki gitops autonomous-fix`; gh-agent "
            "runs include independent-verify.md proof. Run --gate persona-autofix and "
            "persona_autofix_smoke gates.\n"
        )
        good_room = (
            "label: autonomous_fix ticket_repo=owner/repo gh_agent_public_base_url=<url>\n"
            "cmd: kitsoki\n"
            "args: [gitops, autonomous-fix]\n"
            'autonomous_fix_report_path: "stdout_json.autonomous_fix_report_path"\n'
            "Autonomous report\n"
            "id: file_product_journey_findings\n"
            "args:\n"
            "  - tools/product-journey/run.py\n"
            "  - --file-findings\n"
            "              bind:\n"
            "label: file_findings ticket_repo=owner/repo\n"
        )
        bad_room = good_room.replace(
            "  - --file-findings\n",
            "  - --file-findings\n"
            "  - --gh-agent-drain\n",
        )
        good_app = (
            "  file_findings:\n"
            "    slots:\n"
            "      ticket_repo: { type: string, required: true }\n"
            "  autonomous_fix:\n"
            "    slots:\n"
            "      gh_agent_db: { type: string, required: false }\n"
        )
        bad_app = good_app.replace(
            "      ticket_repo: { type: string, required: true }\n",
            "      ticket_repo: { type: string, required: true }\n"
            "      gh_agent_db: { type: string, required: false }\n",
        )
        readme.write_text(docs_body, encoding="utf-8")
        story_readme.write_text(docs_body, encoding="utf-8")

        original_driver = run.DRIVER_AGENT
        original_prompt = run.AUTONOMOUS_DRIVER_PROMPT
        original_skill = run.PRODUCT_JOURNEY_SKILL
        original_readme = run.PRODUCT_JOURNEY_README
        original_root = run.ROOT
        try:
            run.DRIVER_AGENT = good
            run.AUTONOMOUS_DRIVER_PROMPT = prompt
            run.PRODUCT_JOURNEY_SKILL = readme
            run.PRODUCT_JOURNEY_README = story_readme
            issues = []
            run.validate_native_gitops_boundaries(issues)
            check("explicit raw gh prohibition is allowed", not issues, failures)

            cases = [
                ("raw gh filing guidance fails the corpus boundary", bad_create),
                ("raw gh comment guidance fails the corpus boundary", bad_comment),
                ("raw gh close guidance fails the corpus boundary", bad_close),
                ("standalone mutation intent guidance fails the corpus boundary", bad_intent),
            ]
            for label, path in cases:
                run.DRIVER_AGENT = path
                issues = []
                run.validate_native_gitops_boundaries(issues)
                check(
                    label,
                    any(issue.get("id") == "native-gitops-boundary" for issue in issues),
                    failures,
                )
            run.DRIVER_AGENT = good
            run.AUTONOMOUS_DRIVER_PROMPT = bad_prompt
            issues = []
            run.validate_native_gitops_boundaries(issues)
            check(
                "autonomous driver final gates must stay story-owned",
                any(issue.get("id") == "autonomous-driver-finalizer-boundary" for issue in issues),
                failures,
            )
            bad_prompt.write_text(
                "Capture proof, then file the issue with `gh issue create` before returning.\n"
                "The outer product-journey story has already queued the autonomous finalizer; "
                "that finalizer owns `autonomous_watchdog`, `autonomous_fix`, review, validation, stats.\n",
                encoding="utf-8",
            )
            issues = []
            run.validate_native_gitops_boundaries(issues)
            check(
                "autonomous driver prompt raw gh guidance fails the corpus boundary",
                any(issue.get("id") == "native-gitops-boundary" for issue in issues),
                failures,
            )
            run.ROOT = story_root
            room_path.write_text(good_room, encoding="utf-8")
            app_path.write_text(good_app, encoding="utf-8")
            issues = []
            run.validate_autonomous_workflow_docs(issues)
            check("file_findings filing-only boundary accepts clean story", not issues, failures)

            room_path.write_text(bad_room, encoding="utf-8")
            app_path.write_text(good_app, encoding="utf-8")
            issues = []
            run.validate_autonomous_workflow_docs(issues)
            check(
                "file_findings gh-agent drain fails the corpus boundary",
                any(issue.get("id") == "file-findings-gh-agent-boundary" for issue in issues),
                failures,
            )

            room_path.write_text(good_room, encoding="utf-8")
            app_path.write_text(bad_app, encoding="utf-8")
            issues = []
            run.validate_autonomous_workflow_docs(issues)
            check(
                "file_findings gh-agent slots fail the corpus boundary",
                any(issue.get("id") == "file-findings-gh-agent-boundary" for issue in issues),
                failures,
            )
        finally:
            run.DRIVER_AGENT = original_driver
            run.AUTONOMOUS_DRIVER_PROMPT = original_prompt
            run.PRODUCT_JOURNEY_SKILL = original_skill
            run.PRODUCT_JOURNEY_README = original_readme
            run.ROOT = original_root

    if failures:
        raise SystemExit(1)
    print("PASS")


if __name__ == "__main__":
    main()
