def _str(v):
    if v == None:
        return ""
    return str(v)


def _contains(text, needle):
    return len(_str(text).split(needle)) > 1


def _basename(path):
    parts = _str(path).split("/")
    if len(parts) == 0:
        return ""
    return parts[-1]


def _stem(path):
    base = _basename(path)
    parts = base.split(".")
    if len(parts) <= 1:
        return base
    return ".".join(parts[:-1])


def _allowed(ch):
    return _contains("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", ch)


def _slug(value):
    out = ""
    last_dash = False
    for ch in _str(value).strip().elems():
        if _allowed(ch):
            out += ch
            last_dash = False
        elif not last_dash:
            out += "-"
            last_dash = True
    out = out.strip("-")
    if out == "":
        return "score"
    return out


def main(ctx):
    bench = _str(ctx.inputs.get("bench_manifest", ""))
    case_id = _str(ctx.inputs.get("case_id", ""))
    out_dir = _str(ctx.inputs.get("output_dir", "")).rstrip("/")
    if out_dir == "":
        out_dir = ".artifacts/model-task-engineering"

    stem = _slug(case_id)
    if stem == "score":
        stem = _slug(_stem(bench))
    prefix = out_dir + "/" + stem
    return {
        "report_json": prefix + "-report.json",
        "report_markdown": prefix + "-report.md",
        "report_deck": prefix + "-deck.slidey.json",
    }
