#!/usr/bin/env python3
"""Regression tests for validate_reports.py's stdlib schema fallback.

The fallback must remain a real gate when jsonschema is absent: malformed
reports should fail instead of being silently accepted.
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import validate_reports


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    schema_path = os.path.join(TOOL, "schema", "intents.schema.json")
    bad_report = {
        "schema_version": "1.0",
        "job": "fixture-job",
        "total_intents": -1,
        "intents": [{
            "instance_id": "missing-index",
            "user_text": 42,
            "session": "sess",
            "span": [0],
            "tags": {"action": ["fix"], "surprise": ["not allowed"]},
            "analysis_ref": "analysis.json#missing-index",
        }],
        "tags": {},
    }

    original = validate_reports._require_jsonschema
    validate_reports._require_jsonschema = lambda: None
    try:
        with tempfile.TemporaryDirectory() as work:
            report_path = os.path.join(work, "intents.json")
            with open(report_path, "w") as fh:
                json.dump(bad_report, fh)
            errs = validate_reports.validate_report(report_path, schema_path)
    finally:
        validate_reports._require_jsonschema = original

    check(errs, "stdlib fallback accepted a malformed intents report")
    joined = "\n".join(errs)
    check("minimum" in joined, "expected minimum violation, got:\n%s" % joined)
    check("type string" in joined, "expected type violation, got:\n%s" % joined)
    check("pattern" in joined or "does not match" in joined,
          "expected pattern violation, got:\n%s" % joined)
    check("additional properties" in joined,
          "expected additionalProperties violation, got:\n%s" % joined)
    check("too few items" in joined,
          "expected minItems violation, got:\n%s" % joined)

    if failures:
        print("FAIL (%d):" % len(failures))
        for failure in failures:
            print("  -", failure)
        return 1
    print("PASS: validate_reports stdlib schema fallback rejects malformed reports")
    return 0


if __name__ == "__main__":
    sys.exit(run())
