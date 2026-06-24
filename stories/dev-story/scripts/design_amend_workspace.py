#!/usr/bin/env python3
"""Return the scratch workspace for an existing-design amend.

Directory targets are already in-progress design workspaces, so they remain the
workspace. File targets are accepted proposals; they need a scratch workspace for
brief/draft artifacts while ``design_change_target`` continues to point at the
file being amended.
"""

import json
import os
import re
import sys

WORKSPACE_ROOT = os.path.join("docs", "proposals", ".workspace")
PROPOSALS_DIR = os.path.join("docs", "proposals")
MAX_SLUG_WORDS = 6


def sanitize(text: str) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", text.strip().lower()).strip("-")
    slug = "-".join(slug.split("-")[:MAX_SLUG_WORDS])
    return slug or "proposal"


def is_taken(slug: str) -> bool:
    return os.path.exists(os.path.join(WORKSPACE_ROOT, slug)) or os.path.exists(
        os.path.join(PROPOSALS_DIR, f"{slug}.md")
    )


def unique_slug(base: str) -> str:
    if not is_taken(base):
        return base
    for i in range(2, 1000):
        candidate = f"{base}-{i}"
        if not is_taken(candidate):
            return candidate
    raise RuntimeError("too many collisions for slug: " + base)


def main() -> None:
    target = sys.argv[1] if len(sys.argv) > 1 else ""
    proposed_slug = sys.argv[2] if len(sys.argv) > 2 else ""

    normalized = target.rstrip("/")
    if target.endswith("/") or (normalized and not normalized.endswith(".md")):
        slug = sanitize(os.path.basename(normalized))
        print(json.dumps({"slug": slug, "workspace": target}), end="")
        return

    stem = os.path.splitext(os.path.basename(normalized))[0]
    slug = unique_slug(sanitize(proposed_slug or stem))
    print(json.dumps({"slug": slug, "workspace": f"{WORKSPACE_ROOT}/{slug}"}), end="")


if __name__ == "__main__":
    main()
