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


def _is_abs(path):
    return _str(path).startswith("/")


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


def _is_alnum(ch):
    alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
    return _contains(alphabet, ch)


def _tokens(s):
    out = {}
    cur = ""
    for ch in _str(s).lower().elems():
        if _is_alnum(ch):
            cur += ch
        elif cur:
            out[cur] = True
            cur = ""
    if cur:
        out[cur] = True

    joined = "".join(sorted(out.keys()))
    if _contains(joined, "top10") or ("top" in out and "10" in out):
        out["top10"] = True
    if _contains(joined, "gpt55") or ("gpt" in out and "5" in out):
        out["gpt55"] = True
    return out


def _default_state_path(manifest_path):
    base = _stem(manifest_path)
    if base == "":
        base = "punch-list"
    return ".artifacts/punch-list/" + base + ".state.json"


def _story_path_exists(ctx, path):
    p = _str(path).strip()
    if p == "":
        return False
    if _is_abs(p):
        return False
    if ctx.fs.exists(p):
        return True
    return ctx.fs.exists(p.rstrip("/") + "/app.yaml")


def _resolve_manifest_path(ctx, raw):
    raw = _str(raw).strip()
    if raw == "" or _is_abs(raw):
        return raw
    if ctx.fs.exists(raw):
        return raw

    testdata_path = "stories/punch-list/testdata/" + raw
    if ctx.fs.exists(testdata_path):
        return testdata_path

    wanted = _tokens(raw)
    if len(wanted) == 0:
        return raw

    best_path = ""
    best_score = 0
    tied = False
    for p in ctx.fs.glob("stories/punch-list/testdata/*.yaml"):
        have = _tokens(_stem(p))
        score = 0
        for tok in wanted.keys():
            if tok in have:
                score += 1
        if score > best_score:
            best_score = score
            best_path = p
            tied = False
        elif score == best_score and score > 0:
            tied = True
    if best_score == 0 or tied:
        return raw
    return best_path


def _copy_dict(d):
    out = {}
    for k in d.keys():
        out[k] = d[k]
    return out


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


def _trace_path(trace_root, reuse, run_id, item_id, suffix):
    trace_dir = trace_root
    if not reuse:
        trace_dir = trace_root.rstrip("/") + "/" + run_id
    return trace_dir.rstrip("/") + "/" + item_id + suffix + ".jsonl"


def _insert_sorted(items, item):
    priority = item.get("priority", 0)
    item_id = _str(item.get("id", ""))
    out = []
    inserted = False
    for current in items:
        cur_priority = _dict(current).get("priority", 0)
        cur_id = _str(_dict(current).get("id", ""))
        if not inserted and (priority < cur_priority or (priority == cur_priority and item_id < cur_id)):
            out.append(item)
            inserted = True
        out.append(current)
    if not inserted:
        out.append(item)
    return out


def _normalize_manifest(ctx, doc, run_id):
    errors = []
    if type(doc) != "dict":
        return [], ["manifest must be a mapping"], {}
    if doc.get("version") != "punch-list/v1":
        errors.append("version must be punch-list/v1")

    defaults = _copy_dict(_dict(doc.get("defaults", {})))
    defaults.setdefault("harness", "live")
    defaults.setdefault("profile", "codex-native")
    defaults.setdefault("model", "gpt-5.5")
    defaults.setdefault("trace_root", ".artifacts/punch-list/traces")
    defaults.setdefault("reuse_trace_paths", False)
    defaults.setdefault("require_gpt55", True)
    defaults.setdefault("require_trace_model", True)

    if run_id == "":
        run_id = _str(defaults.get("trace_run_id", "")).strip()
    if run_id == "":
        run_id = "deterministic"
    defaults["trace_run_id"] = run_id

    seen = {}
    items = []
    raw_items = doc.get("items", [])
    if type(raw_items) != "list" or len(raw_items) == 0:
        errors.append("items must be a non-empty list")
        raw_items = []

    idx = 0
    for raw in raw_items:
        if type(raw) != "dict":
            errors.append("items[" + str(idx) + "] must be a mapping")
            idx += 1
            continue

        item = _copy_dict(raw)
        item_id = _str(item.get("id", "")).strip()
        if item_id == "":
            errors.append("items[" + str(idx) + "].id is required")
            item_id = "item-" + str(idx + 1)
        if item_id in seen:
            errors.append("duplicate item id: " + item_id)
        seen[item_id] = True

        normalized = _copy_dict(defaults)
        for k in item.keys():
            normalized[k] = item[k]
        normalized["id"] = item_id
        normalized.setdefault("title", item_id)
        normalized.setdefault("priority", idx + 1)
        normalized.setdefault("mode", "drive")
        normalized.setdefault("prompt", "")
        normalized.setdefault("intent", "")
        normalized.setdefault("slots", {})
        normalized.setdefault("implementation_story", "")
        normalized.setdefault("implementation_prompt", "")
        normalized.setdefault("gate_command", "")
        normalized.setdefault("verify", [])
        normalized.setdefault("findings_policy", {})
        normalized.setdefault("status", "pending")
        normalized.setdefault("last_error", "")

        story = _str(normalized.get("story", ""))
        if not _story_path_exists(ctx, story):
            errors.append(item_id + ": story path does not exist: " + story)

        impl_story = _str(normalized.get("implementation_story", ""))
        if impl_story and not _story_path_exists(ctx, impl_story):
            errors.append(item_id + ": implementation_story path does not exist: " + impl_story)

        live_impl = normalized.get("harness") == "live" and (impl_story or normalized.get("mode") == "drive")
        if live_impl and normalized.get("require_gpt55", True):
            if normalized.get("profile") != "codex-native":
                errors.append(item_id + ": live work must use profile codex-native")
            if normalized.get("model") != "gpt-5.5":
                errors.append(item_id + ": live work must use model gpt-5.5")

        verify = normalized.get("verify", [])
        if impl_story and not verify and not normalized.get("gate_command", ""):
            errors.append(item_id + ": implementation items require a deterministic verifier")
        if type(verify) != "list":
            errors.append(item_id + ": verify must be a list")
            verify = []
            normalized["verify"] = verify

        v_idx = 0
        for raw_check in verify:
            if type(raw_check) != "dict":
                errors.append(item_id + ": verify[" + str(v_idx) + "] must be a mapping")
                v_idx += 1
                continue
            check = raw_check
            kind = check.get("kind", "")
            if kind not in {"command": True, "story_validate": True, "story_test": True, "render_tui": True, "render_web": True}:
                errors.append(item_id + ": verify[" + str(v_idx) + "].kind is unsupported: " + _str(kind))
            if kind in {"story_validate": True, "story_test": True, "render_tui": True, "render_web": True} and not _story_path_exists(ctx, check.get("story", "")):
                errors.append(item_id + ": verify[" + str(v_idx) + "].story path does not exist: " + _str(check.get("story", "")))
            if kind == "command" and _is_llm_spending_command(check.get("cmd", "")):
                errors.append(item_id + ": verify[" + str(v_idx) + "].cmd appears to invoke an LLM or live run")
            v_idx += 1

        if normalized.get("gate_command", "") and _is_llm_spending_command(normalized.get("gate_command", "")):
            errors.append(item_id + ": gate_command appears to invoke an LLM or live run")

        trace_root = _str(normalized.get("trace_root", ".artifacts/punch-list/traces"))
        reuse = normalized.get("reuse_trace_paths", False)
        if not normalized.get("trace_path", ""):
            normalized["trace_path"] = _trace_path(trace_root, reuse, run_id, item_id, "")
        if not normalized.get("implementation_trace_path", ""):
            normalized["implementation_trace_path"] = _trace_path(trace_root, reuse, run_id, item_id, "-implementation")

        items = _insert_sorted(items, normalized)
        idx += 1

    return items, errors, defaults


def _join_lines(lines):
    return "\n".join(lines)


def main(ctx):
    raw_manifest_path = _str(ctx.inputs.get("manifest_path", "")).strip()
    if raw_manifest_path == "":
        return {
            "items": [],
            "item_count": "0",
            "manifest_path": "",
            "state_path": "",
            "error": "manifest_path is required",
        }

    manifest_path = _resolve_manifest_path(ctx, raw_manifest_path)
    state_path = _str(ctx.inputs.get("state_path", "")).strip()
    if state_path == "":
        state_path = _default_state_path(manifest_path)

    doc = yaml.decode(ctx.fs.read(manifest_path))
    run_id = _str(ctx.inputs.get("run_id", "")).strip()
    items, errors, defaults = _normalize_manifest(ctx, doc, run_id)
    run_id = _str(defaults.get("trace_run_id", run_id))

    state = {
        "manifest_path": manifest_path,
        "defaults": defaults,
        "run_id": run_id,
        "items": items,
        "results": {"items": []},
        "error": _join_lines(errors),
    }
    ctx.fs.write(state_path, json.encode(state) + "\n")

    return {
        "items": items,
        "item_count": str(len(items)),
        "manifest_path": manifest_path,
        "state_path": state_path,
        "error": _join_lines(errors),
    }
