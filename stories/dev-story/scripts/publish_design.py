#!/usr/bin/env python3
"""publish_design.py — move a drafted proposal out of the per-session
workspace into the docs/proposals/ queue, then mint a feature ticket that
links back to it so the proposal can be driven into implementation.

Usage:
    python3 publish_design.py <workspace> <slug> [change_target] [title] [idea] \
        [workdir] [durable] [doc_filename] [ticket_dir]

  workspace      docs/proposals/.workspace/<slug> — holds 004-proposal.md
                 (the draft) plus the numbered check artifacts 001..003.
  slug           the meaningful slug minted at intake; the final filename
                 prefers the draft's own title heading, falling back to this.
  change_target  when set, the author AMENDED this existing proposal in
                 place instead of writing a new draft — nothing to move;
                 the existing path is reused as the published file.
  title          human-readable proposal title (from the draft artifact);
                 used as the feature ticket title. Falls back to the slug.
  idea           the one-line idea captured at intake; seeds the ticket body.
  workdir        operator working tree (world.workdir); the durable home is
                 <workdir>/<durable>. Defaults to "." (kitsoki repo root).
  durable        durable home for published designs (world.design_durable_path,
                 default "docs/proposals").
  doc_filename   external-target override (world.design_doc_filename, default
                 ""): when set, the design publishes to
                 <workdir>/<durable>/<doc_filename>.md (a FIXED name,
                 overwriting in place) instead of <slug>.md — how a profile
                 lands a gears-sdlc doc at gears/<gear>/docs/DESIGN.md
                 (durable=gears/<gear>/docs, doc_filename=DESIGN).
  ticket_dir     where the linking feature ticket is minted
                 (world.design_ticket_dir, default "issues/features",
                 relative to workdir). EMPTY string SKIPS ticket minting — an
                 external target (gears-rust) tracks work in its own issues,
                 not a kitsoki feature ticket, so the published design stands
                 alone (ticket_id/ticket_path return empty).

stdout: a single JSON object so host.run parses it into `stdout_json` and the
draft room binds several world keys from one call:

    {"design_file": "docs/proposals/<slug>.md",
     "ticket_id":     "F-<ts>-<slug>",
     "ticket_path":   "issues/features/F-<ts>-<slug>.md",
     "ticket_title":  "<title>"}

exit 0 on success, non-zero on error.

The numbered check artifacts (001-brief … 004-references) are left in the
workspace as the per-proposal record, disambiguated by their 3-digit
lexical-sort prefix.
"""

import json
import os
import re
import shlex
import subprocess
import sys
from datetime import datetime, timezone


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


def resolve(workdir: str, path: str) -> str:
    return path if os.path.isabs(path) else os.path.join(workdir, path)


def write_feature_ticket(
    slug: str, title: str, idea: str, design_rel: str, features_dir: str
) -> tuple:
    """Mint <features_dir>/<id>.md linking back to the published proposal.

    Returns (ticket_id, ticket_rel_path). The id is timestamp-prefixed
    (`F-<ISO>-<slug>`) so it sorts newest-first alongside bug ids, and is
    collision-proofed against the features dir.
    """
    os.makedirs(features_dir, exist_ok=True)

    now = datetime.now(timezone.utc)
    base_id = f"F-{now.strftime('%Y-%m-%dT%H%M%SZ')}-{slug}"
    dest = find_path(features_dir, base_id)
    ticket_id = os.path.splitext(os.path.basename(dest))[0]
    filed_at = now.strftime("%Y-%m-%dT%H:%M:%SZ")

    ticket_title = title.strip() or slug
    body_idea = idea.strip()

    content = (
        "---\n"
        f'title: "{ticket_title}"\n'
        "status: open\n"
        "severity: P2\n"
        'assignee: ""\n'
        f'url: "{design_rel}"\n'
        "component: proposal\n"
        f'filed_at: "{filed_at}"\n'
        f'proposal: "{design_rel}"\n'
        "---\n\n"
        f"# {ticket_title}\n\n"
        "Implement the accepted proposal:\n\n"
        f"[{design_rel}]({design_rel})\n\n"
        + (f"{body_idea}\n\n" if body_idea else "")
        + "## Source\n\n"
        "Filed automatically when the proposal was published. The linked\n"
        "proposal document carries the full Why / What changes / Impact spine —\n"
        "read it before starting implementation.\n"
    )

    with open(dest, "w") as f:
        f.write(content)

    return ticket_id, dest


def file_feature_issue_github(slug: str, title: str, idea: str, design_rel: str, repo: str) -> tuple:
    """Mint a GitHub feature issue on `repo` linking the published proposal,
    instead of an issues/features/<id>.md file.

    Labels target:<repo-name> + comp:proposal (the GitHub twin of the local
    format's component: proposal). The target label derives from the repo
    slug's name segment - never a hardcoded project - so an onboarded external
    repo (e.g. acme/gears-rust -> target:gears-rust) gets its own vocabulary,
    and a local project instance can keep its own target label convention.
    Returns (issue_number, issue_url). Routes through Kitsoki's native GitHub
    filing path instead of shelling out to gh.
    """
    ticket_title = title.strip() or slug
    body_idea = idea.strip()
    body = (
        f"Implement the accepted proposal:\n\n[{design_rel}]({design_rel})\n\n"
        + (f"{body_idea}\n\n" if body_idea else "")
        + "## Source\n\n"
        "Filed automatically when the proposal was published. The linked "
        "proposal carries the full Why / What changes / Impact spine — read it "
        "before starting implementation.\n"
    )
    kitsoki = shlex.split(os.environ.get("KITSOKI_BIN", "go run ./cmd/kitsoki"))
    repo_name = repo.rstrip("/").rsplit("/", 1)[-1].lower() or "project"
    args = kitsoki + [
        "bug", "create",
        "--github", repo,
        "--target", repo_name,
        "--title", ticket_title,
        "--body", body,
        "--component", "proposal",
        "--severity", "P3",
        "--trace-ref", design_rel,
    ]
    res = subprocess.run(args, capture_output=True, text=True)
    if res.returncode != 0:
        raise RuntimeError("kitsoki bug create --github failed: " + res.stderr.strip())
    url = res.stdout.strip().splitlines()[-1].strip()
    number = url.rstrip("/").rsplit("/", 1)[-1]
    return number, url


def main() -> None:
    if len(sys.argv) < 3:
        print(
            f"usage: {sys.argv[0]} <workspace> <slug> [change_target] [title] [idea]",
            file=sys.stderr,
        )
        sys.exit(1)

    workspace = sys.argv[1]
    slug_in = sys.argv[2]
    change_target = sys.argv[3] if len(sys.argv) > 3 else ""
    title_in = sys.argv[4] if len(sys.argv) > 4 else ""
    idea_in = sys.argv[5] if len(sys.argv) > 5 else ""
    workdir = sys.argv[6] if len(sys.argv) > 6 else "."
    durable = sys.argv[7] if len(sys.argv) > 7 else os.path.join("docs", "proposals")
    doc_filename = sys.argv[8].strip() if len(sys.argv) > 8 else ""
    ticket_dir = sys.argv[9] if len(sys.argv) > 9 else os.path.join("issues", "features")
    ticket_repo = sys.argv[10].strip() if len(sys.argv) > 10 else ""

    if change_target.strip():
        # Amend path: the author edited an existing proposal in place. Nothing
        # to move — the existing file is the published one.
        design_rel = os.path.relpath(change_target.strip(), workdir)
        title = title_in.strip() or slug_in
    else:
        src = os.path.join(workspace, "004-proposal.md")
        if not os.path.isfile(src):
            print(f"publish_design: no draft at {src}", file=sys.stderr)
            sys.exit(1)

        with open(src) as f:
            draft = f.read()

        base_dir = resolve(workdir, durable)
        os.makedirs(base_dir, exist_ok=True)

        draft_title = title_from_draft(draft)
        title = title_in.strip() or draft_title or slug_in
        if doc_filename:
            # External-target profile: a FIXED doc name (DESIGN.md) at a
            # per-gear durable path, overwriting in place.
            dest = os.path.join(base_dir, f"{doc_filename}.md")
        else:
            slug = slugify(draft_title) if draft_title else slugify(slug_in)
            dest = find_path(base_dir, slug)

        # Move the draft into the queue; leave the numbered checks in the
        # workspace as the record.
        os.replace(src, dest)
        design_rel = os.path.relpath(dest, workdir)

    # Mint the feature ticket that links back to the published proposal, so the
    # draft room can route straight into the implementation pipeline. Precedence:
    #   ticket_repo set  → a GitHub feature issue for the configured repo;
    #   ticket_dir set   → a local issues/features/<id>.md file (the default);
    #   both empty       → skip (an external target tracks work elsewhere).
    ticket_url = ""
    if ticket_repo:
        ticket_id, ticket_url = file_feature_issue_github(
            slug_in, title, idea_in, design_rel, ticket_repo
        )
        ticket_rel = ""
    elif ticket_dir.strip():
        ticket_id, ticket_abs = write_feature_ticket(
            slug_in, title, idea_in, design_rel, resolve(workdir, ticket_dir)
        )
        ticket_rel = os.path.relpath(ticket_abs, workdir)
        ticket_url = design_rel
    else:
        ticket_id, ticket_rel = "", ""

    print(
        json.dumps(
            {
                "design_file": design_rel,
                "ticket_id": ticket_id,
                "ticket_path": ticket_rel,
                "ticket_title": title,
                "ticket_url": ticket_url,
            }
        ),
        end="",
    )


if __name__ == "__main__":
    main()
