MAX_SLUG_WORDS = 6


def _is_slug_char(ch):
    return ("a" <= ch and ch <= "z") or ("0" <= ch and ch <= "9")


def _strip_prd_prefix(text):
    stripped = str(text or "").strip()
    lowered = stripped.lower()
    if lowered.startswith("prd:"):
        return stripped[4:].strip()
    if lowered == "prd":
        return ""
    return stripped


def _slugify(text):
    first = _strip_prd_prefix(str(text or "").strip().split("\n")[0])
    parts = []
    cur = ""
    for ch in first.lower().elems():
        if _is_slug_char(ch):
            cur += ch
        elif cur != "":
            parts.append(cur)
            cur = ""
    if cur != "":
        parts.append(cur)
    if len(parts) > MAX_SLUG_WORDS:
        parts = parts[:MAX_SLUG_WORDS]
    if len(parts) == 0:
        return "prd"
    return "-".join(parts)


def _title_from_draft(draft):
    for raw in str(draft or "").split("\n"):
        line = raw.strip()
        if not line.startswith("#"):
            continue
        idx = 0
        for ch in line.elems():
            if ch == "#":
                idx += 1
            else:
                break
        if idx >= 1 and idx <= 6 and len(line) > idx and line[idx:idx + 1] == " ":
            return line[idx:].strip()
    return ""


def _trim_slashes(path):
    out = str(path or "").strip()
    if out == "/tmp" or out.startswith("/tmp/"):
        for _ in range(len(out)):
            if out.endswith("/") and len(out) > 1:
                out = out[:-1]
            else:
                break
        return out
    for _ in range(len(out)):
        if out.startswith("./"):
            out = out[2:]
        elif out.startswith("/"):
            fail("prd_publish: only /tmp absolute paths are supported for test fixtures: " + out)
        elif out.endswith("/") and len(out) > 1:
            out = out[:-1]
        else:
            break
    return out


def _resolve(workdir, path):
    clean = _trim_slashes(path)
    root = _trim_slashes(workdir)
    if root == "" or root == ".":
        return clean
    prefix = root + "/"
    if clean.startswith(prefix):
        return clean[len(prefix):]
    return clean


def _join(a, b):
    left = _trim_slashes(a)
    right = _trim_slashes(b)
    if left == "":
        return right
    if right == "":
        return left
    return left + "/" + right


def _find_path(ctx, base_dir, slug):
    path = _join(base_dir, slug + ".md")
    if not ctx.fs.exists(path):
        return path
    for i in range(2, 100):
        candidate = _join(base_dir, slug + "-" + str(i) + ".md")
        if not ctx.fs.exists(candidate):
            return candidate
    fail("too many conflicts for slug: " + slug)


def main(ctx):
    slug_in = str(ctx.inputs.get("slug", "") or "")
    draft_path = str(ctx.inputs.get("draft_path", "") or "")
    workspace = str(ctx.inputs.get("workspace", "") or "")
    workdir = str(ctx.inputs.get("workdir", ".") or ".")
    durable = str(ctx.inputs.get("durable", "docs/prd") or "docs/prd")
    change_target = str(ctx.inputs.get("change_target", "") or "").strip()
    doc_filename = str(ctx.inputs.get("doc_filename", "") or "").strip()

    if change_target:
        return {"prd_file": _resolve(workdir, change_target), "publish_status": "done"}

    if not draft_path and workspace:
        draft_path = _join(workspace, "004-prd.md")
    src = _resolve(workdir, draft_path)
    if not ctx.fs.exists(src):
        fail("prd_publish: no draft at " + src)
    draft = ctx.fs.read(src)
    base_dir = _resolve(workdir, durable)

    if doc_filename:
        dest = _join(base_dir, doc_filename + ".md")
    else:
        draft_title = _title_from_draft(draft)
        slug = _slugify(draft_title) if draft_title else _slugify(slug_in)
        dest = _find_path(ctx, base_dir, slug)

    written = ctx.fs.write(dest, draft)
    return {"prd_file": written, "publish_status": "done"}
