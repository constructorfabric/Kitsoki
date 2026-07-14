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


def _contains(text, needle):
    return len(_str(text).split(needle)) > 1


def _is_llm_spending_command(cmd):
    lowered = _str(cmd).lower().strip()
    if lowered == "":
        return False
    needles = [
        "claude",
        "codex",
        "openai",
        "anthropic",
        "kitsoki tour",
        "harness live",
        "harness:live",
        "--harness live",
    ]
    for needle in needles:
        if _contains(lowered, needle):
            return True
    return False


def _valid_ladder_policy(value):
    ladder = _dict(value)
    models = _list(ladder.get("models", []))
    if len(models) == 0:
        return False, "harness_ladder.models must be a non-empty list"
    idx = 0
    for raw in models:
        model = _dict(raw)
        if _str(model.get("model", "")).strip() == "":
            return False, "harness_ladder.models[" + str(idx) + "].model is required"
        backend = _str(model.get("backend", "")).strip()
        if backend not in {"": True, "claude": True, "codex": True, "copilot": True}:
            return False, "harness_ladder.models[" + str(idx) + "].backend is invalid: " + backend
        idx += 1
    for effort in _list(ladder.get("efforts", [])):
        e = _str(effort)
        if e not in {"low": True, "medium": True, "high": True, "xhigh": True, "max": True}:
            return False, "harness_ladder.efforts contains invalid value: " + e
    return True, ""


def _story_path_exists(ctx, path):
    p = _str(path).strip()
    if p == "":
        return False
    if ctx.fs.exists(p):
        return True
    return ctx.fs.exists(p.rstrip("/") + "/app.yaml")


def _find_item(items, item_id):
    for raw in _list(items):
        item = _dict(raw)
        if item.get("id", "") == item_id:
            return item
    return None


def _ok():
    return {
        "policy_result": {
            "status": "ok",
            "message": "profile/model/verifier policy passed",
        },
        "error": "",
    }


def _fail(message):
    return {
        "policy_result": {
            "status": "fail",
            "message": message,
        },
        "error": message,
    }


def main(ctx):
    state_path = _str(ctx.inputs.get("state_path", "")).strip()
    item_id = _str(ctx.inputs.get("item_id", "")).strip()
    if state_path == "":
        return _fail("state_path is required")
    if item_id == "":
        return _fail("item_id is required")

    item = _dict(ctx.inputs.get("item", {}))
    if len(item) == 0:
        state = _dict(json.decode(ctx.fs.read(state_path)))
        item = _find_item(state.get("items", []), item_id)
    if item == None:
        return _fail("item not found: " + item_id)

    errors = []
    story = _str(item.get("story", ""))
    if not _story_path_exists(ctx, story):
        errors.append("story path missing: " + story)

    impl_story = _str(item.get("implementation_story", ""))
    if impl_story and not _story_path_exists(ctx, impl_story):
        errors.append("implementation_story path missing: " + impl_story)

    harness = item.get("harness", "")
    live = harness in {"live": True, "ladder": True}
    if live and harness == "ladder":
        ok, msg = _valid_ladder_policy(item.get("harness_ladder", {}))
        if not ok:
            errors.append(msg)
    if live and harness == "live" and item.get("profile", "") != "codex-native":
        errors.append("live work must use profile codex-native")
    if live and harness == "live" and item.get("model", "") != "gpt-5.5":
        errors.append("live work must use model gpt-5.5")
    if impl_story and len(_list(item.get("verify", []))) == 0 and not item.get("gate_command", ""):
        errors.append("implementation item has no deterministic verifier")

    for raw_check in _list(item.get("verify", [])):
        check = _dict(raw_check)
        if check.get("kind", "") == "command" and _is_llm_spending_command(check.get("cmd", "")):
            errors.append("verifier invokes LLM/live command: " + _str(check.get("cmd", "")))

    if len(errors) > 0:
        return _fail("; ".join(errors))
    return _ok()
