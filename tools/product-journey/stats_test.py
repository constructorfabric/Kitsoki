#!/usr/bin/env python3
"""Runner-level test for --stats.

Run directly:  python3 tools/product-journey/stats_test.py

The stats command is intentionally deterministic here: it reads local
findings.json bundles plus a cached issue-state fixture, never GitHub.
"""

import importlib.util
import json
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main():
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        root = tmp / "product-journey"
        run.ARTIFACT_ROOT = root
        run.ROOT = tmp

        write_json(root / "run-a" / "findings.json", {
            "run_id": "run-a",
            "items": [
                {
                    "id": "finding-1",
                    "kind": "issue",
                    "origin": "observed",
                    "title": "Replay misses fall through to live agent",
                    "summary": "cassette miss used live fallback",
                    "status": "open",
                    "github_issue": {
                        "url": "https://github.com/o/r/issues/101",
                        "repo": "o/r",
                        "number": "101",
                    },
                },
                {
                    "id": "finding-2",
                    "kind": "issue",
                    "origin": "seeded",
                    "title": "seeded issue is not user-found",
                    "summary": "fixture only",
                    "status": "open",
                },
                {
                    "id": "finding-3",
                    "kind": "strength",
                    "title": "good deck",
                    "summary": "not an issue",
                    "status": "observed",
                },
            ],
        })
        write_json(root / "run-b" / "findings.json", {
            "run_id": "run-b",
            "items": [
                {
                    "id": "finding-1",
                    "kind": "issue",
                    "origin": "observed",
                    "title": "Replay miss falls through to live agents",
                    "summary": "near duplicate from another run",
                    "status": "open",
                    "github_issue": {
                        "url": "https://github.com/o/r/issues/102",
                        "repo": "o/r",
                        "number": "102",
                    },
                },
                {
                    "id": "finding-2",
                    "kind": "issue",
                    "origin": "observed",
                    "title": "Done room never closes ticket",
                    "summary": "fixed once then reopened",
                    "status": "open",
                    "github_issue": {
                        "url": "https://github.com/o/r/issues/103",
                        "repo": "o/r",
                        "number": "103",
                    },
                },
                {
                    "id": "finding-3",
                    "kind": "issue",
                    "origin": "observed",
                    "title": "Unfiled issue is still counted as found",
                    "summary": "no GitHub issue yet",
                    "status": "open",
                },
            ],
        })
        issue_state = tmp / "issue-state.json"
        write_json(issue_state, {
            "issues": [
                {
                    "url": "https://github.com/o/r/issues/101",
                    "state": "closed",
                    "comments": [{"body": "kitsoki-fixed-in: 616bdeae"}],
                },
                {
                    "url": "https://github.com/o/r/issues/102",
                    "state": "open",
                    "comments": [{"body": "kitsoki-fixed-in: 616bdeae"}],
                },
                {
                    "url": "https://github.com/o/r/issues/103",
                    "state": "open",
                    "comments": [{"body": "kitsoki-fixed-in: 5cc5fa58"}],
                },
            ],
        })

        stats_out = root / "stats" / "latest.json"
        result = run.derive_stats(root, str(issue_state), 0.72, 25, str(stats_out))

        _check("status", result["status"] == "stats_derived")
        _check("scans two runs", result["runs_scanned"] == 2)
        _check("counts only observed issue findings", result["findings_found_count"] == 4)
        _check("counts filed findings", result["findings_filed_count"] == 3)
        _check("counts fixed closed marker", result["issues_fixed_count"] == 1)
        _check("counts reopened marker", result["issues_reopened_count"] == 2)
        _check("detects similar titles", result["similar_pair_count"] == 1)
        _check("writes stats artifact", stats_out.exists())
        written = run.read_json(stats_out)
        _check("written artifact includes path", written["stats_output"] == str(stats_out))
        _check("written artifact includes scanned run dirs",
               sorted(Path(path).name for path in written["run_dirs"]) == ["run-a", "run-b"])
        _check("manual stats replaced", written["manual_stats_replaced"] == "yes")

    print("PASS")


if __name__ == "__main__":
    main()
