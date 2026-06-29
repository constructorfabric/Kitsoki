#!/usr/bin/env python3
"""Summarize `kitsoki test intents --json` output for routing-tuning reviews."""

import json
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: python3 tools/routing-tuning/intent_report_summary.py <report.json>", file=sys.stderr)
        return 2

    with open(sys.argv[1]) as f:
        report = json.load(f)

    total = report.get("TotalPassed", 0) + report.get("TotalFailed", 0)
    print(
        f"{report.get('TotalPassed', 0)}/{total} fixtures passed "
        f"({report.get('HarnessType', 'unknown')} profile={report.get('ProfileName', '')} "
        f"model={report.get('HarnessModel', '')})"
    )

    failures = [fx for fx in report.get("Fixtures", []) if not fx.get("Passed")]
    if not failures:
        return 0

    print("\nFailures:")
    for fx in failures:
        print(f"- {fx.get('ID')} in state {fx.get('State')}: pass_rate={fx.get('PassRate')}")
        for inp in fx.get("Inputs", []):
            print(
                f"  input: {inp.get('Input')!r}; "
                f"first_actual={inp.get('FirstActualIntent')}; "
                f"passed={inp.get('Passed')}/{inp.get('Runs')}"
            )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
