#!/usr/bin/env python3
"""proposal_slug.py — derive a kebab-case slug + workspace path from an
idea/title string.

Usage:
    python3 proposal_slug.py <text>

stdout: a JSON object {"slug": "...", "workspace": "docs/proposals/.workspace/<slug>"}.
host.run parses it into `stdout_json` so the intake room can bind both
`proposal_slug` and `proposal_workspace` from a single call (binding both
avoids the set-after-bind ordering trap, where a `set:` in the same effect
list is evaluated against the pre-invoke snapshot). publish_proposal.py
derives the final docs/proposals/<slug>.md filename the same way.
"""

import json
import re
import sys

WORKSPACE_ROOT = "docs/proposals/.workspace"


def slugify(text: str) -> str:
    first_line = text.strip().split("\n")[0][:80]
    slug = re.sub(r"[^a-z0-9]+", "-", first_line.lower()).strip("-")
    return slug or "proposal"


def main() -> None:
    text = sys.argv[1] if len(sys.argv) > 1 else ""
    slug = slugify(text)
    print(json.dumps({"slug": slug, "workspace": f"{WORKSPACE_ROOT}/{slug}"}), end="")


if __name__ == "__main__":
    main()
