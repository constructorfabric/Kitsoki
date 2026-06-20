#!/usr/bin/env python3
"""ideas_reconcile.py — apply a reviewer's section moves to an ideas backlog.

Usage:
    python3 ideas_reconcile.py <ideas_path> <review_json_path>

The MOVES are an interpretive decision made by the `ideas_reviewer` agent
(see prompts/ideas_review.md); THIS script is the deterministic executor — the
same decide→script discipline as design_workspace.py. It reads the reviewer's
report (the `ideas_review` object persisted to JSON by host.artifacts_dir),
takes its `reclassifications`, and rewrites <ideas_path> moving each named
bullet from its current section to its target section.

The backlog has three `## ` sections; the canonical headings are:
    ## Done
    ## Partial / in progress
    ## Ideas
keyed in the report as `done`, `partial`, `ideas`. A missing heading is created
in canonical order.

Matching is by normalized bullet text (leading "- " stripped, inner whitespace
collapsed): an item is found wherever it currently lives, removed, and appended
under its `to` section. Items whose text can't be matched are reported in
`not_found` and never abort the run.

stdout: a JSON object
    {"moved": N, "not_found": [...], "sections": {"done": n, "partial": n, "ideas": n}}
host.run parses it into `stdout_json` so the room binds the result.
"""

import json
import re
import sys

# Report key → literal markdown heading.
SECTION_HEADINGS = {
    "done": "## Done",
    "partial": "## Partial / in progress",
    "ideas": "## Ideas",
}
# Canonical order when a heading must be created.
SECTION_ORDER = ["done", "partial", "ideas"]

# Recognise a heading line and map it back to a section key.
HEADING_TO_KEY = {h.lower(): k for k, h in SECTION_HEADINGS.items()}


def normalize(text: str) -> str:
    """Strip a leading bullet marker and collapse whitespace for matching."""
    text = text.strip()
    text = re.sub(r"^[-*]\s+", "", text)
    return re.sub(r"\s+", " ", text).strip().lower()


def parse_sections(lines):
    """Split the file into {key: [bullet-line, ...]} plus a preamble.

    Only `## ` level-2 headings that map to a known section are treated as
    section boundaries; anything else (preamble, blank lines) is preserved in
    `preamble` so the file's top matter survives a rewrite. Within a section we
    keep only the bullet lines (`- ...`); blank lines are re-synthesised on
    write so spacing stays uniform.
    """
    preamble = []
    sections = {k: [] for k in SECTION_ORDER}
    current = None
    seen_heading = False
    for raw in lines:
        line = raw.rstrip("\n")
        key = HEADING_TO_KEY.get(line.strip().lower())
        if key is not None:
            current = key
            seen_heading = True
            continue
        if current is None:
            # Top matter before the first known section heading.
            if not seen_heading:
                preamble.append(line)
            continue
        if line.strip().startswith(("- ", "* ")):
            sections[current].append(line.strip())
        # Non-bullet lines inside a section (blank lines, stray prose) are
        # dropped from the model and re-synthesised on write.
    return preamble, sections


def render(preamble, sections) -> str:
    out = []
    # Trim trailing blank lines from the preamble; we control spacing below.
    while preamble and preamble[-1].strip() == "":
        preamble.pop()
    out.extend(preamble)
    for key in SECTION_ORDER:
        if out and out[-1].strip() != "":
            out.append("")
        out.append(SECTION_HEADINGS[key])
        out.append("")
        out.extend(sections[key])
    text = "\n".join(out).rstrip("\n") + "\n"
    return text


def main() -> None:
    if len(sys.argv) < 3:
        print(json.dumps({"error": "usage: ideas_reconcile.py <ideas_path> <review_json_path>"}), end="")
        sys.exit(2)
    ideas_path, review_path = sys.argv[1], sys.argv[2]

    with open(ideas_path, "r", encoding="utf-8") as f:
        preamble, sections = parse_sections(f.readlines())

    with open(review_path, "r", encoding="utf-8") as f:
        review = json.load(f)
    moves = review.get("reclassifications", []) if isinstance(review, dict) else []

    # Index normalized text → (section_key, list_index) for the current file.
    moved = 0
    not_found = []
    for mv in moves:
        item = mv.get("item", "")
        to = mv.get("to", "")
        if to not in sections:
            not_found.append(item)
            continue
        needle = normalize(item)
        hit = None
        for key in SECTION_ORDER:
            for i, bullet in enumerate(sections[key]):
                if normalize(bullet) == needle:
                    hit = (key, i)
                    break
            if hit:
                break
        if hit is None:
            not_found.append(item)
            continue
        from_key, idx = hit
        if from_key == to:
            continue  # already where it should be
        bullet = sections[from_key].pop(idx)
        sections[to].append(bullet)
        moved += 1

    with open(ideas_path, "w", encoding="utf-8") as f:
        f.write(render(preamble, sections))

    print(
        json.dumps(
            {
                "moved": moved,
                "not_found": not_found,
                "sections": {k: len(sections[k]) for k in SECTION_ORDER},
            }
        ),
        end="",
    )


if __name__ == "__main__":
    main()
