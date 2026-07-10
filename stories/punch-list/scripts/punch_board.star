def _dict(v):
    if type(v) == "dict":
        return v
    return {}


def _list(v):
    if type(v) == "list":
        return v
    return []


def _str(v):
    if v == None:
        return ""
    return str(v)


def _read_state(ctx, path):
    if path == "" or not ctx.fs.exists(path):
        return {"items": [], "results": {"items": []}, "defaults": {}}
    state = _dict(json.decode(ctx.fs.read(path)))
    if "results" not in state or type(state.get("results")) != "dict":
        state["results"] = {"items": []}
    if "items" not in state or type(state.get("items")) != "list":
        state["items"] = []
    if "defaults" not in state or type(state.get("defaults")) != "dict":
        state["defaults"] = {}
    return state


def _result_record(item, mark_id, mark_status, mark_error):
    return {
        "id": mark_id,
        "title": _str(item.get("title", "")),
        "status": mark_status,
        "story": _str(item.get("story", "")),
        "implementation_story": _str(item.get("implementation_story", "")),
        "profile": _str(item.get("profile", "")),
        "model": _str(item.get("model", "")),
        "trace_path": _str(item.get("trace_path", "")),
        "summary": mark_error,
    }


def _count(items, status):
    n = 0
    for raw in items:
        if _dict(raw).get("status", "") == status:
            n += 1
    return n


def _pending(items):
    out = []
    for raw in items:
        item = _dict(raw)
        if item.get("status", "") == "pending":
            out.append(item)
    return out


def _summary(processed, passed, partial, failed, skipped, pending):
    return "Processed " + str(processed) + " | Passed " + str(passed) + " | Partial " + str(partial) + " | Failed " + str(failed) + " | Skipped " + str(skipped) + " | Pending " + str(pending)


def main(ctx):
    state_path = _str(ctx.inputs.get("state_path", "")).strip()
    mark_id = _str(ctx.inputs.get("mark_id", "")).strip()
    mark_status = _str(ctx.inputs.get("mark_status", "")).strip()
    mark_error = _str(ctx.inputs.get("mark_error", ""))

    state = _read_state(ctx, state_path)
    items = _list(state.get("items", []))
    results = _dict(state.get("results", {"items": []}))
    result_items = _list(results.get("items", []))

    if mark_id and mark_status:
        next_results = []
        for raw in result_items:
            result = _dict(raw)
            if result.get("id", "") != mark_id:
                next_results.append(result)
        result_items = next_results

        for raw in items:
            item = _dict(raw)
            if item.get("id", "") == mark_id:
                item["status"] = mark_status
                item["last_error"] = mark_error
                result_items.append(_result_record(item, mark_id, mark_status, mark_error))
                break
        results["items"] = result_items

    pending_items = _pending(items)
    passed_count = _count(items, "passed")
    partial_count = _count(items, "partial")
    failed_count = _count(items, "failed")
    skipped_count = _count(items, "skipped")
    pending_count = _count(items, "pending")
    processed_count = len(items) - pending_count
    next_item = {}
    if len(pending_items) > 0:
        next_item = pending_items[0]

    state["items"] = items
    state["results"] = results
    ctx.fs.write(state_path, json.encode(state) + "\n")

    route = "done"
    if len(pending_items) > 0:
        route = "dispatch"

    return {
        "items": items,
        "results": results,
        "next_item": next_item,
        "next_item_id": _str(next_item.get("id", "")),
        "route": route,
        "has_pending": len(pending_items) > 0,
        "processed_count": processed_count,
        "passed_count": passed_count,
        "partial_count": partial_count,
        "failed_count": failed_count,
        "skipped_count": skipped_count,
        "pending_count": pending_count,
        "count_summary": _summary(processed_count, passed_count, partial_count, failed_count, skipped_count, pending_count),
    }
