#!/usr/bin/env python3
"""prd_slug.py — derive a meaningful slug from the distilled idea and return
the per-PRD workspace path, uniquified so two PRDs authored in the same tree
never collide.

Usage:
    python3 prd_slug.py <idea-or-slug> [workdir] [durable_path]

  idea-or-slug   the distilled idea statement (world.idea). The slug is a
                 bounded kebab-case reduction of its first line — no LLM call
                 is needed (the PRD pipeline already distilled the idea in the
                 idle discovery chat; this is the deterministic naming step,
                 the analogue of dev-story's design_workspace.py).
  workdir        operator working tree (world.workdir); the workspace is
                 minted under <workdir>/.artifacts/prd/<slug>. Defaults to ".".
  durable_path   durable home checked for collisions (world.publish_durable_path,
                 default "docs/prd") — a published <durable>/<slug>.md also
                 reserves the slug so a re-run gets <slug>-2.

The deterministic uniqueness check completes the validation sandwich: it
ensures the slug collides with neither an in-progress workspace
(<workdir>/.artifacts/prd/<slug>/) nor a published PRD
(<workdir>/<durable>/<slug>.md), appending `-2`, `-3`, … until unique.

stdout: a JSON object {"slug": "...", "workspace": ".artifacts/prd/<slug>"}.
host.run parses it into `stdout_json` so the search room binds both
`prd_slug` and `prd_workspace` from this one call. The workspace is returned
RELATIVE to workdir (the rooms prefix it back with {{ world.workdir }}),
matching how host.artifacts_dir's artifacts_root is composed elsewhere.
"""

import json
import os
import re
import sys

MAX_SLUG_WORDS = 6


def sanitize(text: str) -> str:
    """Coerce the idea's first line into a bounded kebab slug."""
    first_line = text.strip().split("\n")[0]
    slug = re.sub(r"[^a-z0-9]+", "-", first_line.lower()).strip("-")
    slug = "-".join(slug.split("-")[:MAX_SLUG_WORDS])
    return slug or "prd"


def is_taken(slug: str, workdir: str, durable: str) -> bool:
    workspace = os.path.join(workdir, ".artifacts", "prd", slug)
    published = os.path.join(workdir, durable, f"{slug}.md")
    return os.path.exists(workspace) or os.path.exists(published)


def unique_slug(base: str, workdir: str, durable: str) -> str:
    if not is_taken(base, workdir, durable):
        return base
    for i in range(2, 1000):
        candidate = f"{base}-{i}"
        if not is_taken(candidate, workdir, durable):
            return candidate
    raise RuntimeError("too many collisions for slug: " + base)


def main() -> None:
    idea = sys.argv[1] if len(sys.argv) > 1 else ""
    workdir = sys.argv[2] if len(sys.argv) > 2 else "."
    durable = sys.argv[3] if len(sys.argv) > 3 else "docs/prd"
    slug = unique_slug(sanitize(idea), workdir, durable)
    print(
        json.dumps({"slug": slug, "workspace": f".artifacts/prd/{slug}"}),
        end="",
    )


if __name__ == "__main__":
    main()
