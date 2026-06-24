#!/usr/bin/env python3
"""End-to-end test of the intent-mining deterministic spine (steps C->F), with NO
LLM. The agent output (step B) is supplied as a fixture file, exactly as required
by AGENTS.md ("automated tests must mock the agent via fixtures/cassettes").

Run:  python3 tools/session-mining/tests/test_intent_pipeline.py
(exits 0 on success, non-zero with a diagnostic on failure)

It runs ground.py -> tag_score.py -> emit.py -> verify_link.py over the fixture in
tests/fixtures/intent/ in a temp dir, then asserts the grounding gate, the
determinism verdicts, the measured signals, the verbatim recovery, and the
cross-link contract.
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
FIX = os.path.join(HERE, "fixtures", "intent")
sys.path.insert(0, TOOL)

import ground
import tag_score
import emit
import verify_link
import validate_reports


def _load(p):
    with open(p) as fh:
        return json.load(fh)


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    with tempfile.TemporaryDirectory() as work:
        grounded = os.path.join(work, "grounded.json")
        scored = os.path.join(work, "scored.json")
        traces = os.path.join(FIX, "traces")
        raw = os.path.join(FIX, "raw")

        # --- C: ground ---
        ground.main(["--agent", os.path.join(FIX, "agent.json"),
                     "--traces", traces, "--out", grounded])
        g = _load(grounded)
        # the fabricated span (3rd, cite ok but param bogus) must be quarantined+dropped
        check(g["stats"]["spans_total"] == 3, "expected 3 input spans, got %s" % g["stats"]["spans_total"])
        check(g["stats"]["spans_dropped_quarantined"] == 1,
              "expected 1 quarantined-drop, got %s" % g["stats"]["spans_dropped_quarantined"])
        kept = g["records"][0]["spans"]
        check(len(kept) == 2, "expected 2 kept spans, got %d" % len(kept))
        # span 1 fully grounded
        s1 = kept[0]
        check(s1["grounding"]["actions_validated"] == 3,
              "span1 should validate 3 actions, got %s" % s1["grounding"])
        check(all(a["grounded"] for a in s1["actions"]), "span1 all actions should be grounded")

        # --- D+E: tag/group + score ---
        tag_score.main(["--grounded", grounded, "--traces", traces, "--out", scored])
        sc = _load(scored)
        spans = sc["records"][0]["spans"]
        by_id = {s["instance_id"]: s for s in spans}
        check("sess-fix#0" in by_id, "instance sess-fix#0 missing")
        # span 0: fully grounded, no gates -> deterministic
        check(by_id["sess-fix#0"]["determinism"] == "deterministic",
              "span0 determinism = %s" % by_id["sess-fix#0"]["determinism"])
        # measured: one edit->rerun cycle, one retry (repeated go test line)
        m = by_id["sess-fix#0"]["measured"]
        check(m["tool_calls"] == 3, "span0 tool_calls = %s" % m["tool_calls"])
        check(m["edit_rerun_cycles"] == 1, "span0 edit_rerun_cycles = %s" % m["edit_rerun_cycles"])
        check(m["retries"] == 1, "span0 retries = %s (expected 1 repeated go test)" % m["retries"])
        # span 1: has an agent gate -> agent-gated
        check(by_id["sess-fix#1"]["determinism"] == "agent-gated",
              "span1 determinism = %s" % by_id["sess-fix#1"]["determinism"])
        # tag rollup
        check(sc["tags"]["action"].get("fix-failing-tests") == 1, "fix-failing-tests count wrong")
        check(len(sc["clusters"]) == 2, "expected 2 clusters, got %d" % len(sc["clusters"]))

        # --- F: emit ---
        emit.main(["--scored", scored, "--traces", traces, "--raw", raw,
                   "--out-dir", work, "--job", "fixture-job"])
        intents = _load(os.path.join(work, "intents.json"))
        analysis = _load(os.path.join(work, "analysis.json"))
        check(intents["total_intents"] == 2, "total_intents = %s" % intents["total_intents"])
        # verbatim recovery: must be the FULL raw text, not the truncated trace line
        it0 = next(i for i in intents["intents"] if i["instance_id"] == "sess-fix#0")
        check("before I can merge" in it0["user_text"],
              "verbatim recovery failed; got: %r" % it0["user_text"][:80])
        it1 = next(i for i in intents["intents"] if i["instance_id"] == "sess-fix#1")
        check("old callers still use" in it1["user_text"],
              "verbatim recovery for span1 failed; got: %r" % it1["user_text"][:80])
        check(it0["analysis_ref"] == "analysis.json#sess-fix#0", "cross-link ref wrong")
        # agent_gates only present on the non-deterministic instance
        a_by_id = {i["instance_id"]: i for i in analysis["instances"]}
        check("agent_gates" not in a_by_id["sess-fix#0"], "deterministic instance must not carry gates")
        check("agent_gates" in a_by_id["sess-fix#1"], "agent-gated instance must carry gates")

        # --- cross-link contract ---
        rc = verify_link.main([work])
        check(rc == 0, "verify_link reported cross-link failures")

        # --- schema conformance (jsonschema) ---
        # both reports must validate against schema/{intents,analysis}.schema.json
        # AND satisfy the cross-link contract JSON Schema can't express.
        schema_errs = validate_reports.validate_job(work)
        check(not schema_errs,
              "reports failed schema validation:\n    " + "\n    ".join(schema_errs))

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: intent-mining C->F pipeline (no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
