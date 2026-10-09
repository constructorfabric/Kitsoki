def _text(v):
    if v == None:
        return ""
    return str(v)


def _brief_text(b):
    for key in ["brief", "agent_brief", "goal", "title"]:
        v = b.get(key)
        if v:
            return _text(v)
    return ""


def main(ctx):
    path = ctx.inputs["decomposition_path"]
    if path == "":
        return {"fleet_briefs": [], "fleet_state_path": "", "error": "no decomposition path"}

    state_path = ctx.inputs["fleet_state_path"]
    if state_path == "":
        state_path = path + ".fleet-state.json"

    doc = yaml.decode(ctx.fs.read(path))
    if doc == None:
        doc = {}

    out = []
    for b in doc.get("briefs", []):
        out.append({
            "id": _text(b.get("id")),
            "brief": _brief_text(b),
            "gate_command": _text(b.get("gate_command") or b.get("test_plan") or "").strip(),
            "status": "pending",
            "last_error": "",
        })

    return {"fleet_briefs": out, "fleet_state_path": state_path, "error": ""}
