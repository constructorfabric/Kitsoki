def _mark(briefs, mark_id, mark_status, mark_error):
    out = []
    for b in briefs:
        n = dict(b)
        if mark_id != "" and mark_status != "" and n.get("id") == mark_id:
            n["status"] = mark_status
            if mark_error != "":
                n["last_error"] = mark_error
        out.append(n)
    return out


def main(ctx):
    briefs = _mark(
        ctx.inputs["fleet_briefs"],
        ctx.inputs["mark_id"],
        ctx.inputs["mark_status"],
        ctx.inputs["mark_error"],
    )

    pending = []
    shipped = 0
    parked = 0
    for b in briefs:
        status = b.get("status")
        if status == "pending":
            pending.append(b)
        elif status == "shipped":
            shipped += 1
        elif status == "parked":
            parked += 1

    return {
        "fleet_briefs": briefs,
        "next_brief": pending[0] if pending else {},
        "has_pending": len(pending) > 0,
        "route": "dispatch" if pending else "done",
        "shipped_count": shipped,
        "parked_count": parked,
        "pending_count": len(pending),
    }
