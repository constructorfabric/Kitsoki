#!/usr/bin/env python3
"""publish_proposal.py — move a drafted proposal out of the per-session
workspace into the docs/proposals/ queue.

Usage:
    python3 publish_proposal.py <workspace> <slug> [change_target]

  workspace      docs/proposals/.workspace/<slug> — holds 005-proposal.md
                 (the draft) plus the numbered check artifacts 001..004.
  slug           the meaningful slug minted at intake; the final filename
                 prefers the draft's own title heading, falling back to this.
  change_target  when set, the author AMENDED this existing proposal in
                 place instead of writing a new draft — nothing to move;
                 just echo its path.

stdout: the relative path of the published file (docs/proposals/<slug>.md
or the amended change_target). exit 0 on success, non-zero on error.

The numbered check artifacts (001-brief … 004-references) are left in the
workspace as the per-proposal record, disambiguated by their 3-digit
lexical-sort prefix.
"""

import os
import re
import sys


MAX_SLUG_WORDS = 6


def slugify(text: str) -> str:
    first_line = text.strip().split("\n")[0]
    slug = re.sub(r"[^a-z0-9]+", "-", first_line.lower()).strip("-")
    slug = "-".join(slug.split("-")[:MAX_SLUG_WORDS])
    return slug or "proposal"


def title_from_draft(draft: str) -> str:
    """First markdown heading in the draft, stripping `#` markers."""
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
        print(f"usage: {sys.argv[0]} <workspace> <slug> [change_target]", file=sys.stderr)
        sys.exit(1)

    workspace = sys.argv[1]
    slug_in = sys.argv[2]
    change_target = sys.argv[3] if len(sys.argv) > 3 else ""

    # Amend path: the author edited an existing proposal in place. Nothing to
    # move — just confirm the path.
    if change_target.strip():
        print(os.path.relpath(change_target.strip()), end="")
        return

    src = os.path.join(workspace, "005-proposal.md")
    if not os.path.isfile(src):
        print(f"publish_proposal: no draft at {src}", file=sys.stderr)
        sys.exit(1)

    with open(src) as f:
        draft = f.read()

    base_dir = os.path.join(os.getcwd(), "docs", "proposals")
    os.makedirs(base_dir, exist_ok=True)

    title = title_from_draft(draft)
    slug = slugify(title) if title else slugify(slug_in)
    dest = find_path(base_dir, slug)

    # Move the draft into the queue; leave the numbered checks in the
    # workspace as the record.
    os.replace(src, dest)

    print(os.path.relpath(dest), end="")


if __name__ == "__main__":
    main()
