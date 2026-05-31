#!/usr/bin/env python3
"""Merge N session-mining reports into one cross-contributor view.

    python3 aggregate.py report1.json report2.json ... > combined.json

Stdlib only (no pyyaml/jsonschema needed) so anyone can run it.

This is the NOISE CONTROL. Cataloged patterns (controlled-vocabulary ids) merge
into clean cross-user counts. Novel (free-form) patterns are clustered by a
normalized key and held in a PROMOTION GATE: a novel cluster is only surfaced as a
promotion candidate once it is independently corroborated by >= PROMOTE_MIN_CONTRIBUTORS
distinct contributors. Below that it sits in `quarantine` and NEVER contributes to
cataloged counts. So adding an open-coding / "propose novel" pass cannot inflate
the shared numbers — it can only fill the quarantine, which the gate ignores.

Merge semantics (group by id):
  occurrences, sessions_seen   -> sum
  mechanical_fraction          -> mean weighted by occurrences
  pain                         -> max (conservative)
  decision_points              -> union
  example_signatures           -> union (deduped)
  contributors                 -> sum of contributing reports for the id  (the real cross-user signal)
  determinism_priority         -> recomputed: commonality * mechanical * pain_weight

ASSOCIATIVE / RE-AGGREGATABLE. The output of this tool is itself a valid input:
aggregate(aggregate(a, b), c) == aggregate(a, b, c). This is what makes the
"share your report, merge everyone's later" workflow sound. The mechanism: every
report carries its own weight (`reports_merged`, default 1 for a raw extractor
report) and every pattern carries a `contributors` count (default 1). The merge
SUMS those weights rather than counting input files, so a previously-merged report
representing N contributors is not collapsed back to 1. Re-aggregation also pulls
forward an input's `novel_promotion_candidates` and `novel_quarantine`, so a novel
pattern that was one contributor short of promotion can still promote in a later
round instead of being silently dropped.

Inputs MUST already pass the share-gate (redact.py --scan). This tool assumes
clean reports and does not re-scrub.
"""
import json
import re
import sys
from collections import defaultdict

PROMOTE_MIN_CONTRIBUTORS = 2          # a novel cluster needs >=2 distinct contributors to promote
PAIN_WEIGHT = {"low": 0.4, "med": 0.7, "high": 1.0}
PAIN_RANK = {"low": 0, "med": 1, "high": 2}
RANK_PAIN = {v: k for k, v in PAIN_RANK.items()}
# models reliably emit synonyms; normalize rather than silently mis-bucket.
PAIN_ALIASES = {"medium": "med", "moderate": "med", "mid": "med",
                "hi": "high", "severe": "high", "critical": "high",
                "lo": "low", "minor": "low", "none": "low", "": "low"}
_UNKNOWN_PAIN = set()


def norm_pain(value):
    p = str(value).strip().lower()
    p = PAIN_ALIASES.get(p, p)
    if p not in PAIN_RANK:
        _UNKNOWN_PAIN.add(str(value))
        return "low"
    return p


def norm_key(s):
    """Normalize a (possibly novel) id for clustering: lowercase, strip non-alnum."""
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")


def load(paths):
    reports = []
    for p in paths:
        with open(p) as f:
            r = json.load(f)
        # how many underlying reports this file represents: 1 for a raw extractor
        # report, N for an already-merged one. Used so re-aggregation stays exact.
        r.setdefault("_weight", r.get("reports_merged", 1))
        reports.append(r)
    return reports


def merge_group(records):
    """records: list of pattern dicts for one id (raw or already-merged).

    A raw pattern represents 1 contributing report (contributors defaults to 1);
    an already-merged pattern carries its own contributors count. Summing those —
    rather than counting how many files we read — is what keeps the merge
    associative across re-aggregation.
    """
    total_occ = sum(p.get("occurrences", 0) for p in records)
    contributors = sum(p.get("contributors", 1) for p in records)
    occ_w = total_occ or 1
    mech = sum(p.get("mechanical_fraction", 0) * p.get("occurrences", 0) for p in records) / occ_w
    pains = [PAIN_RANK[norm_pain(p.get("pain", "low"))] for p in records]
    pain = RANK_PAIN[max(pains)] if pains else "low"
    dpoints, sigs = [], []
    for p in records:
        for d in p.get("decision_points", []):
            if d not in dpoints:
                dpoints.append(d)
        for s in p.get("example_signatures", []):
            if s not in sigs:
                sigs.append(s)
    sessions = sum(p.get("sessions_seen", p.get("corroboration", 0)) for p in records)
    return {
        "occurrences": total_occ,
        "sessions_seen": sessions,
        "contributors": contributors,
        "mechanical_fraction": round(mech, 3),
        "pain": pain,
        "decision_points": dpoints,
        "example_signatures": sigs,
    }


def priority(merged, total_contributors):
    commonality = merged["contributors"] / total_contributors if total_contributors else 0
    return round(commonality * merged["mechanical_fraction"] * PAIN_WEIGHT[merged["pain"]], 3)


def main():
    paths = [a for a in sys.argv[1:] if not a.startswith("-")]
    if not paths:
        sys.stderr.write("usage: aggregate.py report1.json report2.json ...\n")
        sys.exit(2)
    reports = load(paths)
    total_contributors = sum(r["_weight"] for r in reports)
    vocab_versions = sorted({r.get("vocab_version", "?") for r in reports})

    cataloged = defaultdict(list)
    novel = defaultdict(list)
    for r in reports:
        for p in r.get("patterns", []):
            if p.get("source") == "novel":
                novel[norm_key(p["id"])].append(p)
            else:
                cataloged[p["id"]].append(p)
        # carry an already-merged report's novel buckets forward so a pattern that
        # was a contributor short of promotion can still promote in a later round.
        for p in r.get("novel_promotion_candidates", []) + r.get("novel_quarantine", []):
            novel[norm_key(p["id"])].append(p)

    merged_patterns = []
    for pid, recs in cataloged.items():
        m = merge_group(recs)
        m["id"] = pid
        m["determinism_priority"] = priority(m, total_contributors)
        merged_patterns.append(m)
    merged_patterns.sort(key=lambda m: m["determinism_priority"], reverse=True)

    promotion_candidates, quarantine = [], []
    for key, recs in novel.items():
        m = merge_group(recs)
        m["id"] = key
        m["determinism_priority"] = priority(m, total_contributors)
        (promotion_candidates if m["contributors"] >= PROMOTE_MIN_CONTRIBUTORS else quarantine).append(m)
    promotion_candidates.sort(key=lambda m: m["contributors"], reverse=True)
    quarantine.sort(key=lambda m: m["contributors"], reverse=True)

    out = {
        "schema_version": "1.0",
        "vocab_version": vocab_versions[0] if len(vocab_versions) == 1 else vocab_versions,
        "contributors": total_contributors,
        "reports_merged": len(reports),
        "promote_min_contributors": PROMOTE_MIN_CONTRIBUTORS,
        "patterns": merged_patterns,
        "novel_promotion_candidates": promotion_candidates,
        "novel_quarantine": quarantine,
    }
    json.dump(out, sys.stdout, indent=2)
    sys.stdout.write("\n")

    sys.stderr.write(
        f"merged {len(reports)} reports from {total_contributors} contributor(s); "
        f"{len(merged_patterns)} cataloged, "
        f"{len(promotion_candidates)} promotion candidate(s), "
        f"{len(quarantine)} quarantined novel\n"
    )
    if len(vocab_versions) > 1:
        sys.stderr.write(f"WARNING: mixed vocab_versions {vocab_versions} — counts may not be comparable\n")
    if _UNKNOWN_PAIN:
        sys.stderr.write(f"WARNING: unrecognized pain value(s) {sorted(_UNKNOWN_PAIN)} treated as 'low' — "
                         f"expected low/med/high\n")


if __name__ == "__main__":
    main()
