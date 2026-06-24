#!/usr/bin/env python3
"""Tests for eval_pilot_report.py. No LLM, no network."""

import json
import os
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.dirname(HERE)
sys.path.insert(0, TOOL)

import eval_pilot_report as epr


def write(path, content):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(content)


def report(path, candidates, failures=None):
    write(path, json.dumps({
        "kind": "agent_eval_report",
        "eval": "route_gate",
        "app": "../app.yaml",
        "call": "route_gate",
        "generated_at": "2026-06-22T00:00:00Z",
        "adherence_bar": {"min_pass_rate": 0.95},
        "candidates": candidates,
        "failure_samples": failures or [],
    }))


def run():
    failures = []

    def check(cond, msg):
        if not cond:
            failures.append(msg)

    with tempfile.TemporaryDirectory() as d:
        eval_yaml = os.path.join(d, "stories", "pilot", "evals", "route_gate.yaml")
        write(eval_yaml, """
kind: agent_eval
app: ../app.yaml
call: route_gate
matrix:
  profiles: [claude, codex-native, synthetic-codex]
  repeat: 5
task:
  adherence_bar:
    min_pass_rate: 0.95
    max_p95_latency_ms: 8000
    max_avg_cost_usd: 0.002
""")
        # A non-agent_eval YAML living in an evals/ dir must be ignored.
        write(os.path.join(d, "stories", "pilot", "evals", "notes.yaml"), "kind: notes\nfoo: bar\n")
        write(os.path.join(d, "stories", "pilot", "intents", "route.yaml"), """
test_kind: intents
fixtures:
  - id: one
    inputs: ["go"]
""")
        write(os.path.join(d, "stories", "needs-report", "intents", "route.yaml"), """
test_kind: intents
fixtures:
  - id: missing
    inputs: ["go"]
""")
        write(os.path.join(d, "stories", "pilot", "mining.profile.yaml"), "scope: {}\n")
        write(os.path.join(d, "stories", "needs-coverage", "mining.profile.yaml"), "scope: {}\n")
        base = os.path.join(d, "stories", "pilot", "evals", "reports", "route_gate")
        report(os.path.join(base, "a.json"), [
            {
                "profile": "synthetic-codex",
                "backend": "codex",
                "provider": "synthetic.new",
                "model": "syn:small:text",
                "effort": "low",
                "pass": True,
                "schema_valid_rate": 1.0,
                "comparator_pass_rate": 1.0,
                "contract_conformance_rate": 1.0,
                "p95_latency_ms": 3000,
                "avg_cost_usd": 0.001,
                "examples_run": 10,
                "example_results": [
                    {"name": "ok-high", "expect": {"intent": "accept"}, "actual": {"intent": "accept", "confidence": 0.95}},
                    {"name": "bad-mid", "expect": {"intent": "refine"}, "actual": {"intent": "accept", "confidence": 0.70}},
                    {"name": "ok-low", "expect": {"intent": "accept"}, "actual": {"intent": "accept", "confidence": 0.60}},
                ],
            },
            {
                "profile": "claude",
                "backend": "claude",
                "provider": "anthropic",
                "model": "sonnet",
                "effort": "medium",
                "pass": True,
                "schema_valid_rate": 1.0,
                "comparator_pass_rate": 1.0,
                "contract_conformance_rate": 1.0,
                "p95_latency_ms": 5000,
                "avg_cost_usd": 0.004,
                "examples_run": 10,
            },
        ])
        report(os.path.join(base, "b.json"), [
            {
                "profile": "synthetic-codex",
                "backend": "codex",
                "provider": "synthetic.new",
                "model": "syn:small:text",
                "effort": "low",
                "pass": False,
                "schema_valid_rate": 1.0,
                "comparator_pass_rate": 0.8,
                "contract_conformance_rate": 1.0,
                "p95_latency_ms": 3400,
                "avg_cost_usd": 0.0012,
                "examples_run": 10,
            },
        ], failures=[{
            "example": "ambiguous-route",
            "profile": "synthetic-codex",
            "model": "syn:small:text",
            "reason": "intent mismatch",
        }])
        intent_path = os.path.join(d, "intent-reports", "pilot.json")
        write(intent_path, json.dumps({
            "Fixtures": [
                {
                    "ID": "ok-route",
                    "State": "idle",
                    "MinPassRate": 0.8,
                    "Inputs": [{"Input": "go", "Runs": 5, "Passed": 5, "PassRate": 1.0}],
                    "TotalRuns": 5,
                    "TotalPassed": 5,
                    "PassRate": 1.0,
                    "Passed": True,
                },
                {
                    "ID": "bad-route",
                    "State": "idle",
                    "MinPassRate": 0.8,
                    "Inputs": [{"Input": "hmm", "Runs": 5, "Passed": 0, "PassRate": 0.0}],
                    "TotalRuns": 5,
                    "TotalPassed": 0,
                    "PassRate": 0.0,
                    "Passed": False,
                },
                {
                    "ID": "skipped-static",
                    "State": "review",
                    "MinPassRate": 0.8,
                    "Inputs": [{"Input": "semantic only", "Runs": 0, "Passed": 0, "PassRate": 1.0}],
                    "TotalRuns": 0,
                    "TotalPassed": 0,
                    "PassRate": 1.0,
                    "Passed": True,
                },
            ]
        }))
        coverage_dir = os.path.join(d, "coverage", "git-ops")
        write(os.path.join(coverage_dir, "intents.json"), json.dumps({
            "job": "gitops-flagship",
            "total_intents": 2,
        }))
        write(os.path.join(coverage_dir, "analysis.json"), json.dumps({
            "instances": [
                {
                    "determinism": "deterministic",
                    "grounding": {"actions_validated": 2, "actions_cited": 2},
                    "satisfaction": {"corrected": False},
                },
                {
                    "determinism": "agent-gated",
                    "grounding": {"actions_validated": 1, "actions_cited": 2},
                    "satisfaction": {"corrected": True},
                },
            ],
            "clusters": [{}, {}],
        }))
        write(os.path.join(coverage_dir, "intents.git.json"), json.dumps({
            "intents": [{}, {}],
            "groups": [{}],
            "out_of_scope": [],
        }))
        write(os.path.join(coverage_dir, "coverage.md"), "# coverage")

        datasets = epr.find_datasets(os.path.join(d, "stories"))
        reports = epr.find_reports(os.path.join(d, "stories"))
        summary = epr.summarize(datasets, reports)
        intent_summaries = epr.summarize_intent_reports(epr.find_intent_reports(os.path.join(d, "intent-reports")))
        coverage_summaries = epr.summarize_coverage_jobs(epr.find_coverage_jobs(os.path.join(d, "coverage")))
        readiness = epr.summarize_readiness(
            epr.find_intent_fixture_suites(os.path.join(d, "stories")),
            intent_summaries,
            epr.find_mining_profiles(os.path.join(d, "stories")),
            coverage_summaries,
        )

        check(len(datasets) == 1, "one dataset discovered (non-agent_eval yaml ignored)")
        ds0 = datasets[0]
        check(ds0["min_pass_rate"] == 0.95, "declared min_pass_rate parsed")
        check(ds0["max_p95_latency_ms"] == 8000, "declared max_p95_latency_ms parsed")
        check(ds0["max_avg_cost_usd"] == 0.002, "declared max_avg_cost_usd parsed")
        check(epr.inline_list("profiles: [a, b, c]\n", "profiles") == ["a", "b", "c"], "inline list parsed")
        block_text = "matrix:\n  profiles:\n    - a\n    - b\n  repeat: 3\n"
        check(epr.inline_list(block_text, "profiles") == ["a", "b"], "block-style list parsed")
        check(epr.inline_list("models:\n  - x  # note\n", "models") == ["x"], "block item trailing comment stripped")
        check(len(reports) == 2, "two reports discovered")
        check(len(summary["candidates"]) == 2, "two aggregate candidates")
        syn = next(c for c in summary["candidates"] if c["profile"] == "synthetic-codex")
        check(abs(syn["pass_rate"] - 0.5) < 0.0001, "pass observation rate aggregated")
        check(abs(syn["comparator_pass_rate"]["median"] - 0.9) < 0.0001, "median comparator is interpolated")
        check(syn["examples_run"] == 20, "examples summed")
        # synthetic-codex passes its cost/latency ceilings but its 0.5 pass rate
        # is below the declared 0.95 bar — and the report still marked a run pass.
        check(syn["meets_declared_bar"] is False, "synthetic violates declared bar")
        check(syn["bar_divergence"] is True, "synthetic bar divergence flagged")
        check(any("pass rate" in v for v in syn["bar_violations"]), "pass-rate violation reported")
        claude = next(c for c in summary["candidates"] if c["profile"] == "claude")
        # claude meets pass-rate/latency but its $0.004 avg cost exceeds the $0.002 ceiling.
        check(claude["meets_declared_bar"] is False, "claude violates declared cost bar")
        check(any("avg cost" in v for v in claude["bar_violations"]), "cost violation reported")
        check(summary["confidence_rows"], "confidence rows extracted")
        sweep_065 = next(r for r in summary["confidence_sweeps"] if r["profile"] == "synthetic-codex" and abs(r["threshold"] - 0.65) < 0.0001)
        check(sweep_065["accepted"] == 2, "threshold sweep accepted count")
        check(sweep_065["false_accepts"] == 1, "threshold sweep false accepts count")
        sweep_090 = next(r for r in summary["confidence_sweeps"] if r["profile"] == "synthetic-codex" and abs(r["threshold"] - 0.90) < 0.0001)
        check(sweep_090["accepted"] == 1 and sweep_090["false_accepts"] == 0, "higher threshold reduces false accepts")
        sweep_100 = next(r for r in summary["confidence_sweeps"] if r["profile"] == "synthetic-codex" and abs(r["threshold"] - 1.00) < 0.0001)
        check(sweep_100["accepted"] == 0 and sweep_100["precision"] is None, "precision is None when nothing is accepted")
        cov = summary["coverage"][0]
        check(cov["measured_profiles"] == ["claude", "synthetic-codex"], "measured profiles collected")
        check(cov["missing_profiles"] == ["codex-native"], "missing profile reported")
        check(len(intent_summaries) == 1, "one intent report summarized")
        check(intent_summaries[0]["fixtures"] == 3, "intent fixtures counted")
        check(intent_summaries[0]["fixtures_passed"] == 2, "intent fixture passes counted")
        check(intent_summaries[0]["skipped_inputs"] == 1, "zero-run static inputs counted")
        check(len(coverage_summaries) == 1, "one coverage job summarized")
        check(coverage_summaries[0]["grounding_validated"] == 3, "grounded actions summed")
        check(coverage_summaries[0]["grounding_cited"] == 4, "cited actions summed")
        check(coverage_summaries[0]["corrected"] == 1, "corrected satisfaction counted")
        check(len(readiness["intent_suites"]) == 2, "intent fixture suites discovered")
        check(any(not r["has_report"] for r in readiness["intent_suites"]), "missing intent report flagged")
        check(len(readiness["mining_profiles"]) == 2, "mining profiles discovered")
        check(any(not r["has_coverage_job"] for r in readiness["mining_profiles"]), "missing coverage job flagged")
        md = epr.render_markdown(summary, intent_summaries, coverage_summaries, readiness)
        check("Reusable pilot loop" in md, "markdown includes process section")
        check("codex-native" in md, "markdown includes missing profile")
        check("Routing intent suites" in md, "markdown includes intent suite section")
        check("Transcript-derived coverage jobs" in md, "markdown includes coverage section")
        check("Intent-suite readiness gaps" in md, "markdown includes intent readiness gaps")
        check("Coverage-mining readiness gaps" in md, "markdown includes coverage readiness gaps")
        check("Adherence-bar compliance" in md, "markdown includes bar compliance section")
        check("divergence" in md, "markdown flags a bar divergence")
        deck = epr.render_deck(summary, intent_summaries, coverage_summaries, readiness)
        check("<section" in deck and "Pilot loop" in deck, "deck renders slide sections")

    if failures:
        print("FAIL (%d):" % len(failures))
        for f in failures:
            print("  -", f)
        return 1
    print("PASS: eval_pilot_report (offline aggregation, coverage gaps, renderers)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
