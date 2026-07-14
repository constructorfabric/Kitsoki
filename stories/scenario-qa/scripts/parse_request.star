# parse_request.star — turn a general operator request into scenario-qa slots.
#
# This is intentionally conservative. Exact `key=value` qualifiers win for
# portable, scriptable use; lightweight natural phrases cover common typing
# without pretending to be a full language parser. Anything left over becomes
# the ad-hoc scenario description unless it is a single catalog-looking id.

_ALPHANUM = "abcdefghijklmnopqrstuvwxyz0123456789"
_IDENT = _ALPHANUM + "-_."
_TRANSPORTS = ["tui", "web", "vscode", "cli"]
_TRANSPORT_MARKERS = ["on", "in", "via", "using", "across", "transport", "transports"]
_TRANSPORT_FILLERS = ["and", "or", "with", "the", "a", "for"]


def _str(v):
    if v == None:
        return ""
    return str(v)


def _clean_word(v):
    out = _str(v).lower().strip()
    for ch in [",", ".", ";", ":", "(", ")", "[", "]", "{", "}", "`", "\"", "'"]:
        out = out.replace(ch, "")
    if out == "vs-code" or out == "vscode" or out == "vs_code":
        return "vscode"
    return out


def _collapse_ws(text):
    parts = []
    for part in _str(text).replace("\n", " ").split(" "):
        s = part.strip()
        if s != "":
            parts.append(s)
    return " ".join(parts)


def _strip_prefix(text):
    s = _collapse_ws(text)
    lower = s.lower()
    prefixes = [
        "preview whether",
        "preview that",
        "preview",
        "scenario qa check whether",
        "scenario qa check",
        "check whether",
        "check that",
        "check",
        "verify whether",
        "verify that",
        "verify",
        "test whether",
        "test that",
        "test",
        "qa",
    ]
    for prefix in prefixes:
        if lower == prefix:
            return ""
        if lower.startswith(prefix + " "):
            return s[len(prefix):].strip()
    return s


def _transport_token(token):
    t = _clean_word(token)
    if t == "all":
        return "all"
    if t == "vs" or t == "code":
        return ""
    if t in _TRANSPORTS:
        return t
    return ""


def _normalize_transport(raw, default_value):
    text = _str(raw).lower().replace("vs code", "vscode").replace("vs-code", "vscode")
    text = text.replace("+", ",").replace("/", ",").replace(";", ",").replace(" and ", ",")
    seen = []
    for part in text.split(","):
        p = _transport_token(part)
        if p == "":
            stripped = part.strip()
            p = _transport_token(stripped.split(" ")[0] if stripped != "" else "")
        if p == "all":
            return "all"
        if p != "" and p not in seen:
            seen.append(p)
    if len(seen) > 0:
        return ",".join(seen)
    return _str(default_value).strip() or "all"


def _extract_key_values(text):
    opts = {}
    keep = []
    for raw in _str(text).split(" "):
        if "=" not in raw and ":" not in raw:
            keep.append(raw)
            continue
        sep = "=" if "=" in raw else ":"
        parts = raw.split(sep)
        key = _clean_word(parts[0]).replace("-", "_")
        value = sep.join(parts[1:]).strip().strip(",.;")
        if key in ["scenario", "scenario_id", "catalog", "id"]:
            opts["scenario"] = value
        elif key in ["description", "desc"]:
            opts["description"] = value.replace("_", " ")
        elif key in ["transport", "transports", "surface", "surfaces"]:
            opts["transport"] = value
        elif key in ["persona", "personas"]:
            opts["persona"] = value
        elif key in ["target", "project", "repo", "repository"]:
            opts["target"] = value
        elif key == "seed":
            opts["seed"] = value
        elif key in ["profile", "live_profile"]:
            opts["profile"] = value
        elif key in ["budget", "live_budget", "live_budget_minutes"]:
            opts["live_budget_minutes"] = value
        elif key == "pause":
            opts["pause"] = value.lower()
        elif key == "parallel":
            opts["parallel"] = value.lower()
        else:
            keep.append(raw)
    return opts, _collapse_ws(" ".join(keep))


def _extract_transport_phrase(text, default_value):
    normalized = _str(text).replace("vs code", "vscode").replace("VS Code", "vscode").replace(",", " , ")
    tokens = normalized.split(" ")
    keep = []
    found = []
    capture = False
    pending_marker = ""
    captured_any = False
    for raw in tokens:
        word = _clean_word(raw)
        if word == "":
            continue
        if capture:
            if word in _TRANSPORT_FILLERS or word == ",":
                continue
            tv = _transport_token(word)
            if tv == "all":
                found = ["all"]
                captured_any = True
                continue
            if tv != "":
                if tv not in found:
                    found.append(tv)
                captured_any = True
                continue
            if word == "transport" or word == "transports":
                continue
            if not captured_any and pending_marker != "":
                keep.append(pending_marker)
            keep.append(raw)
            capture = False
            pending_marker = ""
            captured_any = False
            continue
        if word in _TRANSPORT_MARKERS:
            capture = True
            pending_marker = raw
            captured_any = False
            continue
        keep.append(raw)
    if len(found) == 0:
        return _str(default_value).strip() or "all", _collapse_ws(" ".join(keep))
    if "all" in found:
        return "all", _collapse_ws(" ".join(keep))
    return ",".join(found), _collapse_ws(" ".join(keep))


def _slugish(text):
    s = _str(text).strip().lower()
    if s == "":
        return False
    for ch in s.elems():
        if ch not in _IDENT:
            return False
    return True


def _slug(text):
    out = ""
    last_dash = False
    for ch in _str(text).lower().elems():
        if ch in _ALPHANUM:
            out += ch
            last_dash = False
        elif len(out) > 0 and not last_dash:
            out += "-"
            last_dash = True
    return out.strip("-")


def _int_or_default(value, default_value):
    s = _str(value).strip()
    if s == "":
        return int(default_value or 0)
    # Starlark has no exceptions; only accept digit-only values.
    for ch in s.elems():
        if ch not in "0123456789":
            return int(default_value or 0)
    return int(s)


def main(ctx):
    request = _strip_prefix(ctx.inputs.get("request", ""))
    opts, remaining = _extract_key_values(request)
    transport, remaining = _extract_transport_phrase(
        remaining,
        opts.get("transport", ctx.inputs.get("default_transport", "all")),
    )
    transport = _normalize_transport(opts.get("transport", transport), transport)

    scenario_ref = _slug(opts.get("scenario", ""))
    description = _collapse_ws(opts.get("description", ""))
    if description == "":
        if scenario_ref == "" and _slugish(remaining):
            scenario_ref = _slug(remaining)
        else:
            description = remaining

    mode = "catalog" if scenario_ref != "" else "adhoc"
    return {
        "scenario_ref": scenario_ref,
        "scenario_description": description,
        "transport_arg": transport,
        "persona": _slug(opts.get("persona", ctx.inputs.get("default_persona", ""))),
        "target": _slug(opts.get("target", ctx.inputs.get("default_target", ""))),
        "seed": opts.get("seed", ctx.inputs.get("default_seed", "scenario-qa")),
        "live_profile": opts.get("profile", ctx.inputs.get("default_profile", "")),
        "live_budget_minutes": _int_or_default(
            opts.get("live_budget_minutes", ""),
            ctx.inputs.get("default_live_budget_minutes", 0),
        ),
        "pause": opts.get("pause", ctx.inputs.get("default_pause", "auto")) or "auto",
        "parallel": opts.get("parallel", ctx.inputs.get("default_parallel", "false")) or "false",
        "mode": mode,
        "request_status": {
            "parsed": True,
            "source": "request",
        },
    }
