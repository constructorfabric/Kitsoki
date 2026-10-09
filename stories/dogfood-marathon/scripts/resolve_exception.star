# resolve_exception.star — mark a serious exception as answered or parked.

def _str(v):
    if v == None:
        return ""
    return str(v)

def _dict(v):
    if type(v) == "dict":
        return v
    return {}

def _items(v):
    if type(v) == "list":
        return v
    return []

def main(ctx):
    exceptions = _dict(ctx.inputs.get("exceptions")) or {"items": []}
    case_id = _str(ctx.inputs.get("case_id")).strip()
    status = _str(ctx.inputs.get("status")).strip() or "parked"
    answer = _str(ctx.inputs.get("answer")).strip()

    out = []
    matched = False
    for item in _items(exceptions.get("items", [])):
        row = dict(_dict(item))
        if case_id != "" and _str(row.get("case_id", "")).strip() == case_id and not matched:
            row["status"] = status
            if answer != "":
                row["answer"] = answer
            matched = True
        out.append(row)

    if not matched and case_id != "":
        row = {
            "case_id": case_id,
            "status": status,
        }
        if answer != "":
            row["answer"] = answer
        out.append(row)

    return {"exceptions": {"items": out}}
