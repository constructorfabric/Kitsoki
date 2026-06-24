#!/usr/bin/env python3
"""Robustness tests for the two hand-rolled stdlib parsers, NO LLM.

  coverage_prep.load_profile  — the mining.profile.yaml subset reader
  intent_common.load_tag_vocab — the vocab/tags.yaml reader

Both are tiny indentation-aware subset parsers (no pyyaml dependency). The risk
is SILENT mis-parsing of realistic-but-unsupported YAML; these tests pin the
quote-aware behavior and assert the fail-loud guards.

Run:  python3 tools/session-mining/tests/test_parsers.py
"""
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import coverage_prep
import intent_common as ic


def _w(d, name, text):
    p = os.path.join(d, name)
    with open(p, "w") as fh:
        fh.write(text)
    return p


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    with tempfile.TemporaryDirectory() as work:
        # --- profile: quote-aware flow-list split (comma inside quotes) ---
        prof = _w(work, "ok.profile.yaml",
                  'story: demo\n'
                  'scope:\n'
                  '  grep: [rebase, "git merge, squash", conflict]\n'
                  '  sample: recency          # inline comment on a scalar\n'
                  '  action_tags: [commit-or-pr, rebase-or-resolve-conflicts]\n'
                  'owns:\n'
                  '  commit-or-pr: [commit, staging]\n'
                  'non_goals: [force-push, cherry-pick]\n')
        p = coverage_prep.load_profile(prof)
        check(p["scope"]["grep"] == ["rebase", "git merge, squash", "conflict"],
              "profile: quoted comma must stay one element, got %r" % p["scope"]["grep"])
        check(p["scope"]["sample"] == "recency",
              "profile: inline comment must be stripped from scalar, got %r" % p["scope"]["sample"])
        check(p["scope"]["action_tags"] == ["commit-or-pr", "rebase-or-resolve-conflicts"],
              "profile: action_tags flow list, got %r" % p["scope"].get("action_tags"))
        check(p["owns"]["commit-or-pr"] == ["commit", "staging"],
              "profile: nested owns map, got %r" % p.get("owns"))
        check(p["non_goals"] == ["force-push", "cherry-pick"],
              "profile: non_goals flow list, got %r" % p.get("non_goals"))

        # --- profile: fail loud on a block-style list ---
        blk = _w(work, "block.profile.yaml",
                 "story: demo\nnon_goals:\n  - force-push\n  - cherry-pick\n")
        try:
            coverage_prep.load_profile(blk)
            check(False, "profile: block-style list must raise, but parsed silently")
        except ValueError as e:
            check("block-style list" in str(e), "profile: block-list error wording: %r" % str(e))

        # --- profile: fail loud on tab indentation ---
        tab = _w(work, "tab.profile.yaml", "story: demo\nscope:\n\tgrep: [a, b]\n")
        try:
            coverage_prep.load_profile(tab)
            check(False, "profile: tab indentation must raise, but parsed silently")
        except ValueError as e:
            check("tab" in str(e).lower(), "profile: tab error wording: %r" % str(e))

        # --- vocab: tolerate inline comments + quotes on members ---
        vocab = _w(work, "tags.yaml",
                   'tags_version: "2026-06-17"\n'
                   'dimensions:\n'
                   '  action:\n'
                   '    members:\n'
                   '      - commit-or-pr   # the headline git action\n'
                   '      - "rebase-or-resolve-conflicts"\n'
                   '      - explore-codebase\n'
                   '  surface:\n'
                   '    members:\n'
                   '      - code\n')
        v = ic.load_tag_vocab(vocab)
        act = v["dimensions"].get("action", set())
        check("commit-or-pr" in act,
              "vocab: member with inline comment must survive, got %r" % act)
        check("rebase-or-resolve-conflicts" in act,
              "vocab: quoted member must be unquoted, got %r" % act)
        check("explore-codebase" in act, "vocab: plain member, got %r" % act)
        check(v["dimensions"].get("surface") == {"code"}, "vocab: surface dim, got %r" % v["dimensions"].get("surface"))
        check(v["tags_version"] == "2026-06-17", "vocab: tags_version, got %r" % v["tags_version"])

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: profile + vocab parser robustness (no LLM)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
