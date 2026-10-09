#!/usr/bin/env python3
"""Deterministic tests for Persona QA Slidey deck generation.

Run directly:
  python3 tools/persona_qa/tests/test_deck_cli.py

The test reads committed example run bundles and never records media, opens a
browser, or calls a model.
"""

from __future__ import annotations

import contextlib
import io
import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from tools.persona_qa import kit  # noqa: E402

failures: list[str] = []


def check(label: str, cond: bool, detail: object = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


def check_eq(label: str, got: object, want: object) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def run_cli(argv: list[str]) -> tuple[int, str, str]:
    stdout = io.StringIO()
    stderr = io.StringIO()
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        code = kit.main(argv)
    return code, stdout.getvalue(), stderr.getvalue()


def generate_deck(run_dir: str, out: Path, title: str) -> dict:
    code, stdout, stderr = run_cli([
        "deck",
        "--run-dir",
        run_dir,
        "--out",
        str(out),
        "--title",
        title,
        "--json-output",
    ])
    check_eq(f"{run_dir} deck exit", code, 0)
    if stderr:
        failures.append(f"unexpected stderr for {run_dir}: {stderr}")
    payload = json.loads(stdout)
    check_eq(f"{run_dir} status", payload["status"], "deck_built")
    check("deck was written", out.is_file(), out)
    return payload


EXAMPLES = [
    {
        "run_dir": "tools/persona_qa/examples/runs/kitsoki-product-review",
        "title": "Kitsoki Persona QA Example",
        "golden": "docs/decks/persona-qa-kitsoki-example.slidey.json",
        "run_id": "persona-qa-kitsoki-product-review",
        "playback": 0,
        "expected": "real playback captures are attached",
    },
    {
        "run_dir": "tools/persona_qa/examples/runs/slidey-architect-review",
        "title": "Slidey Architect Review",
        "golden": "docs/decks/persona-qa-slidey-architect-review.slidey.json",
        "run_id": "persona-qa-slidey-architect-review",
        "playback": 2,
        "expected": "Published decks need proof-grade capture provenance",
    },
]


with tempfile.TemporaryDirectory(prefix="persona-qa-deck-") as td:
    temp = Path(td)
    for case in EXAMPLES:
        out_a = temp / (Path(case["golden"]).stem + ".a.slidey.json")
        out_b = temp / (Path(case["golden"]).stem + ".b.slidey.json")
        payload_a = generate_deck(case["run_dir"], out_a, case["title"])
        payload_b = generate_deck(case["run_dir"], out_b, case["title"])
        text_a = out_a.read_text(encoding="utf-8")
        text_b = out_b.read_text(encoding="utf-8")
        golden = (ROOT / case["golden"]).read_text(encoding="utf-8")
        check_eq(f"{case['run_id']} reports run id", payload_a["run_id"], case["run_id"])
        check_eq(f"{case['run_id']} repeat output is byte-stable", text_a, text_b)
        check_eq(f"{case['run_id']} generated output matches golden", text_a, golden)
        check(f"{case['run_id']} has no workspace absolute path", "/Users/" not in text_a)
        check(f"{case['run_id']} contains review text", case["expected"] in text_a)
        deck = json.loads(text_a)
        check_eq(f"{case['run_id']} meta mode", deck["meta"]["mode"], "pitch")
        scene_types = [scene.get("type") for scene in deck["scenes"]]
        check(f"{case['run_id']} uses card/objective/evidence layouts", "objectives" in scene_types and "evidence" in scene_types)
        for scene in deck["scenes"]:
            check(f"{case['run_id']} does not use long frame holds", int(scene.get("hold", 0)) <= 180, scene)
            if scene.get("type") == "narrative":
                body = str(scene.get("body", ""))
                check(f"{case['run_id']} narrative scene is not a wall of text", len(body) <= 360 and body.count("\n") <= 4, scene)
            if scene.get("type") == "evidence":
                check(f"{case['run_id']} evidence scene fits the layout", len(scene.get("items", [])) <= 4, scene)
            if scene.get("type") == "objectives":
                check(f"{case['run_id']} objectives scene fits the layout", len(scene.get("items", [])) <= 5, scene)
            if scene.get("type") == "cards":
                check(f"{case['run_id']} card grid stays scan-friendly", len(scene.get("cards", [])) <= 6, scene)
                for card in scene.get("cards", []):
                    check(f"{case['run_id']} card subcopy is short", len(str(card.get("sub", ""))) <= 72, card)
                    for line in card.get("lines", []):
                        check(f"{case['run_id']} card line is short", len(str(line)) <= 140, card)
        playback = [
            scene
            for scene in deck["scenes"]
            if isinstance(scene, dict) and scene.get("eyebrow") == "Playback evidence"
        ]
        check_eq(f"{case['run_id']} playback scene count", len(playback), case["playback"])
        for scene in playback:
            check(f"{case['run_id']} playback scene uses rrweb", str(scene.get("rrweb", "")).endswith(".rrweb.json"), scene)
            rrweb = ROOT / "docs" / "decks" / str(scene.get("rrweb", ""))
            check(f"{case['run_id']} rrweb playback resolves", rrweb.is_file(), rrweb)
            if rrweb.is_file():
                events = json.loads(rrweb.read_text(encoding="utf-8"))
                if isinstance(events, dict):
                    events = events.get("events", [])
                first_href = ""
                for event in events:
                    if isinstance(event, dict) and event.get("type") == 4:
                        first_href = str(event.get("data", {}).get("href", ""))
                        break
                check(f"{case['run_id']} rrweb playback is not a placeholder", len(events) >= 20 and first_href != "about:blank", scene)

    mp4_run = temp / "mp4-run"
    mp4_run.mkdir()
    (mp4_run / "run.json").write_text(
        json.dumps(
            {
                "run_id": "mp4-run",
                "project": {"id": "demo", "label": "Demo"},
                "persona": {"id": "architect", "label": "Architect"},
                "scenarios": [{"id": "demo-video", "label": "Demo video", "stage": "review"}],
                "stages": [{"id": "review", "status": "captured", "scenarios": ["demo-video"]}],
            },
            sort_keys=True,
        ),
        encoding="utf-8",
    )
    (mp4_run / "media-manifest.json").write_text(
        json.dumps(
            {
                "run_id": "mp4-run",
                "items": [
                    {
                        "scenario": "demo-video",
                        "evidence_kind": "key_interaction_video",
                        "media_kind": "video",
                        "path": "media/walkthrough.mp4",
                        "status": "captured",
                        "source": "local",
                        "capture_provenance": {"kind": "mp4-capture", "tool": "fixture"},
                        "notes": "Recorded MP4 playback.",
                        "playback": True,
                    }
                ],
                "summary": {"total": 1, "playback_items": 1, "video": 1, "image": 0},
            },
            sort_keys=True,
        ),
        encoding="utf-8",
    )
    (mp4_run / "media").mkdir()
    (mp4_run / "media" / "walkthrough.mp4").write_bytes(b"fixture mp4 placeholder for path resolution\n")
    mp4_out = temp / "mp4.slidey.json"
    generate_deck(str(mp4_run), mp4_out, "MP4 Probe")
    mp4_deck = json.loads(mp4_out.read_text(encoding="utf-8"))
    mp4_scene = next(scene for scene in mp4_deck["scenes"] if scene.get("eyebrow") == "Playback evidence")
    check_eq("mp4 playback uses Slidey src field", mp4_scene.get("src"), "media/walkthrough.mp4")
    check("mp4 playback does not use legacy video field", "video" not in mp4_scene, mp4_scene)

    bad_run = temp / "bad-run"
    bad_run.mkdir()
    (bad_run / "media").mkdir()
    bad_events = [
        {"type": 4, "data": {"href": "about:blank", "width": 1280, "height": 800}, "timestamp": 1},
        {"type": 2, "data": {"node": {"type": 0, "childNodes": []}}, "timestamp": 2},
    ]
    (bad_run / "media" / "blank.rrweb.json").write_text(json.dumps(bad_events), encoding="utf-8")
    (bad_run / "run.json").write_text(
        json.dumps(
            {
                "run_id": "bad-run",
                "project": {"id": "demo", "label": "Demo"},
                "persona": {"id": "architect", "label": "Architect"},
                "scenarios": [{"id": "blank-video", "label": "Blank video", "stage": "review"}],
                "stages": [{"id": "review", "status": "captured", "scenarios": ["blank-video"]}],
            },
            sort_keys=True,
        ),
        encoding="utf-8",
    )
    (bad_run / "media-manifest.json").write_text(
        json.dumps(
            {
                "run_id": "bad-run",
                "items": [
                    {
                        "scenario": "blank-video",
                        "evidence_kind": "key_interaction_video",
                        "media_kind": "video",
                        "path": "media/blank.rrweb.json",
                        "status": "captured",
                        "source": "local",
                        "notes": "This incorrectly claims local playback evidence.",
                        "playback": True,
                    }
                ],
                "summary": {"total": 1, "playback_items": 1, "video": 1, "image": 0},
            },
            sort_keys=True,
        ),
        encoding="utf-8",
    )
    bad_out = temp / "bad.slidey.json"
    code, stdout, stderr = run_cli([
        "deck",
        "--run-dir",
        str(bad_run),
        "--out",
        str(bad_out),
        "--title",
        "Bad Probe",
        "--json-output",
    ])
    check_eq("unproven local playback is blocked", code, 2)
    check_eq("unproven playback status", json.loads(stdout)["status"], "blocked")
    check("unproven playback reports proof-grade reason", "not proof-grade" in stdout or "missing capture_provenance" in stdout, stdout)
    check("unproven deck is not written", not bad_out.exists(), bad_out)

if failures:
    print("FAIL: persona-qa deck CLI")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: persona-qa deck CLI")
