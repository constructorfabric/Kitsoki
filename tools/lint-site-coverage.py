#!/usr/bin/env python3
"""lint-site-coverage — feature-site + pitch truthful, positive-coverage lint (WP.2).

Extends the W5 site-truthfulness idea (README-only honesty, see 0.5's
`! grep -qi 'status.*poc' README.md` gate) to the feature catalog
(`features/*.yaml`) and the pitch deck (`docs/decks/kitsoki-pitch.slidey.json`),
and adds a POSITIVE half on top of the honesty half:

  (a) HONESTY — no untrue-maturity-claim string (see UNTRUE_MATURITY_PHRASES)
      appears anywhere in the feature catalog or the pitch deck. Overclaiming
      ("production-ready", "enterprise-grade", ...) is exactly as dishonest as
      the README saying "PoC." while the product ships — just in the other
      direction.

  (b) COVERAGE — every SHIPPED (integrated), `demo_video:`-flagged change in
      the goal's decomposition.yaml has a corresponding `features/*.yaml`
      entry AND is referenced from the pitch deck. A stranger reading the
      site should be able to see that a shipped, demo-worthy capability
      actually exists.

Ground truth for "which change maps to which feature" is the hand-authored
CHANGE_TO_FEATURE table below (same discipline as WP.1's
docs-migration-index.md: a committed mapping, updated as part of a shipped
change's definition of done — see decomposition.yaml's `definition_of_done`,
which names this lint, WP.2, as the gate on that update).

"Shipped" status is resolved two ways:

  * By default (no --ledger/--work; this is what the WP.2 gate's bare
    `python3 tools/lint-site-coverage.py` runs), only the changes already
    present as CHANGE_TO_FEATURE keys are checked for consistency (their
    feature file still exists, still says something true, and the pitch deck
    still references it) — this is the offline, always-runnable form and
    catches drift/regression.

  * When --ledger PATH (a `goal.py ledger`-shaped JSON file) or --work DIR
    (a directory holding ledger.json) is given, the lint cross-checks the
    LIVE ledger: every change with `state: integrated` that also carries a
    `demo_video:` field in decomposition.yaml MUST be a CHANGE_TO_FEATURE key
    — catching a newly-shipped demo_video change whose site coverage was
    never authored. This is the live/CI form.

Usage:
  lint-site-coverage.py [--goal-dir docs/goals/generalized-usage]
                        [--features-dir features]
                        [--pitch docs/decks/kitsoki-pitch.slidey.json]
                        [--ledger PATH | --work DIR]
                        [--map PATH]          # override CHANGE_TO_FEATURE (JSON: {change_id: feature_id})
  lint-site-coverage.py --fixture DIR         # self-contained fixture, see tools/testdata/lint-site-coverage/
"""
from __future__ import annotations

import argparse
import json
import os
import sys

try:
    import yaml  # PyYAML — present in the repo's python env (goal.py depends on it too)
except ImportError:  # pragma: no cover
    yaml = None

# --- (a) honesty: overclaiming is as dishonest as underclaiming -----------
UNTRUE_MATURITY_PHRASES = (
    "proof of concept",
    "poc only",
    "production-ready",
    "production ready",
    "enterprise-grade",
    "enterprise grade",
    "battle-tested",
    "battle tested",
    "zero-config, works for any project",
    "100% reliable",
    "never fails",
    "guaranteed to work",
    "fully autonomous, no review needed",
    "bulletproof",
    "flawless",
)

# --- (b) coverage: committed change_id -> features/<id>.yaml mapping ------
# Update this table as part of a shipped, demo_video-flagged change's
# definition of done (decomposition.yaml's WP.2 gate). Each entry must name
# a real features/<feature_id>.yaml.
CHANGE_TO_FEATURE = {
    "WM.0": "goal-seeker",
    "0.1": "stranger-install",
    "0.3": "get-started-actions",
    "0.4": "first-run-message",
    "4.3": "kitsoki-doctor",
}


def _die(msg):
    print("lint-site-coverage: %s" % msg, file=sys.stderr)
    sys.exit(2)


def load_yaml(path):
    if yaml is None:
        _die("PyYAML is required to parse %s" % path)
    with open(path, encoding="utf-8") as f:
        return yaml.safe_load(f) or {}


def load_decomposition(goal_dir):
    path = os.path.join(goal_dir, "decomposition.yaml")
    if not os.path.exists(path):
        return None, "decomposition.yaml not found: %s" % path
    return load_yaml(path), None


def demo_video_change_ids(decomp):
    """{change_id: title} for every change carrying a truthy demo_video field."""
    out = {}
    for c in decomp.get("changes", []) or []:
        if c.get("demo_video"):
            out[str(c["id"])] = c.get("title", "")
    return out


def shipped_ids_from_ledger(ledger_path):
    with open(ledger_path, encoding="utf-8") as f:
        data = json.load(f)
    return {
        str(c.get("change_id"))
        for c in data.get("changes", []) or []
        if c.get("state") == "integrated"
    }


def resolve_ledger_path(args):
    if args.ledger:
        return args.ledger
    if args.work:
        p = os.path.join(args.work, "ledger.json")
        if os.path.exists(p):
            return p
    return None


def read_text_files(paths):
    out = {}
    for p in paths:
        if p and os.path.exists(p) and os.path.isfile(p):
            out[p] = open(p, encoding="utf-8", errors="replace").read()
    return out


def scan_untrue_claims(text_by_path):
    violations = []
    for path, text in text_by_path.items():
        low = text.lower()
        for phrase in UNTRUE_MATURITY_PHRASES:
            if phrase in low:
                violations.append("%s: untrue maturity-claim string %r" % (path, phrase))
    return violations


def feature_files(features_dir):
    """id -> path for every features/*.yaml (skips unparsable files)."""
    out = {}
    if not os.path.isdir(features_dir):
        return out
    for name in sorted(os.listdir(features_dir)):
        if not name.endswith(".yaml"):
            continue
        path = os.path.join(features_dir, name)
        try:
            data = load_yaml(path)
        except Exception:
            continue
        fid = data.get("id") if isinstance(data, dict) else None
        if fid:
            out[str(fid)] = path
    return out


def check_coverage(decomp, features_dir, pitch_path, change_map, ids_to_check):
    """Every change_id in ids_to_check must resolve, via change_map, to a
    features/<id>.yaml that exists AND is referenced from the pitch deck."""
    violations = []
    flagged = demo_video_change_ids(decomp)
    by_id = feature_files(features_dir)
    pitch_text = ""
    if pitch_path and os.path.exists(pitch_path):
        pitch_text = open(pitch_path, encoding="utf-8", errors="replace").read()
    elif pitch_path:
        violations.append("pitch deck not found: %s" % pitch_path)

    for cid in sorted(ids_to_check):
        if cid not in flagged:
            violations.append(
                "%s: mapped in CHANGE_TO_FEATURE but has no demo_video: field in decomposition.yaml (stale mapping?)"
                % cid
            )
            continue
        fid = change_map.get(cid)
        if not fid:
            violations.append(
                "%s: shipped demo_video-flagged change has no features/*.yaml entry (add one + a pitch-deck reference, then map it in CHANGE_TO_FEATURE)"
                % cid
            )
            continue
        if fid not in by_id:
            violations.append("%s: mapped feature %r has no features/%s.yaml on disk" % (cid, fid, fid))
            continue
        needle = "features/%s.yaml" % fid
        if needle not in pitch_text and fid not in pitch_text:
            violations.append(
                "%s: feature %r (features/%s.yaml) is not referenced from the pitch deck (%s)"
                % (cid, fid, fid, pitch_path)
            )
    return violations


def lint(args):
    decomp, err = load_decomposition(args.goal_dir)
    if err:
        return 1, [err], [], "n/a"

    violations = []

    # (a) honesty over the whole feature catalog + pitch deck
    files_to_scan = list(feature_files(args.features_dir).values())
    if args.pitch:
        files_to_scan.append(args.pitch)
    violations += scan_untrue_claims(read_text_files(files_to_scan))

    # (b) coverage
    change_map = args.change_map
    ledger_path = resolve_ledger_path(args)
    if ledger_path:
        flagged = demo_video_change_ids(decomp)
        shipped = shipped_ids_from_ledger(ledger_path)
        ids_to_check = sorted(set(flagged) & shipped)
        source = "live ledger %s" % ledger_path
    else:
        ids_to_check = sorted(change_map)
        source = "built-in CHANGE_TO_FEATURE snapshot"
    violations += check_coverage(decomp, args.features_dir, args.pitch, change_map, ids_to_check)

    return (1 if violations else 0), violations, ids_to_check, source


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--goal-dir", default="docs/goals/generalized-usage")
    ap.add_argument("--features-dir", default="features")
    ap.add_argument("--pitch", default="docs/decks/kitsoki-pitch.slidey.json")
    ap.add_argument("--ledger", help="path to a goal.py-ledger-shaped JSON file")
    ap.add_argument("--work", help="directory holding ledger.json (alternative to --ledger)")
    ap.add_argument("--map", help="override CHANGE_TO_FEATURE with a JSON file {change_id: feature_id}")
    ap.add_argument("--fixture", help="self-contained fixture dir: DIR/decomposition.yaml, DIR/features/, DIR/pitch.slidey.json, optional DIR/map.json, DIR/ledger.json")
    a = ap.parse_args()

    if a.fixture:
        a.goal_dir = a.fixture
        a.features_dir = os.path.join(a.fixture, "features")
        a.pitch = os.path.join(a.fixture, "pitch.slidey.json")
        map_path = os.path.join(a.fixture, "map.json")
        if os.path.exists(map_path):
            a.map = map_path
        ledger_path = os.path.join(a.fixture, "ledger.json")
        if os.path.exists(ledger_path) and not a.ledger and not a.work:
            a.ledger = ledger_path

    a.change_map = json.load(open(a.map, encoding="utf-8")) if a.map else dict(CHANGE_TO_FEATURE)

    code, violations, ids_checked, source = lint(a)
    if code == 0:
        print(
            "lint-site-coverage: OK — %d shipped demo_video change(s) covered (%s); no untrue maturity claims"
            % (len(ids_checked), source)
        )
    else:
        print("lint-site-coverage: %d violation(s)" % len(violations))
        for v in violations:
            print("  ✗ " + v)
    return code


if __name__ == "__main__":
    sys.exit(main())
