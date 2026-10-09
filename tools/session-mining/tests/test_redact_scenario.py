#!/usr/bin/env python3
"""Unit tests for redact.py's `--scenario` mode (task 2.3): the mined-scenario
redaction GATE that a `kind: conversation` IR document must pass before it may
be written to a committable path. No LLM, no network, no real corpus.

Run:  python3 tools/session-mining/tests/test_redact_scenario.py
(exits 0 on success, non-zero with a diagnostic on failure)
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import redact


def _scenario(**overrides):
    doc = {
        "schema_version": "1.0",
        "kind": "conversation",
        "id": "scn-test-0000",
        "source": "mined",
        "provenance": {"corpus": "claude-code", "session_id": "sess-1", "span_idx": 0},
        "persona": "core-maintainer",
        "goal": "rebase this onto main",
        "turns": [{"role": "user", "text": "rebase this onto main"}],
        "expected_effects": [],
        "abandoned": False,
    }
    doc.update(overrides)
    return doc


def _run_cli(argv, stdin_text):
    return subprocess.run(
        [sys.executable, os.path.join(TOOL, "redact.py")] + argv,
        input=stdin_text, capture_output=True, text=True)


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    # --- clean_scenario() genericizes free-text fields, leaves structure alone
    doc = _scenario(
        goal="fix the bug in /Users/brad/code/myrepo/internal/foo.go",
        turns=[{
            "role": "user",
            "text": "fix the bug in /Users/brad/code/myrepo/internal/foo.go",
            "corrected": True,
            "corrective_ops": ["git rebase --abort in /Users/brad/code/myrepo"],
            "followup_text_head": "no wait, stash my changes in /Users/brad/code/myrepo first",
        }],
        expected_effects=["edit /Users/brad/code/myrepo/internal/foo.go completed"],
    )
    changed = redact.clean_scenario(doc)
    check(changed > 0, "clean_scenario should report changed fields")
    check("/Users/brad/code/myrepo" not in doc["goal"], "goal should have its home+repo path genericized: %r" % doc["goal"])
    check("<path>" in doc["goal"] or "~" not in doc["goal"], "goal should not leak the raw path: %r" % doc["goal"])
    check("/Users/brad" not in doc["turns"][0]["text"], "turn text should be genericized")
    check("/Users/brad" not in doc["turns"][0]["corrective_ops"][0], "corrective_ops should be genericized")
    check("/Users/brad" not in doc["turns"][0]["followup_text_head"], "followup_text_head should be genericized")
    # schema-load-bearing fields must be untouched by the scrub
    check(doc["id"] == "scn-test-0000", "id must not be touched")
    check(doc["persona"] == "core-maintainer", "persona must not be touched")
    check(doc["provenance"]["session_id"] == "sess-1", "provenance must not be touched")

    # --- CLI --scenario: PASS path (exit 0, clean JSON on stdout)
    clean_doc = _scenario(goal="rebase this onto main",
                           turns=[{"role": "user", "text": "rebase this onto main"}])
    proc = _run_cli(["--scenario"], json.dumps(clean_doc))
    check(proc.returncode == 0, "clean scenario should pass the gate: stderr=%r" % proc.stderr)
    out = json.loads(proc.stdout)
    check(out["id"] == "scn-test-0000", "CLI --scenario should round-trip the document")

    # --- CLI --scenario: FAIL-CLOSED path. clean_scenario()'s allowlist only
    # covers the documented free-text fields (goal/turns/expected_effects) —
    # this proves the final --scan re-check is real defense-in-depth, not
    # theater: a secret planted in a field OUTSIDE that allowlist (e.g. a
    # provenance field a future writer might carelessly stuff a raw value
    # into) still trips the gate -> exit 1, NOTHING on stdout, so a caller
    # can't accidentally treat a partial/leaky write as success.
    leaky_doc = _scenario(
        goal="rotate the leaked key",
        provenance={"corpus": "claude-code",
                    "session_id": "sess-AKIAABCDEFGHIJKLMNOP", "span_idx": 0},
    )
    proc = _run_cli(["--scenario"], json.dumps(leaky_doc))
    check(proc.returncode == 1, "a document with a surviving secret must fail closed")
    check(proc.stdout == "", "a failed gate must emit nothing on stdout: %r" % proc.stdout)
    check("GATE FAIL" in proc.stderr, "failure must be self-explanatory on stderr")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: redact.py --scenario (mined-IR redaction gate, no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
