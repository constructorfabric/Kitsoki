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


def run_tool_with_args(payload, *args):
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        src = tmp_path / "input.json"
        src.write_text(json.dumps(payload), encoding="utf-8")
        proc = subprocess.run(
            ["python3", str(TOOL), "--kind", "workflow", "--input", str(src), *args],
            cwd=ROOT,
            text=True,
            capture_output=True,
            check=True,
        )
        return json.loads(proc.stdout)


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

    def test_job_output_derives_artifact_run_folder(self):
        with tempfile.TemporaryDirectory() as tmp:
            summary = run_tool_with_args(
                {
                    "title": "Derived output",
                    "objectives": [{"label": "Deck", "status": "done", "detail": "derived path"}],
                    "artifacts": [],
                },
                "--job", "fan out job",
                "--run-id", "2026-06-26T00:00:00Z",
                "--artifact-root", tmp,
            )
            self.assertTrue(summary["spec_path"].endswith("fan-out-job/2026-06-26t00-00-00z/deck.slidey.json"))
            self.assertTrue(Path(summary["spec_path"]).exists())
            self.assertTrue(Path(summary["markdown_path"]).exists())

    def test_job_output_allows_nested_artifact_folders(self):
        with tempfile.TemporaryDirectory() as tmp:
            summary = run_tool_with_args(
                {
                    "title": "Dynamic workflow",
                    "objectives": [{"label": "Receipt", "status": "done", "detail": "generated"}],
                    "artifacts": [],
                },
                "--job", "dynamic-workflows/dwf_123",
                "--run-id", "dwf_123",
                "--artifact-root", tmp,
            )
            self.assertTrue(summary["spec_path"].endswith("dynamic-workflows/dwf-123/dwf-123/deck.slidey.json"))

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

    def test_fix_tests_deck_surfaces_blocked_question(self):
        _, deck = run_tool("fix-tests", {
            "outcome": "blocked",
            "tests_passed": False,
            "cycle": 1,
            "max_cycles": 3,
            "report_path": ".artifacts/fix-tests/run/report.md",
            "test_log": ".artifacts/test-reports/test-1.log",
            "fix_artifact": {
                "summary_title": "Ambiguous intended behaviour",
                "files_changed": [],
                "fixed_tests": [],
                "remaining_failures": ["TestAmbiguous"],
                "open_questions": ["Should Add() round half-up or half-even?"],
                "confidence": 0,
            },
        })
        self.assertEqual(deck["scenes"][0]["subtitle"], "blocked; tests red")
        questions = [scene for scene in deck["scenes"] if scene.get("title") == "Open questions"][0]
        self.assertEqual(questions["items"][0]["status"], "blocked")

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

    def test_dynamic_workflow_deck_uses_receipt_artifacts(self):
        _, deck = run_tool("dynamic-workflow", {
            "workflow_id": "dwf_20260625T170429Z_tui-dwf-test",
            "goal": "implement workflow commands from the TUI",
            "slug": "tui-dwf-test",
            "validation": {"ok": True, "warnings": ["review generated flow"]},
            "launch_command": "kitsoki run .artifacts/dynamic-workflows/dwf/app/app.yaml",
            "manifest_path": ".artifacts/dynamic-workflows/dwf/manifest.yaml",
            "events_path": ".artifacts/dynamic-workflows/dwf/events.jsonl",
            "artifacts": [".artifacts/dynamic-workflows/dwf/app/app.yaml"],
        })
        self.assertEqual(deck["meta"]["title"], "Dynamic Workflow: dwf_20260625T170429Z_tui-dwf-test")
        lifecycle = [scene for scene in deck["scenes"] if scene.get("title") == "Lifecycle"][0]
        self.assertEqual(lifecycle["rows"][3]["cells"], ["Validation", "ok"])
        artifacts = [scene for scene in deck["scenes"] if scene.get("title") == "Generated artifacts"][0]
        self.assertEqual(artifacts["rows"][0]["cells"][0], "app.yaml")

    def test_model_harness_deck_uses_report_artifact(self):
        _, deck = run_tool("model-harness", {
            "question": "Which configured model should route this room?",
            "status": "complete",
            "markdown_path": ".context/model-harness-eval.md",
            "summary_path": ".artifacts/model-harness-eval/summary.json",
            "case_study_path": "docs/case-studies/model-harness-eval.md",
            "configured_options": [
                {"profile": "codex-native", "backend": "codex", "model": "gpt-5-codex", "effort": "medium", "source": "local"},
            ],
            "recommendations": {
                "fastest": {"profile": "codex-native", "model": "gpt-5-codex", "evidence_status": "measured", "rationale": "fast"},
                "cheapest": {"profile": "codex-native", "model": "gpt-5-codex", "evidence_status": "measured", "rationale": "cheap"},
                "best": {"profile": "codex-native", "model": "gpt-5-codex", "evidence_status": "measured", "rationale": "best"},
                "selected": {"profile": "codex-native", "model": "gpt-5-codex", "evidence_status": "measured", "rationale": "selected"},
            },
            "override": {"applied": True, "summary": "Applied local default."},
            "evidence": {"eval_reports": 3, "readiness_gaps": 1},
            "limitations": ["No live benchmark."],
        })
        self.assertEqual(deck["meta"]["title"], "Model Harness Evaluation")
        recommendations = [scene for scene in deck["scenes"] if scene.get("title") == "Recommendations"][0]
        self.assertEqual(recommendations["rows"][3]["cells"][:3], ["selected", "codex-native", "gpt-5-codex"])
        evidence = [scene for scene in deck["scenes"] if scene.get("title") == "Review artifacts"][0]
        labels = [item["label"] for item in evidence["items"]]
        self.assertIn("Report artifact", labels)

    def test_eval_pilot_deck_summarizes_candidates_and_gaps(self):
        _, deck = run_tool("eval-pilot", {
            "datasets": [{"call": "route_gate"}],
            "reports": [{"call": "route_gate"}],
            "candidates": [
                {
                    "call": "route_gate",
                    "profile": "synthetic-codex",
                    "model": "syn:small:text",
                    "pass_rate": 0.5,
                    "comparator_pass_rate": {"median": 0.9},
                    "p95_latency_ms": {"median": 3200},
                    "avg_cost_usd": {"median": 0.001},
                    "meets_declared_bar": False,
                }
            ],
            "coverage": [{"call": "route_gate", "has_report": True, "measured_profiles": ["synthetic-codex"], "missing_profiles": ["codex-native"]}],
            "readiness": {"intent_suites": [{"story": "pilot", "has_report": False}]},
            "failures": [],
        })
        self.assertEqual(deck["meta"]["title"], "Eval Pilot Report")
        matrix = [scene for scene in deck["scenes"] if scene.get("title") == "Candidate matrix"][0]
        self.assertEqual(matrix["rows"][0]["cells"][:3], ["route_gate", "synthetic-codex", "syn:small:text"])

    def test_cost_report_deck_summarizes_savings(self):
        _, deck = run_tool("cost-report", {
            "markdown_path": ".artifacts/cost-report/cost-report.md",
            "stories": [
                {
                    "name": "git-ops",
                    "measured": True,
                    "recorded": False,
                    "story_cost_usd": 0.02,
                    "savings_median_usd": 0.48,
                    "baseline": {
                        "ops_sampled": 7,
                        "median_usd": 0.5,
                        "p90_usd": 0.9,
                        "reprocessing_tokens": 123456,
                    },
                    "intents": [
                        {"operation": "git commit", "n": 4, "median_usd": 0.4, "p90_usd": 0.7, "sessions": 3},
                    ],
                }
            ],
        })
        self.assertEqual(deck["meta"]["title"], "Per-story Cost Report")
        savings = [scene for scene in deck["scenes"] if scene.get("title") == "Per-story savings"][0]
        self.assertEqual(savings["rows"][0]["cells"], ["git-ops", "$0.02", "$0.50", "$0.90", "$0.48", "7", "authored"])
        intents = [scene for scene in deck["scenes"] if scene.get("title") == "Intent distributions"][0]
        self.assertEqual(intents["rows"][0]["cells"][:3], ["git-ops", "git commit", "4"])

    def test_story_qa_deck_summarizes_project_lanes(self):
        _, deck = run_tool("story-qa", {
            "project": "all",
            "catalog": "tools/product-journey/catalog.json",
            "markdown_path": ".artifacts/story-qa/run/report.md",
            "targets": [
                {
                    "id": "gears-rust",
                    "stack": "rust",
                    "run_mode": "external-benchmark",
                    "status": "validated",
                    "notes": "sister project",
                    "verify": {"status": "validated", "detail": "cached", "output": []},
                },
                {
                    "id": "postgresql",
                    "stack": "c",
                    "run_mode": "local-oracle",
                    "status": "validated",
                    "notes": "oracle",
                    "verify": {"status": "blocked", "detail": "missing checkout", "output": []},
                },
            ],
        })
        self.assertEqual(deck["meta"]["title"], "Story QA Report")
        lanes = [scene for scene in deck["scenes"] if scene.get("title") == "Project lanes"][0]
        self.assertEqual(lanes["rows"][0]["cells"][:3], ["gears-rust", "rust", "external-benchmark"])
        status = [scene for scene in deck["scenes"] if scene.get("title") == "QA status"][0]
        self.assertEqual(status["items"][2]["status"], "blocked")

    def test_session_mining_action_deck_summarizes_candidates(self):
        _, deck = run_tool("session-mining-action", {
            "contributors": 3,
            "promote_min_contributors": 2,
            "markdown_path": ".artifacts/session-mining/job/BRIEF.md",
            "summary_path": ".artifacts/session-mining/job/brief.summary.json",
            "patterns": [
                {
                    "id": "fix-failing-tests",
                    "verdict": "BUILD NOW",
                    "determinism_priority": 0.75,
                    "contributors": 2,
                    "occurrences": 8,
                    "pain": "high",
                    "decision_points": ["fix code vs tests"],
                    "example_signatures": ["go test -> edit -> go test"],
                    "ladder_target": "L2",
                }
            ],
            "candidates": [
                {
                    "id": "fix-failing-tests",
                    "verdict": "BUILD NOW",
                    "determinism_priority": 0.75,
                    "decision_points": ["fix code vs tests"],
                    "example_signatures": ["go test -> edit -> go test"],
                    "ladder_target": "L2",
                }
            ],
            "novel_quarantine": [{"id": "visual-qc", "contributors": 1, "occurrences": 2}],
        })
        self.assertEqual(deck["meta"]["title"], "Session-Mining Action Brief")
        candidates = [scene for scene in deck["scenes"] if scene.get("title") == "Build candidates"][0]
        self.assertEqual(candidates["rows"][0]["cells"][:2], ["fix-failing-tests", "BUILD NOW"])
        ranking = [scene for scene in deck["scenes"] if scene.get("title") == "Full ranking"][0]
        self.assertEqual(ranking["rows"][0]["cells"][3], "2/3")

    def test_session_mining_intent_deck_summarizes_recipes(self):
        _, deck = run_tool("session-mining-intent", {
            "job": "intent-job",
            "intents_path": ".artifacts/session-mining/job/intents.json",
            "analysis_path": ".artifacts/session-mining/job/analysis.json",
            "markdown_path": ".artifacts/session-mining/job/BRIEF.md",
            "summary_path": ".artifacts/session-mining/job/intent.summary.json",
            "determinism_counts": {"deterministic": 1, "agent-gated": 1, "irreducible-llm": 0},
            "grounding": {"valid": 3, "cited": 4, "percent": 75},
            "tags": {"action": {"fix": 2}, "surface": {"cli": 1}},
            "clusters": [{"count": 2, "key": "fix failing test"}],
            "intents": [
                {
                    "instance_id": "i1",
                    "determinism": "agent-gated",
                    "tags": {"action": ["fix"], "surface": ["cli"]},
                    "measured": {"tool_calls": 3},
                    "agent_gates": ["code vs test"],
                    "recipe": "go test -> edit",
                }
            ],
        })
        self.assertEqual(deck["meta"]["title"], "Session-Mining Intent Brief")
        recipes = [scene for scene in deck["scenes"] if scene.get("title") == "Intent recipes"][0]
        self.assertEqual(recipes["rows"][0]["cells"][:3], ["i1", "agent-gated", "fix"])
        status = [scene for scene in deck["scenes"] if scene.get("title") == "Intent-mining status"][0]
        self.assertEqual(status["items"][2]["status"], "next")

    def test_session_idea_mining_deck_summarizes_themes(self):
        _, deck = run_tool("session-idea-mining", {
            "title": "Kitsoki ideas mined from chats",
            "headline": "Several recurring workflow gaps are ready to act on.",
            "markdown_path": ".artifacts/session-idea-mining/run/BRIEF.md",
            "summary_path": ".artifacts/session-idea-mining/run/ideas.summary.json",
            "themes": [
                {
                    "theme": "Make decks standard",
                    "priority": "now",
                    "target": "reporting",
                    "categories": ["feature", "design"],
                    "summary": "Every job should emit a deck.",
                    "rationale": "Repeated across sessions.",
                    "supporting_ideas": ["status decks", "rrweb playbacks"],
                    "session_count": 5,
                    "sessions": ["abc123"],
                },
                {"theme": "Later cleanup", "priority": "later", "session_count": 1},
            ],
        })
        self.assertEqual(deck["meta"]["title"], "Kitsoki ideas mined from chats")
        themes = [scene for scene in deck["scenes"] if scene.get("title") == "Ranked themes"][0]
        self.assertEqual(themes["rows"][0]["cells"][:3], ["Make decks standard", "now", "5"])
        status = [scene for scene in deck["scenes"] if scene.get("title") == "Idea-mining status"][0]
        self.assertEqual(status["items"][1]["status"], "next")


if __name__ == "__main__":
    unittest.main()
