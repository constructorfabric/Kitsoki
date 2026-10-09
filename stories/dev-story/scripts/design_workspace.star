# design_workspace — mint a unique slug + workspace path for a design idea.
#
# The workspace root is DERIVED from the caller-supplied `durable_path` input
# (world.design_durable_path, already resolved to a writable location by the
# `resolve_durable_path` step in design_search.yaml's on_enter — see
# docs there) rather than a hardcoded constant. This keeps the scratch
# workspace and the eventual published doc under the SAME configured/resolved
# root, so an instance (or a fallback away from a read-only default) only
# ever has to steer one value.
DEFAULT_DURABLE_PATH = "docs/proposals"
MAX_SLUG_WORDS = 6

STOPWORDS = {
    "a": True,
    "able": True,
    "add": True,
    "allow": True,
    "an": True,
    "and": True,
    "as": True,
    "at": True,
    "author": True,
    "be": True,
    "called": True,
    "claude": True,
    "can": True,
    "could": True,
    "create": True,
    "design": True,
    "docs": True,
    "feature": True,
    "for": True,
    "from": True,
    "i": True,
    "in": True,
    "introduced": True,
    "is": True,
    "it": True,
    "kitsoki": True,
    "let": True,
    "make": True,
    "md": True,
    "of": True,
    "on": True,
    "or": True,
    "per": True,
    "prd": True,
    "recently": True,
    "realize": True,
    "runtime": True,
    "should": True,
    "session": True,
    "style": True,
    "that": True,
    "the": True,
    "this": True,
    "to": True,
    "user": True,
    "where": True,
    "with": True,
    "would": True,
    "want": True,
    "writes": True,
    "virtual": True,
}


def _is_slug_char(ch):
    return ("a" <= ch and ch <= "z") or ("0" <= ch and ch <= "9")


def _words(text):
    out = []
    cur = ""
    for ch in str(text or "").strip().lower().elems():
        if _is_slug_char(ch):
            cur += ch
        elif cur != "":
            out.append(cur)
            cur = ""
    if cur != "":
        out.append(cur)
    return out


def _singular(word):
    if len(word) > 3 and word.endswith("s"):
        return word[:-1]
    return word


def _sanitize(text):
    raw_words = _words(text)
    words = []
    for word in raw_words:
        if word not in STOPWORDS:
            words.append(word)
    if len(words) == 0:
        words = raw_words

    deduped = []
    seen = {}
    for word in words:
        key = _singular(word)
        if key in seen:
            continue
        seen[key] = True
        deduped.append(key)
        if len(deduped) >= MAX_SLUG_WORDS:
            break
    if len(deduped) == 0:
        return "proposal"
    return "-".join(deduped)


def _rstrip_slash(path):
    out = str(path or "")
    for _ in range(len(out)):
        if out.endswith("/"):
            out = out[:-1]
        else:
            break
    return out


def _durable_path(ctx):
    raw = ctx.inputs.get("durable_path", "")
    cleaned = _rstrip_slash(raw)
    return cleaned if cleaned != "" else DEFAULT_DURABLE_PATH


def _workspace_root(durable_path):
    return durable_path + "/.workspace"


def _workspace(durable_path, slug):
    return _workspace_root(durable_path) + "/" + slug


def _published(durable_path, slug):
    return durable_path + "/" + slug + ".md"


def _taken(ctx, durable_path, slug):
    return ctx.fs.exists(_workspace(durable_path, slug)) or ctx.fs.exists(_published(durable_path, slug))


def _unique_slug(ctx, durable_path, base):
    if not _taken(ctx, durable_path, base):
        return base
    for i in range(2, 1000):
        candidate = base + "-" + str(i)
        if not _taken(ctx, durable_path, candidate):
            return candidate
    fail("too many collisions for slug: " + base)


def main(ctx):
    durable_path = _durable_path(ctx)
    proposed = ctx.inputs.get("proposed", "")
    slug = _unique_slug(ctx, durable_path, _sanitize(proposed))
    return {"slug": slug, "workspace": _workspace(durable_path, slug)}
