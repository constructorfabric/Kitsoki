def _str(v):
    if v == None:
        return ""
    return str(v)


def _basename(path):
    pieces = _str(path).rstrip("/").split("/")
    return pieces[-1] if pieces else ""


def _stem(path):
    pieces = _basename(path).split(".")
    return ".".join(pieces[:-1]) if len(pieces) > 1 else _basename(path)


def main(ctx):
    out = _str(ctx.inputs.get("output_dir", "")).rstrip("/")
    if out == "":
        out = ".artifacts/task-optimization"
    study = _stem(_str(ctx.inputs.get("study", "")))
    if study == "":
        study = "study"
    root = out + "/" + study
    return {
        "campaign_root": root,
        "campaign_plan": root + "/plan.json",
        "campaign_markdown": root + "/plan.md",
        "campaign_deck": root + "/plan-deck.slidey.json",
        "campaign_arm_receipt": root + "/armed.json",
    }
