# verify_case.star — INDEPENDENT verify of a case's deliverable. The honesty
# control: grade on the produced deliverable, NOT on the maker's self-report.
#
# A real drive runs the operator's hidden oracle / targeted tests on the produced
# worktree and checks deliverable existence (the files + key edit are present).
# This script journals that result deterministically from the drive_result:
#   - exit "skipped"                      → status skipped (already handled upstream)
#   - oracle_pass true AND deliverable    → solved
#   - deliverable present, oracle missing → partial (needs human adjudication)
#   - no deliverable / oracle_pass false  → failed
# It NEVER promotes a missing/empty deliverable to a pass.
#
# Under flow tests this call is stubbed by id; this body is the live-drive default.
#
# Interface (authoritative in verify_case.star.yaml):
#   inputs:  case (object), drive_result (object)
#   world:   verify_result (object)
#   outputs: verify_result (object {status, how})

def main(ctx):
    drive = ctx.inputs.get("drive_result", {})
    if type(drive) != "dict":
        drive = {}

    exit = drive.get("exit", "")
    if exit == "skipped":
        return {"verify_result": {"status": "skipped", "how": "case skipped before drive"}}

    deliverable = drive.get("deliverable_present", False)
    oracle_pass = drive.get("oracle_pass", None)

    if not deliverable:
        return {"verify_result": {"status": "failed", "how": "no deliverable present on the produced worktree (independent check)"}}

    if oracle_pass == True:
        return {"verify_result": {"status": "solved", "how": "independent oracle passed on the produced worktree"}}

    if oracle_pass == False:
        return {"verify_result": {"status": "failed", "how": "independent oracle FAILED on the produced worktree"}}

    # Deliverable present but no oracle signal → partial, flagged for adjudication.
    return {"verify_result": {"status": "partial", "how": "deliverable present but no oracle signal — needs adjudication"}}
