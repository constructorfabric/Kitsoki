#!/usr/bin/env python3
"""Load and lint a punch-list/v1 manifest."""

from __future__ import annotations

import json
import sys
from pathlib import Path

from punch_lib import default_run_id, default_state_path, load_yaml_or_json, normalize_manifest, write_state


ROOT = Path(__file__).resolve().parents[3]


def _tokens(s: str) -> set[str]:
    out: set[str] = set()
    cur = []
    for ch in s.lower():
        if ch.isalnum():
            cur.append(ch)
            continue
        if cur:
            out.add("".join(cur))
            cur = []
    if cur:
        out.add("".join(cur))
    joined = "".join(sorted(out))
    if "top10" in joined or ("top" in out and "10" in out):
        out.add("top10")
    if "gpt55" in joined or ("gpt" in out and "5" in out):
        out.add("gpt55")
    return out


def resolve_manifest_path(raw: str) -> str:
    candidates = [Path(raw)]
    if raw and not Path(raw).is_absolute():
        candidates.append(ROOT / raw)
        candidates.append(ROOT / "stories/punch-list/testdata" / raw)
    for p in candidates:
        if p.exists():
            if p.is_absolute() and p.is_relative_to(ROOT):
                return str(p.relative_to(ROOT))
            return str(p)

    wanted = _tokens(raw)
    if not wanted:
        return raw
    testdata = ROOT / "stories/punch-list/testdata"
    matches: list[tuple[int, Path]] = []
    for p in sorted(testdata.glob("*.yaml")):
        have = _tokens(p.stem)
        score = len(wanted & have)
        if score:
            matches.append((score, p))
    if not matches:
        return raw
    matches.sort(key=lambda it: (-it[0], str(it[1])))
    if len(matches) > 1 and matches[0][0] == matches[1][0]:
        return raw
    return str(matches[0][1].relative_to(ROOT))


def main() -> None:
    if len(sys.argv) < 2 or not sys.argv[1]:
        print(json.dumps({"items": [], "item_count": "0", "state_path": "", "error": "manifest_path is required"}))
        return
    manifest_path = resolve_manifest_path(sys.argv[1])
    state_path = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else default_state_path(manifest_path)

    try:
        doc = load_yaml_or_json(manifest_path)
        run_id = default_run_id()
        items, errors, defaults = normalize_manifest(doc, run_id=run_id)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"items": [], "item_count": "0", "manifest_path": manifest_path, "state_path": state_path, "error": f"parse failed: {exc}"}))
        return

    state = {
        "manifest_path": manifest_path,
        "defaults": defaults,
        "run_id": run_id,
        "items": items,
        "results": {"items": []},
        "error": "\n".join(errors),
    }
    try:
        write_state(state_path, state)
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"items": items, "item_count": str(len(items)), "manifest_path": manifest_path, "state_path": state_path, "error": f"state write failed: {exc}"}))
        return

    print(json.dumps({"items": items, "item_count": str(len(items)), "manifest_path": manifest_path, "state_path": state_path, "error": "\n".join(errors)}))


if __name__ == "__main__":
    main()
