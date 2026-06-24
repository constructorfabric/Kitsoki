#!/usr/bin/env python3
"""Write the fix-tests run report as Markdown and print its path on stdout.

Invoked by the terminal rooms of stories/fix-tests via host.run (argv mode):

    write_report.py <outcome> <cycle> <max_cycles> <fix_artifact_json> <tests_passed>

All inputs are small scalars (the fixer's artifact is a compact JSON object
spliced by the engine). Raw test stdout is intentionally NOT passed here — it is
large and already captured by `make test` under .artifacts/test-reports/; the
report links to the newest such log instead.

The report lands under .artifacts/fix-tests/ (gitignored generated-artifact
dir, per repo convention). Only the path is emitted on stdout so the caller can
bind it to world.report_path.
"""
import glob
import json
import os
import sys
from datetime import datetime


def _load_artifact(raw: str) -> dict:
    raw = (raw or "").strip()
    if not raw or raw in ("{}", "map[]", "null"):
        return {}
    try:
        val = json.loads(raw)
        return val if isinstance(val, dict) else {}
    except (ValueError, TypeError):
        return {}


def _bullets(items, empty="(none)"):
    items = [str(x) for x in (items or []) if str(x).strip()]
    if not items:
        return f"- {empty}\n"
    return "".join(f"- {x}\n" for x in items)


def _newest_test_log():
    logs = sorted(
        glob.glob(".artifacts/test-reports/test-*.log"),
        key=os.path.getmtime,
        reverse=True,
    )
    return logs[0] if logs else ""


def main() -> int:
    argv = sys.argv[1:]
    outcome = argv[0] if len(argv) > 0 else "unknown"
    cycle = argv[1] if len(argv) > 1 else "0"
    max_cycles = argv[2] if len(argv) > 2 else "0"
    artifact = _load_artifact(argv[3] if len(argv) > 3 else "")
    tests_passed = (argv[4].strip().lower() == "true") if len(argv) > 4 else False

    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    out_dir = os.path.join(".artifacts", "fix-tests")
    os.makedirs(out_dir, exist_ok=True)
    path = os.path.join(out_dir, f"report-{stamp}.md")

    headline = {
        "clean": "✅ All tests passed on the first run — nothing to fix.",
        "fixed": "✅ Tests are green after auto-fixing.",
        "exhausted": "❌ Tests still failing after the fix budget was exhausted.",
        "blocked": "⏸️  Blocked — the fixer needs a human decision.",
    }.get(outcome, f"Outcome: {outcome}")

    test_log = _newest_test_log()

    lines = []
    lines.append("# fix-tests report\n\n")
    lines.append(f"_Generated {datetime.now().isoformat(timespec='seconds')}_\n\n")
    lines.append(f"**{headline}**\n\n")
    lines.append("| | |\n|---|---|\n")
    lines.append(f"| Outcome | `{outcome}` |\n")
    lines.append(f"| Final status | {'green' if tests_passed else 'red'} |\n")
    lines.append(f"| Fix cycles used | {cycle} / {max_cycles} |\n")
    if test_log:
        lines.append(f"| Full test log | `{test_log}` |\n")
    lines.append("\n")

    if artifact:
        lines.append("## What the fixer did (final cycle)\n\n")
        if artifact.get("summary_title"):
            lines.append(f"**{artifact['summary_title']}**\n\n")
        if artifact.get("summary_markdown"):
            lines.append(artifact["summary_markdown"].rstrip() + "\n\n")
        lines.append("### Files changed\n\n")
        lines.append(_bullets(artifact.get("files_changed"), "(no files changed)"))
        lines.append("\n### Tests fixed\n\n")
        lines.append(_bullets(artifact.get("fixed_tests")))
        lines.append("\n### Remaining failures\n\n")
        lines.append(_bullets(artifact.get("remaining_failures")))
        if artifact.get("open_questions"):
            lines.append("\n## ❓ Open questions (need a human answer)\n\n")
            lines.append(_bullets(artifact.get("open_questions")))
        lines.append("\n")
    elif outcome != "clean":
        lines.append("## What the fixer did\n\n")
        lines.append(
            "No fixer artifact was recorded — the fixer or a test run errored "
            "before producing one. See the full test log above.\n\n"
        )

    if outcome in ("exhausted", "blocked"):
        lines.append("---\n\nRe-run `make fix-tests` after resolving the above.\n")

    with open(path, "w", encoding="utf-8") as fh:
        fh.write("".join(lines))

    # stdout: the path only (bound to world.report_path).
    print(path)
    return 0


if __name__ == "__main__":
    sys.exit(main())
