SECTION_HEADINGS = {
    "done": "## Done",
    "partial": "## Partial / in progress",
    "ideas": "## Ideas",
}

SECTION_ORDER = ["done", "partial", "ideas"]

HEADING_TO_KEY = {
    "## done": "done",
    "## partial / in progress": "partial",
    "## ideas": "ideas",
}


def _str(v):
    if v == None:
        return ""
    return str(v)


def _is_space(ch):
    return ch == " " or ch == "\t" or ch == "\n" or ch == "\r"


def _collapse_ws(text):
    out = ""
    prev_space = False
    for ch in _str(text).elems():
        if _is_space(ch):
            if not prev_space:
                out += " "
            prev_space = True
        else:
            out += ch
            prev_space = False
    return out.strip()


def _normalize(text):
    text = _str(text).strip()
    if text.startswith("- ") or text.startswith("* "):
        text = text[2:]
    return _collapse_ws(text).lower()


def _split_lines(text):
    lines = text.split("\n")
    if len(lines) > 0 and lines[-1] == "":
        return lines[:-1]
    return lines


def _parse_sections(text):
    preamble = []
    sections = {k: [] for k in SECTION_ORDER}
    current = None
    seen_heading = False

    for line in _split_lines(text):
        stripped = line.strip()
        key = HEADING_TO_KEY.get(stripped.lower())
        if key != None:
            current = key
            seen_heading = True
            continue
        if current == None:
            if not seen_heading:
                preamble.append(line)
            continue
        if stripped.startswith("- ") or stripped.startswith("* "):
            sections[current].append(stripped)

    return preamble, sections


def _render(preamble, sections):
    pre = list(preamble)
    out = []
    out.extend(pre)
    for key in SECTION_ORDER:
        if len(out) > 0 and out[-1].strip() != "":
            out.append("")
        out.append(SECTION_HEADINGS[key])
        out.append("")
        out.extend(sections[key])
    return "\n".join(out).rstrip("\n") + "\n"


def _review_moves(review):
    if type(review) != "dict":
        return []
    moves = review.get("reclassifications", [])
    if type(moves) != "list":
        return []
    return moves


def _find_bullet(sections, needle):
    for key in SECTION_ORDER:
        i = 0
        for bullet in sections[key]:
            if _normalize(bullet) == needle:
                return [key, i]
            i += 1
    return None


def main(ctx):
    ideas_path = _str(ctx.inputs.get("ideas_path", "")).strip()
    review_path = _str(ctx.inputs.get("review_path", "")).strip()
    if ideas_path == "":
        fail("ideas_path is required")
    if review_path == "":
        fail("review_path is required")

    preamble, sections = _parse_sections(ctx.fs.read(ideas_path))
    review = json.decode(ctx.fs.read(review_path))

    moved = 0
    not_found = []
    for mv in _review_moves(review):
        if type(mv) != "dict":
            continue
        item = _str(mv.get("item", ""))
        to = _str(mv.get("to", ""))
        if to not in sections:
            not_found.append(item)
            continue
        hit = _find_bullet(sections, _normalize(item))
        if hit == None:
            not_found.append(item)
            continue
        from_key = hit[0]
        idx = hit[1]
        if from_key == to:
            continue
        bullet = sections[from_key].pop(idx)
        sections[to].append(bullet)
        moved += 1

    written_path = ctx.fs.write(ideas_path, _render(preamble, sections))
    return {"result": {
        "moved": moved,
        "not_found": not_found,
        "sections": {k: len(sections[k]) for k in SECTION_ORDER},
        "written_path": written_path,
    }}
