#!/usr/bin/env python3
"""save_proposal.py — write a proposal draft to docs/proposals/<slug>.md.

Usage:
    python3 save_proposal.py <idea_text> <draft_content>

stdout: the relative path of the written file (e.g. docs/proposals/my-idea.md)
exit 0 on success, non-zero on error.
"""

import os
import re
import sys


def slugify(text: str) -> str:
    first_line = text.strip().split("\n")[0][:80]
    slug = re.sub(r"[^a-z0-9]+", "-", first_line.lower()).strip("-")
    return slug or "proposal"


def title_from_draft(draft: str) -> str:
    """Extract the first markdown heading from the draft, stripping # markers."""
    for line in draft.splitlines():
        m = re.match(r"^#{1,6}\s+(.+)", line.strip())
        if m:
            return m.group(1).strip()
    return ""


def find_path(base_dir: str, slug: str) -> str:
    path = os.path.join(base_dir, f"{slug}.md")
    if not os.path.exists(path):
        return path
    for i in range(2, 100):
        path = os.path.join(base_dir, f"{slug}-{i}.md")
        if not os.path.exists(path):
            return path
    raise RuntimeError("too many conflicts for slug: " + slug)


def main() -> None:
    if len(sys.argv) < 3:
        print(f"usage: {sys.argv[0]} <idea> <draft>", file=sys.stderr)
        sys.exit(1)

    idea = sys.argv[1]
    draft = sys.argv[2]

    base_dir = os.path.join(os.getcwd(), "docs", "proposals")
    os.makedirs(base_dir, exist_ok=True)

    title = title_from_draft(draft)
    slug = slugify(title) if title else slugify(idea)
    path = find_path(base_dir, slug)

    with open(path, "w") as f:
        f.write(draft)
        if not draft.endswith("\n"):
            f.write("\n")

    # Print relative path for bind
    print(os.path.relpath(path), end="")


if __name__ == "__main__":
    main()
