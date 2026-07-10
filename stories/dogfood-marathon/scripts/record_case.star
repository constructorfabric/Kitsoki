# record_case.star — append one per-case result to world.results, carry
# findings up to the run, and queue serious exceptions for operator review.

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

def _get(obj, key, default):
    v = obj.get(key)
    if v == None:
        return default
    return v

def _bool(v):
    if v == True:
        return True
    if type(v) == "string":
        s = v.strip().lower()
        return s == "true" or s == "1" or s == "yes"
    return False

def _display_number(v):
    s = _str(v).strip()
    if s == "":
        return "0"
    if s.endswith(".000000"):
        return s[:-7]
    if s.endswith(".0"):
        return s[:-2]
    return s

def _severity(drive, verify):
    explicit = _str(drive.get("exception_severity", "")).strip().lower()
    if explicit != "":
        return explicit
    if _bool(drive.get("requires_operator", False)):
        return "serious"
    status = _str(verify.get("status", "")).strip().lower()
    if status == "failed" and not _bool(drive.get("deliverable_present", False)):
        return "serious"
    return ""

def _is_serious(severity):
    s = _str(severity).strip().lower()
    return s == "serious" or s == "critical" or s == "high" or s == "p0" or s == "p1" or s == "blocker"

def _exception_summary(case, drive, verify, severity):
    supplied = _str(drive.get("operator_question", "")).strip()
    if supplied != "":
        return supplied
    supplied = _str(drive.get("exception_summary", "")).strip()
    if supplied != "":
        return supplied
    status = _str(verify.get("status", "")).strip()
    if status != "":
        return "Case " + _str(case.get("id", "")) + " needs operator review after verify status " + status
    return "Case " + _str(case.get("id", "")) + " needs operator review"

def _operator_impact(drive):
    supplied = _str(drive.get("operator_impact", "")).strip()
    if supplied != "":
        return supplied
    supplied = _str(drive.get("exception_impact", "")).strip()
    if supplied != "":
        return supplied
    return "The case remains parked as needs-human until an operator supplies scope."

def _issue_label(case):
    supplied = _str(case.get("issue_label", "")).strip()
    if supplied != "":
        return supplied
    source_url = _str(case.get("issue_url", "") or case.get("source_url", "")).strip()
    marker = "/issues/"
    if marker in source_url:
        parts = source_url.split(marker)
        rest = parts[1] if len(parts) > 1 else ""
        num = rest.split("/")[0].split("#")[0].split("?")[0]
        if num != "":
            return "Issue #" + num
    return _str(case.get("id", "")).strip()

def _link(label, target):
    l = _str(label).strip()
    t = _str(target).strip()
    if l != "" and t != "":
        return "[" + l + "](" + t + ")"
    if t != "":
        return t
    return l

def main(ctx):
    case = _dict(ctx.inputs.get("case"))
    triage = _dict(ctx.inputs.get("triage_verdict"))
    drive = _dict(ctx.inputs.get("drive_result"))
    verify = _dict(ctx.inputs.get("verify_result"))
    exception_policy = _str(_get(ctx.inputs, "exception_policy", _get(ctx.world, "exception_policy", "ask-serious"))).strip() or "ask-serious"

    results = _dict(_get(ctx.inputs, "results", ctx.world.get("results"))) or {"items": []}
    result_items = list(_items(results.get("items", [])))

    source_url = _str(case.get("source_url", "")).strip()
    source_path = _str(case.get("source_path", "")).strip()
    source_repo = _str(case.get("source_repo", "")).strip()
    issue_url = _str(case.get("issue_url", "") or source_url).strip()
    issue_label = _issue_label(case)
    trace = _str(drive.get("trace", "")).strip()
    trace_label = _str(drive.get("trace_label", "")).strip()
    if trace_label == "" and trace != "":
        trace_label = "trace"
    record = {
        "case_id": case.get("id", ""),
        "title": case.get("title", ""),
        "source_kind": case.get("source_kind", ""),
        "source_path": source_path,
        "source_repo": source_repo,
        "source_url": source_url,
        "issue_label": issue_label,
        "issue_url": issue_url,
        "issue_link": _link(issue_label, issue_url),
        "baseline": case.get("baseline", ""),
        "baseline_policy": case.get("baseline_policy", ""),
        "repro_command": case.get("repro_command", ""),
        "triage": triage.get("verdict", ""),
        "triage_evidence": triage.get("evidence", ""),
        "exit": drive.get("exit", ""),
        "worktree": drive.get("worktree", ""),
        "branch": drive.get("branch", ""),
        "fix_ref": drive.get("fix_ref", drive.get("branch", "")),
        "deliverable_present": drive.get("deliverable_present", False),
        "verify_status": verify.get("status", ""),
        "verify_how": verify.get("how", ""),
        "cost_usd": drive.get("cost_usd", 0),
        "cost_display": _display_number(drive.get("cost_usd", 0)),
        "tokens": drive.get("tokens", 0),
        "tokens_display": _display_number(drive.get("tokens", 0)),
        "wall_s": drive.get("wall_s", 0),
        "wall_s_display": _display_number(drive.get("wall_s", 0)),
        "trace": trace,
        "trace_label": trace_label,
        "trace_link": _link(trace_label, trace),
        "summary": drive.get("summary", verify.get("how", "")),
        "operator_question": drive.get("operator_question", ""),
        "operator_impact": _operator_impact(drive),
    }
    result_items.append(record)

    findings = _dict(_get(ctx.inputs, "findings", ctx.world.get("findings"))) or {"items": []}
    finding_items = list(_items(findings.get("items", [])))
    for f in _items(drive.get("findings", [])):
        finding_items.append(f)

    exceptions = _dict(_get(ctx.inputs, "exceptions", ctx.world.get("exceptions"))) or {"items": []}
    exception_items = list(_items(exceptions.get("items", [])))
    severity = _severity(drive, verify)
    pending = ""
    summary = ""
    if _is_serious(severity):
        pending = _str(case.get("id", ""))
        summary = _exception_summary(case, drive, verify, severity)
        exception_items.append({
            "case_id": pending,
            "title": case.get("title", ""),
            "severity": severity,
            "summary": summary,
            "question": drive.get("operator_question", summary),
            "impact": _operator_impact(drive),
            "trace": trace,
            "trace_label": trace_label,
            "trace_link": _link(trace_label, trace),
            "issue_label": issue_label,
            "issue_url": issue_url,
            "issue_link": _link(issue_label, issue_url),
            "source_url": source_url,
            "source_path": source_path,
            "status": "open" if exception_policy == "ask-serious" else "journaled",
        })

    return {
        "results": {"items": result_items},
        "last_result": record,
        "findings": {"items": finding_items},
        "exceptions": {"items": exception_items},
        "exception_pending": pending,
        "exception_summary": summary,
        "record_status": "recorded",
    }
