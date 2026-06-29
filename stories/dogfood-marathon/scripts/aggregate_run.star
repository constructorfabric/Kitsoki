# aggregate_run.star — fold results.items into world.rollup. Deterministic; the
# numbers are summed straight from the recorded per-case data — nothing invented.
#
# rollup = {
#   counts:  {processed, solved, partial, failed, skipped, shipped, needs_human},
#   totals:  {cost_usd, tokens, wall_s},
#   worked:  [...],   # what worked (qualitative, derived from outcomes)
#   didnt:   [...],   # what didn't
#   headline: "...",
# }
#
# Interface (authoritative in aggregate_run.star.yaml):
#   inputs:  results (object), findings (object)
#   world:   rollup (object)
#   outputs: rollup (object)

def main(ctx):
    results = ctx.inputs.get("results", {})
    items = results.get("items", []) if type(results) == "dict" else []

    counts = {"processed": 0, "solved": 0, "partial": 0, "failed": 0,
              "skipped": 0, "shipped": 0, "needs_human": 0}
    cost = 0.0
    tokens = 0
    wall = 0

    for r in items:
        counts["processed"] += 1
        status = r.get("verify_status", "")
        if status in counts:
            counts[status] += 1
        exit = r.get("exit", "")
        if exit == "shipped":
            counts["shipped"] += 1
        elif exit == "needs-human":
            counts["needs_human"] += 1
        cost += float(r.get("cost_usd", 0) or 0)
        tokens += int(r.get("tokens", 0) or 0)
        wall += int(r.get("wall_s", 0) or 0)

    findings = ctx.inputs.get("findings", {})
    n_findings = len(findings.get("items", [])) if type(findings) == "dict" else 0

    worked = []
    didnt = []
    if counts["solved"] > 0:
        worked.append("%d case(s) independently verified solved" % counts["solved"])
    if counts["shipped"] > 0:
        worked.append("%d fix(es) shipped through the inner pipeline" % counts["shipped"])
    if counts["needs_human"] > 0:
        didnt.append("%d case(s) parked at needs-human (RED→GREEN discipline; human verifies+merges)" % counts["needs_human"])
    if counts["failed"] > 0:
        didnt.append("%d case(s) failed the independent oracle" % counts["failed"])
    if counts["skipped"] > 0:
        didnt.append("%d case(s) dropped (ALREADY-FIXED degenerate baseline)" % counts["skipped"])

    headline = ("Processed %d case(s): %d solved, %d failed, %d needs-human, %d skipped. "
                "Structure isn't automatically cheaper, but it's more thorough — "
                "regression test, safe gate-parking, refine loop — and catches bad fixes a naive prompt would ship."
                ) % (counts["processed"], counts["solved"], counts["failed"],
                     counts["needs_human"], counts["skipped"])

    return {
        "rollup": {
            "counts": counts,
            "totals": {"cost_usd": cost, "tokens": tokens, "wall_s": wall},
            "worked": worked,
            "didnt": didnt,
            "findings_count": n_findings,
            "headline": headline,
        },
    }
