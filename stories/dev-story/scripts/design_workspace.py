#!/usr/bin/env python3
"""design_workspace.py — validate a proposed slug for uniqueness and
return the per-session workspace path.

Usage:
    python3 design_workspace.py <proposed-slug-or-idea>

This script deterministically turns a proposed slug or raw idea into a bounded
kebab-case slug, then ensures it collides with neither an accepted proposal
(`docs/proposals/<slug>.md`) nor an in-progress draft
(`docs/proposals/.workspace/<slug>/`), appending `-2`, `-3`, ... until it is
unique. It does not create the workspace directory; the materialize room calls
it again behind the overlap gate and performs the actual artifact writes.

stdout: a JSON object {"slug": "...", "workspace": "docs/proposals/.workspace/<slug>"}.
host.run parses it into `stdout_json` so the intake room binds both
`design_slug` and `design_workspace` from this one call.
"""

import json
import os
import re
import sys

WORKSPACE_ROOT = os.path.join("docs", "proposals", ".workspace")
PROPOSALS_DIR = os.path.join("docs", "proposals")
MAX_SLUG_WORDS = 6
STOPWORDS = {
    "a",
    "able",
    "add",
    "allow",
    "an",
    "and",
    "be",
    "called",
    "claude",
    "can",
    "could",
    "create",
    "feature",
    "for",
    "from",
    "i",
    "in",
    "introduced",
    "is",
    "it",
    "kitsoki",
    "let",
    "make",
    "of",
    "on",
    "or",
    "recently",
    "should",
    "that",
    "the",
    "this",
    "to",
    "user",
    "where",
    "with",
    "would",
    "writes",
}


def sanitize(text: str) -> str:
    """Defensive: coerce whatever we got into a bounded kebab slug."""
    raw_words = re.findall(r"[a-z0-9]+", text.strip().lower())
    words = [word for word in raw_words if word not in STOPWORDS]
    words = words or raw_words

    deduped = []
    seen = set()
    for word in words:
        singular = word[:-1] if len(word) > 3 and word.endswith("s") else word
        if singular in seen:
            continue
        seen.add(singular)
        deduped.append(word)
        if len(deduped) >= MAX_SLUG_WORDS:
            break

    slug = "-".join(deduped)
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
    proposed = sys.argv[1] if len(sys.argv) > 1 else ""
    slug = unique_slug(sanitize(proposed))
    print(json.dumps({"slug": slug, "workspace": f"{WORKSPACE_ROOT}/{slug}"}), end="")


if __name__ == "__main__":
    main()
