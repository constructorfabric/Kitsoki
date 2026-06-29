#!/usr/bin/env python3
"""Merge results/cells/*.json into results/summary.json (and, optionally, one
internal/agenteval Report per bug). Offline + deterministic — no clock, no LLM.

summary.json carries the manifest's bug/candidate/treatment headers, every cell
result, and a `rollup` with three buckets — by_treatment, by_candidate, and
by_cell_key ("<candidate>|<treatment>") — each holding the averages listed in
results/SCHEMA.md. `generated_at` is NOT read from the wall clock (the repo bans
nondeterministic timestamps in some contexts): pass --generated-at or set
BAKEOFF_GENERATED_AT.

  --emit-agenteval additionally writes results/agenteval/<bug>/latest.json in the
  agenteval.Report shape so deterministic_deck.py regenerates the deck offline.
  The candidate profile field there is "<candidate>|<treatment>".

Usage:
  aggregate.py --manifest tools/bugfix-bakeoff/bakeoff.yaml \
               --cells-dir tools/bugfix-bakeoff/results/cells \
               --out tools/bugfix-bakeoff/results/summary.json \
               --generated-at 2026-06-24T00:00:00Z [--emit-agenteval]
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import subprocess
import sys

import yaml


def load_manifest(path):
    with open(path) as fh:
        return yaml.safe_load(fh)


def load_cells(cells_dir):
    cells = []
    for path in sorted(glob.glob(os.path.join(cells_dir, "*.json"))):
        with open(path) as fh:
            cells.append(json.load(fh))
    return cells


def _avg(vals):
    vals = [v for v in vals if v is not None]
    return round(sum(vals) / len(vals), 6) if vals else 0.0


def _m(c, key):
    """metrics[key], tolerant of a cell that omits the block (None ⇒ _avg skips)."""
    return (c.get("metrics") or {}).get(key)


def _bucket(cells):
    """The SCHEMA rollup bucket for a list of cells. Tolerant of cells from the
    external grader (bench.py), which may carry None metrics / unmeasured
    compliance — _avg skips Nones rather than crashing."""
    n = len(cells)
    solved = sum(1 for c in cells if (c.get("outcome") or {}).get("quality") == "solved")
    return {
        "n": n,
        "solved": solved,
        "solve_rate": round(solved / n, 6) if n else 0.0,
        "avg_cost_usd": _avg([_m(c, "cost_usd") for c in cells]),
        "avg_total_tokens": _avg([_m(c, "total_tokens") for c in cells]),
        "avg_wall_time_s": _avg([_m(c, "wall_time_s") for c in cells]),
        "avg_guidance_turns": _avg([_m(c, "guidance_turns") for c in cells]),
        "avg_compliance": _avg([(c.get("compliance") or {}).get("rate") for c in cells]),
    }


def _group(cells, keyfn):
    groups = {}
    for c in cells:
        groups.setdefault(keyfn(c), []).append(c)
    return {k: _bucket(v) for k, v in sorted(groups.items())}


def build_summary(manifest, cells, generated_at):
    return {
        "generated_at": generated_at,
        "manifest": manifest.get("__path__", "tools/bugfix-bakeoff/external/projects/kitsoki/manifest.yaml"),
        "bugs": [
            {k: b.get(k) for k in
             ("id", "title", "severity", "component", "fix_sha",
              "baseline_sha", "oracle_test")}
            for b in manifest.get("bugs", [])
        ],
        "candidates": [
            {k: c.get(k) for k in
             ("key", "profile", "model", "effort", "provider")}
            for c in manifest.get("candidates", [])
        ],
        "treatments": manifest.get("treatments", ["kitsoki", "single"]),
        "cells": cells,
        "rollup": {
            "by_treatment": _group(cells, lambda c: c["treatment"]),
            "by_candidate": _group(cells, lambda c: c["candidate"]),
            "by_cell_key": _group(
                cells, lambda c: f"{c['candidate']}|{c['treatment']}"),
        },
    }


def _p95(vals):
    vals = sorted(v for v in vals if v is not None)
    if not vals:
        return 0.0
    idx = min(len(vals) - 1, int(round(0.95 * (len(vals) - 1))))
    return round(vals[idx], 6)


def build_agenteval_reports(manifest, cells, generated_at):
    """One agenteval.Report per bug. candidate.profile = "<candidate>|<treatment>".
    pass = oracle passed (the manifest's min_pass_rate=1.0 outcome bar)."""
    bar = manifest.get("adherence_bar", {})
    by_bug = {}
    for c in cells:
        by_bug.setdefault(c["bug"], []).append(c)

    reports = {}
    for bug_id, bug_cells in sorted(by_bug.items()):
        candidates = []
        for c in sorted(bug_cells, key=lambda x: (x["candidate"], x["treatment"])):
            # pass tracks the (possibly adjudicated) quality, not the raw oracle:
            # a behaviorally-correct fix that an wording-coupled oracle false-fails
            # but a judge adjudicated `solved` should count as a pass here.
            passed = c["outcome"]["quality"] == "solved"
            candidates.append({
                "profile": f"{c['candidate']}|{c['treatment']}",
                "model": c.get("model", ""),
                "effort": c.get("effort", ""),
                "provider": c.get("provider", ""),
                "pass": passed,
                "schema_valid_rate": 1.0,
                "comparator_pass_rate": 1.0 if passed else 0.0,
                "contract_conformance_rate": (c.get("compliance") or {}).get("rate"),
                "avg_cost_usd": (c.get("metrics") or {}).get("cost_usd"),
                "p95_cost_usd": (c.get("metrics") or {}).get("cost_usd"),
                "examples_run": 1,
            })
        reports[bug_id] = {
            "kind": "agenteval_report",
            "eval": "bugfix_bakeoff",
            "app": bug_id,
            "call": "bugfix",
            "generated_at": generated_at,
            "dataset_hash": "",
            "adherence_bar": {
                "min_pass_rate": bar.get("min_pass_rate", 1.0),
                "max_avg_cost_usd": bar.get("max_avg_cost_usd", 0),
            },
            "candidates": candidates,
        }
    return reports


def resolve_generated_at(arg):
    if arg:
        return arg
    env = os.environ.get("BAKEOFF_GENERATED_AT")
    if env:
        return env
    raise SystemExit(
        "aggregate.py: --generated-at (or BAKEOFF_GENERATED_AT) is required "
        "(deterministic build: no implicit wall-clock timestamp)")


def main(argv=None):
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--manifest",
                    default=os.path.join(here, "external", "projects", "kitsoki", "manifest.yaml"))
    # The candidate (model/effort) axis lives in the shared external candidates.yaml,
    # not in each project manifest. Merged into the manifest if it lacks `candidates`.
    ap.add_argument("--candidates",
                    default=os.path.join(here, "external", "candidates.yaml"))
    ap.add_argument("--cells-dir", default=os.path.join(here, "results", "cells"))
    ap.add_argument("--out", default=os.path.join(here, "results", "summary.json"))
    ap.add_argument("--generated-at", default="")
    ap.add_argument("--emit-agenteval", action="store_true")
    ap.add_argument("--agenteval-dir",
                    default=os.path.join(here, "results", "agenteval"))
    ap.add_argument("--deck", default="",
                    help="optional Slidey JSON report spec to generate offline")
    ap.add_argument("--markdown", default="",
                    help="optional Markdown review index to generate with --deck")
    args = ap.parse_args(argv)

    generated_at = resolve_generated_at(args.generated_at)
    manifest = load_manifest(args.manifest)
    manifest["__path__"] = os.path.relpath(args.manifest)
    # External project manifests carry bugs+treatments; the candidate axis is the
    # shared candidates.yaml. Merge it in so the deck headers resolve either way.
    if not manifest.get("candidates") and os.path.exists(args.candidates):
        manifest["candidates"] = load_manifest(args.candidates).get("candidates", [])
    cells = load_cells(args.cells_dir)

    summary = build_summary(manifest, cells, generated_at)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "w") as fh:
        json.dump(summary, fh, indent=2)
        fh.write("\n")
    print(f"wrote {args.out}  cells={len(cells)}", file=sys.stderr)

    if args.deck:
        root = os.path.abspath(os.path.join(here, "..", ".."))
        builder = os.path.join(root, "tools", "report-deck", "deterministic_deck.py")
        cmd = [
            sys.executable,
            builder,
            "--kind", "bakeoff-summary",
            "--input", args.out,
            "--out", args.deck,
        ]
        if args.markdown:
            cmd += ["--markdown", args.markdown]
        subprocess.run(cmd, check=True, stdout=subprocess.PIPE, text=True)

    if args.emit_agenteval:
        reports = build_agenteval_reports(manifest, cells, generated_at)
        for bug_id, report in reports.items():
            dest = os.path.join(args.agenteval_dir, bug_id, "latest.json")
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            with open(dest, "w") as fh:
                json.dump(report, fh, indent=2)
                fh.write("\n")
            print(f"wrote {dest}", file=sys.stderr)

    print(json.dumps({
        "summary_path": args.out,
        "generated_at": generated_at,
        "cells": summary["cells"],
        "rollup": summary["rollup"],
        "deck": {"spec_path": args.deck, "summary": "Bugfix bake-off report deck."} if args.deck else {},
        "markdown": args.markdown,
    }))


if __name__ == "__main__":
    main()
