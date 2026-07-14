# build_report.star — fold leg_results into a report summary/path for the
# per-transport verdict table. The actual report.md file (including its
# per-leg rows and the deterministic `cause` column -- see
# record_leg_result.star) is written by
# tools/product-journey/run.py --scenario-qa-report's render_scenario_qa_markdown,
# in the same run directory as deck.slidey.json; this script stays
# file-system-free so a scenario-qa run bundle can live in an automatically
# managed capsule workspace. It ONLY recomputes the summary counts and the
# report path -- do not re-add markdown table rendering here, it would be
# dead code no reader ever sees (report.md's real byte contents come from the
# Python function above; keeping two divergent table builders is exactly the
# kind of drift issue group B's report-cause work found and removed).
# leg_results is a templated INPUT (not read via ctx.world) so the engine's
# dispatch-time re-render of this invoke's `inputs:` picks up the very last
# leg's recording even when this room is entered in the same cascading turn
# as that leg's record_leg_result (see plan.yaml's header comment for the
# general gotcha).
#
# Interface (authoritative in build_report.star.yaml):
#   inputs:  run_id (string), run_dir (string, absolute or repo-relative),
#            scenario_ref (string), scenario_description (string),
#            leg_results (object {items:[...]})
#   outputs: report_path (string), report_summary (string)

def _str(v):
    if v == None:
        return ""
    return str(v)


def _items(v):
    if type(v) == "dict":
        return v.get("items", [])
    return []


def main(ctx):
    run_id = _str(ctx.inputs.get("run_id", ""))
    run_dir = _str(ctx.inputs.get("run_dir", ""))

    leg_results = ctx.inputs.get("leg_results") or {"items": []}
    items = _items(leg_results)

    passes = 0
    fails = 0
    degraded = 0
    for item in items:
        v = item.get("verdict", "")
        if v == "pass":
            passes += 1
        elif v == "degraded-evidence":
            degraded += 1
        elif v != "" and v != "unjudged":
            fails += 1

    summary = _str(passes) + " / " + _str(len(items)) + " transport checks passed"
    if fails > 0:
        summary += ", " + _str(fails) + " failed"
    if degraded > 0:
        summary += ", " + _str(degraded) + " degraded-evidence"
    summary += "."

    if run_dir != "":
        path = run_dir + "/report.md"
    else:
        path = ".artifacts/scenario-qa/" + (run_id if run_id != "" else "adhoc") + "/report.md"

    return {"report_path": path, "report_summary": summary}
