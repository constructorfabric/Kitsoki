#!/usr/bin/env python3
"""prd_publish.py — move a drafted PRD out of the per-PRD workspace into a
durable docs home on accept, so the deliverable does not end its life in the
gitignored .artifacts/ scratch tree.

Usage:
    python3 prd_publish.py <workspace> <slug> <draft_path> [workdir] [durable] [change_target] [doc_filename]

  workspace      <workdir>/.artifacts/prd/<slug> — holds the numbered check
                 artifacts (001-brief, 003-references) plus the draft.
  slug           the meaningful slug minted at the search gate; the published
                 filename prefers the draft's own title heading, falling back
                 to this.
  draft_path     where the author wrote the PRD (world.prd_artifact.file_path,
                 typically <workspace>/004-prd.md). Resolved relative to
                 workdir if not absolute.
  workdir        operator working tree (world.workdir); the durable home is
                 <workdir>/<durable>. Defaults to ".".
  durable        durable home for published PRDs (world.publish_durable_path,
                 default "docs/prd").
  change_target  when set, the author AMENDED this existing PRD in place
                 instead of writing a new draft — nothing to move; the
                 existing path is reused as the published file.
  doc_filename   external-target override (world.prd_doc_filename, default ""):
                 when set, the PRD publishes to <workdir>/<durable>/<doc_filename>.md
                 (a FIXED name, overwriting in place) instead of the slug-derived
                 <slug>.md. This is how an external profile lands a gears-sdlc
                 doc at e.g. gears/<gear>/docs/PRD.md (durable=gears/<gear>/docs,
                 doc_filename=PRD). Empty preserves the default slug behaviour.

stdout: a JSON object so host.run parses it into `stdout_json` and the
drafting room binds the durable path from one call:

    {"prd_file": "docs/prd/<slug>.md"}

exit 0 on success, non-zero on error. The numbered check artifacts are left
in the workspace as the per-PRD record (audit trail), disambiguated by their
3-digit lexical-sort prefix.
"""

import json
import os
import re
import sys

MAX_SLUG_WORDS = 6


def slugify(text: str) -> str:
    first_line = text.strip().split("\n")[0]
    slug = re.sub(r"[^a-z0-9]+", "-", first_line.lower()).strip("-")
    slug = "-".join(slug.split("-")[:MAX_SLUG_WORDS])
    return slug or "prd"


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


def resolve(workdir: str, path: str) -> str:
    return path if os.path.isabs(path) else os.path.join(workdir, path)


def main() -> None:
    if len(sys.argv) < 4:
        print(
            f"usage: {sys.argv[0]} <workspace> <slug> <draft_path> "
            "[workdir] [durable] [change_target]",
            file=sys.stderr,
        )
        sys.exit(1)

    workspace = sys.argv[1]
    slug_in = sys.argv[2]
    draft_path = sys.argv[3]
    workdir = sys.argv[4] if len(sys.argv) > 4 else "."
    durable = sys.argv[5] if len(sys.argv) > 5 else "docs/prd"
    change_target = sys.argv[6] if len(sys.argv) > 6 else ""
    doc_filename = sys.argv[7].strip() if len(sys.argv) > 7 else ""

    if change_target.strip():
        # Amend path: the author edited an existing PRD in place. Nothing to
        # move — the existing file is the published one.
        prd_rel = change_target.strip()
    else:
        src = resolve(workdir, draft_path)
        if not os.path.isfile(src):
            print(f"prd_publish: no draft at {src}", file=sys.stderr)
            sys.exit(1)

        with open(src) as f:
            draft = f.read()

        base_dir = resolve(workdir, durable)
        os.makedirs(base_dir, exist_ok=True)

        if doc_filename:
            # External-target profile: a FIXED doc name (PRD.md) at a per-gear
            # durable path, overwriting in place — the gears-sdlc shape carries
            # its own cpt-IDs, not a kitsoki slug.
            dest = os.path.join(base_dir, f"{doc_filename}.md")
        else:
            draft_title = title_from_draft(draft)
            slug = slugify(draft_title) if draft_title else slugify(slug_in)
            dest = find_path(base_dir, slug)

        # Copy the draft into the durable home; leave the numbered checks (and
        # the draft itself) in the workspace as the per-PRD record.
        with open(dest, "w") as f:
            f.write(draft)
        prd_rel = os.path.relpath(dest, workdir)

    print(json.dumps({"prd_file": prd_rel}), end="")


if __name__ == "__main__":
    main()
