# summarize_transport_suite.star -- turn run.py's transport-suite JSON into
# operator-facing preview text. The JSON keeps internal terms like "legs";
# this script keeps that vocabulary out of the UI.

def _str(v):
    if v == None:
        return ""
    return str(v)


def _dict(v):
    if type(v) == "dict":
        return v
    return {}


def _list(v):
    if type(v) == "list":
        return v
    return []


def _join(items, sep):
    out = ""
    first = True
    for item in items:
        text = _str(item)
        if text == "":
            continue
        if not first:
            out += sep
        out += text
        first = False
    return out


def _plural(n, singular, plural):
    if n == 1:
        return singular
    return plural


def _scenario_name(scenario, fallback):
    sid = _str(scenario.get("scenario", ""))
    label = _str(scenario.get("label", ""))
    if label != "" and sid != "" and label != sid:
        return label + " (`" + sid + "`)"
    if label != "":
        return label
    if sid != "":
        return "`" + sid + "`"
    if fallback != "":
        return "`" + fallback + "`"
    return "the requested scenario"


def _contract_text(leg):
    contract = _dict(leg.get("evidence_contract", {}))
    kind = _str(contract.get("evidence_kind", ""))
    if kind == "":
        kind = _join(_list(leg.get("evidence", [])), ", ")
    if kind == "":
        kind = "evidence"

    level = _str(contract.get("level", ""))
    tool = _str(contract.get("primary_tool", ""))
    text = kind
    if level != "":
        text += " (" + level + ")"
    if tool != "":
        text += " via " + tool
    return text


def _transport_name(leg):
    label = _str(leg.get("transport_label", ""))
    transport = _str(leg.get("transport", ""))
    if label != "" and transport != "" and label != transport:
        return label + " (`" + transport + "`)"
    if label != "":
        return label
    if transport != "":
        return "`" + transport + "`"
    return "transport"


def main(ctx):
    suite = _dict(ctx.inputs.get("suite", {}))
    scenario_ref = _str(ctx.inputs.get("scenario_ref", ""))
    scenario_description = _str(ctx.inputs.get("scenario_description", ""))
    mode = _str(ctx.inputs.get("mode", "catalog"))
    summary = _dict(suite.get("summary", {}))
    scenarios = _list(suite.get("scenarios", []))
    legs = _list(suite.get("legs", []))
    live_authorization_summary = _list(suite.get("live_authorization_summary", []))

    leg_count = int(summary.get("leg_count", len(legs)) or 0)
    skipped_count = int(summary.get("skipped_count", 0) or 0)
    scenario_name = "the requested scenario"
    if mode == "adhoc" and scenario_description != "":
        scenario_name = scenario_description
    elif len(scenarios) > 0:
        scenario_name = _scenario_name(_dict(scenarios[0]), scenario_ref)
    elif scenario_ref != "":
        scenario_name = "`" + scenario_ref + "`"

    preview_status = _str(suite.get("status", ""))
    if preview_status == "":
        preview_status = "ready"

    suite_summary = (
        "Preview " + preview_status + ": " + _str(leg_count) + " " +
        _plural(leg_count, "transport check", "transport checks") +
        " for " + scenario_name +
        ". No run bundle or capture has been created."
    )
    if skipped_count > 0:
        suite_summary += (
            " " + _str(skipped_count) + " requested " +
            _plural(skipped_count, "transport is", "transports are") +
            " not applicable for this scenario."
        )
    if len(live_authorization_summary) > 0:
        suite_summary += (
            " " + _str(len(live_authorization_summary)) + " " +
            _plural(len(live_authorization_summary), "leg needs", "legs need") +
            " a live profile to drive live; add profile=<name> to the request or they will run replay-only."
        )

    lines = []
    if len(live_authorization_summary) > 0:
        lines.append("Live authorization:")
        for note in live_authorization_summary:
            lines.append("- " + _str(note))
        lines.append("")
    if mode == "adhoc" and scenario_description != "":
        lines.append("Scenario: " + scenario_description)
        lines.append("Goal: Check that the requested behavior is usable in the selected transports: " + scenario_description)
        requested = _join(_list(summary.get("requested_transports", [])), ", ")
        if requested != "":
            lines.append("Transports to check: " + requested)
        lines.append("")
    elif len(scenarios) > 0:
        for scenario in scenarios:
            scenario = _dict(scenario)
            lines.append("Scenario: " + _scenario_name(scenario, scenario_ref))
            task = _str(scenario.get("task", ""))
            if task != "":
                lines.append("Goal: " + task)
            applicable = _join(_list(scenario.get("applicable_transports", [])), ", ")
            if applicable != "":
                lines.append("Transports to check: " + applicable)
            skipped = _join(_list(scenario.get("skipped_transports", [])), ", ")
            if skipped != "":
                lines.append("Not applicable for this scenario: " + skipped)
            lines.append("")

    if len(legs) > 0:
        lines.append("Transport checks:")
        for leg in legs:
            leg = _dict(leg)
            visual = _str(leg.get("visual_surface", ""))
            transport = _str(leg.get("transport", ""))
            surface = ""
            if visual != "" and visual != transport:
                surface = " (" + visual + " surface)"
            lines.append("- " + _transport_name(leg) + surface + ": " + _contract_text(leg))
    else:
        lines.append("No transport checks resolved for this preview.")

    return {
        "suite_summary": suite_summary,
        "suite_plan": _join(lines, "\n"),
    }
