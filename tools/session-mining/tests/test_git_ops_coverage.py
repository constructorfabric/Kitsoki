#!/usr/bin/env python3
"""Flagship git-ops coverage-mining test — the whole chain end-to-end, NO LLM.

Runs the deterministic spine (ground -> tag_score -> outcomes -> emit --outcomes
-> coverage_prep) over the COMMITTED git-ops example corpus
(examples/git-ops/{raw,traces,agent.json}) and asserts every signal the flagship
demonstrates:

  * outcome recovery per action (is_error / stdout / stderr),
  * the rebase conflict surfaces as is_error:true then resolved,
  * the commit->amend gate-gap surfaces as satisfaction.corrected:true with the
    corrective ops,
  * coverage_prep scope-filters, dedups arg-aware, joins candidate rooms, inlines
    outcomes, and HINTS the force-push non_goal,
  * schema + cross-link contracts hold.

The agent output (step B, the one LLM pass) is the committed examples/git-ops/
agent.json fixture — exactly as test_outcomes.py / test_intent_pipeline.py do.
No LLM, no cost (per AGENTS.md). When `jq` is on PATH we ALSO regenerate the
traces from the real distill.jq and assert byte-fidelity against the committed
traces, so the corpus can never silently drift from the real distiller.

Run:  python3 tools/session-mining/tests/test_git_ops_coverage.py
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
EX = os.path.join(TOOL, "examples", "git-ops")
PROFILE = os.path.normpath(os.path.join(TOOL, "..", "..", "stories", "git-ops", "mining.profile.yaml"))
sys.path.insert(0, TOOL)

import ground
import tag_score
import outcomes
import emit
import coverage_prep
import validate_reports
import verify_link


def _load(p):
    with open(p) as fh:
        return json.load(fh)


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    traces = os.path.join(EX, "traces")
    raw = os.path.join(EX, "raw")

    # --- (0) trace fidelity: regenerate from the REAL distill.jq when jq present ---
    if shutil.which("jq"):
        for fn in sorted(os.listdir(raw)):
            if not fn.endswith(".jsonl"):
                continue
            sid = fn[:-len(".jsonl")]
            got = subprocess.run(
                ["jq", "-r", "-f", os.path.join(TOOL, "distill.jq"), os.path.join(raw, fn)],
                capture_output=True, text=True).stdout
            with open(os.path.join(traces, sid + ".txt")) as fh:
                want = fh.read()
            check(got == want,
                  "trace fidelity: committed traces/%s.txt != distill.jq(raw/%s) — re-distill the corpus" % (sid, sid))
    else:
        print("note: jq not found; skipping trace-fidelity regeneration check", file=sys.stderr)

    with tempfile.TemporaryDirectory() as work:
        grounded = os.path.join(work, "grounded.json")
        scored = os.path.join(work, "scored.json")
        outc = os.path.join(work, "outcomes.json")

        ground.main(["--agent", os.path.join(EX, "agent.json"), "--traces", traces, "--out", grounded])
        tag_score.main(["--grounded", grounded, "--traces", traces, "--out", scored])
        outcomes.main(["--raw", raw, "--out", outc])
        emit.main(["--scored", scored, "--traces", traces, "--raw", raw,
                   "--outcomes", outc, "--out-dir", work, "--job", "gitops-flagship"])

        analysis = _load(os.path.join(work, "analysis.json"))
        by_id = {i["instance_id"]: i for i in analysis["instances"]}

        # (1) all 7 instances present
        expect_ids = {"sess-commit-happy#0", "sess-commit-amend#0", "sess-commit-amend#1",
                      "sess-rebase-conflict#0", "sess-merge-direct#0", "sess-worktree#0",
                      "sess-forcepush#0"}
        check(expect_ids <= set(by_id), "missing instances: %s" % (expect_ids - set(by_id)))
        if not expect_ids <= set(by_id):
            print("FAIL (%d):" % len(failures))
            for f in failures:
                print("  -", f)
            return 1

        def outcome_at(iid, line):
            for a in by_id[iid]["actions"]:
                if (a.get("cite") or {}).get("line") == line:
                    return a.get("outcome")
            return None

        # (2) CONFORMS happy commit — both ok
        oc = outcome_at("sess-commit-happy#0", 4)
        check(oc and oc.get("is_error") is False and "feat-auth 1a2b3c4" in oc.get("stdout_head", ""),
              "commit-happy: commit outcome should be ok with the commit sha line: %r" % oc)

        # (3) FIXTURE-GAP rebase conflict — rebase is_error:true (CONFLICT), continue ok
        rc = outcome_at("sess-rebase-conflict#0", 2)
        check(rc and rc.get("is_error") is True and "CONFLICT" in rc.get("stderr_head", ""),
              "rebase-conflict: `git rebase main` must be is_error:true w/ CONFLICT: %r" % rc)
        cont = outcome_at("sess-rebase-conflict#0", 6)
        check(cont and cont.get("is_error") is False and "Successfully rebased" in cont.get("stdout_head", ""),
              "rebase-conflict: `git rebase --continue` must be ok w/ 'Successfully rebased': %r" % cont)

        # (4) gate-gap — commit succeeded BUT satisfaction.corrected:true w/ corrective ops
        sat = by_id["sess-commit-amend#0"]["satisfaction"]
        commit_oc = outcome_at("sess-commit-amend#0", 2)
        check(commit_oc and commit_oc.get("is_error") is False,
              "commit-amend#0: the commit itself must be is_error:false (the point: succeeded yet reworked)")
        check(sat and sat.get("corrected") is True,
              "commit-amend#0: satisfaction.corrected must be True (next span amends): %r" % sat)
        check(sat and any("--amend" in o or "reset" in o for o in sat.get("corrective_ops", [])),
              "commit-amend#0: corrective_ops must include reset/amend: %r" % (sat or {}).get("corrective_ops"))
        check("amend it" in (sat or {}).get("followup_text_head", ""),
              "commit-amend#0: followup_text_head must carry the correction: %r" % (sat or {}).get("followup_text_head"))
        # the corrective span itself must NOT be flagged corrected (no following span)
        sat1 = by_id["sess-commit-amend#1"]["satisfaction"]
        check(sat1 and sat1.get("corrected") is False,
              "commit-amend#1: corrected must be False (it IS the correction): %r" % sat1)

        # (5) DIVERGES merge — merged ok (so the story's descendant guard, which would
        #     block, diverges from what really worked). We assert the recovered fact.
        mo = outcome_at("sess-merge-direct#0", 3)
        check(mo and mo.get("is_error") is False and "Merge made" in mo.get("stdout_head", ""),
              "merge-direct: `git merge --no-ff` must be ok w/ 'Merge made': %r" % mo)

        # (6) contracts hold
        check(not verify_link.main([work]), "verify_link must pass")
        errs = validate_reports.validate_job(work)
        check(not errs, "schema validation failed:\n    " + "\n    ".join(errs))

        # --- coverage_prep: scope filter, dedup, candidate rooms, non_goal hint ---
        coverage_prep.main(["--job-dir", work, "--profile", PROFILE, "--out-dir", work])
        git = _load(os.path.join(work, "intents.git.json"))
        check(git["total_in_scope"] == 7, "coverage_prep: expected 7 in-scope, got %d" % git["total_in_scope"])
        check(git["deduped_shapes"] == 7, "coverage_prep: expected 7 deduped shapes, got %d" % git["deduped_shapes"])
        rows = {r["instance_id"]: r for r in git["intents"]}
        # candidate-room join from profile.owns
        check("conflict" in rows["sess-rebase-conflict#0"]["candidate_rooms"],
              "coverage_prep: rebase-conflict candidate rooms must include 'conflict': %r"
              % rows["sess-rebase-conflict#0"]["candidate_rooms"])
        check("worktree_create" in rows["sess-worktree#0"]["candidate_rooms"],
              "coverage_prep: worktree candidate rooms must include 'worktree_create'")
        # non_goal HINT on the force-push intent
        check(rows["sess-forcepush#0"]["non_goal_hint"],
              "coverage_prep: force-push intent must carry a non_goal hint: %r"
              % rows["sess-forcepush#0"]["non_goal_hint"])
        check(not rows["sess-commit-happy#0"]["non_goal_hint"],
              "coverage_prep: a plain commit must NOT carry a non_goal hint")
        # outcome inlined into the scope-filtered recipe
        rc_actions = {a["signature"]: a for a in rows["sess-rebase-conflict#0"]["actions"]}
        check(rc_actions.get("git rebase <branch>", {}).get("outcome", {}).get("is_error") is True,
              "coverage_prep: rebase action must carry its is_error:true outcome")
        # coverage.md skeleton rendered with a Verdict column to fill
        with open(os.path.join(work, "coverage.md")) as fh:
            md = fh.read()
        check("Verdict" in md and "force push" in md and "corrected" in md,
              "coverage.md skeleton missing Verdict column / non_goal hint / satisfaction marker")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: git-ops flagship coverage mining (5 verdict signals + coverage_prep, no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
