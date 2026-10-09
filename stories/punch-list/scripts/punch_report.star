STATUSES = ["passed", "partial", "failed", "skipped", "pending"]


def _str(v):
    if v == None:
        return ""
    return str(v)


def _list(v):
    if type(v) == "list":
        return v
    return []


def _dict(v):
    if type(v) == "dict":
        return v
    return {}


def _escape_cell(text):
    out = ""
    for ch in _str(text).elems():
        if ch == "|":
            out += "\\|"
        elif ch == "\n":
            out += "<br>"
        else:
            out += ch
    return out


def _count_status(items, status):
    n = 0
    for item in items:
        if _dict(item).get("status", "") == status:
            n += 1
    return n


def _results_by_id(results):
    out = {}
    for result in _list(_dict(results).get("items", [])):
        r = _dict(result)
        rid = _str(r.get("id", ""))
        if rid:
            out[rid] = r
    return out


def _render(state, summary):
    items = _list(state.get("items", []))
    by_id = _results_by_id(state.get("results", {}))
    lines = [
        "# Punch-list Report",
        "",
        "Manifest: `" + _str(state.get("manifest_path", "")) + "`",
        "Summary: " + summary,
        "",
        "| Item | Status | Story | Trace | Summary |",
        "|---|---:|---|---|---|",
    ]
    for raw in items:
        item = _dict(raw)
        item_id = _str(item.get("id", ""))
        result = _dict(by_id.get(item_id, {}))
        trace = result.get("trace_path", "")
        if not trace:
            trace = item.get("trace_path", "")
        result_summary = result.get("summary", "")
        if not result_summary:
            result_summary = item.get("last_error", "")
        lines.append(
            "| "
            + item_id
            + " | "
            + _str(item.get("status", ""))
            + " | `"
            + _str(item.get("story", ""))
            + "` | `"
            + _str(trace)
            + "` | "
            + _escape_cell(result_summary)
            + " |"
        )
    return "\n".join(lines) + "\n"


def main(ctx):
    state_path = _str(ctx.inputs.get("state_path", "")).strip()
    if state_path == "":
        fail("state_path is required")
    state = _dict(json.decode(ctx.fs.read(state_path)))
    items = _list(state.get("items", []))
    counts = {status: _count_status(items, status) for status in STATUSES}
    summary = (
        str(counts["passed"])
        + " passed, "
        + str(counts["partial"])
        + " partial, "
        + str(counts["failed"])
        + " failed, "
        + str(counts["skipped"])
        + " skipped, "
        + str(counts["pending"])
        + " pending"
    )
    report_path = ctx.fs.write(".artifacts/punch-list/report.md", _render(state, summary))
    return {"report_path": report_path, "summary": summary}
