#!/usr/bin/env python3
"""Phase 1 slice test: outcome + intent-satisfaction capture, NO LLM.

The oracle output (step B) is supplied as a fixture file exactly like
test_intent_pipeline.py. This test runs:

    outcomes.py  (raw -> outcomes.json)
    ground.py -> tag_score.py -> emit.py --outcomes  (the scored spine)

over tests/fixtures/intent_outcomes/ in a temp dir, then asserts the five groups
from the spec §7: outcomes recovery (incl. id-regime missing-result -> null), the
ordinal-alignment invariant, the satisfaction review flag, back-compat (no new
keys without --outcomes), and schema conformance.

Run:  python3 tools/session-mining/tests/test_outcomes.py
(exits 0 on success, non-zero with a diagnostic on failure)
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
FIX = os.path.join(HERE, "fixtures", "intent_outcomes")
sys.path.insert(0, TOOL)

import outcomes
import ground
import tag_score
import emit
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
        traces = os.path.join(FIX, "traces")
        raw = os.path.join(FIX, "raw")
        outcomes_p = os.path.join(work, "outcomes.json")
        grounded = os.path.join(work, "grounded.json")
        scored = os.path.join(work, "scored.json")

        # --- outcomes.py: recover per-tool-call results from raw ---
        outcomes.main(["--raw", raw, "--out", outcomes_p])
        oc = _load(outcomes_p)
        sess = oc["sessions"]["sess-git"]
        to = sess["tool_outcomes"]

        # (1) outcomes recovery
        check(len(to) == 4, "expected 4 tool_outcomes, got %d" % len(to))
        check(to[0]["is_error"] is True, "tool_outcomes[0].is_error should be True")
        check("CONFLICT" in to[0]["stderr_head"],
              "tool_outcomes[0].stderr_head missing CONFLICT: %r" % to[0]["stderr_head"])
        check(to[2]["is_error"] is False, "tool_outcomes[2].is_error should be False")
        check("Successfully rebased" in to[2]["stdout_head"],
              "tool_outcomes[2].stdout_head missing 'Successfully rebased': %r" % to[2]["stdout_head"])
        check(sess["join"] == "id", "expected join=id, got %r" % sess["join"])

        # (1b) id-regime missing-result -> null (no positional cascade). Craft a
        # tiny id-bearing transcript where the MIDDLE call's result is absent
        # (interrupted/abandoned). The missing call must become null and the
        # following call must keep ITS OWN result, never a cascaded later one.
        mraw = os.path.join(work, "missing_raw")
        os.makedirs(mraw)
        with open(os.path.join(mraw, "sess-miss.jsonl"), "w") as fh:
            fh.write("\n".join([
                json.dumps({"type": "assistant", "message": {"content": [
                    {"type": "tool_use", "id": "m1", "name": "Bash", "input": {}}]}}),
                json.dumps({"type": "user", "message": {"content": [
                    {"type": "tool_result", "tool_use_id": "m1", "is_error": False, "content": "R1"}]}}),
                json.dumps({"type": "assistant", "message": {"content": [
                    {"type": "tool_use", "id": "m2", "name": "Bash", "input": {}}]}}),
                # NOTE: no result for m2 (interrupted/abandoned).
                json.dumps({"type": "assistant", "message": {"content": [
                    {"type": "tool_use", "id": "m3", "name": "Bash", "input": {}}]}}),
                json.dumps({"type": "user", "message": {"content": [
                    {"type": "tool_result", "tool_use_id": "m3", "is_error": False, "content": "R3"}]}}),
            ]) + "\n")
        moc_p = os.path.join(work, "missing_outcomes.json")
        outcomes.main(["--raw", mraw, "--out", moc_p])
        mto = _load(moc_p)["sessions"]["sess-miss"]["tool_outcomes"]
        check(len(mto) == 3, "missing-result: expected 3 tool_outcomes, got %d" % len(mto))
        check(mto[1] is None, "missing-result: m2 (no result) must be null, got %r" % mto[1])
        check(mto[0] is not None and "R1" in mto[0]["stdout_head"],
              "missing-result: m1 should keep R1, got %r" % mto[0])
        check(mto[2] is not None and "R3" in mto[2]["stdout_head"],
              "missing-result: m3 must keep its OWN R3 (no cascade), got %r" % mto[2])

        # --- scored spine: ground -> tag_score ---
        ground.main(["--oracle", os.path.join(FIX, "oracle.json"),
                     "--traces", traces, "--out", grounded])
        tag_score.main(["--grounded", grounded, "--traces", traces, "--out", scored])

        # --- emit WITH --outcomes ---
        emit.main(["--scored", scored, "--traces", traces, "--raw", raw,
                   "--outcomes", outcomes_p, "--out-dir", work, "--job", "outcomes-job"])
        analysis = _load(os.path.join(work, "analysis.json"))
        by_id = {i["instance_id"]: i for i in analysis["instances"]}
        check("sess-git#0" in by_id, "instance sess-git#0 missing")
        check("sess-git#1" in by_id, "instance sess-git#1 missing")
        # A grounding regression quarantine-drops a span and removes its instance;
        # bail with a named failure instead of letting the lookups below KeyError.
        if "sess-git#0" not in by_id or "sess-git#1" not in by_id:
            print("FAIL (%d):" % len(failures))
            for f in failures:
                print("  -", f)
            return 1

        # (2) ordinal alignment invariant
        a0 = {a["cite"]["line"]: a for a in by_id["sess-git#0"]["actions"]}
        check(a0[2].get("outcome", {}).get("is_error") is True,
              "sess-git#0 cite.line==2 outcome.is_error should be True: %r" % a0[2].get("outcome"))
        check(a0[4].get("outcome", {}).get("is_error") is False,
              "sess-git#0 cite.line==4 outcome.is_error should be False: %r" % a0[4].get("outcome"))
        # Pin a NON-error action to its specific stdout so an internal join
        # permutation among the (collapsed) is_error==False actions can't pass
        # spuriously: cite.line==4 is ordinal 2 == tu3, the "Successfully rebased"
        # call. is_error alone ([True,False,False,False]) cannot distinguish them.
        check("Successfully rebased" in (a0[4].get("outcome") or {}).get("stdout_head", ""),
              "sess-git#0 cite.line==4 outcome.stdout_head should carry 'Successfully rebased': %r"
              % a0[4].get("outcome"))
        a1 = {a["cite"]["line"]: a for a in by_id["sess-git#1"]["actions"]}
        check(a1[6].get("outcome", {}).get("is_error") is False,
              "sess-git#1 cite.line==6 outcome.is_error should be False: %r" % a1[6].get("outcome"))

        # (MINOR-2/3) grounded-dependency: the abort action MUST be grounded:true,
        # so the structural corrective tier rests on a grounded op, not noise.
        check(a1[6].get("grounded") is True,
              "sess-git#1 abort action (cite.line==6) must be grounded:true")

        # (3) satisfaction review flag
        sat0 = by_id["sess-git#0"]["satisfaction"]
        check(sat0["corrected"] is True, "sess-git#0.satisfaction.corrected should be True")
        check(any("--abort" in op for op in sat0["corrective_ops"]),
              "sess-git#0.satisfaction.corrective_ops missing a --abort entry: %r"
              % sat0["corrective_ops"])
        check("no that's wrong" in sat0["followup_text_head"],
              "sess-git#0.satisfaction.followup_text_head missing follow-up: %r"
              % sat0["followup_text_head"])
        sat1 = by_id["sess-git#1"]["satisfaction"]
        check(sat1["corrected"] is False,
              "sess-git#1.satisfaction.corrected should be False (no following span)")

        # (5) schema: additive fields validate
        schema_errs = validate_reports.validate_job(work)
        check(not schema_errs,
              "reports failed schema validation:\n    " + "\n    ".join(schema_errs))

        # (4) back-compat: emit WITHOUT --outcomes must not introduce the two new
        # keys. (This asserts new-key absence, not full byte-identity vs HEAD —
        # that stronger guarantee is proven out-of-band in the slice's review and
        # is independently exercised by the untouched golden test_intent_pipeline.)
        nobc = os.path.join(work, "nobc")
        os.makedirs(nobc)
        emit.main(["--scored", scored, "--traces", traces, "--raw", raw,
                   "--out-dir", nobc, "--job", "outcomes-job"])
        nobc_analysis_bytes = open(os.path.join(nobc, "analysis.json"), "rb").read()
        check(b'"outcome"' not in nobc_analysis_bytes,
              "back-compat: analysis.json should NOT contain 'outcome' without --outcomes")
        check(b'"satisfaction"' not in nobc_analysis_bytes,
              "back-compat: analysis.json should NOT contain 'satisfaction' without --outcomes")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: outcome + satisfaction capture (Phase 1, no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
