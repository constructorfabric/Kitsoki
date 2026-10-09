#!/usr/bin/env python3
"""lint-docs-migrated — narrative-docs migration discipline lint (WP.1).

Enforces the docs/proposals lifecycle (see docs/proposals/README.md): when a
proposal-driven change SHIPS, its source proposal must be GONE or TRIMMED, and
an authoritative narrative page must exist under docs/. Without this, shipped
features leave stale planning material behind and no honest docs home.

Ground truth is a hand-authored migration index (a markdown table) that maps
each proposal-driven change id -> (source proposal file, narrative doc). The
lint walks that index and, for every row, asserts:

  * proposal GONE-or-TRIMMED: the proposal file is absent, OR present but its
    opening status line marks it trimmed/shipped/superseded (per the README's
    "Every proposal carries a status line"); a proposal still present with a
    Draft/"nothing implemented" status for a SHIPPED change is a violation.
  * narrative doc EXISTS: the mapped docs/** page is present and non-empty.

Rows may use "-" / "(deleted)" for a proposal that was fully removed.

Exit 0 when every row is consistent (green); exit 1 with a report otherwise.

Usage:
  lint-docs-migrated.py --goal-dir docs/goals/<slug>     # lints <goal-dir>/docs-migration-index.md
  lint-docs-migrated.py --index PATH                     # explicit index
  lint-docs-migrated.py --fixture DIR                    # lint DIR/docs-migration-index.md, paths relative to DIR
"""
import argparse, os, re, sys

TRIMMED_MARKERS = ("trimmed", "shipped", "superseded", "migrated")  # explicit ship markers; NOT "implemented" ("nothing implemented yet" is a DRAFT)
GONE_TOKENS = ("-", "—", "(deleted)", "(gone)", "n/a", "none")


def parse_index(path):
    """Parse the markdown table; yield (change_id, proposal, doc) tuples."""
    rows = []
    if not os.path.exists(path):
        return rows, "index not found: %s" % path
    header_seen = False
    for line in open(path, encoding="utf-8"):
        line = line.strip()
        if not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        # skip the header row and the |---|---| separator
        if not header_seen:
            if cells and cells[0].lower() in ("change", "change_id", "id"):
                header_seen = True
            continue
        if all(set(c) <= set("-: ") for c in cells):
            continue
        if len(cells) < 3:
            continue
        rows.append((cells[0], cells[1], cells[2]))
    return rows, None


def proposal_ok(root, ref):
    """True if the proposal is gone-or-trimmed."""
    ref = ref.strip().strip("`")
    if ref.lower() in GONE_TOKENS or ref == "":
        return True, "gone"
    p = os.path.join(root, ref)
    if not os.path.exists(p):
        return True, "gone"
    head = "".join(open(p, encoding="utf-8").readlines()[:12]).lower()
    if any(m in head for m in TRIMMED_MARKERS):
        return True, "trimmed"
    return False, "still present with no trimmed/shipped status line"


def doc_ok(root, ref):
    ref = ref.strip().strip("`")
    if ref.lower() in GONE_TOKENS or ref == "":
        return False, "no narrative doc mapped"
    p = os.path.join(root, ref)
    if not os.path.exists(p):
        return False, "narrative doc missing: %s" % ref
    if os.path.getsize(p) == 0:
        return False, "narrative doc empty: %s" % ref
    return True, "present"


def lint(index_path, root):
    rows, err = parse_index(index_path)
    if err:
        return 1, [err]
    violations = []
    for cid, proposal, doc in rows:
        pok, pwhy = proposal_ok(root, proposal)
        if not pok:
            violations.append("%s: proposal %s %s" % (cid, proposal, pwhy))
        dok, dwhy = doc_ok(root, doc)
        if not dok:
            violations.append("%s: %s" % (cid, dwhy))
    return (1 if violations else 0), violations


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--goal-dir")
    ap.add_argument("--index")
    ap.add_argument("--fixture")
    ap.add_argument("--repo-root", default=".")
    a = ap.parse_args()
    if a.fixture:
        index_path = os.path.join(a.fixture, "docs-migration-index.md")
        root = a.fixture
    elif a.index:
        index_path = a.index
        root = a.repo_root
    elif a.goal_dir:
        index_path = os.path.join(a.goal_dir, "docs-migration-index.md")
        root = a.repo_root
    else:
        ap.error("one of --goal-dir / --index / --fixture is required")
    code, violations = lint(index_path, root)
    n = len(parse_index(index_path)[0])
    if code == 0:
        print("lint-docs-migrated: OK — %d mapped change(s) consistent (%s)" % (n, index_path))
    else:
        print("lint-docs-migrated: %d violation(s) in %s" % (len(violations), index_path))
        for v in violations:
            print("  ✗ " + v)
    return code


if __name__ == "__main__":
    sys.exit(main())
