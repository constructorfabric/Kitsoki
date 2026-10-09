#!/usr/bin/env python3
"""No-LLM exact-import gate for the strict MCP calibration corpus."""

import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / "corpus" / "mcp-os"
lock = json.loads((ROOT / "strict-corpus.lock.json").read_text(encoding="utf-8"))
assert lock["schema_version"] == "mcp_os_strict_corpus_lock/v1"
cards = json.loads((ROOT / "strict-calibration-cards.json").read_text(encoding="utf-8"))
cells = json.loads((ROOT / "cells.json").read_text(encoding="utf-8"))
assert len(cards["cards"]) == lock["case_count"] == len(cells["cells"]) == 12
assert [card["id"] for card in cards["cards"]] == [cell["case_id"] for cell in cells["cells"]]
for relative, expected in lock["files"].items():
    path = ROOT / relative
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    assert actual == expected, f"strict MCP corpus drift: {relative}"
print("PASS: exact strict-MCP corpus lock (12 cards, no LLM)")
