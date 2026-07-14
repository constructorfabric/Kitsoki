#!/usr/bin/env python3
"""lint-video-catalog - validate goal demo_video rows and produced rrweb clips.

The goal decomposition is the source of truth for which changes carry a
`demo_video:` field. The catalog is the reviewable, committed index that maps
each such change to a produced clip. This lint checks both directions:

  * every demo_video-flagged change has exactly one catalog row;
  * every catalog row names a non-empty clip file that exists on disk.

Usage:
  lint-video-catalog.py --goal-dir docs/goals/generalized-usage
  lint-video-catalog.py --fixture tools/testdata/lint-video-catalog/green
"""
from __future__ import annotations

import argparse
import os
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None


REQUIRED_COLUMNS = ("change", "title", "rrweb_clip", "compendium", "shows")


def _die(msg: str) -> None:
    print("lint-video-catalog: %s" % msg, file=sys.stderr)
    sys.exit(2)


def load_yaml(path: str):
    if yaml is None:
        _die("PyYAML is required to parse %s" % path)
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f) or {}


def load_decomposition(goal_dir: str):
    path = os.path.join(goal_dir, "decomposition.yaml")
    if not os.path.exists(path):
        return None, ["decomposition.yaml not found: %s" % path]
    return load_yaml(path), []


def demo_video_changes(decomp) -> dict[str, str]:
    out: dict[str, str] = {}
    for change in decomp.get("changes", []) or []:
        if change.get("demo_video"):
            out[str(change["id"])] = str(change.get("title") or "")
    return out


def split_md_row(line: str) -> list[str]:
    stripped = line.strip()
    if not stripped.startswith("|") or not stripped.endswith("|"):
        return []
    return [cell.strip() for cell in stripped.strip("|").split("|")]


def is_separator(cells: list[str]) -> bool:
    if not cells:
        return False
    for cell in cells:
        if not cell:
            return False
        for ch in cell:
            if ch not in "-:":
                return False
    return True


def parse_catalog(path: str):
    if not os.path.exists(path):
        return {}, ["video-catalog.md not found: %s" % path]
    rows = {}
    violations = []
    header: list[str] = []
    with open(path, encoding="utf-8") as f:
        for line_no, line in enumerate(f, start=1):
            cells = split_md_row(line)
            if not cells:
                continue
            if not header:
                header = [c.lower().replace(" ", "_") for c in cells]
                missing = [c for c in REQUIRED_COLUMNS if c not in header]
                if missing:
                    violations.append("%s:%d missing required column(s): %s" % (path, line_no, ", ".join(missing)))
                continue
            if is_separator(cells):
                continue
            rec = {}
            for i in range(min(len(header), len(cells))):
                rec[header[i]] = cells[i]
            cid = rec.get("change", "").strip()
            if not cid:
                violations.append("%s:%d row missing Change id" % (path, line_no))
                continue
            if cid in rows:
                violations.append("%s:%d duplicate row for change %s" % (path, line_no, cid))
                continue
            rec["_line"] = line_no
            rows[cid] = rec
    if not header:
        violations.append("video-catalog.md has no Markdown table: %s" % path)
    return rows, violations


def resolve_clip(goal_dir: str, clip: str) -> str:
    if os.path.isabs(clip):
        return clip
    cwd_path = os.path.abspath(clip)
    if os.path.exists(cwd_path):
        return cwd_path
    repo_root = os.path.abspath(os.path.join(goal_dir, "..", "..", ".."))
    return os.path.normpath(os.path.join(repo_root, clip))


def check_catalog(goal_dir: str):
    decomp, violations = load_decomposition(goal_dir)
    if violations:
        return violations, {}, {}
    flagged = demo_video_changes(decomp)
    catalog_path = os.path.join(goal_dir, "video-catalog.md")
    rows, row_violations = parse_catalog(catalog_path)
    violations.extend(row_violations)

    flagged_ids = set(flagged)
    row_ids = set(rows)
    for cid in sorted(flagged_ids - row_ids):
        violations.append("%s: demo_video-flagged change is missing from video-catalog.md" % cid)
    for cid in sorted(row_ids - flagged_ids):
        violations.append("%s: catalog row has no demo_video field in decomposition.yaml" % cid)

    for cid in sorted(flagged_ids & row_ids):
        rec = rows[cid]
        clip = (rec.get("rrweb_clip") or "").strip()
        if not clip:
            violations.append("%s: rrweb_clip is blank" % cid)
            continue
        path = resolve_clip(goal_dir, clip)
        if not os.path.exists(path):
            violations.append("%s: rrweb_clip does not exist: %s" % (cid, clip))
            continue
        if not os.path.isfile(path):
            violations.append("%s: rrweb_clip is not a file: %s" % (cid, clip))
            continue
        if os.path.getsize(path) == 0:
            violations.append("%s: rrweb_clip is empty: %s" % (cid, clip))
        if not (rec.get("shows") or "").strip():
            violations.append("%s: Shows column is blank" % cid)
    return violations, flagged, rows


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--goal-dir", default="docs/goals/generalized-usage")
    ap.add_argument("--fixture", help="self-contained fixture dir with decomposition.yaml and video-catalog.md")
    args = ap.parse_args()
    if args.fixture:
        args.goal_dir = args.fixture

    violations, flagged, rows = check_catalog(args.goal_dir)
    if violations:
        print("lint-video-catalog: %d violation(s)" % len(violations))
        for violation in violations:
            print("  ✗ " + violation)
        return 1
    print(
        "lint-video-catalog: OK - %d demo_video change(s) cataloged with produced clips"
        % len(flagged)
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
