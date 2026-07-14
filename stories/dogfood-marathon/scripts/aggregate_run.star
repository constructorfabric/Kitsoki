# aggregate_run.star — fold results.items into world.rollup. Deterministic; all
# numbers are summed straight from recorded per-case data.

def _dict(v):
    if type(v) == "dict":
        return v
    return {}

def _items(v):
    if type(v) == "list":
        return v
    return []

def _str(v):
    if v == None:
        return ""
    return str(v)

def _num(v):
    if v == None or v == "":
        return 0.0
    return float(v)

def _int(v):
    if v == None or v == "":
        return 0
    return int(v)

def main(ctx):
    results = _dict(ctx.inputs.get("results", {}))
    items = _items(results.get("items", []))
    findings = _dict(ctx.inputs.get("findings", {}))
    exceptions = _dict(ctx.inputs.get("exceptions", {}))

    counts = {
        "processed": 0,
        "solved": 0,
        "partial": 0,
        "failed": 0,
        "skipped": 0,
        "shipped": 0,
        "needs_human": 0,
        "not_reproducible": 0,
        "abandoned": 0,
        "github": 0,
        "local": 0,
        "exceptions": len(_items(exceptions.get("items", []))),
    }
    cost = 0.0
    tokens = 0
    wall = 0

    for r in items:
        item = _dict(r)
        counts["processed"] += 1
        status = _str(item.get("verify_status", "")).lower()
        if status in counts:
            counts[status] += 1
        exit = _str(item.get("exit", "")).lower()
        if exit == "shipped":
            counts["shipped"] += 1
        elif exit == "needs-human":
            counts["needs_human"] += 1
        elif exit == "not-reproducible":
            counts["not_reproducible"] += 1
        elif exit == "abandoned":
            counts["abandoned"] += 1
        source_kind = _str(item.get("source_kind", "")).lower()
        if source_kind == "github":
            counts["github"] += 1
        elif source_kind == "local":
            counts["local"] += 1
        cost += _num(item.get("cost_usd", 0))
        tokens += _int(item.get("tokens", 0))
        wall += _int(item.get("wall_s", 0))

    n_findings = len(_items(findings.get("items", [])))

    worked = []
    didnt = []
    if counts["solved"] > 0:
        worked.append(str(counts["solved"]) + " case(s) independently verified solved")
    if counts["shipped"] > 0:
        worked.append(str(counts["shipped"]) + " case(s) completed through the inner workflow")
    if counts["github"] > 0 or counts["local"] > 0:
        worked.append("backlog mixed " + str(counts["github"]) + " GitHub and " + str(counts["local"]) + " local case(s)")
    if counts["needs_human"] > 0:
        didnt.append(str(counts["needs_human"]) + " case(s) parked at needs-human")
    if counts["exceptions"] > 0:
        didnt.append(str(counts["exceptions"]) + " serious exception(s) raised to the operator")
    if counts["failed"] > 0:
        didnt.append(str(counts["failed"]) + " case(s) failed the independent oracle")
    if counts["skipped"] > 0:
        didnt.append(str(counts["skipped"]) + " case(s) dropped (ALREADY-FIXED degenerate baseline)")

    headline = (
        "Processed " + str(counts["processed"]) + " case(s): " +
        str(counts["solved"]) + " solved, " +
        str(counts["partial"]) + " partial, " +
        str(counts["failed"]) + " failed, " +
        str(counts["exceptions"]) + " serious exception(s)."
    )

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
