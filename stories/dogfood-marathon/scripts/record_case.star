# record_case.star — append one per-case result to world.results (+ surface any
# findings into world.findings). expr-lang has no list-append, so the record is
# assembled here from the per-case scratch — the slidey-edit accumulate_addressed
# discipline. Nothing is fabricated: every field comes from the journaled scratch.
#
# Interface (authoritative in record_case.star.yaml):
#   inputs:  case (object), triage_verdict (object), drive_result (object), verify_result (object)
#   world:   results (object {items:[...]}), findings (object {items:[...]})
#   outputs: results (object {items:[...]}), findings (object {items:[...]})

def _d(v):
    return v if type(v) == "dict" else {}

def main(ctx):
    case = _d(ctx.inputs.get("case"))
    triage = _d(ctx.inputs.get("triage_verdict"))
    drive = _d(ctx.inputs.get("drive_result"))
    verify = _d(ctx.inputs.get("verify_result"))

    results = ctx.world.get("results") or {"items": []}
    if type(results) != "dict":
        results = {"items": []}
    items = list(results.get("items", []))

    record = {
        "case_id":       case.get("id", ""),
        "title":         case.get("title", ""),
        "triage":        triage.get("verdict", ""),
        "exit":          drive.get("exit", ""),
        "verify_status": verify.get("status", ""),
        "verify_how":    verify.get("how", ""),
        "cost_usd":      drive.get("cost_usd", 0),
        "tokens":        drive.get("tokens", 0),
        "wall_s":        drive.get("wall_s", 0),
        "trace":         drive.get("trace", ""),
    }
    items.append(record)

    # Carry any per-case findings up into the run-level findings list.
    findings = ctx.world.get("findings") or {"items": []}
    if type(findings) != "dict":
        findings = {"items": []}
    fitems = list(findings.get("items", []))
    case_findings = drive.get("findings", [])
    if type(case_findings) == "list":
        for f in case_findings:
            fitems.append(f)

    return {
        "results":  {"items": items},
        "findings": {"items": fitems},
    }
