# load_backlog.star — load local markdown tickets and GitHub issues into the
# marathon backlog, or resume the durable journal if requested.
#
# Source grammar:
#   local:<dir-or-glob>     local markdown tickets; a directory expands to *.md
#   github:<owner/repo>     open GitHub issues via the gh.issue.list probe
#   <path>                  legacy local path shorthand
#
# The script never fabricates cases. If a source is unreadable or the probe
# returns no issues, that source simply contributes no cases.

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

def _int(v, default):
    if v == None or v == "":
        return default
    return int(v)

def _bool(v):
    if v == True:
        return True
    if type(v) == "string":
        s = v.strip().lower()
        return s == "true" or s == "1" or s == "yes"
    return False

def _safe_slug(raw, fallback):
    text = _str(raw).strip().lower()
    if text == "":
        text = fallback
    out = ""
    last_dash = False
    for ch in text.elems():
        ok = (ch >= "a" and ch <= "z") or (ch >= "0" and ch <= "9")
        if ok:
            out += ch
            last_dash = False
        elif not last_dash:
            out += "-"
            last_dash = True
    if out == "":
        return fallback
    return out

def _run_id(ctx):
    supplied = _str(_get(ctx.inputs, "run_id", "")).strip()
    if supplied != "":
        return _safe_slug(supplied, "dogfood-marathon")
    world_run = _str(_get(ctx.world, "run_id", "")).strip()
    if world_run != "":
        return _safe_slug(world_run, "dogfood-marathon")
    return "dogfood-marathon"

def _run_dir(ctx, run_id):
    supplied = _str(_get(ctx.inputs, "run_dir", "")).strip()
    if supplied != "":
        return supplied
    world_dir = _str(_get(ctx.world, "run_dir", "")).strip()
    if world_dir != "":
        return world_dir
    return ".artifacts/dogfood-marathon/" + run_id

def _journal_path(ctx, run_dir):
    supplied = _str(_get(ctx.inputs, "journal_path", "")).strip()
    if supplied != "":
        return supplied
    world_path = _str(_get(ctx.world, "journal_path", "")).strip()
    if world_path != "":
        return world_path
    return run_dir + "/journal.json"

def _markdown_path(journal_path, run_dir):
    if journal_path.endswith(".json"):
        return journal_path[:-5] + ".md"
    return run_dir + "/journal.md"

def _first_heading(body):
    for line in body.split("\n"):
        s = line.strip()
        if s.startswith("#"):
            idx = 0
            for ch in s.elems():
                if ch == "#":
                    idx += 1
                else:
                    break
            s = s[idx:].strip()
            if s != "":
                return s
    return ""

def _unquote(v):
    s = _str(v).strip()
    if len(s) >= 2:
        if (s[0] == "\"" and s[-1] == "\"") or (s[0] == "'" and s[-1] == "'"):
            return s[1:-1]
    return s

def _field(body, key):
    prefix = key + ":"
    for line in body.split("\n")[:80]:
        s = line.strip()
        if s.startswith(prefix):
            return _unquote(s[len(prefix):].strip())
    return ""

def _snippet(body):
    lines = []
    for line in body.split("\n"):
        s = line.strip()
        if s == "" or s.startswith("---") or s.startswith("#"):
            continue
        if s.endswith(":") and len(s.split(" ")) == 1:
            continue
        lines.append(s)
        if len(lines) >= 4:
            break
    text = " ".join(lines)
    if len(text) > 700:
        return text[:700] + "..."
    return text

def _has_glob(path):
    for ch in _str(path).elems():
        if ch == "*" or ch == "?" or ch == "[":
            return True
    return False

def _local_paths(ctx, source):
    path = _str(source).strip()
    if path.startswith("local:"):
        path = path[len("local:"):].strip()
    if path == "":
        return []
    if _has_glob(path):
        return ctx.fs.glob(path)
    if path.endswith(".md"):
        return [path] if ctx.fs.exists(path) else []
    return ctx.fs.glob(path + "/*.md")

def _local_case(ctx, path, baseline_policy):
    body = ctx.fs.read(path)
    name = path.split("/")[-1]
    stem = name[:-3] if name.endswith(".md") else name
    title = _field(body, "title")
    if title == "":
        title = _first_heading(body)
    if title == "":
        title = stem
    case_id = _field(body, "id")
    if case_id == "":
        case_id = stem
    source_kind = _field(body, "source_kind")
    if source_kind == "":
        source_kind = "local"
    return {
        "id": case_id,
        "title": title,
        "baseline": _field(body, "baseline"),
        "baseline_policy": baseline_policy,
        "repro_command": _field(body, "repro_command"),
        "source_kind": source_kind,
        "source_path": path,
        "source_repo": _field(body, "source_repo"),
        "source_url": _field(body, "source_url"),
        "body_excerpt": _snippet(body),
    }

def _github_cases(ctx, repo, baseline_policy):
    repo = _str(repo).strip()
    if repo == "":
        return []
    resp = ctx.probe("gh.issue.list", [repo])
    if _dict(resp).get("exit", 1) != 0:
        return []
    out = _str(_dict(resp).get("out", "")).strip()
    if out == "":
        return []
    decoded = json.decode(out)
    cases = []
    for issue in _items(decoded):
        item = _dict(issue)
        number = _str(item.get("number", "")).strip()
        if number == "":
            continue
        state = _str(item.get("state", "open")).lower()
        if state != "" and state != "open":
            continue
        title = _str(item.get("title", "")).strip()
        if title == "":
            title = "GitHub issue #" + number
        url = _str(item.get("url", "")).strip()
        if url == "":
            url = "https://github.com/" + repo + "/issues/" + number
        cases.append({
            "id": repo + "#" + number,
            "title": title,
            "baseline": "",
            "baseline_policy": baseline_policy,
            "repro_command": "",
            "source_kind": "github",
            "source_repo": repo,
            "source_url": url,
            "state": state or "open",
            "body_excerpt": _str(item.get("body", ""))[:700],
        })
    return cases

def _load_sources(ctx, source_text, limit, baseline_policy):
    items = []
    sources = []
    for raw in _str(source_text).split(","):
        s = raw.strip()
        if s != "":
            sources.append(s)
    if len(sources) == 0:
        sources = ["local:.artifacts/issues/bugs"]

    for source in sources:
        if len(items) >= limit:
            break
        if source.startswith("github:"):
            for case in _github_cases(ctx, source[len("github:"):], baseline_policy):
                if len(items) >= limit:
                    break
                items.append(case)
        else:
            for path in _local_paths(ctx, source):
                if len(items) >= limit:
                    break
                items.append(_local_case(ctx, path, baseline_policy))
    return items

def _resume(ctx, journal_path):
    if journal_path == "":
        return None
    if not ctx.fs.exists(journal_path):
        return None
    return _dict(json.decode(ctx.fs.read(journal_path)))

def _display_number(v):
    s = _str(v).strip()
    if s == "":
        return "0"
    if s.endswith(".000000"):
        return s[:-7]
    if s.endswith(".0"):
        return s[:-2]
    return s

def _last_result(results):
    items = _items(_dict(results).get("items", []))
    if len(items) == 0:
        return {}
    result = dict(_dict(items[-1]))
    if _str(result.get("cost_display", "")).strip() == "":
        result["cost_display"] = _display_number(result.get("cost_usd", 0))
    if _str(result.get("tokens_display", "")).strip() == "":
        result["tokens_display"] = _display_number(result.get("tokens", 0))
    if _str(result.get("wall_s_display", "")).strip() == "":
        result["wall_s_display"] = _display_number(result.get("wall_s", 0))
    return result

def _summary(stage, items, case_index, cases_processed):
    return stage + ": " + str(cases_processed) + " processed, " + str(len(items)) + " loaded, next index " + str(case_index)

def main(ctx):
    run_id = _run_id(ctx)
    run_dir = _run_dir(ctx, run_id)
    journal_path = _journal_path(ctx, run_dir)
    journal_markdown_path = _markdown_path(journal_path, run_dir)
    resume = _bool(_get(ctx.inputs, "resume", False)) or _bool(_get(ctx.world, "resume_from_journal", False))

    if resume:
        journal = _resume(ctx, journal_path)
        if journal != None:
            backlog = _dict(journal.get("backlog", {"items": []}))
            results = _dict(journal.get("results", {"items": []}))
            findings = _dict(journal.get("findings", {"items": []}))
            exceptions = _dict(journal.get("exceptions", {"items": []}))
            case_index = _int(journal.get("case_index", len(_items(results.get("items", [])))), 0)
            cases_processed = _int(journal.get("cases_processed", len(_items(results.get("items", [])))), 0)
            return {
                "backlog": backlog,
                "run_id": run_id,
                "run_dir": run_dir,
                "journal_path": journal_path,
                "journal_markdown_path": journal_markdown_path,
                "results": results,
                "last_result": _last_result(results),
                "findings": findings,
                "exceptions": exceptions,
                "case_index": case_index,
                "cases_processed": cases_processed,
                "loaded_journal": True,
                "journal_status": "resumed",
                "journal_summary": _summary("resumed", _items(backlog.get("items", [])), case_index, cases_processed),
                "last_checkpoint": _str(journal.get("last_checkpoint", "resumed")),
            }

    seeded = _dict(ctx.world.get("backlog"))
    if len(_items(seeded.get("items", []))) > 0:
        items = _items(seeded.get("items", []))
        backlog = {"items": items}
    else:
        source = _str(_get(ctx.inputs, "source", "")).strip()
        limit = _int(_get(ctx.inputs, "limit", 15), 15)
        baseline_policy = _str(_get(ctx.inputs, "baseline_policy", "fix-parent")).strip() or "fix-parent"
        backlog = {"items": _load_sources(ctx, source, limit, baseline_policy)}

    case_index = _int(_get(ctx.world, "case_index", 0), 0)
    cases_processed = _int(_get(ctx.world, "cases_processed", 0), 0)
    results = _dict(_get(ctx.world, "results", {"items": []})) or {"items": []}
    findings = _dict(_get(ctx.world, "findings", {"items": []})) or {"items": []}
    exceptions = _dict(_get(ctx.world, "exceptions", {"items": []})) or {"items": []}

    return {
        "backlog": backlog,
        "run_id": run_id,
        "run_dir": run_dir,
        "journal_path": journal_path,
        "journal_markdown_path": journal_markdown_path,
        "results": results,
        "last_result": _last_result(results),
        "findings": findings,
        "exceptions": exceptions,
        "case_index": case_index,
        "cases_processed": cases_processed,
        "loaded_journal": False,
        "journal_status": "loaded",
        "journal_summary": _summary("loaded", _items(backlog.get("items", [])), case_index, cases_processed),
        "last_checkpoint": "intake",
    }
