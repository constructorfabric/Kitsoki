#!/usr/bin/env python3
"""Tests for run_checks.py — the WS-F gate's check-suite runner.

Run directly: python3 tools/dev-workflow-matrix/run_checks_test.py

Uses a FAKE `run_fn` (dependency injection, per AGENTS.md) so this suite never
shells out to `go run`/`python3 tools/product-journey/run.py` — zero LLM, zero
network, zero real subprocess. Covers: flow-suite pass/fail mapping, the
driver-replay-smoke report-path/verdict mapping (including its infra-signal
paths), verdict file naming + shape (schema-conformant, keyed for
generate.py's scan_verdicts_dir), and the `--only` CLI filter.
"""
from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "dwm_run_checks", str(Path(__file__).with_name("run_checks.py"))
)
rc = importlib.util.module_from_spec(_spec)
sys.modules["dwm_run_checks"] = rc  # dataclasses needs the module registered before exec
_spec.loader.exec_module(rc)

_spec2 = importlib.util.spec_from_file_location(
    "dwm_generate", str(Path(__file__).with_name("generate.py"))
)
gen = importlib.util.module_from_spec(_spec2)
sys.modules["dwm_generate"] = gen
_spec2.loader.exec_module(gen)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def fake_proc(returncode=0, stdout="", stderr="") -> "subprocess.CompletedProcess[str]":
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def main():
    check = rc.CheckDef(
        workflow="wf1",
        surface="s1",
        repo="r1",
        check_type="replay",
        command=["true"],
        summary="fake flow suite",
    )

    # 1. a passing flow suite -> solved / model:result.
    payload = rc.run_check(check, Path("."), lambda cmd, cwd: fake_proc(0))
    _check("passing flow suite -> solved", payload["verdict"] == "solved")
    _check("passing flow suite -> model:result", payload["health"] == "model:result")
    _check("check_type carried through", payload["check_type"] == "replay")
    _check("axis carries workflow/surface", payload["axis"] == {"workflow": "wf1", "surface": "s1"})
    _check("target_id is the repo id", payload["target_id"] == "r1")
    _check("schema_version present", payload["schema_version"] == rc.SCHEMA_VERSION)
    _check("evidence_refs present (empty ok)", payload["evidence_refs"] == [])

    # 2. a failing flow suite -> failed / model:result (a real model result,
    # not an infra break) with the failure tail in the summary.
    payload = rc.run_check(check, Path("."), lambda cmd, cwd: fake_proc(1, stdout="FAIL: flow xyz\n"))
    _check("failing flow suite -> failed", payload["verdict"] == "failed")
    _check("failing flow suite -> model:result", payload["health"] == "model:result")
    _check("failure tail surfaces in summary", "FAIL: flow xyz" in payload["summary"])

    # 3. verdict_filename() is stable and matches generate.py's scan key shape.
    fname = rc.verdict_filename(check)
    _check("filename encodes workflow/surface/repo/check_type", fname == "wf1__s1__r1__replay.json")

    # 4. driver-replay-smoke: a crash (nonzero exit) is an infra signal.
    jcheck = rc.CheckDef(
        workflow="fix-bug",
        surface="vscode",
        repo="kitsoki-dev",
        check_type="journey-verdict",
        command=["false"],
        summary="fake smoke",
    )
    payload = rc.run_check(jcheck, Path("."), lambda cmd, cwd: fake_proc(1, stderr="Traceback\n"))
    _check("smoke crash -> blocked", payload["verdict"] == "blocked")
    _check("smoke crash -> infra:harness", payload["health"] == "infra:harness")

    # 5. driver-replay-smoke: exit 0 but no report path in stdout -> infra
    # signal (never guessed as a model result).
    payload = rc.run_check(jcheck, Path("."), lambda cmd, cwd: fake_proc(0, stdout="nothing useful here\n"))
    _check("missing report path -> blocked", payload["verdict"] == "blocked")
    _check(
        "missing report path -> infra:missing-completion-state",
        payload["health"] == "infra:missing-completion-state",
    )

    # 6. driver-replay-smoke: a real report path with status "passed" -> solved.
    with tempfile.TemporaryDirectory() as tmpdir:
        smoke_dir = Path(tmpdir)
        report_path = smoke_dir / "driver-replay-smoke.json"
        report_path.write_text(json.dumps({"status": "passed"}), encoding="utf-8")
        payload = rc.run_check(
            jcheck, Path("."), lambda cmd, cwd: fake_proc(0, stdout=f"Artifacts: {smoke_dir}\n")
        )
        _check("passed report -> solved", payload["verdict"] == "solved")
        _check("passed report -> model:result", payload["health"] == "model:result")
        _check("report path attached as evidence", str(report_path) in payload["evidence_refs"])

        # 7. status "failed" in the report -> failed (still a model result).
        report_path.write_text(json.dumps({"status": "failed"}), encoding="utf-8")
        payload = rc.run_check(
            jcheck, Path("."), lambda cmd, cwd: fake_proc(0, stdout=f"Artifacts: {smoke_dir}\n")
        )
        _check("failed report -> failed verdict", payload["verdict"] == "failed")
        _check("failed report still model:result", payload["health"] == "model:result")

    # 8. run_all() writes one verdict file per check, and generate.py's
    # scan_verdicts_dir can read every one of them back by (workflow, surface,
    # repo, check_type) — proving the writer/reader contract round-trips.
    with tempfile.TemporaryDirectory() as tmpdir:
        vdir = Path(tmpdir) / "verdicts"
        checks = [check, jcheck]

        def run_fn(cmd, cwd):
            if cmd == check.command:
                return fake_proc(0)
            return fake_proc(1, stderr="crash\n")

        results = rc.run_all(checks, Path("."), vdir, run_fn=run_fn)
        _check("run_all returns one result per check", len(results) == 2)
        written = sorted(p.name for p in vdir.glob("*.json"))
        _check(
            "run_all writes one file per check",
            written == ["fix-bug__vscode__kitsoki-dev__journey-verdict.json", "wf1__s1__r1__replay.json"],
        )
        scanned = gen.scan_verdicts_dir(vdir)
        _check(
            "generate.py reads the replay verdict back by its cell key",
            scanned[("wf1", "s1", "r1", "replay")].verdict == "solved",
        )
        _check(
            "generate.py reads the journey-verdict entry back by its cell key",
            scanned[("fix-bug", "vscode", "kitsoki-dev", "journey-verdict")].verdict == "blocked",
        )

    # 9. dry-run writes nothing.
    with tempfile.TemporaryDirectory() as tmpdir:
        vdir = Path(tmpdir) / "verdicts"
        rc.run_all([check], Path("."), vdir, dry_run=True)
        _check("dry-run leaves the verdicts dir empty", list(vdir.glob("*.json")) == [])

    # 10. --only filters the declared check suite by workflow id.
    with tempfile.TemporaryDirectory() as tmpdir:
        argv = ["--only", "onboard,fix-bug", "--dry-run", "--repo-root", tmpdir]
        exit_code = rc.main(argv)
        _check("--only + --dry-run runs cleanly", exit_code == 0)

    # 11. the real declared CHECKS list has no duplicate verdict filenames
    # (every cell it evidences is unique).
    names = [rc.verdict_filename(c) for c in rc.CHECKS]
    _check("declared checks have unique verdict filenames", len(names) == len(set(names)))

    # 12. the dev-story routing check (dwf3 routing triage follow-through) is
    # declared, scoped to `kitsoki test routing` (not `test intents` — the
    # Mode 0 no-LLM-tier runner, internal/testrunner/routing.go), and its
    # verdict maps the same pass/fail-tail shape a flow suite does (`test
    # routing`'s `PrintRoutingReport` ends the same way `test flows` does:
    # exit 0 on every fixture passing, exit 1 with a summary line otherwise).
    routing_checks = [c for c in rc.CHECKS if c.workflow == "routing"]
    _check("a routing CheckDef is declared", len(routing_checks) == 1)
    routing_check = routing_checks[0]
    _check("routing check runs `test routing`, not `test intents`", "routing" in routing_check.command)
    _check("routing check targets dev-story", "stories/dev-story/app.yaml" in routing_check.command)
    _check("routing check is check_type replay", routing_check.check_type == "replay")

    payload = rc.run_check(routing_check, Path("."), lambda cmd, cwd: fake_proc(0))
    _check("passing routing suite -> solved", payload["verdict"] == "solved")
    payload = rc.run_check(
        routing_check, Path("."), lambda cmd, cwd: fake_proc(1, stdout="Summary: 38/39 fixtures pass\n")
    )
    _check("failing routing suite -> failed", payload["verdict"] == "failed")
    _check("failure tail (fixture summary) surfaces in summary", "38/39 fixtures pass" in payload["summary"])

    print("PASS")


if __name__ == "__main__":
    main()
