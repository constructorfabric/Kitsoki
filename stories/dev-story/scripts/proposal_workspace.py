#!/usr/bin/env python3
"""proposal_workspace.py — validate a proposed slug for uniqueness and
return the per-session workspace path.

Usage:
    python3 proposal_workspace.py <proposed-slug>

The slug itself is named by an oracle.decide call (the LLM turns the idea
into a short kebab-case name — see prompts/proposal_slug.md); THIS script is
the deterministic uniqueness check that completes the validation sandwich:
it ensures the slug collides with neither an accepted proposal
(`docs/proposals/<slug>.md`) nor an in-progress draft
(`docs/proposals/.workspace/<slug>/`), appending `-2`, `-3`, … until it is
unique.

stdout: a JSON object {"slug": "...", "workspace": "docs/proposals/.workspace/<slug>"}.
host.run parses it into `stdout_json` so the intake room binds both
`proposal_slug` and `proposal_workspace` from this one call.
"""

import json
import os
import re
import sys

WORKSPACE_ROOT = os.path.join("docs", "proposals", ".workspace")
PROPOSALS_DIR = os.path.join("docs", "proposals")
MAX_SLUG_WORDS = 6


def sanitize(text: str) -> str:
    """Defensive: coerce whatever we got into a bounded kebab slug."""
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
    proposed = sys.argv[1] if len(sys.argv) > 1 else ""
    slug = unique_slug(sanitize(proposed))
    print(json.dumps({"slug": slug, "workspace": f"{WORKSPACE_ROOT}/{slug}"}), end="")


if __name__ == "__main__":
    main()
