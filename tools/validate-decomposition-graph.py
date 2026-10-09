#!/usr/bin/env python3
"""Validate a Kitsoki decomposition graph or deliver brief manifest.

The schema pins the shared node shape. This script adds deterministic graph
checks that JSON Schema cannot express: unique ids, dangling deps, dependency
cycles, known gate classes, and repo-bounded scope paths.

Usage:
    python3 tools/validate-decomposition-graph.py <path> [<path> ...]
"""

import argparse
import json
import os
import sys
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - the repo test env has PyYAML.
    yaml = None


REPO_ROOT = Path(__file__).resolve().parents[1]
SCHEMA_PATH = REPO_ROOT / "docs" / "schemas" / "decomposition-graph.schema.json"
GLOB_CHARS = "*?["
KNOWN_GATE_CLASSES = {
    "G-ARENA",
    "G-FLOW",
    "G-GOTEST",
    "G-LINT",
    "G-LIVE+REPLAY",
    "G-MANUAL",
    "G-NODE",
    "G-PYTEST",
    "G-SCRIPT",
    "G-SITE",
    "G-SMOKE",
    "G-TRACE",
}


def load_doc(path: Path):
    text = path.read_text(encoding="utf-8")
    if path.suffix in {".yaml", ".yml"}:
        if yaml is None:
            raise RuntimeError("PyYAML is not installed; cannot read YAML")
        return yaml.safe_load(text)
    return json.loads(text)


def schema_errors(doc):
    try:
        import jsonschema
    except ImportError:
        return ["jsonschema not installed; cannot run shape validation"]
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = jsonschema.Draft7Validator(schema)
    out = []
    for err in sorted(validator.iter_errors(doc), key=lambda e: list(e.path)):
        loc = "/".join(str(p) for p in err.path) or "(root)"
        out.append(f"schema {loc}: {err.message}")
    return out


def nodes_for(doc):
    if not isinstance(doc, dict):
        return [], "document is not an object"
    if "changes" in doc:
        return doc.get("changes") or [], "changes"
    if "briefs" in doc:
        return doc.get("briefs") or [], "briefs"
    return [], "document has neither changes nor briefs"


def deps_for(node):
    deps = node.get("depends_on")
    if deps is None:
        deps = node.get("deps")
    return deps or []


def literal_prefix(pattern):
    kept = []
    for part in str(pattern).split("/"):
        if any(ch in part for ch in GLOB_CHARS):
            break
        kept.append(part)
    return "/".join(kept)


def scope_is_bounded(pattern):
    prefix = literal_prefix(pattern)
    target = (REPO_ROOT / prefix).resolve() if prefix else REPO_ROOT.resolve()
    try:
        return os.path.commonpath([str(REPO_ROOT.resolve()), str(target)]) == str(REPO_ROOT.resolve())
    except ValueError:
        return False


def graph_errors(doc, path: Path):
    errors = []
    nodes, kind = nodes_for(doc)
    if isinstance(nodes, str):
        return [nodes]
    if not isinstance(nodes, list):
        return [f"{kind}: expected list"]

    ids = []
    seen = {}
    for idx, node in enumerate(nodes):
        if not isinstance(node, dict):
            errors.append(f"{kind}[{idx}] is not an object")
            continue
        nid = str(node.get("id", "")).strip()
        if not nid:
            errors.append(f"{kind}[{idx}] missing id")
            continue
        if nid in seen:
            errors.append(f"duplicate id {nid!r} at {kind}[{idx}] and {kind}[{seen[nid]}]")
        seen[nid] = idx
        ids.append(nid)

    idset = set(ids)
    indeg = {nid: 0 for nid in idset}
    adj = {nid: [] for nid in idset}
    for node in nodes:
        if not isinstance(node, dict):
            continue
        nid = str(node.get("id", "")).strip()
        if not nid:
            continue
        for dep in deps_for(node):
            dep = str(dep)
            if dep not in idset:
                errors.append(f"{nid}: depends_on unknown id {dep!r}")
                continue
            adj[dep].append(nid)
            indeg[nid] += 1

        gate = node.get("gate") or {}
        gclass = str(gate.get("class", "")).strip()
        if gclass and gclass not in KNOWN_GATE_CLASSES:
            errors.append(f"{nid}: unknown gate.class {gclass!r}")
        if not str(gate.get("red_now", "")).strip():
            errors.append(f"{nid}: gate.red_now is required for RED-first traceability")
        if not str(gate.get("green_when", "")).strip():
            errors.append(f"{nid}: gate.green_when is required")
        if not str(gate.get("cmd") or gate.get("replay_cmd") or "").strip():
            errors.append(f"{nid}: gate requires cmd or replay_cmd")

        for scope in node.get("scope") or []:
            if not scope_is_bounded(scope):
                errors.append(f"{nid}: scope {scope!r} escapes the repo root")

    queue = [nid for nid, degree in indeg.items() if degree == 0]
    visited = 0
    while queue:
        nid = queue.pop()
        visited += 1
        for child in adj[nid]:
            indeg[child] -= 1
            if indeg[child] == 0:
                queue.append(child)
    if visited < len(idset):
        stuck = ", ".join(sorted(nid for nid, degree in indeg.items() if degree > 0))
        errors.append(f"dependency cycle among ids: {stuck}")

    if kind == "briefs":
        for node in nodes:
            if not isinstance(node, dict):
                continue
            nid = node.get("id", "?")
            deliver_deps = [str(d) for d in node.get("deps") or []]
            graph_deps = [str(d) for d in node.get("depends_on") or []]
            if deliver_deps != graph_deps:
                errors.append(f"{nid}: deps and depends_on must match in deliver compatibility fixtures")
            if str(node.get("gate_command", "")).strip() != str((node.get("gate") or {}).get("cmd", "")).strip():
                errors.append(f"{nid}: gate_command must match gate.cmd in deliver compatibility fixtures")

    return errors


def validate(path: Path):
    doc = load_doc(path)
    errors = schema_errors(doc)
    if not errors:
        errors.extend(graph_errors(doc, path))
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="+")
    parser.add_argument("--json", action="store_true", help="emit machine-readable results")
    args = parser.parse_args()

    results = {}
    ok = True
    for raw in args.paths:
        path = Path(raw)
        errors = validate(path)
        results[str(path)] = errors
        ok = ok and not errors

    if args.json:
        print(json.dumps({"ok": ok, "results": results}, indent=2, sort_keys=True))
    else:
        for path, errors in results.items():
            if not errors:
                print(f"OK {path}")
            else:
                print(f"FAIL {path}")
                for err in errors:
                    print(f"  - {err}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
