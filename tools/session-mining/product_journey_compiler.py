#!/usr/bin/env python3
"""product_journey_compiler.py — task 3.3: project mined `kind: conversation`
scenario IR documents (schema/scenario_ir.schema.json, compiled by
scenario_compiler.py) into `tools/product-journey/personas.json` and
`tools/product-journey/scenarios.json` entries.

## What this compiler does NOT do

`tools/product-journey/run.py`'s scenarios are STAGE-LEVEL task templates
bound to one of a handful of stories (`stories/product-site`,
`stories/dev-story`, `stories/bugfix`, ...) that a persona is dispatched to
LIVE, then scored against `success_criteria` (`stage_plan`/`scenario_plan` in
run.py). A mined scenario has no bound story and no live-runnable success
check — it is a historical record of one real goal-bounded session span. This
compiler does not invent a story binding or a live success check for mined
data; it projects the mined scenario's own already-derived signal (goal,
persona, grounded expected_effects, the mcp/host tool names actually used)
into the SAME two JSON files, tagged `"source": "mined"`, so a reviewer can
browse real usage patterns alongside the hand-authored stage plan without the
hand-authored path ever being touched or reordered.

## Personas: clustered, not one-per-scenario

Mined scenarios carry a `persona` field already heuristically assigned by
scenario_compiler.py's `_persona_for` (task 2.2, coarse action-tag lookup).
This compiler CLUSTERS scenarios by that field — one new personas.json entry
per DISTINCT mined persona id not already present in the file (by id) — rather
than emitting one entry per scenario. A persona id that happens to collide
with an existing hand-authored one (e.g. "core-maintainer") is left alone:
the hand-authored entry wins, no duplicate is emitted.

## Scenarios: one scenarios.json entry per mined IR document

Each mined scenario becomes its own `id: mined-<scenario-id>` entry (its own
IR id is already a stable, provenance-bearing key — see
schema/scenario_ir.schema.json). `task` is the scenario's own verbatim `goal`.
`required_mcp` is the sorted, de-duplicated set of `mcp__*` tool names found
in the scenario's `expected_effects` (a real, grounded signal — see
scenario_compiler.py's `_grounded_effects` — not invented). `stage` is the
fixed sentinel `"mined_from_sessions"`, deliberately NOT one of a project's
configured stage ids, so `run.py`'s `stage_plan` (which only surfaces
scenarios whose `stage` matches a stage the target project actually declares)
never auto-selects a mined entry into a live run — these are for browsing/
audit, not automatic dispatch, until a human promotes one.

## Idempotence / merge semantics

`merge_into` only APPENDS entries whose `id` is not already present in the
target file (by any source — hand-authored OR previously-merged mined). Every
existing entry, in its existing order, is preserved byte-for-byte; new
entries are appended in a stable (sorted-by-id) order. Re-running the
compiler against the same scenario set a second time is a no-op (verified in
tests/test_product_journey_compiler.py).

Usage:
  python3 product_journey_compiler.py --scenarios-dir calibration \
      --personas-json ../product-journey/personas.json \
      --scenarios-json ../product-journey/scenarios.json
"""
import argparse
import glob
import json
import os
import re
import sys

_MCP_TOOL_RE = re.compile(r"\b(mcp__[a-zA-Z0-9_]+)")


def extract_mcp_tools(expected_effects):
    """Sorted, de-duplicated mcp__* tool names found across expected_effects
    strings (e.g. "mcp__kitsoki__story_graph: {...} completed" -> "mcp__kitsoki__story_graph")."""
    tools = set()
    for effect in expected_effects or []:
        for m in _MCP_TOOL_RE.finditer(effect):
            tools.add(m.group(1))
    return sorted(tools)


def persona_label(persona_id):
    """"bugfix-contributor" -> "Bugfix Contributor"."""
    return " ".join(w.capitalize() for w in persona_id.replace("_", "-").split("-") if w)


def cluster_personas(scenarios):
    """Groups scenarios by persona id. Returns {persona_id: [scenario, ...]},
    insertion-ordered by first encounter, for deterministic downstream
    iteration when the caller sorts by id anyway."""
    clusters = {}
    for s in scenarios:
        clusters.setdefault(s["persona"], []).append(s)
    return clusters


def compile_personas(scenarios, existing_ids):
    """Returns new persona entries (dicts) for every persona id clustered from
    `scenarios` that is NOT already in `existing_ids`. Deterministic: sorted by
    persona id. Never mutates or returns anything for an existing id — hand-
    authored (or previously-mined) entries always win.
    """
    clusters = cluster_personas(scenarios)
    out = []
    for persona_id in sorted(clusters):
        if persona_id in existing_ids:
            continue
        cluster = clusters[persona_id]
        sample_goals = [s["goal"] for s in cluster[:3]]
        out.append(
            {
                "id": persona_id,
                "label": persona_label(persona_id),
                "description": "Clustered from %d mined session span(s). Sample goal(s): %s"
                % (len(cluster), " | ".join(g[:120] for g in sample_goals)),
                "surface_preference": "unspecified",
                "risk_focus": [],
                "source": "mined",
                "sample_count": len(cluster),
            }
        )
    return out


def compile_scenario_entries(scenarios, existing_ids):
    """Returns new scenarios.json entries (dicts), one per scenario IR
    document whose derived id (`mined-<scenario.id>`) is not already present.
    Deterministic: sorted by the derived id.
    """
    out = []
    for s in sorted(scenarios, key=lambda x: x["id"]):
        entry_id = "mined-%s" % s["id"]
        if entry_id in existing_ids:
            continue
        success_criteria = []
        for turn in s["turns"]:
            if turn.get("corrected") and turn.get("corrective_ops"):
                success_criteria.append(
                    "A correction occurred in the source session (%s); the workbench should not silently re-diverge the same way."
                    % "; ".join(turn["corrective_ops"])
                )
        if s.get("expected_effects"):
            success_criteria.append(
                "Grounded actions from the source session are reproducible: %s" % "; ".join(s["expected_effects"])
            )
        if s.get("abandoned"):
            success_criteria.append(
                "The source session ended unresolved — the workbench must not silently claim success on this goal."
            )
        if not success_criteria:
            success_criteria.append("Persona can restate the goal and the real actions it required.")

        out.append(
            {
                "id": entry_id,
                "label": "Mined: %s" % s["goal"][:80],
                "stage": "mined_from_sessions",
                "task": s["goal"],
                "primary_story": "",
                "required_mcp": extract_mcp_tools(s.get("expected_effects")),
                "evidence": ["session_trace"],
                "success_criteria": success_criteria,
                "source": "mined",
                "provenance": s.get("provenance", {}),
            }
        )
    return out


def merge_into(path, key, new_entries):
    """Loads the JSON document at `path` (must have a top-level list under
    `key`), appends every new_entries item whose `id` is not already present
    (by any existing entry's id), and writes it back. Returns the count
    actually appended (0 on a fully-idempotent rerun). Existing entries are
    never reordered or mutated.
    """
    with open(path, "r", encoding="utf-8") as f:
        doc = json.load(f)
    existing = doc.setdefault(key, [])
    existing_ids = {e["id"] for e in existing if "id" in e}
    appended = 0
    for entry in new_entries:
        if entry["id"] in existing_ids:
            continue
        existing.append(entry)
        existing_ids.add(entry["id"])
        appended += 1
    with open(path, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2)
        f.write("\n")
    return appended


def load_scenarios(scenarios_dir):
    docs = []
    for p in sorted(glob.glob(os.path.join(scenarios_dir, "scn-*.json"))):
        with open(p, "r", encoding="utf-8") as f:
            docs.append(json.load(f))
    return [d for d in docs if d.get("kind") == "conversation"]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--scenarios-dir", required=True, help="directory of scn-*.json scenario IR documents")
    ap.add_argument("--personas-json", required=True, help="path to tools/product-journey/personas.json")
    ap.add_argument("--scenarios-json", required=True, help="path to tools/product-journey/scenarios.json")
    ap.add_argument("--dry-run", action="store_true", help="print what would be appended without writing")
    args = ap.parse_args(argv)

    scenarios = load_scenarios(args.scenarios_dir)
    if not scenarios:
        print("product_journey_compiler: no scenario IR documents found in %s" % args.scenarios_dir, file=sys.stderr)
        return 2

    with open(args.personas_json, "r", encoding="utf-8") as f:
        existing_persona_ids = {p["id"] for p in json.load(f).get("personas", []) if "id" in p}
    with open(args.scenarios_json, "r", encoding="utf-8") as f:
        existing_scenario_ids = {s["id"] for s in json.load(f).get("scenarios", []) if "id" in s}

    new_personas = compile_personas(scenarios, existing_persona_ids)
    new_scenario_entries = compile_scenario_entries(scenarios, existing_scenario_ids)

    if args.dry_run:
        print("would append %d persona(s): %s" % (len(new_personas), [p["id"] for p in new_personas]))
        print("would append %d scenario entrie(s): %s" % (len(new_scenario_entries), [s["id"] for s in new_scenario_entries]))
        return 0

    p_count = merge_into(args.personas_json, "personas", new_personas)
    s_count = merge_into(args.scenarios_json, "scenarios", new_scenario_entries)
    print("product_journey_compiler: appended %d persona(s), %d scenario entrie(s)" % (p_count, s_count))
    return 0


if __name__ == "__main__":
    sys.exit(main())
