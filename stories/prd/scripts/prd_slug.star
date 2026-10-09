MAX_SLUG_WORDS = 6


def _is_slug_char(ch):
    return ("a" <= ch and ch <= "z") or ("0" <= ch and ch <= "9")


def _sanitize(text):
    first_line = str(text or "").strip().split("\n")[0].lower()
    parts = []
    cur = ""
    for ch in first_line.elems():
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


def _workspace(slug):
    return ".artifacts/prd/" + slug


def _published(slug, durable):
    durable = str(durable or "docs/prd").strip()
    if durable == "":
        durable = "docs/prd"
    return durable.rstrip("/") + "/" + slug + ".md"


def _is_tmp_durable(durable):
    durable = str(durable or "").strip()
    return durable == "/tmp" or durable.startswith("/tmp/")


def _taken(ctx, slug, durable):
    if _is_tmp_durable(durable):
        return ctx.fs.exists(_workspace(slug))
    return ctx.fs.exists(_workspace(slug)) or ctx.fs.exists(_published(slug, durable))


def _unique_slug(ctx, base, durable):
    if not _taken(ctx, base, durable):
        return base
    for i in range(2, 1000):
        candidate = base + "-" + str(i)
        if not _taken(ctx, candidate, durable):
            return candidate
    fail("too many collisions for slug: " + base)


def main(ctx):
    idea = ctx.inputs.get("idea", "")
    durable = ctx.inputs.get("durable_path", "docs/prd")
    slug = _unique_slug(ctx, _sanitize(idea), durable)
    return {
        "slug": slug,
        "workspace": _workspace(slug),
    }
