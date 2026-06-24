#!/usr/bin/env python3
"""lint_decomposition.py — deterministic validation of a decomposition YAML.

Usage: python3 stories/deliver/scripts/lint_decomposition.py <decomposition_path>

Stdout JSON: { "route": "ok"|"fail", "error": "<specific message on fail>" }
Exit code: always 0 (lint logic is expressed in the JSON, not the exit code;
the host.run caller reads stdout_json.route / stdout_json.error).

Checks (in order):
  1. At least one brief in the `briefs:` list.
  2. Every brief has a non-empty `id`.
  3. No duplicate IDs.
  4. Every brief has a non-empty `brief` (or `agent_brief`) field.
  5. Every brief has a non-empty `gate_command` (or `test_plan`) field.
  6. Every dep in `deps` (or `depends_on`) references an existing ID.
  7. No dependency cycle.
"""
import sys
import json


def main():
    if len(sys.argv) < 2:
        _fail("usage: lint_decomposition.py <path>")
        return

    path = sys.argv[1]
    try:
        with open(path) as f:
            content = f.read()
    except Exception as e:
        _fail(f"cannot read '{path}': {e}")
        return

    # Parse YAML (preferred) or fall back to JSON.
    data = None
    try:
        import yaml  # type: ignore
        data = yaml.safe_load(content)
    except ImportError:
        pass
    except Exception as e:
        _fail(f"YAML parse error in '{path}': {e}")
        return

    if data is None:
        try:
            data = json.loads(content)
        except Exception as e:
            _fail(f"cannot parse '{path}' as YAML or JSON: {e}")
            return

    if not isinstance(data, dict):
        _fail(f"decomposition file must be a YAML/JSON object, got {type(data).__name__}")
        return

    briefs = data.get("briefs") or []

    # Rule 1: at least one brief.
    if not briefs:
        _fail("decomposition has no briefs (top-level 'briefs' list is empty or missing)")
        return

    # Collect IDs and check uniqueness / non-empty (rules 2 & 3).
    ids: dict[str, int] = {}
    for i, b in enumerate(briefs):
        if not isinstance(b, dict):
            _fail(f"brief at index {i} is not an object")
            return
        bid = (b.get("id") or "").strip()
        if not bid:
            _fail(f"brief at index {i} has empty or missing 'id'")
            return
        if bid in ids:
            _fail(f"duplicate brief id '{bid}' (index {i} duplicates index {ids[bid]})")
            return
        ids[bid] = i

    # Validate each brief (rules 4, 5, 6).
    for b in briefs:
        bid = b["id"]

        # Rule 4: non-empty brief text.
        brief_text = (b.get("brief") or b.get("agent_brief") or "").strip()
        if not brief_text:
            _fail(f"brief '{bid}' has empty 'brief' (and empty 'agent_brief') field")
            return

        # Rule 5: non-empty gate_command.
        gate = (b.get("gate_command") or b.get("test_plan") or "").strip()
        if not gate:
            _fail(f"brief '{bid}' has empty 'gate_command' (and empty 'test_plan') field")
            return

        # Rule 6: every dep is a known ID.
        deps = b.get("deps") or b.get("depends_on") or []
        for dep in deps:
            if dep not in ids:
                _fail(f"brief '{bid}' has dep '{dep}' which is not a known brief id")
                return

    # Rule 7: no dependency cycle (iterative DFS).
    adj: dict[str, list[str]] = {}
    for b in briefs:
        adj[b["id"]] = list(b.get("deps") or b.get("depends_on") or [])

    visited: set[str] = set()
    rec_stack: set[str] = set()

    def has_cycle(node: str) -> bool:
        visited.add(node)
        rec_stack.add(node)
        for dep in adj.get(node, []):
            if dep not in visited:
                if has_cycle(dep):
                    return True
            elif dep in rec_stack:
                return True
        rec_stack.discard(node)
        return False

    for bid in list(ids.keys()):
        if bid not in visited:
            if has_cycle(bid):
                _fail(f"dependency cycle detected involving '{bid}'")
                return

    _ok()


def _ok() -> None:
    print(json.dumps({"route": "ok", "error": ""}))


def _fail(msg: str) -> None:
    print(json.dumps({"route": "fail", "error": msg}))


if __name__ == "__main__":
    main()
