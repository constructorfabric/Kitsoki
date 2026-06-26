import json
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
TOOL = ROOT / "tools" / "report-deck" / "deterministic_deck.py"


def run_tool(kind, payload):
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        src = tmp_path / "input.json"
        out = tmp_path / "deck.slidey.json"
        src.write_text(json.dumps(payload), encoding="utf-8")
        proc = subprocess.run(
            ["python3", str(TOOL), "--kind", kind, "--input", str(src), "--out", str(out)],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=True,
        )
        return json.loads(proc.stdout), json.loads(out.read_text(encoding="utf-8"))


class DeterministicDeckTest(unittest.TestCase):
    def test_bakeoff_deck_uses_summary_numbers(self):
        summary, deck = run_tool("bakeoff-summary", {
            "manifest": "tools/bugfix-bakeoff/bakeoff.yaml",
            "bugs": [{"id": "bug1"}],
            "cells": [
                {
                    "bug": "bug1",
                    "candidate": "opus",
                    "treatment": "kitsoki",
                    "outcome": {"quality": "solved"},
                    "metrics": {"cost_usd": 1.25, "guidance_turns": 0, "total_tokens": 100},
                }
            ],
            "rollup": {
                "by_treatment": {"kitsoki": {"n": 1, "solved": 1, "solve_rate": 1, "avg_cost_usd": 1.25, "avg_guidance_turns": 0}},
                "by_candidate": {"opus": {"n": 1, "solved": 1, "solve_rate": 1, "avg_cost_usd": 1.25, "avg_total_tokens": 100}},
            },
        })
        self.assertTrue(summary["spec_path"].endswith("deck.slidey.json"))
        self.assertEqual(deck["meta"]["title"], "Bugfix Bake-off")
        self.assertTrue(deck["scenes"][0]["subtitle"].startswith("1 scored cells, 1 solved"))
        self.assertEqual(deck["scenes"][2]["rows"][0]["cells"], ["kitsoki", "1", "1", "1", "$1.25", "0"])

    def test_onboarding_deck_links_applied_artifacts(self):
        _, deck = run_tool("onboarding", {
            "project_title": "Slidey",
            "target_path": "/tmp/slidey",
            "stack": "node",
            "test_command": "npm test",
            "apply_result": {
                "config_path": "/tmp/slidey/.kitsoki.yaml",
                "profile_path": "/tmp/slidey/.kitsoki/project-profile.yaml",
                "instance_path": "/tmp/slidey/.kitsoki/stories/slidey-dev/app.yaml",
                "writes": ["/tmp/slidey/.kitsoki.yaml"],
            },
        })
        evidence = deck["scenes"][2]["items"]
        refs = {item["label"]: item["ref"] for item in evidence}
        self.assertEqual(refs["Config"], "/tmp/slidey/.kitsoki.yaml")
        self.assertEqual(refs["Profile"], "/tmp/slidey/.kitsoki/project-profile.yaml")

    def test_workflow_deck_embeds_rrweb_media(self):
        _, deck = run_tool("workflow", {
            "title": "Hybrid workflow",
            "objectives": [{"label": "Demo", "status": "done", "detail": "Captured replay."}],
            "artifacts": [{"label": "Trace", "status": "done", "ref": ".artifacts/run.jsonl"}],
            "media": [{"title": "Replay", "rrweb": "clips/replay.rrweb.json"}],
        })
        videos = [scene for scene in deck["scenes"] if scene["type"] == "video"]
        self.assertEqual(videos[0]["rrweb"], "clips/replay.rrweb.json")
        self.assertEqual(videos[0]["chapters"], "auto")

    def test_feature_demo_deck_uses_personas_and_rrweb(self):
        _, deck = run_tool("feature-demo", {
            "title": "Speaker notes export",
            "personas": [{"id": "pm", "name": "Priya", "role": "PM"}],
            "phases": [{"who": "pm", "action": "Reviews PRD", "detail": "validated"}],
            "media": [{"title": "PM walkthrough", "rrweb": "assets/pm.rrweb.json"}],
        })
        self.assertEqual(deck["meta"]["personas"][0]["id"], "pm")
        self.assertEqual(deck["scenes"][2]["type"], "personas")
        videos = [scene for scene in deck["scenes"] if scene["type"] == "video"]
        self.assertEqual(videos[0]["rrweb"], "assets/pm.rrweb.json")

    def test_bug_report_deck_includes_reproducer_and_playback(self):
        _, deck = run_tool("bug-report", {
            "title": "Bug 128",
            "summary": "Cards drift after item six.",
            "reproducer": {"command": "node --test test/timing.test.js", "expected": "pass", "actual": "fail"},
            "evidence": [{"label": "Trace", "status": "done", "ref": ".artifacts/bug128.trace.jsonl"}],
            "media": [{"title": "Playback", "rrweb": "bugs/128.rrweb.json"}],
        })
        tables = [scene for scene in deck["scenes"] if scene.get("title") == "Reproducer"]
        self.assertEqual(tables[0]["rows"][0]["cells"][1], "node --test test/timing.test.js")
        videos = [scene for scene in deck["scenes"] if scene["type"] == "video"]
        self.assertEqual(videos[0]["eyebrow"], "Bug playback")

    def test_fanout_deck_counts_success_fail_retry(self):
        _, deck = run_tool("fanout", {
            "title": "Fan-out run",
            "items": [
                {"id": "a", "status": "succeeded", "attempts": 1, "trace": ".artifacts/a.jsonl"},
                {"id": "b", "status": "failed", "attempts": 2, "trace": ".artifacts/b.jsonl"},
                {"id": "c", "status": "retried", "attempts": 3, "trace": ".artifacts/c.jsonl"},
            ],
        })
        self.assertEqual(deck["scenes"][0]["subtitle"], "1 succeeded, 1 failed, 1 retried")
        table = [scene for scene in deck["scenes"] if scene.get("title") == "Fan-out items"][0]
        self.assertEqual(table["rows"][1]["cells"][:3], ["b", "failed", "2"])

    def test_product_journey_deck_preserves_reference_deck(self):
        _, deck = run_tool("product-journey", {
            "program": "Product journey evaluator",
            "reference_deck": "docs/decks/product-journey-eval.slidey.json",
            "catalog": "tools/product-journey/catalog.json",
            "run_log": ".context/product-journey-runlog.md",
            "targets": [
                {"id": "gears-rust", "stack": "rust", "status": "validated", "run_mode": "external-benchmark", "manifest": "manifest.yaml"},
                {"id": "postgresql", "stack": "c", "status": "validated", "run_mode": "local-oracle", "validation_command": "bash check.sh"},
            ],
            "perspectives": [{"id": "postgresql", "owner": "sre", "status": "validated", "description": "docs + issue corpus"}],
        })
        evidence = [scene for scene in deck["scenes"] if scene.get("title") == "Review artifacts"][0]
        refs = {item["label"]: item["ref"] for item in evidence["items"]}
        self.assertEqual(refs["Reference deck"], "docs/decks/product-journey-eval.slidey.json")
        target_table = [scene for scene in deck["scenes"] if scene.get("title") == "Target lanes"][0]
        self.assertEqual(target_table["rows"][0]["cells"][:3], ["gears-rust", "rust", "validated"])


if __name__ == "__main__":
    unittest.main()
