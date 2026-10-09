#!/usr/bin/env python3
"""Runner-level test for --scenario-qa-report (docs/persona-qa.md,
"all-transports fan-out + one report").

stories/scenario-qa owns report.md itself (folded in Starlark,
scripts/build_report.star) and dispatches the driver/judge agents per
transport check; this subcommand only owns deck.slidey.json -- the one derived
artifact that most benefits from this module's existing Slidey-deck-shape
validation. Covers:
  - scenario_qa_leg_counts()/scenario_qa_leg_level() over a mixed
    pass/fail/degraded-evidence leg set, including the vscode
    bridge-level label (never mistaken for editor-level coverage)
  - parse_scenario_qa_leg_results(): inline JSON, "@<path>" file JSON, and
    the empty/invalid-JSON error paths
  - render_scenario_qa_deck() produces a deck.slidey.json shape that passes
    this module's own validate_slidey_deck_shape() gate
  - the --scenario-qa-report CLI wiring end to end via run.main(), writing
    deck.slidey.json into an existing run dir and printing --json-output

This never calls a live LLM or GitHub; every check is local and deterministic.
Run directly:  python3 tools/product-journey/scenario_qa_report_test.py
"""

import contextlib
import importlib.util
import io
import json
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def _expect_system_exit(name, fn, expected_text):
    try:
        fn()
    except SystemExit as exc:
        _check(name, expected_text in str(exc))
        return
    print(f"FAIL: {name}")
    sys.exit(1)


_LEG_RESULTS = {
    "items": [
        {
            "leg_id": "bugfix::tui",
            "scenario": "bugfix",
            "transport": "tui",
            "driver_status": "captured",
            "verdict": "pass",
            "verdict_summary": "TUI frame confirms the fix.",
            "playback_path": "clips/tui-required-input.rrweb.json",
            "playback_caption": "TUI replay shows the input-required prompt and answer path.",
            "natural_utterance_count": 2,
            "natural_utterance_example": "resolve the red gate test that's already written but not committed",
            "natural_utterance_sources": [
                "mined-scn-1b4ace86-f192-43b0-ab86-16142fec0079-0001",
                "mined-scn-c4d281a2-30e4-4002-9152-59d28d824abc-0001",
            ],
        },
        {
            "leg_id": "bugfix::web",
            "scenario": "bugfix",
            "transport": "web",
            "driver_status": "captured",
            "verdict": "pass",
            "verdict_summary": "Browser screenshot confirms the fix.",
            "evidence_refs": [{"path": "clips/web-required-input.rrweb.json", "caption": "Web replay shows the forwarded question modal."}],
        },
        {"leg_id": "bugfix::vscode", "scenario": "bugfix", "transport": "vscode", "driver_status": "degraded-evidence", "verdict": "degraded-evidence", "verdict_summary": "IDE bridge came back JSON-degraded."},
    ]
}


def _test_leg_counts():
    items = run.scenario_qa_leg_items(_LEG_RESULTS)
    _check("scenario_qa_leg_items extracts the items list", len(items) == 3)
    _check("scenario_qa_leg_items tolerates a non-dict input", run.scenario_qa_leg_items(None) == [])
    _check("scenario_qa_leg_items tolerates a missing items key", run.scenario_qa_leg_items({}) == [])

    counts = run.scenario_qa_leg_counts(items)
    _check("counts total", counts["total"] == 3)
    _check("counts pass", counts["pass"] == 2)
    _check("counts degraded", counts["degraded"] == 1)
    _check("counts fail", counts["fail"] == 0)

    mixed = [
        {"verdict": "pass"},
        {"verdict": "fail"},
        {"verdict": "degraded-evidence"},
        {"verdict": "unjudged"},
        {"verdict": ""},
    ]
    mixed_counts = run.scenario_qa_leg_counts(mixed)
    _check("mixed counts total counts every item", mixed_counts["total"] == 5)
    _check("mixed counts pass", mixed_counts["pass"] == 1)
    _check("mixed counts degraded", mixed_counts["degraded"] == 1)
    _check("mixed counts fail excludes unjudged/empty verdicts", mixed_counts["fail"] == 1)

    summary = run.scenario_qa_report_summary("bugfix", counts)
    _check("summary names the pass/total ratio", "2 / 3 transport checks passed" in summary)
    _check("summary names the degraded count", "1 degraded-evidence" in summary)
    _check("summary omits a failed clause when there are no failures", "failed" not in summary)
    _check("slide title is compact", run.scenario_qa_report_title(counts) == "Transport checks: 2 / 3 passed · 1 degraded")


def _test_leg_level():
    _check(
        "vscode leg with no explicit level falls back to bridge-level",
        run.scenario_qa_leg_level({"transport": "vscode"}) == "bridge-level",
    )
    _check(
        "tui leg with no explicit level falls back to frame-level",
        run.scenario_qa_leg_level({"transport": "tui"}) == "frame-level",
    )
    _check(
        "web leg with no explicit level falls back to frame-level",
        run.scenario_qa_leg_level({"transport": "web"}) == "frame-level",
    )
    _check(
        "cli leg with no explicit level falls back to terminal-level",
        run.scenario_qa_leg_level({"transport": "cli"}) == "terminal-level",
    )
    _check(
        "an unknown transport with no contract has no level",
        run.scenario_qa_leg_level({"transport": "holodeck"}) == "",
    )
    _check(
        "an explicit evidence_level wins over the transport fallback",
        run.scenario_qa_leg_level({"transport": "tui", "evidence_level": "custom-level"}) == "custom-level",
    )
    _check(
        "a carried transport_evidence_contract.level wins over the bare transport fallback",
        run.scenario_qa_leg_level({"transport": "web", "transport_evidence_contract": {"level": "frame-level"}}) == "frame-level",
    )


def _test_natural_prompt_lines():
    items = run.scenario_qa_leg_items(_LEG_RESULTS)
    lines = run.scenario_qa_natural_prompt_lines(items)
    _check("natural prompt lines include only legs with transcript wording", len(lines) == 1)
    line = lines[0]
    _check("natural prompt line names the scenario leg", "tui / bugfix" in line)
    _check("natural prompt line names the prompt count", "2 transcript-derived prompt(s)" in line)
    _check("natural prompt line carries the mined example", "resolve the red gate test" in line)
    _check("natural prompt line carries source refs", "mined-scn-1b4ace86-f192-43b0-ab86-16142fec0079-0001" in line)


def _test_playback_items():
    items = run.scenario_qa_leg_items(_LEG_RESULTS)
    playback = run.scenario_qa_playback_items(items)
    _check("playback items include explicit playback_path and rrweb evidence refs", len(playback) == 2)
    paths = {item["path"] for item in playback}
    _check("playback items include the TUI rrweb path", "clips/tui-required-input.rrweb.json" in paths)
    _check("playback items include the web rrweb path", "clips/web-required-input.rrweb.json" in paths)
    _check("playback items are rendered as video media", all(item["media_kind"] == "video" for item in playback))


def _test_parse_leg_results(tmp: Path):
    _check("empty raw returns an empty items list", run.parse_scenario_qa_leg_results("") == {"items": []})
    inline = json.dumps({"items": [{"transport": "tui"}]})
    _check("inline JSON parses", run.parse_scenario_qa_leg_results(inline) == {"items": [{"transport": "tui"}]})

    path = tmp / "leg-results.json"
    path.write_text(json.dumps({"items": [{"transport": "web"}]}), encoding="utf-8")
    _check("@path reads a JSON file", run.parse_scenario_qa_leg_results(f"@{path}") == {"items": [{"transport": "web"}]})

    _expect_system_exit(
        "invalid JSON raises a clear SystemExit",
        lambda: run.parse_scenario_qa_leg_results("{not json"),
        "not valid JSON",
    )
    _expect_system_exit(
        "a JSON scalar (not an object) is rejected",
        lambda: run.parse_scenario_qa_leg_results("[1, 2, 3]"),
        "must decode to a JSON object",
    )


def _test_render_deck():
    items = run.scenario_qa_leg_items(_LEG_RESULTS)
    counts = run.scenario_qa_leg_counts(items)
    deck = run.render_scenario_qa_deck("bugfix", "scenario-qa-run-all", items, counts)

    issues: list[dict] = []
    run.validate_slidey_deck_shape(deck, {"items": []}, issues)
    _check("the rendered deck passes this module's own Slidey deck-shape validator", issues == [])

    body_text = json.dumps(deck)
    _check("the deck names the scenario", "bugfix" in body_text)
    _check("the deck labels the vscode leg bridge-level", "bridge-level" in body_text)
    _check("the deck labels the tui leg frame-level", "frame-level" in body_text)
    _check("the deck carries the run id", "scenario-qa-run-all" in body_text)
    _check("the deck's transport scene names the pass/total ratio", "Transport checks: 2 / 3 passed" in body_text)
    _check("the deck includes a natural prompt scene", "Natural prompts" in body_text)
    _check("the deck includes a session evidence scene", "Session evidence" in body_text)
    _check("the deck includes user session replay scenes", "User session replay" in body_text)
    _check("the deck carries the TUI rrweb path", "clips/tui-required-input.rrweb.json" in body_text)
    _check("the deck carries the web rrweb path", "clips/web-required-input.rrweb.json" in body_text)
    _check("the deck emits rrweb scene keys", "\"rrweb\"" in body_text)
    _check("the deck labels transcript-derived wording", "Transcript-derived scenario wording" in body_text)
    _check("the deck carries the natural prompt count", "2 transcript-derived prompt" in body_text)
    _check("the deck carries the natural prompt example", "resolve the red gate test" in body_text)
    _check("the deck carries natural prompt source refs", "mined-scn-1b4ace86-f192-43b0-ab86-16142fec0079-0001" in body_text)

    transport_scene = deck["scenes"][1]
    labels = [item.get("label", "") for item in transport_scene["items"]]
    details = [item.get("detail", "") for item in transport_scene["items"]]
    _check("transport-check slide uses short transport labels", labels == ["TUI", "Web UI", "VS Code bridge"])
    _check("transport-check slide title stays short", transport_scene["title"] == "Transport checks: 2 / 3 passed · 1 degraded")
    _check("transport-check slide summarizes check tags", "input path" in details[0] and "summary report" in details[0])
    _check("transport-check slide details stay readable", all(len(detail) <= 180 for detail in details))
    _check("transport-check slide does not inline long verdict prose", "Browser screenshot confirms the fix." not in json.dumps(transport_scene))

    empty_deck = run.render_scenario_qa_deck("adhoc-thing", "run-empty", [], run.scenario_qa_leg_counts([]))
    empty_issues: list[dict] = []
    run.validate_slidey_deck_shape(empty_deck, {"items": []}, empty_issues)
    _check("a deck with zero recorded legs still passes deck-shape validation", empty_issues == [])


def _test_render_review():
    items = run.scenario_qa_leg_items(_LEG_RESULTS)
    counts = run.scenario_qa_leg_counts(items)
    review = run.render_scenario_qa_review("bugfix", "scenario-qa-run-all", items, counts)
    _check("scenario-qa review marks degraded runs as needing evidence", review["status"] == "needs_evidence")
    _check("scenario-qa review carries leg counts", review["summary_counts"]["total"] == 3)
    _check("scenario-qa review carries natural prompt counts", review["summary_counts"]["natural_prompts"] == 2)
    review_text = json.dumps(review)
    _check("scenario-qa review records degraded evidence checks", "degraded evidence" in review_text)
    _check("scenario-qa review records natural prompt checks", "transcript-derived prompt" in review_text)

    passing_items = [item for item in items if item.get("transport") != "vscode"]
    passing_review = run.render_scenario_qa_review("bugfix", "run-pass", passing_items, run.scenario_qa_leg_counts(passing_items))
    _check("scenario-qa review marks all-pass recorded runs ready", passing_review["status"] == "ready")

    empty_review = run.render_scenario_qa_review("adhoc-thing", "run-empty", [], run.scenario_qa_leg_counts([]))
    _check("scenario-qa review leaves empty runs not reviewed", empty_review["status"] == "not_reviewed")


def _run_cli(tmp: Path, extra_args, expected_exit=None):
    out = io.StringIO()
    sys.argv = [
        "run.py",
        "--scenario-qa-report",
        "--json-output",
        "--run-dir",
        str(tmp),
        *extra_args,
    ]
    with contextlib.redirect_stdout(out):
        if expected_exit is None:
            run.main()
        else:
            try:
                run.main()
            except SystemExit as exc:
                _check(f"CLI exits {expected_exit}", exc.code == expected_exit or (expected_exit != 0 and exc.code))
                return None
            print("FAIL: expected SystemExit")
            sys.exit(1)
    return json.loads(out.getvalue())


def _test_cli(tmp: Path):
    run_dir = tmp / "run-cli"
    run_dir.mkdir()
    leg_results_json = json.dumps(_LEG_RESULTS)

    payload = _run_cli(
        run_dir,
        ["--scenario", "bugfix", "--leg-results-json", leg_results_json],
    )
    _check("CLI reports the built status", payload["status"] == "scenario_qa_deck_built")
    _check("CLI reports the run dir", payload["run_dir"] == str(run_dir))
    _check("CLI reports leg/pass/fail/degraded counts", (payload["leg_count"], payload["pass_count"], payload["fail_count"], payload["degraded_count"]) == (3, 2, 0, 1))
    _check("CLI reports the scenario-qa review status", payload["review_status"] == "needs_evidence")
    deck_path = Path(payload["deck_path"])
    _check("CLI writes deck.slidey.json into the run dir", deck_path == run_dir / "deck.slidey.json")
    _check("CLI actually wrote the deck file", deck_path.exists())
    written = json.loads(deck_path.read_text(encoding="utf-8"))
    _check("the written deck names the scenario", written["scenes"][0]["subtitle"] == "bugfix")
    written_text = json.dumps(written)
    _check("the written deck includes natural prompt coverage", "Transcript-derived scenario wording" in written_text)
    _check("the written deck embeds playback coverage", "User session replay" in written_text)
    _check("the written deck preserves rrweb replay paths", "clips/web-required-input.rrweb.json" in written_text)
    _check("the written deck preserves the mined example", "resolve the red gate test" in written_text)
    review_path = Path(payload["review_path"])
    _check("CLI writes review.json into the run dir", review_path == run_dir / "review.json")
    _check("CLI actually wrote the review file", review_path.exists())
    written_review = json.loads(review_path.read_text(encoding="utf-8"))
    _check("the written review marks degraded evidence", written_review["status"] == "needs_evidence")
    _check("the written review carries natural prompt counts", written_review["summary_counts"]["natural_prompts"] == 2)

    adhoc_dir = tmp / "run-cli-adhoc"
    adhoc_dir.mkdir()
    adhoc_payload = _run_cli(
        adhoc_dir,
        ["--scenario-description", "open the onboarding tour", "--leg-results-json", ""],
    )
    _check("CLI falls back to --scenario-description when there is no catalog scenario", adhoc_payload["leg_count"] == 0)
    adhoc_deck = json.loads((adhoc_dir / "deck.slidey.json").read_text(encoding="utf-8"))
    _check("an ad-hoc run names the description in the deck", adhoc_deck["scenes"][0]["subtitle"] == "open the onboarding tour")
    adhoc_review = json.loads((adhoc_dir / "review.json").read_text(encoding="utf-8"))
    _check("an ad-hoc run with no legs stays not reviewed", adhoc_review["status"] == "not_reviewed")

    missing_run_dir_argv = [
        "run.py",
        "--scenario-qa-report",
        "--json-output",
    ]
    original_argv = sys.argv
    try:
        sys.argv = missing_run_dir_argv
        _expect_system_exit(
            "--scenario-qa-report without --run-dir raises a clear error",
            run.main,
            "requires --run-dir",
        )
    finally:
        sys.argv = original_argv


def _test_cli_deck_failure_still_writes_report(tmp: Path):
    # Regression for persona-qa productization brief issue group F / P1.6
    # ("quiet partial failures"): a deck-shape validation failure used to
    # raise SystemExit BEFORE report.md was written at all, silently
    # dropping the report. render_scenario_qa_deck is monkeypatched to
    # simulate a validation failure (deliberately malformed rather than
    # hand-crafting a leg_results shape that happens to trip the real
    # validator, so this test stays independent of validator internals).
    run_dir = tmp / "run-cli-deck-failure"
    run_dir.mkdir()
    leg_results_json = json.dumps(_LEG_RESULTS)

    original_render_deck = run.render_scenario_qa_deck

    def _broken_render_deck(name, run_id, items, counts):
        deck = original_render_deck(name, run_id, items, counts)
        del deck["scenes"]  # trips validate_slidey_deck_shape
        return deck

    run.render_scenario_qa_deck = _broken_render_deck
    try:
        payload = _run_cli(
            run_dir,
            ["--scenario", "bugfix", "--leg-results-json", leg_results_json],
        )
    finally:
        run.render_scenario_qa_deck = original_render_deck

    _check("a failed deck build reports a distinct status", payload["status"] == "scenario_qa_report_built_deck_failed")
    _check("a failed deck build carries a non-empty deck_error", payload["deck_error"] != "")
    _check("a failed deck build carries the deck validation cause", "deck validation failed" in payload["deck_error"])
    _check("a failed deck build reports an empty deck_path", payload["deck_path"] == "")
    _check("a failed deck build reports an empty review_path", payload["review_path"] == "")
    _check("a failed deck build still reports the pass/fail/degraded counts", (payload["pass_count"], payload["fail_count"], payload["degraded_count"]) == (2, 0, 1))
    # The load-bearing assertion: report.md must exist and be honest anyway.
    report_path = Path(payload["report_path"])
    _check("report.md path is reported even when the deck build failed", str(report_path) == str(run_dir / "report.md"))
    _check("report.md was actually written despite the deck failure", report_path.exists())
    report_text = report_path.read_text(encoding="utf-8")
    _check("report.md still carries the transport verdict table", "bugfix::tui" in report_text or "tui" in report_text)
    _check("report.md carries an honest deck-failure line", "Deck generation failed:" in report_text)
    _check("report.md's deck-failure line names the cause", "deck validation failed" in report_text)
    _check("deck.slidey.json was NOT written on a failed build", not (run_dir / "deck.slidey.json").exists())
    _check("review.json was NOT written on a failed build", not (run_dir / "review.json").exists())


def main():
    _test_leg_counts()
    _test_leg_level()
    _test_natural_prompt_lines()
    _test_playback_items()
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        _test_parse_leg_results(tmp)
        _test_cli(tmp)
        _test_cli_deck_failure_still_writes_report(tmp)
    _test_render_deck()
    _test_render_review()
    print("PASS")


if __name__ == "__main__":
    main()
