#!/usr/bin/env python3
"""Runner-level test for autonomous marathon watchdog checks.

The test uses deterministic timestamps and never calls GitHub or a live LLM.
"""

import importlib.util
import sys
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

        created = run.autonomous_marathon(
            catalog,
            github_targets,
            personas,
            scenarios,
            None,
            "vscode",
            "core-maintainer",
            "autonomous-watchdog-test",
            "bugfix",
            0,
            "",
            "",
            "stories/bugfix",
            "",
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
        created_at = run.parse_iso_datetime(run_json["created_at"])
        fresh_at = (created_at + run.datetime.timedelta(minutes=30)).isoformat(timespec="seconds")
        stale_at = (created_at + run.datetime.timedelta(minutes=46)).isoformat(timespec="seconds")

        fresh = run.autonomous_marathon_watchdog(run_dir, fresh_at)
        fresh_markdown = Path(fresh["autonomous_watchdog_markdown_path"])
        check(
            "watchdog accepts heartbeat within control interval",
            fresh["autonomous_watchdog_status"] == "autonomous_watchdog_ok"
            and fresh["heartbeat_age_minutes"] == 30
            and fresh_markdown.exists()
            and "No watchdog blocker is active" in fresh_markdown.read_text(encoding="utf-8"),
            failures,
        )

        stale = run.autonomous_marathon_watchdog(run_dir, stale_at)
        stale_markdown = Path(stale["autonomous_watchdog_markdown_path"])
        check(
            "watchdog fails closed when no driver heartbeat arrives",
            stale["autonomous_watchdog_status"] == "autonomous_watchdog_blocked"
            and stale["heartbeat_age_minutes"] == 46
            and "stop before spend" in stale["autonomous_watchdog_summary"]
            and "Missed heartbeat" in stale_markdown.read_text(encoding="utf-8"),
            failures,
        )

        event_at = created_at + run.datetime.timedelta(minutes=50)
        original_now = run.now_utc
        run.now_utc = lambda: event_at.isoformat(timespec="seconds")
        try:
            run.record_driver_event(
                run_dir,
                run_json["scenarios"][0]["id"],
                "replay",
                "attempted",
                "Watchdog regression heartbeat.",
                "session.open",
                "",
                "",
                None,
            )
        finally:
            run.now_utc = original_now
        recovered_at = (created_at + run.datetime.timedelta(minutes=60)).isoformat(timespec="seconds")
        recovered = run.autonomous_marathon_watchdog(run_dir, recovered_at)
        summary = run.run_story_summary(run_dir)
        check(
            "recent driver event refreshes watchdog heartbeat",
            recovered["autonomous_watchdog_status"] == "autonomous_watchdog_ok"
            and recovered["heartbeat_age_minutes"] == 10
            and summary["autonomous_watchdog_status"] == "autonomous_watchdog_ok"
            and summary["autonomous_watchdog_age_minutes"] == 10,
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
