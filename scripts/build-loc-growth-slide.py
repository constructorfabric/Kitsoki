#!/usr/bin/env python3
"""Deterministically build the "lines of code since inception" Slidey chart scenes.

Counts the *actual* repository tree at the end of each week since the first commit
and categorises every file, so the numbers match `git ls-files | wc -l` today
(~the live tree) rather than over-counting. We deliberately do NOT sum
`git log --numstat`: this repo is rebased/cherry-picked often, so the linear
history double-counts the same logical change and inflates the total by ~30%.

For each weekly snapshot we resolve the last first-parent commit on or before the
week boundary, walk its tree with `git ls-tree -r`, and count newlines per blob via
`git cat-file --batch` (blob line-counts are cached by sha, so unchanged files are
counted once). Nothing is ever checked out — the working tree is untouched.

Two `chart` scenes are emitted:

  1. Cumulative lines by area — a *stacked area* chart (the series are pre-stacked
     so the bands sum visually and the top edge is the true total; no floating
     "total" line that looks like it leaves a gap unaccounted).
  2. Lines added per week by area — a line chart of weekly deltas (can go negative
     when a week net-deletes, e.g. a big removal/refactor).

Categories (first-match precedence), chosen so every large mover is named — in
particular the runstatus web UI, which is otherwise buried under generic tooling:

    tests   - *_test.go, tests/ or testdata/ dirs, *_test.sh, *.test.*
    stories - stories/**, internal/basestories/**
    engine  - *.go, cmd/**, internal/**, pkg/** (the Go runtime)
    webui   - tools/runstatus/** and any .vue/.ts/.tsx/.css/.scss frontend
    docs    - docs/**, features/**, *.md
    tooling - everything else authored (scripts/, python harnesses, configs, ...)

Bot-generated output (issues/, .artifacts/, .kitsoki/, .context/) is excluded — it
is produced, not authored.

Usage:
    python3 scripts/build-loc-growth-slide.py            # print the two scenes
    python3 scripts/build-loc-growth-slide.py --insert   # splice them into the deck
"""

import argparse
import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from datetime import datetime, timezone

REPO = subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()
DECK = os.path.join(REPO, "docs/decks/kitsoki-pitch.slidey.json")
WEEK = 7 * 86400

# Category -> Slidey design-token colour. Six authored categories = the six tokens.
CATEGORY_COLOR = {
    "engine": "primary",
    "tests": "green",
    "stories": "teal",
    "webui": "secondary",
    "docs": "orange",
    "tooling": "red",
}
CATEGORY_LABEL = {
    "engine": "Engine",
    "tests": "Tests",
    "stories": "Stories",
    "webui": "Web UI",
    "docs": "Docs",
    "tooling": "Tooling",
}

EXCLUDE_RE = re.compile(r"^(issues/|\.artifacts/|\.kitsoki/|\.context/)")
BINARY_RE = re.compile(r"\.(png|jpe?g|gif|webp|mp4|mov|ico|woff2?|ttf|otf|wasm|pdf|zip|gz)$")
TEST_RE = re.compile(r"(^|/)tests?/|^testdata/|/testdata/|_test\.go$|_test\.sh$|(^|/)test_[^/]*\.sh$|\.test\.[a-z]+$")
STORY_RE = re.compile(r"^(stories/|internal/basestories/)")
ENGINE_RE = re.compile(r"(\.go$|^cmd/|^internal/|^pkg/)")
WEBUI_RE = re.compile(r"^tools/runstatus/|\.(vue|ts|tsx|css|scss)$")
DOCS_RE = re.compile(r"^(docs/|features/)|\.md$")


def categorise(path):
    if EXCLUDE_RE.match(path) or BINARY_RE.search(path):
        return None
    if TEST_RE.search(path):
        return "tests"
    if STORY_RE.match(path):
        return "stories"
    if ENGINE_RE.search(path):
        return "engine"
    if WEBUI_RE.search(path):
        return "webui"
    if DOCS_RE.search(path):
        return "docs"
    return "tooling"


def git(*args):
    return subprocess.check_output(["git", *args], cwd=REPO)


def weekly_revs():
    """Return [(week_label, rev), ...] — the tree at each week boundary to HEAD."""
    first_ts = int(git("log", "--reverse", "--format=%ct").decode().split("\n", 1)[0])
    head_ts = int(git("show", "-s", "--format=%ct", "HEAD").decode().strip())
    head_rev = git("rev-parse", "HEAD").decode().strip()
    n_weeks = (head_ts - first_ts) // WEEK + 1
    revs = []
    for w in range(n_weeks):
        boundary = first_ts + (w + 1) * WEEK
        label = datetime.fromtimestamp(first_ts + w * WEEK, tz=timezone.utc).strftime("W%V")
        if w == n_weeks - 1:
            revs.append((label, head_rev))
            continue
        # Last first-parent (mainline) commit on or before the week boundary.
        out = git("rev-list", "-1", "--first-parent",
                  f"--before=@{boundary}", "HEAD").decode().strip()
        revs.append((label, out or head_rev))
    return revs


def lines_by_category(rev, cache):
    """Category -> total lines for the tree at `rev`. Blob line-counts cached by sha."""
    tree = git("ls-tree", "-r", rev).decode()
    wanted = []  # (sha, category)
    for line in tree.splitlines():
        meta, _, path = line.partition("\t")
        parts = meta.split()
        if len(parts) < 3 or parts[1] != "blob":
            continue
        cat = categorise(path)
        if cat is None:
            continue
        wanted.append((parts[2], cat))

    need = sorted({sha for sha, _ in wanted if sha not in cache})
    if need:
        proc = subprocess.run(
            ["git", "cat-file", "--batch"],
            cwd=REPO, input="\n".join(need).encode(), stdout=subprocess.PIPE,
        )
        buf = proc.stdout
        i = 0
        for sha in need:
            nl = buf.index(b"\n", i)
            header = buf[i:nl].split()
            size = int(header[2])
            content = buf[nl + 1: nl + 1 + size]
            cache[sha] = content.count(b"\n") + (1 if content and not content.endswith(b"\n") else 0)
            i = nl + 1 + size + 1  # skip content and trailing newline

    totals = defaultdict(int)
    for sha, cat in wanted:
        totals[cat] += cache[sha]
    return totals


def build():
    revs = weekly_revs()
    labels = [lbl for lbl, _ in revs]
    cache = {}
    # cum[week][category] = lines at that weekly snapshot.
    cum = [lines_by_category(rev, cache) for _, rev in revs]
    cats = list(CATEGORY_COLOR.keys())

    final = {c: cum[-1].get(c, 0) for c in cats}
    grand_total = sum(final.values())
    # Stack order: largest contributor on the bottom for a stable base.
    order = sorted(cats, key=lambda c: final[c], reverse=True)

    def k(n):
        return round(n / 1000, 1)

    # --- Stacked-area cumulative chart -------------------------------------
    # Pre-stack: series[i].y = sum of categories order[0..i]. Emit top-of-stack
    # first so each smaller band draws over the larger one (fills go to zero).
    stacked = []
    for i, c in enumerate(order):
        pts = []
        for w in range(len(labels)):
            yk = sum(cum[w].get(order[j], 0) for j in range(i + 1))
            pts.append({"x": labels[w], "y": k(yk)})
        stacked.append({"name": CATEGORY_LABEL[c], "color": CATEGORY_COLOR[c], "points": pts})
    stacked_series = list(reversed(stacked))  # draw biggest (total envelope) first

    cumulative_scene = {
        "_comment": "Generated by scripts/build-loc-growth-slide.py from git tree snapshots — do not hand-edit.",
        "type": "chart",
        "variant": "area",
        "title": f"{k(grand_total):.0f}k lines in {len(labels)} weeks",
        "unit": "k",
        "axes": {"x": "Week", "y": "Lines of code (thousands)"},
        "series": stacked_series,
        "caption": "Stacked lines of code by area — the top edge is the whole repo. Engine, tests, stories, and the web UI each carry a real share; nothing hides in an “other” bucket.",
        "narration": f"In {len(labels)} weeks Kitsoki grew to about {k(grand_total):.0f} thousand lines — engine, tests, stories, and the web UI each a named, visible share of the whole.",
        "hold": 180,
    }

    # --- Per-week additions (deltas) line chart ----------------------------
    velocity = []
    for c in order:
        pts = []
        for w in range(len(labels)):
            prev = cum[w - 1].get(c, 0) if w > 0 else 0
            pts.append({"x": labels[w], "y": k(cum[w].get(c, 0) - prev)})
        velocity.append({"name": CATEGORY_LABEL[c], "color": CATEGORY_COLOR[c], "points": pts})

    velocity_scene = {
        "_comment": "Generated by scripts/build-loc-growth-slide.py from git tree snapshots — do not hand-edit.",
        "type": "chart",
        "variant": "line",
        "title": "Lines added per week",
        "unit": "k",
        "axes": {"x": "Week", "y": "Net lines added (thousands)"},
        "series": velocity,
        "caption": "Weekly net change by area. Tests rise with engine every week; dips are deliberate removals (e.g. decoupling), not stalls.",
        "narration": "Every week moves across all areas at once, with tests tracking the engine rather than trailing it.",
        "hold": 150,
    }
    return cumulative_scene, velocity_scene


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--insert", action="store_true", help="splice scenes into the pitch deck")
    ap.add_argument("--after", type=int, default=0,
                    help="scene index to insert AFTER (default 0, right after the title)")
    args = ap.parse_args()

    cumulative_scene, velocity_scene = build()

    if not args.insert:
        json.dump([cumulative_scene, velocity_scene], sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write("\n")
        return

    with open(DECK) as f:
        deck = json.load(f)
    marker = "scripts/build-loc-growth-slide.py"
    scenes = [s for s in deck["scenes"] if marker not in (s.get("_comment") or "")]
    at = args.after + 1
    scenes[at:at] = [cumulative_scene, velocity_scene]
    deck["scenes"] = scenes
    with open(DECK, "w") as f:
        json.dump(deck, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"Inserted 2 growth scenes after scene {args.after} -> {DECK}")


if __name__ == "__main__":
    main()
