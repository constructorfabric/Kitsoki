#!/usr/bin/env python3
"""codex adapter tests, NO LLM, no network, no real corpus.

Exercises tools/session-mining/codex_adapter.py + its codex_prep.py/
codex_outcomes.py CLIs against SANITIZED, INVENTED fixture rollouts under
tests/fixtures/codex/raw_root/ (fictional project names/paths only — no real
session content, per AGENTS.md/hard rules). Three groups:

  (1) unit-level: the distilled line grammar matches distill.jq's shape exactly
      (USER:/AI:/  > Tool: arg), the originator-based agent/exec filter, and the
      outcome extraction (exit-code -> is_error, "timed out" -> interrupted,
      patch_apply_end.success overriding a missing/absent exit code).
  (2) codex_prep.py end-to-end: distills the fixture root's 3 rollouts, drops
      the codex_exec (headless dispatched agent) one by default, honors --cwd
      filtering, and stages a flat raw/<sid>.jsonl the same way prep.py's
      project-flat raw dir already is.
  (3) full pipeline: codex_prep -> codex_outcomes -> ground.py -> tag_score.py
      -> emit.py --outcomes, proving the FOUR existing modules run completely
      UNCHANGED against a codex-sourced job (this is the load-bearing claim of
      the codex adapter task).

Run:  python3 tools/session-mining/tests/test_codex_adapter.py
(exits 0 on success, non-zero with a diagnostic on failure)
"""
import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
FIX_ROOT = os.path.join(HERE, "fixtures", "codex", "raw_root")
sys.path.insert(0, TOOL)

import codex_adapter as ca
import codex_prep
import codex_outcomes
import ground
import tag_score
import emit


def _load(p):
    with open(p) as fh:
        return json.load(fh)


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    demo1 = os.path.join(FIX_ROOT, "2026/07/03/rollout-2026-07-03T10-00-00-demo1.jsonl")
    demo2 = os.path.join(FIX_ROOT, "2026/07/03/rollout-2026-07-03T11-00-00-demo2.jsonl")
    demo3 = os.path.join(FIX_ROOT, "2026/07/04/rollout-2026-07-04T09-00-00-demo3.jsonl")

    # --- (1a) discovery + originator filter ---
    found = ca.find_session_files(FIX_ROOT)
    check(len(found) == 3, "expected 3 fixture rollouts, found %d: %r" % (len(found), found))
    check(ca.session_originator(demo1) == "codex-tui", "demo1 originator should be codex-tui")
    check(ca.session_originator(demo2) == "codex_exec", "demo2 originator should be codex_exec")
    check(ca.is_agent_session(demo1) is False, "demo1 (codex-tui) must NOT be an agent session")
    check(ca.is_agent_session(demo2) is True, "demo2 (codex_exec) MUST be an agent session")
    check(ca.session_id(demo1) == "rollout-2026-07-03T10-00-00-demo1",
          "session_id should be the rollout basename minus .jsonl")

    # --- (1b) distilled line grammar (mirrors distill.jq's USER:/AI:/  > shape) ---
    lines, call_ids = ca.distill_session(demo1)
    expect_lines = [
        "USER: run the tests and fix the failing one",
        "AI: I'll run the test suite first.",
        "  > exec_command: go test ./...",
        "  > apply_patch: *** Begin Patch *** Update File: widget.go @@ -return 3 +return 2 *** End Patch",
        "  > exec_command: go test ./...",
        "USER: actually revert that, I want to try something else",
        "  > exec_command: git checkout -- widget.go",
    ]
    check(lines == expect_lines, "distilled lines mismatch:\n got=%r\nwant=%r" % (lines, expect_lines))
    check(call_ids == ["call_1", "call_2", "call_3", "call_4"],
          "tool_call_ids order mismatch: %r" % call_ids)

    # a genuine developer/system message (response_item message role=developer)
    # must NOT leak into the trace as a USER: line -- only event_msg.user_message
    # is the real human ask.
    check(not any("permissions instructions" in l for l in lines),
          "harness-injected developer boilerplate must not appear as a USER: line")

    # --- (1c) outcome extraction: exit codes, patch success override, heads ---
    records = list(ca.iter_records(demo1))
    outc = ca.session_tool_outcomes(records)
    check(outc["join"] == "id", "codex join should always be id (real call_id key)")
    to = outc["tool_outcomes"]
    check(len(to) == 4, "expected 4 tool_outcomes, got %d" % len(to))
    check(to[0]["is_error"] is True, "call_1 (exit code 1) should be is_error=True")
    check("CONFLICT" in to[0]["stdout_head"], "call_1 stdout_head should carry the failure text")
    check(to[1]["is_error"] is False,
          "call_2 (apply_patch, no exec output but patch_apply_end.success=True) should be is_error=False")
    check(to[2]["is_error"] is False, "call_3 (exit code 0) should be is_error=False")
    check("Successfully ran tests" in to[2]["stdout_head"], "call_3 stdout_head should carry success text")
    check(to[3]["is_error"] is False, "call_4 (git checkout, exit code 0) should be is_error=False")

    # timeout/interrupted detection (lexical "timed out" scan)
    to_text = ca._outcome_from_text(
        "Wall time: 300.0s\nOutput:\ntool call error: timed out awaiting tools/call after 299s\n",
        None, 600)
    check(to_text["interrupted"] is True, "'timed out' in output text must set interrupted=True")
    check(to_text["is_error"] is False,
          "a timeout with no explicit non-zero exit code should NOT be force-flagged is_error "
          "(honest gap: codex doesn't always pair timeout with an exit code) got %r" % to_text)

    # --- (2) codex_prep.py end-to-end: distill + drop-agent + --cwd filter ---
    with tempfile.TemporaryDirectory() as work:
        out_all = os.path.join(work, "all")
        rc = codex_prep.main([FIX_ROOT, "--out", out_all, "--min-bytes", "0", "--min-trace", "0"])
        check(rc == 0, "codex_prep.py should exit 0")
        manifest = _load(os.path.join(out_all, "manifest.json"))
        check(manifest["traces"] == 2,
              "expected 2 kept traces (demo1, demo3), got %d" % manifest["traces"])
        check(manifest["agent_sessions_dropped"] == 1,
              "expected demo2 (codex_exec) dropped by default, got %d"
              % manifest["agent_sessions_dropped"])
        sid1 = ca.session_id(demo1)
        sid3 = ca.session_id(demo3)
        check(os.path.exists(os.path.join(out_all, "traces", sid1 + ".txt")),
              "traces/<sid>.txt missing for demo1")
        check(os.path.exists(os.path.join(out_all, "raw", sid1 + ".jsonl")),
              "raw/<sid>.jsonl missing for demo1 (flat staging for emit.py --raw)")
        check(os.path.exists(os.path.join(out_all, "traces", sid3 + ".txt")),
              "traces/<sid>.txt missing for demo3")

        # --keep-agent-sessions must bring demo2 back
        out_keep = os.path.join(work, "keep")
        codex_prep.main([FIX_ROOT, "--out", out_keep, "--min-bytes", "0", "--min-trace", "0",
                          "--keep-agent-sessions"])
        manifest_keep = _load(os.path.join(out_keep, "manifest.json"))
        check(manifest_keep["traces"] == 3,
              "with --keep-agent-sessions expected 3 traces, got %d" % manifest_keep["traces"])

        # --cwd filters to only the widget-app project (demo1), excludes demo3
        out_cwd = os.path.join(work, "cwd")
        codex_prep.main([FIX_ROOT, "--out", out_cwd, "--min-bytes", "0", "--min-trace", "0",
                          "--cwd", "widget-app"])
        manifest_cwd = _load(os.path.join(out_cwd, "manifest.json"))
        check(manifest_cwd["traces"] == 1,
              "--cwd widget-app should keep exactly demo1, got %d traces" % manifest_cwd["traces"])
        check(os.path.exists(os.path.join(out_cwd, "traces", sid1 + ".txt")),
              "--cwd widget-app should keep demo1's trace")
        check(not os.path.exists(os.path.join(out_cwd, "traces", sid3 + ".txt")),
              "--cwd widget-app should NOT keep demo3's trace (different project)")

        # --- (3) full pipeline: codex_outcomes -> ground -> tag_score -> emit ---
        traces_dir = os.path.join(out_all, "traces")
        raw_dir = os.path.join(out_all, "raw")
        outcomes_p = os.path.join(work, "outcomes.json")
        rc = codex_outcomes.main(["--raw", raw_dir, "--out", outcomes_p])
        check(rc == 0, "codex_outcomes.py should exit 0")
        oc = _load(outcomes_p)
        check(oc["sessions"][sid1]["join"] == "id", "outcomes.json join should be id for demo1")
        check(len(oc["sessions"][sid1]["tool_outcomes"]) == 4,
              "outcomes.json should carry 4 tool_outcomes for demo1")

        agent_doc = {"session": sid1, "spans": [
            {"span": [1, 5], "tags": {"action": ["fix-failing-tests"], "surface": ["code"]},
             "actions": [
                 {"tool": "exec_command", "signature": "run go tests",
                  "parameters": {"cmd": "go test ./..."}, "cite": {"line": 3}},
                 {"tool": "apply_patch", "signature": "fix widget.go",
                  "parameters": {"file": "widget.go"}, "cite": {"line": 4}},
                 {"tool": "exec_command", "signature": "rerun go tests",
                  "parameters": {}, "cite": {"line": 5}},
             ]},
            {"span": [6, 7], "tags": {"action": ["rebase-or-resolve-conflicts"], "surface": ["code"]},
             "actions": [
                 {"tool": "exec_command", "signature": "git checkout file",
                  "parameters": {"cmd": "git checkout -- widget.go"}, "cite": {"line": 7}},
             ]},
        ]}
        agent_p = os.path.join(work, "agent.json")
        with open(agent_p, "w") as fh:
            json.dump(agent_doc, fh)

        grounded_p = os.path.join(work, "grounded.json")
        scored_p = os.path.join(work, "scored.json")
        ground.main(["--agent", agent_p, "--traces", traces_dir, "--out", grounded_p])
        tag_score.main(["--grounded", grounded_p, "--traces", traces_dir, "--out", scored_p])
        rc = emit.main(["--scored", scored_p, "--traces", traces_dir, "--raw", raw_dir,
                         "--outcomes", outcomes_p, "--out-dir", work, "--job", "codex-job"])
        check(rc == 0, "emit.py --outcomes should exit 0 UNCHANGED against a codex job")

        analysis = _load(os.path.join(work, "analysis.json"))
        by_id = {i["instance_id"]: i for i in analysis["instances"]}
        inst0 = sid1 + "#0"
        inst1 = sid1 + "#1"
        check(inst0 in by_id, "instance %s missing from analysis.json" % inst0)
        check(inst1 in by_id, "instance %s missing from analysis.json" % inst1)
        if inst0 not in by_id or inst1 not in by_id:
            print("FAIL (%d):" % len(failures))
            for f in failures:
                print("  -", f)
            return 1

        a0 = {a["cite"]["line"]: a for a in by_id[inst0]["actions"]}
        check(a0[3].get("outcome", {}).get("is_error") is True,
              "cite.line==3 (failing test run) outcome.is_error should be True: %r" % a0.get(3))
        check(a0[4].get("outcome", {}).get("is_error") is False,
              "cite.line==4 (apply_patch, success) outcome.is_error should be False: %r" % a0.get(4))
        check(a0[5].get("outcome", {}).get("is_error") is False,
              "cite.line==5 (passing rerun) outcome.is_error should be False: %r" % a0.get(5))
        check("Successfully ran tests" in (a0[5].get("outcome") or {}).get("stdout_head", ""),
              "cite.line==5 outcome.stdout_head should carry the success text (ordinal-alignment "
              "invariant, not just is_error): %r" % a0[5].get("outcome"))

        intents = _load(os.path.join(work, "intents.json"))
        by_iid = {i["instance_id"]: i for i in intents["intents"]}
        # verbatim recovery gracefully degrades on a codex raw/<sid>.jsonl (it
        # doesn't match Claude Code's type=="user" raw shape) -- falls back to
        # the (untruncated-in-this-case) distilled trace line rather than
        # raising or fabricating.
        check(by_iid[inst0]["user_text"] == "run the tests and fix the failing one",
              "user_text should fall back to the distilled USER: line for a codex raw jsonl: %r"
              % by_iid[inst0]["user_text"])

        # satisfaction: span0 is immediately followed by a corrective ask
        # ("actually revert that") + a GROUNDED corrective git op (checkout),
        # so the STRUCTURAL tier fires correctly (corrected:True). The LEXICAL
        # tier (`followup_text_head`) is a KNOWN GAP for codex sessions today:
        # emit.py's `raw_user_turns()` only recognizes Claude Code's raw
        # `type=="user"` shape, so it silently returns [] for a codex raw/
        # <sid>.jsonl and the lexical follow-up stays "" (no crash, no
        # fabrication -- just an absent signal). Flagged in this task's report
        # as a follow-up for whoever wires emit.py's `--raw` against a
        # multi-source job dir; NOT fixed here since it requires touching
        # emit.py, which task 1.1-1.3 (adapter only) deliberately leaves
        # unchanged.
        sat0 = by_id[inst0].get("satisfaction")
        check(sat0 is not None, "instance %s should carry a satisfaction flag with --outcomes" % inst0)
        check(sat0.get("corrected") is True,
              "instance %s satisfaction.corrected should be True (structural tier: grounded "
              "corrective git-checkout op in the immediately-following span): %r" % (inst0, sat0))
        check(sat0.get("corrective_ops"),
              "instance %s satisfaction.corrective_ops should be non-empty: %r" % (inst0, sat0))
        check(sat0.get("followup_text_head") == "",
              "instance %s satisfaction.followup_text_head is expected EMPTY today (documented "
              "gap: raw_user_turns() doesn't recognize codex raw shape) -- got %r; if this now "
              "passes non-empty, emit.py gained codex-aware verbatim recovery and this assertion "
              "should be updated to check for the real text" % (inst0, sat0))

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("OK: codex_adapter/codex_prep/codex_outcomes + ground/tag_score/emit --outcomes "
          "(unchanged) all pass over sanitized fixture rollouts")
    return 0


if __name__ == "__main__":
    sys.exit(run())
