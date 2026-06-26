#!/usr/bin/env python3
"""Tests for focus_brief.py artifact outputs. No LLM, no network."""

import json
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
TOOL = ROOT / "tools" / "session-mining" / "focus_brief.py"


def run():
    payload = {
        "result": {
            "headline": "Decks should become the review surface.",
            "themes": [
                {
                    "theme": "Standardize decks",
                    "priority": "now",
                    "target": "reporting",
                    "categories": ["feature", "design"],
                    "summary": "Jobs need deterministic deck artifacts.",
                    "rationale": "Repeated across sessions.",
                    "supporting_ideas": ["fan-out status", "bug playback"],
                    "session_count": 4,
                    "sessions": ["session-a", "session-b"],
                },
                {
                    "theme": "Small cleanup",
                    "priority": "later",
                    "summary": "A lower-priority theme.",
                    "session_count": 1,
                },
            ],
        }
    }
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        src = tmp_path / "synthesis.json"
        md = tmp_path / "ideas.md"
        summary = tmp_path / "ideas.summary.json"
        deck = tmp_path / "deck.slidey.json"
        src.write_text(json.dumps(payload), encoding="utf-8")
        proc = subprocess.run(
            [
                sys.executable,
                str(TOOL),
                str(src),
                "--title", "Focused ideas",
                "--subtitle", "2 themes",
                "--markdown", str(md),
                "--summary", str(summary),
                "--slidey-spec", str(deck),
            ],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=True,
        )
        if proc.stdout:
            raise AssertionError("focus_brief.py should not write stdout when --markdown is set")
        if "# Focused ideas" not in md.read_text(encoding="utf-8"):
            raise AssertionError("markdown title missing")
        summary_json = json.loads(summary.read_text(encoding="utf-8"))
        if summary_json["themes"][0]["theme"] != "Standardize decks":
            raise AssertionError("theme sorting or summary failed")
        if summary_json["themes"][0]["supporting_ideas"] != ["fan-out status", "bug playback"]:
            raise AssertionError("supporting ideas missing")
        deck_json = json.loads(deck.read_text(encoding="utf-8"))
        if deck_json["meta"]["title"] != "Focused ideas":
            raise AssertionError("deck title mismatch")
    print("PASS: focus brief renders markdown, summary JSON, and deterministic Slidey deck")
    return 0


if __name__ == "__main__":
    sys.exit(run())
