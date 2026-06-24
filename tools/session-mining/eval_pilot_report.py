#!/usr/bin/env python3
"""Aggregate story-local agent eval reports into a pilot brief and slide deck.

This is intentionally offline-only: it reads committed or imported
agent_eval_report JSON files and renders review artifacts. It never calls a
provider, starts a harness, or estimates live cost.

Example:
  python3 tools/session-mining/eval_pilot_report.py \
    --root stories \
    --markdown .context/model-harness-eval-pilot.md \
    --deck .artifacts/eval-pilot/index.html \
    --summary .artifacts/eval-pilot/summary.json
"""

import argparse
import html
import json
import math
import os
import re
import sys
from collections import defaultdict


def load_json(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def find_reports(root):
    out = []
    for base, _, files in os.walk(root):
        if "%sevals%sreports%s" % (os.sep, os.sep, os.sep) not in base + os.sep:
            continue
        for name in files:
            if not name.endswith(".json"):
                continue
            path = os.path.join(base, name)
            try:
                report = load_json(path)
            except (OSError, json.JSONDecodeError):
                continue
            if report.get("kind") == "agent_eval_report":
                report["_path"] = path
                out.append(report)
    return sorted(out, key=lambda r: (r.get("call", ""), r.get("generated_at", ""), r.get("_path", "")))


def find_intent_reports(root):
    out = []
    if not root or not os.path.exists(root):
        return out
    for base, _, files in os.walk(root):
        for name in files:
            if not name.endswith(".json"):
                continue
            path = os.path.join(base, name)
            try:
                report = load_json(path)
            except (OSError, json.JSONDecodeError):
                continue
            if isinstance(report.get("Fixtures"), list):
                report["_path"] = path
                report["_story"] = os.path.splitext(name)[0]
                out.append(report)
    return sorted(out, key=lambda r: r["_path"])


def find_intent_fixture_suites(root):
    out = []
    if not root or not os.path.exists(root):
        return out
    for story_dir in sorted(os.path.join(root, p) for p in os.listdir(root)):
        intents_dir = os.path.join(story_dir, "intents")
        if not os.path.isdir(intents_dir):
            continue
        files = []
        fixture_count = 0
        for base, _, names in os.walk(intents_dir):
            for name in names:
                if not name.endswith((".yaml", ".yml")):
                    continue
                path = os.path.join(base, name)
                files.append(path)
                try:
                    with open(path, "r", encoding="utf-8") as fh:
                        text = fh.read()
                except OSError:
                    text = ""
                fixture_count += len(re.findall(r"(?m)^\s*-\s+id:\s*", text))
        out.append({
            "story": os.path.basename(story_dir),
            "path": intents_dir,
            "files": len(files),
            "fixtures": fixture_count,
        })
    return out


def find_mining_profiles(root):
    out = []
    if not root or not os.path.exists(root):
        return out
    for story_dir in sorted(os.path.join(root, p) for p in os.listdir(root)):
        path = os.path.join(story_dir, "mining.profile.yaml")
        if os.path.exists(path):
            out.append({"story": os.path.basename(story_dir), "path": path})
    return out


def find_coverage_jobs(root):
    out = []
    if not root or not os.path.exists(root):
        return out
    for base, _, files in os.walk(root):
        names = set(files)
        if {"intents.json", "analysis.json", "coverage.md"}.issubset(names):
            try:
                intents = load_json(os.path.join(base, "intents.json"))
                analysis = load_json(os.path.join(base, "analysis.json"))
            except (OSError, json.JSONDecodeError):
                continue
            git_json = None
            git_path = os.path.join(base, "intents.git.json")
            if os.path.exists(git_path):
                try:
                    git_json = load_json(git_path)
                except (OSError, json.JSONDecodeError):
                    git_json = None
            out.append({
                "path": base,
                "job": intents.get("job") or os.path.basename(base),
                "story": infer_story_from_coverage_path(base),
                "intents": intents,
                "analysis": analysis,
                "git": git_json,
            })
    return sorted(out, key=lambda j: j["path"])


def infer_story_from_coverage_path(path):
    name = os.path.basename(path)
    for suffix in ("-coverage", "-flagship"):
        if name.endswith(suffix):
            return name[:-len(suffix)]
    return name


def find_datasets(root):
    datasets = []
    for base, _, files in os.walk(root):
        if os.path.basename(base) != "evals":
            continue
        for name in files:
            if name.endswith((".yaml", ".yml")):
                path = os.path.join(base, name)
                stub = read_dataset_stub(path)
                if stub is not None:
                    datasets.append(stub)
    return sorted(datasets, key=lambda d: d["path"])


def read_dataset_stub(path):
    text = ""
    try:
        with open(path, "r", encoding="utf-8") as fh:
            text = fh.read()
    except OSError:
        pass
    if first_scalar(text, "kind") != "agent_eval":
        return None
    story = "?"
    parts = path.split(os.sep)
    if "stories" in parts:
        idx = parts.index("stories")
        if idx + 1 < len(parts):
            story = parts[idx + 1]
    return {
        "path": path,
        "story": story,
        "call": first_scalar(text, "call") or os.path.splitext(os.path.basename(path))[0],
        "profiles": inline_list(text, "profiles"),
        "repeat": first_int(text, "repeat"),
        "min_pass_rate": first_float(text, "min_pass_rate"),
        "max_p95_latency_ms": first_int(text, "max_p95_latency_ms"),
        "max_avg_cost_usd": first_float(text, "max_avg_cost_usd"),
    }


def first_scalar(text, key):
    match = re.search(r"(?m)^\s*%s:\s*([^\n#]+)" % re.escape(key), text)
    if not match:
        return ""
    return match.group(1).strip().strip('"').strip("'")


def first_int(text, key):
    value = first_scalar(text, key)
    try:
        return int(value)
    except ValueError:
        return 0


def first_float(text, key):
    value = first_scalar(text, key)
    try:
        return float(value)
    except ValueError:
        return 0.0


def _clean_item(value):
    return value.strip().strip('"').strip("'")


def inline_list(text, key):
    """Parse a YAML list value for `key`, supporting inline and block styles."""
    inline = re.search(r"(?m)^\s*%s:\s*\[([^\]]*)\]" % re.escape(key), text)
    if inline:
        return [_clean_item(p) for p in inline.group(1).split(",") if p.strip()]
    header = re.search(r"(?m)^(\s*)%s:\s*(?:#.*)?$" % re.escape(key), text)
    if not header:
        return []
    base_indent = len(header.group(1))
    items = []
    # header.end() lands just after `key:` on the same line; skip that remainder.
    for line in text[header.end():].split("\n")[1:]:
        if not line.strip():
            break
        indent = len(line) - len(line.lstrip())
        stripped = line.strip()
        if indent <= base_indent or not stripped.startswith("- "):
            break
        item = _clean_item(re.sub(r"\s+#.*$", "", stripped[2:]))
        if item:
            items.append(item)
    return items


def candidate_key(call, candidate):
    return "|".join([
        call,
        candidate.get("profile", ""),
        candidate.get("backend", ""),
        candidate.get("provider", ""),
        candidate.get("model", ""),
        candidate.get("effort", ""),
    ])


def pct(values, q):
    values = sorted(v for v in values if v is not None)
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]
    rank = q * (len(values) - 1)
    lo = int(math.floor(rank))
    hi = int(math.ceil(rank))
    if lo == hi:
        return values[lo]
    return values[lo] + (values[hi] - values[lo]) * (rank - lo)


def median(values):
    return pct(values, 0.5)


def fmt_pct(v):
    if v is None:
        return "-"
    return "%.0f%%" % (100.0 * v)


def fmt_ms(v):
    if v is None:
        return "-"
    return "%dms" % round(v)


def fmt_usd(v):
    if v is None:
        return "-"
    return "$%.4f" % v


_STAT_FMT = {"pct": fmt_pct, "ms": fmt_ms, "usd": fmt_usd}


def stat_cell(stat, kind):
    """Format a single statistic, distinguishing no-data ("-") from a real zero."""
    if not isinstance(stat, dict) or not stat.get("n"):
        return "-"
    return _STAT_FMT[kind](stat.get("median"))


def stat_triple(stat, kind):
    """Format median/p5/p95 for a statistic, or "-" cells when no samples exist."""
    if not isinstance(stat, dict) or not stat.get("n"):
        return "- / - / -"
    fmt = _STAT_FMT[kind]
    return "%s / %s / %s" % (fmt(stat.get("median")), fmt(stat.get("p5")), fmt(stat.get("p95")))


def fmt_bar(min_pass_rate, max_p95_latency_ms, max_avg_cost_usd):
    """Render a dataset's declared adherence bar, with "-" for unset thresholds."""
    return " / ".join([
        fmt_pct(min_pass_rate) if min_pass_rate else "-",
        fmt_ms(max_p95_latency_ms) if max_p95_latency_ms else "-",
        fmt_usd(max_avg_cost_usd) if max_avg_cost_usd else "-",
    ])


def evaluate_bar(candidate, bar):
    """Compare a candidate's measured medians against a dataset's declared bar.

    Returns (meets, violations). meets is None when no bar threshold is declared
    or when the relevant measurement is missing, so callers can distinguish
    "satisfies the contract" from "no contract to check".
    """
    if not bar:
        return None, []
    min_pass = bar.get("min_pass_rate") or 0.0
    max_lat = bar.get("max_p95_latency_ms") or 0
    max_cost = bar.get("max_avg_cost_usd") or 0.0
    if not (min_pass or max_lat or max_cost):
        return None, []
    violations = []
    if min_pass and candidate["pass_rate"] < min_pass:
        violations.append("pass rate %s < min %s" % (
            fmt_pct(candidate["pass_rate"]), fmt_pct(min_pass)))
    lat = candidate["p95_latency_ms"]
    if max_lat and lat.get("n") and lat["median"] > max_lat:
        violations.append("p95 latency %s > max %s" % (
            fmt_ms(lat["median"]), fmt_ms(max_lat)))
    cost = candidate["avg_cost_usd"]
    if max_cost and cost.get("n") and cost["median"] > max_cost:
        violations.append("avg cost %s > max %s" % (
            fmt_usd(cost["median"]), fmt_usd(max_cost)))
    return len(violations) == 0, violations


def summarize(datasets, reports):
    by_key = {}
    failures = []
    confidence_rows = []
    report_rows = []
    latest_by_call = {}

    for report in reports:
        call = report.get("call") or report.get("eval") or "?"
        report_rows.append({
            "path": report["_path"],
            "call": call,
            "generated_at": report.get("generated_at", ""),
            "candidate_count": len(report.get("candidates", [])),
        })
        old = latest_by_call.get(call)
        if old is None or report.get("generated_at", "") >= old.get("generated_at", ""):
            latest_by_call[call] = report
        for sample in report.get("failure_samples", []) or []:
            failures.append({
                "call": call,
                "report": report["_path"],
                "example": sample.get("example", ""),
                "profile": sample.get("profile", ""),
                "model": sample.get("model", ""),
                "reason": sample.get("reason", ""),
            })
        for candidate in report.get("candidates", []) or []:
            key = candidate_key(call, candidate)
            confidence_rows.extend(extract_confidence_rows(call, report, candidate))
            row = by_key.setdefault(key, {
                "call": call,
                "profile": candidate.get("profile", ""),
                "backend": candidate.get("backend", ""),
                "provider": candidate.get("provider", ""),
                "model": candidate.get("model", ""),
                "effort": candidate.get("effort", ""),
                "observations": 0,
                "examples_run": 0,
                "pass_observations": 0,
                "schema_valid_rate": [],
                "comparator_pass_rate": [],
                "contract_conformance_rate": [],
                "p50_latency_ms": [],
                "p95_latency_ms": [],
                "avg_cost_usd": [],
                "p95_cost_usd": [],
                "reports": [],
            })
            row["observations"] += 1
            row["examples_run"] += int(candidate.get("examples_run", 0) or 0)
            row["pass_observations"] += 1 if candidate.get("pass") else 0
            row["reports"].append(report["_path"])
            for field in (
                "schema_valid_rate",
                "comparator_pass_rate",
                "contract_conformance_rate",
                "p50_latency_ms",
                "p95_latency_ms",
                "avg_cost_usd",
                "p95_cost_usd",
            ):
                value = candidate.get(field)
                if isinstance(value, (int, float)):
                    row[field].append(float(value))

    bars = {
        ds["call"]: {
            "min_pass_rate": ds.get("min_pass_rate", 0.0),
            "max_p95_latency_ms": ds.get("max_p95_latency_ms", 0),
            "max_avg_cost_usd": ds.get("max_avg_cost_usd", 0.0),
        }
        for ds in datasets
    }

    candidates = []
    for row in by_key.values():
        summary = dict(row)
        summary["pass_rate"] = row["pass_observations"] / row["observations"] if row["observations"] else 0.0
        for field in (
            "schema_valid_rate",
            "comparator_pass_rate",
            "contract_conformance_rate",
            "p50_latency_ms",
            "p95_latency_ms",
            "avg_cost_usd",
            "p95_cost_usd",
        ):
            values = row[field]
            summary[field] = {
                "p5": pct(values, 0.05),
                "median": median(values),
                "p95": pct(values, 0.95),
                "n": len(values),
            }
        bar = bars.get(summary["call"])
        meets, violations = evaluate_bar(summary, bar)
        summary["declared_bar"] = bar
        summary["meets_declared_bar"] = meets
        summary["bar_violations"] = violations
        # The report's own `pass` flag can disagree with the declared dataset
        # bar (e.g. a row marked passing that still exceeds the cost ceiling).
        summary["bar_divergence"] = bool(violations) and summary["pass_rate"] > 0
        candidates.append(summary)

    candidates.sort(key=lambda c: (
        c["call"],
        -c["pass_rate"],
        -c["comparator_pass_rate"]["median"],
        c["avg_cost_usd"]["median"],
        c["p95_latency_ms"]["median"],
        c["profile"],
        c["model"],
    ))

    coverage = []
    latest_calls = set(latest_by_call)
    for ds in datasets:
        seen_profiles = sorted({c["profile"] for c in candidates if c["call"] == ds["call"] and c["profile"]})
        coverage.append({
            **ds,
            "has_report": ds["call"] in latest_calls,
            "measured_profiles": seen_profiles,
            "missing_profiles": [p for p in ds["profiles"] if p not in seen_profiles],
        })

    decisions = []
    for call, report in sorted(latest_by_call.items()):
        decision = report.get("decision") or {}
        if not decision:
            continue
        decisions.append({
            "call": call,
            "report": report["_path"],
            "strategy": decision.get("strategy", ""),
            "selected_profile": decision.get("selected_profile", ""),
            "selected_model": decision.get("selected_model", ""),
            "selected_effort": decision.get("selected_effort", ""),
            "evidence": decision.get("evidence", ""),
            "rejected_summary": decision.get("rejected_summary", ""),
            "fallback_profile": decision.get("fallback_profile", ""),
        })

    return {
        "datasets": datasets,
        "coverage": coverage,
        "reports": report_rows,
        "candidates": candidates,
        "decisions": decisions,
        "failures": failures,
        "confidence_sweeps": summarize_confidence_sweeps(confidence_rows),
        "confidence_rows": confidence_rows,
    }


def extract_confidence_rows(call, report, candidate):
    rows = []
    profile = candidate.get("profile", "")
    model = candidate.get("model", "")
    effort = candidate.get("effort", "")
    result_blocks = []
    for field in ("example_results", "examples", "runs", "samples"):
        values = candidate.get(field)
        if isinstance(values, list):
            result_blocks.extend(values)
    if not result_blocks:
        return rows
    for item in result_blocks:
        if not isinstance(item, dict):
            continue
        actual = item.get("actual") if isinstance(item.get("actual"), dict) else item
        expect = item.get("expect") if isinstance(item.get("expect"), dict) else {}
        confidence = actual.get("confidence", item.get("confidence"))
        try:
            confidence = float(confidence)
        except (TypeError, ValueError):
            continue
        expected_intent = expect.get("intent") or item.get("expected_intent")
        actual_intent = actual.get("intent") or item.get("actual_intent")
        correct = item.get("correct")
        if correct is None and expected_intent and actual_intent:
            correct = expected_intent == actual_intent
        if correct is None:
            continue
        rows.append({
            "call": call,
            "profile": profile,
            "model": model,
            "effort": effort,
            "report": report.get("_path", ""),
            "example": item.get("example") or item.get("name") or "",
            "confidence": confidence,
            "correct": bool(correct),
        })
    return rows


def summarize_confidence_sweeps(rows):
    if not rows:
        return []
    by_key = defaultdict(list)
    for row in rows:
        by_key[(row["call"], row["profile"], row["model"], row["effort"])].append(row)
    thresholds = [round(x / 100.0, 2) for x in range(50, 101, 5)]
    sweeps = []
    for (call, profile, model, effort), items in sorted(by_key.items()):
        for threshold in thresholds:
            accepted = [row for row in items if row["confidence"] >= threshold]
            false_accepts = [row for row in accepted if not row["correct"]]
            true_accepts = [row for row in accepted if row["correct"]]
            rejected = len(items) - len(accepted)
            sweeps.append({
                "call": call,
                "profile": profile,
                "model": model,
                "effort": effort,
                "threshold": threshold,
                "total": len(items),
                "accepted": len(accepted),
                "rejected": rejected,
                "true_accepts": len(true_accepts),
                "false_accepts": len(false_accepts),
                "precision": len(true_accepts) / len(accepted) if accepted else None,
                "coverage": len(accepted) / len(items) if items else 0.0,
            })
    return sweeps


def summarize_intent_reports(reports):
    summaries = []
    for report in reports:
        fixtures = report.get("Fixtures", [])
        total = len(fixtures)
        passed = sum(1 for f in fixtures if f.get("Passed"))
        runs = sum(int(f.get("TotalRuns") or 0) for f in fixtures)
        passed_runs = sum(int(f.get("TotalPassed") or 0) for f in fixtures)
        inputs = sum(len(f.get("Inputs") or []) for f in fixtures)
        skipped_inputs = sum(1 for f in fixtures for i in f.get("Inputs") or [] if int(i.get("Runs") or 0) == 0)
        rates = [float(f.get("PassRate") or 0) for f in fixtures]
        failed = [f for f in fixtures if not f.get("Passed")]
        by_state = defaultdict(lambda: {"fixtures": 0, "failed": 0})
        for fixture in fixtures:
            state = fixture.get("State") or "?"
            by_state[state]["fixtures"] += 1
            if not fixture.get("Passed"):
                by_state[state]["failed"] += 1
        summaries.append({
            "story": report.get("_story", "?"),
            "path": report.get("_path", ""),
            "fixtures": total,
            "fixtures_passed": passed,
            "fixture_pass_rate": passed / total if total else 0.0,
            "inputs": inputs,
            "skipped_inputs": skipped_inputs,
            "runs": runs,
            "passed_runs": passed_runs,
            "run_pass_rate": passed_runs / runs if runs else 0.0,
            "pass_rate_distribution": {
                "p5": pct(rates, 0.05),
                "median": median(rates),
                "p95": pct(rates, 0.95),
            },
            "failed_fixtures": [{
                "id": f.get("ID", ""),
                "state": f.get("State", ""),
                "pass_rate": float(f.get("PassRate") or 0),
                "inputs": [i.get("Input", "") for i in f.get("Inputs") or []],
            } for f in failed[:20]],
            "states": dict(sorted(by_state.items())),
        })
    return summaries


def summarize_coverage_jobs(jobs):
    summaries = []
    for job in jobs:
        analysis = job.get("analysis") or {}
        intents = job.get("intents") or {}
        git_json = job.get("git") or {}
        instances = analysis.get("instances") or []
        grounding_valid = sum(int(i.get("grounding", {}).get("actions_validated") or 0) for i in instances)
        grounding_cited = sum(int(i.get("grounding", {}).get("actions_cited") or 0) for i in instances)
        corrected = sum(1 for i in instances if i.get("satisfaction", {}).get("corrected"))
        det = defaultdict(int)
        for i in instances:
            det[i.get("determinism") or "?"] += 1
        in_scope = None
        deduped = None
        out_scope = None
        if git_json:
            intents_block = git_json.get("intents") or []
            in_scope = len(intents_block)
            groups = git_json.get("groups") or []
            deduped = len(groups)
            out_scope = len(git_json.get("out_of_scope") or [])
        summaries.append({
            "job": job.get("job", ""),
            "story": job.get("story", ""),
            "path": job.get("path", ""),
            "total_intents": int(intents.get("total_intents") or len(instances)),
            "instances": len(instances),
            "clusters": len(analysis.get("clusters") or []),
            "grounding_validated": grounding_valid,
            "grounding_cited": grounding_cited,
            "grounding_rate": grounding_valid / grounding_cited if grounding_cited else 1.0,
            "corrected": corrected,
            "determinism": dict(sorted(det.items())),
            "in_scope": in_scope,
            "deduped_shapes": deduped,
            "out_of_scope": out_scope,
        })
    return summaries


def summarize_readiness(intent_suites, intent_summaries, mining_profiles, coverage_summaries):
    intent_reported = {row["story"] for row in intent_summaries}
    coverage_reported = {row["story"] for row in coverage_summaries}
    return {
        "intent_suites": [{
            **row,
            "has_report": row["story"] in intent_reported,
        } for row in intent_suites],
        "mining_profiles": [{
            **row,
            "has_coverage_job": row["story"] in coverage_reported,
        } for row in mining_profiles],
    }


def render_markdown(summary, intent_summaries=None, coverage_summaries=None, readiness=None):
    intent_summaries = intent_summaries or []
    coverage_summaries = coverage_summaries or []
    readiness = readiness or {"intent_suites": [], "mining_profiles": []}
    candidates = summary["candidates"]
    coverage = summary["coverage"]
    reports = summary["reports"]
    passing = [c for c in candidates if c["pass_rate"] > 0]
    best = passing[0] if passing else (candidates[0] if candidates else None)
    decisions = summary.get("decisions", [])

    lines = []
    w = lines.append
    w("# Model and harness eval pilot")
    w("")
    w("This is a reusable offline reporting pass over story-local `agent_eval_report` JSON evidence. It does not call live models. Fresh model/harness runs should import or commit reports in the same shape, then rerun this script.")
    w("")
    w("## Current evidence")
    w("")
    w("| item | count |")
    w("|---|--:|")
    w("| eval datasets | %d |" % len(summary["datasets"]))
    w("| report files | %d |" % len(reports))
    w("| candidate rows | %d |" % len(candidates))
    w("| failure samples | %d |" % len(summary["failures"]))
    w("| intent-suite reports | %d |" % len(intent_summaries))
    w("| coverage-mining jobs | %d |" % len(coverage_summaries))
    w("| intent fixture suites discovered | %d |" % len(readiness["intent_suites"]))
    w("| mining profiles discovered | %d |" % len(readiness["mining_profiles"]))
    w("")
    if decisions:
        w("## Headline")
        w("")
        for decision in decisions:
            w("Latest committed decision for `%s` selects `%s/%s` with `%s` effort using `%s`; fallback is `%s`." % (
                decision["call"],
                decision["selected_profile"],
                decision["selected_model"],
                decision["selected_effort"] or "-",
                decision["strategy"] or "-",
                decision["fallback_profile"] or "-",
            ))
        if best:
            w("")
            w("Across all loaded evidence, the strongest aggregate candidate by acceptance-bar pass rate, comparator score, cost, and latency is `%s/%s` on `%s` with median comparator %s, median p95 latency %s, and median average cost %s." % (
                best["profile"], best["model"], best["call"],
                stat_cell(best["comparator_pass_rate"], "pct"),
                stat_cell(best["p95_latency_ms"], "ms"),
                stat_cell(best["avg_cost_usd"], "usd"),
            ))
        w("")
    w("## Dataset coverage")
    w("")
    w("| dataset | story | call | declared bar (min pass / max p95 / max cost) | planned profiles | measured profiles | missing profiles |")
    w("|---|---|---|---|---|---|---|")
    for row in coverage:
        w("| `%s` | `%s` | `%s` | %s | %s | %s | %s |" % (
            row["path"],
            row["story"],
            row["call"],
            fmt_bar(row.get("min_pass_rate"), row.get("max_p95_latency_ms"), row.get("max_avg_cost_usd")),
            ", ".join("`%s`" % p for p in row["profiles"]) or "-",
            ", ".join("`%s`" % p for p in row["measured_profiles"]) or "-",
            ", ".join("`%s`" % p for p in row["missing_profiles"]) or "-",
        ))
    w("")
    if intent_summaries:
        w("## Routing intent suites")
        w("")
        w("| story | fixtures | fixture pass | inputs | skipped inputs | runs | run pass | pass-rate median/p5/p95 |")
        w("|---|--:|--:|--:|--:|--:|--:|---|")
        for row in intent_summaries:
            dist = row["pass_rate_distribution"]
            w("| `%s` | %d | %s | %d | %d | %d | %s | %s / %s / %s |" % (
                row["story"],
                row["fixtures"],
                fmt_pct(row["fixture_pass_rate"]),
                row["inputs"],
                row["skipped_inputs"],
                row["runs"],
                fmt_pct(row["run_pass_rate"]),
                fmt_pct(dist["median"]),
                fmt_pct(dist["p5"]),
                fmt_pct(dist["p95"]),
            ))
        w("")
        failures = [f for row in intent_summaries for f in row["failed_fixtures"]]
        if failures:
            w("### Intent-suite failures")
            w("")
            w("| state | fixture | pass rate | sample input |")
            w("|---|---|--:|---|")
            for f in failures[:20]:
                sample = (f["inputs"] or [""])[0]
                w("| `%s` | `%s` | %s | %s |" % (
                    f["state"], f["id"], fmt_pct(f["pass_rate"]), sample.replace("|", "\\|")
                ))
            w("")
    if readiness["intent_suites"]:
        missing = [row for row in readiness["intent_suites"] if not row["has_report"]]
        if missing:
            w("### Intent-suite readiness gaps")
            w("")
            w("| story | files | fixtures declared | gap |")
            w("|---|--:|--:|---|")
            for row in missing:
                w("| `%s` | %d | %d | no JSON report loaded |" % (
                    row["story"], row["files"], row["fixtures"]
                ))
            w("")
    if coverage_summaries:
        w("## Transcript-derived coverage jobs")
        w("")
        w("| job | intents | in scope | deduped shapes | grounding | corrected | determinism |")
        w("|---|--:|--:|--:|---|--:|---|")
        for row in coverage_summaries:
            det = ", ".join("`%s` %d" % (k, v) for k, v in sorted(row["determinism"].items())) or "-"
            w("| `%s` | %d | %s | %s | %d/%d %s | %d | %s |" % (
                row["job"],
                row["total_intents"],
                row["in_scope"] if row["in_scope"] is not None else "-",
                row["deduped_shapes"] if row["deduped_shapes"] is not None else "-",
                row["grounding_validated"],
                row["grounding_cited"],
                fmt_pct(row["grounding_rate"]),
                row["corrected"],
                det,
            ))
        w("")
    if readiness["mining_profiles"]:
        missing = [row for row in readiness["mining_profiles"] if not row["has_coverage_job"]]
        if missing:
            w("### Coverage-mining readiness gaps")
            w("")
            w("%d stories have `mining.profile.yaml` but no loaded coverage job in this run: %s." % (
                len(missing),
                ", ".join("`%s`" % row["story"] for row in missing),
            ))
            w("")
    w("## Confidence threshold sweep")
    w("")
    sweeps = summary.get("confidence_sweeps") or []
    if sweeps:
        w("| call | profile/model | effort | threshold | accepted | true accepts | false accepts | precision | coverage |")
        w("|---|---|---|--:|--:|--:|--:|--:|--:|")
        for row in sweeps:
            w("| `%s` | `%s/%s` | `%s` | %.2f | %d/%d | %d | %d | %s | %s |" % (
                row["call"],
                row["profile"],
                row["model"],
                row["effort"] or "-",
                row["threshold"],
                row["accepted"],
                row["total"],
                row["true_accepts"],
                row["false_accepts"],
                fmt_pct(row["precision"]),
                fmt_pct(row["coverage"]),
            ))
        w("")
    else:
        w("No per-example actual confidence values were present in the loaded `agent_eval_report` files, so this run cannot show how false accepts change as the confidence bar is lowered. Add per-example result rows with `actual.intent`, `actual.confidence`, and expected `intent` to enable this table.")
        w("")
    w("## Candidate performance")
    w("")
    w("| call | profile/model | effort | observations | examples run | acceptance-bar pass rate | effectiveness median/p5/p95 | p95 latency median/p5/p95 | avg cost median/p5/p95 |")
    w("|---|---|---|--:|--:|--:|---|---|---|")
    for c in candidates:
        w("| `%s` | `%s/%s` | `%s` | %d | %d | %s | %s | %s | %s |" % (
            c["call"],
            c["profile"],
            c["model"],
            c["effort"] or "-",
            c["observations"],
            c["examples_run"],
            fmt_pct(c["pass_rate"]),
            stat_triple(c["comparator_pass_rate"], "pct"),
            stat_triple(c["p95_latency_ms"], "ms"),
            stat_triple(c["avg_cost_usd"], "usd"),
        ))
    w("")
    w("`acceptance-bar pass rate` is the share of candidate summary rows that passed the eval's configured bar. It is not the per-example success rate; use `effectiveness median/p5/p95` for comparator success across examples.")
    w("")
    w("## Adherence-bar compliance")
    w("")
    checked = [c for c in candidates if c.get("meets_declared_bar") is not None]
    if not checked:
        w("No loaded dataset declared an adherence bar (`min_pass_rate`, `max_p95_latency_ms`, or `max_avg_cost_usd`) that could be checked against measured candidates.")
        w("")
    else:
        w("Measured candidate medians are re-checked against the bar declared in each dataset. A `divergence` row passed the upstream report's own check but still violates the declared bar.")
        w("")
        w("| call | profile/model | declared bar | measured pass / p95 / cost | verdict |")
        w("|---|---|---|---|---|")
        for c in checked:
            bar = c["declared_bar"] or {}
            measured = "%s / %s / %s" % (
                fmt_pct(c["pass_rate"]),
                stat_cell(c["p95_latency_ms"], "ms"),
                stat_cell(c["avg_cost_usd"], "usd"),
            )
            if c["meets_declared_bar"]:
                verdict = "meets bar"
            else:
                verdict = ("**divergence** — " if c["bar_divergence"] else "violates — ") + "; ".join(c["bar_violations"])
            w("| `%s` | `%s/%s` | %s | %s | %s |" % (
                c["call"], c["profile"], c["model"],
                fmt_bar(bar.get("min_pass_rate"), bar.get("max_p95_latency_ms"), bar.get("max_avg_cost_usd")),
                measured,
                verdict,
            ))
        w("")
    w("## Failure samples")
    w("")
    if not summary["failures"]:
        w("No failure samples in the loaded reports.")
    else:
        w("| call | profile/model | example | reason |")
        w("|---|---|---|---|")
        for f in summary["failures"][:20]:
            w("| `%s` | `%s/%s` | `%s` | %s |" % (
                f["call"], f["profile"], f["model"], f["example"], f["reason"].replace("|", "\\|")
            ))
    w("")
    w("## Reusable pilot loop")
    w("")
    w("1. Mine or choose intent/task candidates from session-mining output and existing story intent fixtures.")
    w("2. Add or update `stories/<story>/evals/*.yaml` for bounded call sites with expected outputs and a matrix.")
    w("3. Run offline validation first: `go run ./cmd/kitsoki eval run stories/<story>/evals/<call>.yaml`.")
    w("4. Run larger no-cost validations: static intent suites with `kitsoki test intents --harness static --json ...` and committed transcript coverage jobs such as `tools/session-mining/examples/git-ops/run.sh --keep ...`.")
    w("5. Run any cost-bearing live harness matrix only by explicit operator action, then save provider evidence as `agent_eval_report` JSON under `.artifacts/` for review or `evals/reports/<call>/` when accepted.")
    w("6. Rerun this report: `python3 tools/session-mining/eval_pilot_report.py --root stories --intent-root .artifacts/eval-pilot/intent-reports --coverage-root .artifacts/eval-pilot --markdown .context/model-harness-eval-pilot.md --deck .artifacts/eval-pilot/index.html --summary .artifacts/eval-pilot/summary.json`.")
    w("")
    w("## Interpretation limits")
    w("")
    w("- Current variability is across available report files and candidate summaries, not raw per-repeat provider samples.")
    w("- Missing profiles in the dataset coverage table are planned by the eval matrix but not represented in current evidence.")
    w("- Automated tests and this report remain no-LLM/no-cost; live collection is a separately gated manual step.")
    w("")
    return "\n".join(lines)


def html_table(rows):
    return "\n".join(rows)


def render_deck(summary, intent_summaries=None, coverage_summaries=None, readiness=None):
    intent_summaries = intent_summaries or []
    coverage_summaries = coverage_summaries or []
    readiness = readiness or {"intent_suites": [], "mining_profiles": []}
    candidates = summary["candidates"]
    best = next((c for c in candidates if c["pass_rate"] > 0), candidates[0] if candidates else None)
    decision = (summary.get("decisions") or [None])[0]
    rows = []
    for c in candidates[:8]:
        rows.append("<tr><td>%s</td><td>%s/%s</td><td>%s</td><td>%d</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>" % (
            html.escape(c["call"]),
            html.escape(c["profile"]),
            html.escape(c["model"]),
            html.escape(c["effort"] or "-"),
            c["observations"],
            c["examples_run"],
            fmt_pct(c["pass_rate"]),
            html.escape(stat_triple(c["comparator_pass_rate"], "pct")),
            html.escape(stat_triple(c["p95_latency_ms"], "ms")),
            html.escape(stat_triple(c["avg_cost_usd"], "usd")),
        ))
    missing = []
    for row in summary["coverage"]:
        if row["missing_profiles"]:
            missing.append("%s: %s" % (row["call"], ", ".join(row["missing_profiles"])))
    headline = "No measured candidates yet."
    if decision:
        headline = "%s/%s is the latest committed selection for %s." % (
            decision["selected_profile"], decision["selected_model"], decision["call"])
    elif best:
        headline = "%s/%s is the current cheapest passing seed for %s." % (
            best["profile"], best["model"], best["call"])
    intent_rows = []
    for row in intent_summaries[:6]:
        intent_rows.append("<tr><td>%s</td><td>%d</td><td>%s</td><td>%d</td><td>%s</td></tr>" % (
            html.escape(row["story"]),
            row["fixtures"],
            fmt_pct(row["fixture_pass_rate"]),
            row["runs"],
            fmt_pct(row["run_pass_rate"]),
        ))
    coverage_rows = []
    for row in coverage_summaries[:6]:
        coverage_rows.append("<tr><td>%s</td><td>%d</td><td>%s</td><td>%d/%d</td><td>%d</td></tr>" % (
            html.escape(row["story"] or row["job"]),
            row["total_intents"],
            row["deduped_shapes"] if row["deduped_shapes"] is not None else "-",
            row["grounding_validated"],
            row["grounding_cited"],
            row["corrected"],
        ))
    sweep_rows = []
    for row in (summary.get("confidence_sweeps") or [])[:8]:
        sweep_rows.append("<tr><td>%s</td><td>%s/%s</td><td>%.2f</td><td>%d/%d</td><td>%d</td><td>%s</td><td>%s</td></tr>" % (
            html.escape(row["call"]),
            html.escape(row["profile"]),
            html.escape(row["model"]),
            row["threshold"],
            row["accepted"],
            row["total"],
            row["false_accepts"],
            fmt_pct(row["precision"]),
            fmt_pct(row["coverage"]),
        ))
    return """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Model and harness eval pilot</title>
<style>
:root { color-scheme: light; --ink:#17202a; --muted:#5c6670; --line:#d8dee4; --accent:#0f766e; --bg:#f7f8fa; }
* { box-sizing: border-box; }
body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background:var(--bg); color:var(--ink); }
main { width:100vw; height:100vh; overflow:hidden; }
section { width:100vw; height:100vh; display:none; padding:6vh 6vw; }
section.active { display:flex; flex-direction:column; justify-content:center; gap:24px; }
h1 { font-size: clamp(44px, 7vw, 86px); line-height:1; margin:0; letter-spacing:0; max-width:980px; }
h2 { font-size: clamp(34px, 5vw, 64px); line-height:1.05; margin:0; letter-spacing:0; }
p, li { font-size: clamp(20px, 2.2vw, 30px); line-height:1.35; max-width:1040px; }
.muted { color:var(--muted); }
.grid { display:grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap:18px; }
.metric { border:1px solid var(--line); background:white; border-radius:8px; padding:22px; }
.metric b { display:block; font-size: clamp(32px, 4vw, 58px); color:var(--accent); }
table { border-collapse:collapse; width:min(1280px, 100%%); background:white; border:1px solid var(--line); font-size:16px; }
th, td { text-align:left; padding:10px 12px; border-bottom:1px solid var(--line); vertical-align:top; }
th { color:var(--muted); font-weight:650; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.pager { position:fixed; right:24px; bottom:20px; color:var(--muted); font-size:14px; }
</style>
</head>
<body>
<main>
<section class="active">
  <p class="muted">Reusable pilot process</p>
  <h1>Model and harness evals from mined intent evidence</h1>
  <p>%s</p>
</section>
<section>
  <h2>Evidence loaded</h2>
  <div class="grid">
    <div class="metric"><b>%d</b><span>eval datasets</span></div>
    <div class="metric"><b>%d</b><span>report files</span></div>
    <div class="metric"><b>%d</b><span>intent suites</span></div>
  </div>
  <p class="muted">%d mining profiles and %d coverage jobs were included in readiness accounting.</p>
  <p class="muted">This deck is rendered offline from JSON reports; it makes no provider calls.</p>
</section>
<section>
  <h2>Static routing intent suites</h2>
  <table>
    <thead><tr><th>story</th><th>fixtures</th><th>fixture pass</th><th>runs</th><th>run pass</th></tr></thead>
    <tbody>%s</tbody>
  </table>
</section>
<section>
  <h2>Transcript-derived coverage</h2>
  <table>
    <thead><tr><th>job</th><th>intents</th><th>deduped shapes</th><th>grounded actions</th><th>corrected</th></tr></thead>
    <tbody>%s</tbody>
  </table>
</section>
<section>
  <h2>Candidate comparison</h2>
  <table>
    <thead><tr><th>call</th><th>profile/model</th><th>effort</th><th>obs</th><th>examples run</th><th>bar pass rate</th><th>effectiveness med/p5/p95</th><th>p95 latency med/p5/p95</th><th>avg cost med/p5/p95</th></tr></thead>
    <tbody>%s</tbody>
  </table>
</section>
<section>
  <h2>Confidence threshold sweep</h2>
  <table>
    <thead><tr><th>call</th><th>profile/model</th><th>threshold</th><th>accepted</th><th>false accepts</th><th>precision</th><th>coverage</th></tr></thead>
    <tbody>%s</tbody>
  </table>
  <p class="muted">Requires per-example actual confidence in the loaded eval reports.</p>
</section>
<section>
  <h2>Coverage gaps drive the next run</h2>
  <p>%s</p>
  <p class="muted">The process is useful even when the table is sparse: missing profiles become the next explicit live-harness collection target.</p>
</section>
<section>
  <h2>Pilot loop</h2>
  <ol>
    <li>Mine intents and select bounded call sites.</li>
    <li>Define story-local eval datasets and matrices.</li>
    <li>Validate offline in CI.</li>
    <li>Run live matrices only by explicit operator action.</li>
    <li>Aggregate reports into Markdown, JSON, and this deck.</li>
  </ol>
</section>
</main>
<div class="pager"><span id="idx">1</span>/<span id="count">-</span> - arrow keys</div>
<script>
const slides = [...document.querySelectorAll('section')];
let index = 0;
function show(i) {
  index = Math.max(0, Math.min(slides.length - 1, i));
  slides.forEach((s, n) => s.classList.toggle('active', n === index));
  document.getElementById('idx').textContent = String(index + 1);
  document.getElementById('count').textContent = String(slides.length);
}
document.addEventListener('keydown', e => {
  if (e.key === 'ArrowRight' || e.key === 'PageDown' || e.key === ' ') show(index + 1);
  if (e.key === 'ArrowLeft' || e.key === 'PageUp') show(index - 1);
});
show(0);
</script>
</body>
</html>
""" % (
        html.escape(headline),
        len(summary["datasets"]),
        len(summary["reports"]),
        len(intent_summaries),
        len(readiness["mining_profiles"]),
        len(coverage_summaries),
        html_table(intent_rows) or '<tr><td colspan="5">No intent-suite reports loaded.</td></tr>',
        html_table(coverage_rows) or '<tr><td colspan="5">No coverage jobs loaded.</td></tr>',
        html_table(rows),
        html_table(sweep_rows) or '<tr><td colspan="7">No per-example actual confidence values loaded.</td></tr>',
        html.escape("; ".join(missing) if missing else "All planned profiles have at least one report in the loaded evidence."),
    )


def write(path, content):
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(content)


def main(argv=None):
    ap = argparse.ArgumentParser(description="Render an offline model/harness eval pilot report.")
    ap.add_argument("--root", default="stories", help="root to scan for stories/*/evals reports")
    ap.add_argument("--intent-root", help="root containing kitsoki test intents JSON reports")
    ap.add_argument("--coverage-root", help="root containing session-mining coverage job dirs")
    ap.add_argument("--markdown", help="write Markdown report")
    ap.add_argument("--deck", help="write slide-style HTML deck")
    ap.add_argument("--summary", help="write machine-readable summary JSON")
    args = ap.parse_args(argv)

    datasets = find_datasets(args.root)
    reports = find_reports(args.root)
    summary = summarize(datasets, reports)
    intent_summaries = summarize_intent_reports(find_intent_reports(args.intent_root))
    coverage_summaries = summarize_coverage_jobs(find_coverage_jobs(args.coverage_root))
    readiness = summarize_readiness(
        find_intent_fixture_suites(args.root),
        intent_summaries,
        find_mining_profiles(args.root),
        coverage_summaries,
    )
    summary["intent_suites"] = intent_summaries
    summary["coverage_jobs"] = coverage_summaries
    summary["readiness"] = readiness

    if args.markdown:
        write(args.markdown, render_markdown(summary, intent_summaries, coverage_summaries, readiness))
    if args.deck:
        write(args.deck, render_deck(summary, intent_summaries, coverage_summaries, readiness))
    if args.summary:
        write(args.summary, json.dumps(summary, indent=2, sort_keys=True) + "\n")
    if not (args.markdown or args.deck or args.summary):
        sys.stdout.write(render_markdown(summary))
    return 0


if __name__ == "__main__":
    sys.exit(main())
