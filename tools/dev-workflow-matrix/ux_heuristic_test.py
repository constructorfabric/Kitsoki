#!/usr/bin/env python3
"""Tests for ux_heuristic.py — the WS-G G1 `ux-heuristic` check runner.

Run directly: python3 tools/dev-workflow-matrix/ux_heuristic_test.py

Uses a FAKE dispatch function (dependency injection, per AGENTS.md) so this
suite never shells out to a real vision agent — zero LLM, zero network, zero
real subprocess. Covers: clean findings -> solved, an error-severity finding
-> failed (even if the agent self-reports overall_pass), missing captured
frames -> blocked/infra:missing-evidence, a dispatch failure ->
blocked/infra:harness, catalog loading from the REAL shared
kitsoki-ui-review heuristics.yaml, verdict-file shape validates against
`schemas/completion-state.schema.json`, the writer/reader round-trip with
generate.py's scan_verdicts_dir, and the downgrade path: a failed
ux-heuristic verdict flips a manifest `works` cell's effective status.
"""
from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent

if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))


def _load(name: str, filename: str):
    spec = importlib.util.spec_from_file_location(name, str(HERE / filename))
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


ux = _load("dwm_ux_heuristic", "ux_heuristic.py")
gen = _load("dwm_generate", "generate.py")


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def fake_dispatch_factory(response_obj: dict):
    def _dispatch(prompt: str, cwd: Path) -> str:
        return json.dumps(response_obj)

    return _dispatch


def main():
    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = Path(tmpdir)
        frame = tmp / "frames" / "fix-bug-tui.png"
        frame.parent.mkdir(parents=True)
        frame.write_bytes(b"\x89PNG\r\n\x1a\nfake-frame-bytes")

        # a tiny local catalog (not the real one) for the pure-logic cases so
        # this suite doesn't depend on the real catalog's exact contents.
        catalog_path = tmp / "heuristics.yaml"
        catalog_path.write_text(
            "checks:\n"
            "  - id: unreadable-or-broken-content\n"
            "    title: Content unreadable\n"
            "    severity: error\n"
            "    nielsen: 8\n"
            "    look_for: overlapping text\n"
            "    not_this: legible but de-emphasised text\n",
            encoding="utf-8",
        )

        check = ux.UxHeuristicCheck(
            workflow="fix-bug",
            surface="tui",
            repo="kitsoki-dev",
            frame_paths=("frames/fix-bug-tui.png",),
        )

        # 1. clean findings + overall_pass -> solved.
        dispatch = fake_dispatch_factory(
            {"findings": [], "overall_pass": True, "summary": "no issues found"}
        )
        payload = ux.run_check(check, tmp, dispatch, catalog_path=catalog_path)
        _check("clean findings -> solved", payload["verdict"] == "solved")
        _check("health is model:result", payload["health"] == "model:result")
        _check("check_type is ux-heuristic", payload["check_type"] == "ux-heuristic")
        _check("schema_version present", payload["schema_version"] == ux.SCHEMA_VERSION)
        _check("axis carries workflow/surface", payload["axis"] == {"workflow": "fix-bug", "surface": "tui"})
        _check("target_id is the repo id", payload["target_id"] == "kitsoki-dev")
        _check("evidence_refs cites the frame", payload["evidence_refs"] == ["frames/fix-bug-tui.png"])

        # 2. an error-severity finding -> failed, EVEN if the agent claims
        # overall_pass=True (the runner recomputes pass/fail authoritatively,
        # mirroring kitsoki-ui-review stage 3's "never trust the model's own
        # verdict" discipline).
        dispatch = fake_dispatch_factory(
            {
                "findings": [
                    {
                        "id": "unreadable-or-broken-content",
                        "frame": "frames/fix-bug-tui.png",
                        "severity": "error",
                        "summary": "the error banner text overlaps the status line",
                    }
                ],
                "overall_pass": True,
                "summary": "looks fine to me",
            }
        )
        payload = ux.run_check(check, tmp, dispatch, catalog_path=catalog_path)
        _check("error finding -> failed (never trusts self-reported overall_pass)", payload["verdict"] == "failed")
        _check("findings carried through", payload["findings"][0]["severity"] == "error")

        # 3. a warn-severity-only finding set can still pass (matches the
        # skill's severity discipline: warn only blocks under --strict, which
        # this runner does not implement — error is the sole hard gate here).
        dispatch = fake_dispatch_factory(
            {
                "findings": [
                    {
                        "id": "cramped-or-cluttered",
                        "frame": "frames/fix-bug-tui.png",
                        "severity": "warn",
                        "summary": "a bit tight but readable",
                    }
                ],
                "overall_pass": True,
                "summary": "one warn, nothing blocking",
            }
        )
        payload = ux.run_check(check, tmp, dispatch, catalog_path=catalog_path)
        _check("warn-only findings can still pass", payload["verdict"] == "solved")

        # 4. missing captured frame -> blocked/infra:missing-evidence, never
        # reaches dispatch (evidence is a precondition this runner checks
        # itself, not the agent).
        missing_check = ux.UxHeuristicCheck(
            workflow="fix-bug", surface="vscode", repo="kitsoki-dev", frame_paths=("frames/nope.png",)
        )

        def _should_not_be_called(prompt, cwd):
            raise AssertionError("dispatch must not be called when evidence is missing")

        payload = ux.run_check(missing_check, tmp, _should_not_be_called, catalog_path=catalog_path)
        _check("missing frame -> blocked", payload["verdict"] == "blocked")
        _check("missing frame -> infra:missing-evidence", payload["health"] == "infra:missing-evidence")

        # 5. dispatch failure -> blocked/infra:harness, never a crash.
        def _boom(prompt, cwd):
            raise ux.AgentDispatchError("vision agent crashed")

        payload = ux.run_check(check, tmp, _boom, catalog_path=catalog_path)
        _check("dispatch failure -> blocked", payload["verdict"] == "blocked")
        _check("dispatch failure -> infra:harness", payload["health"] == "infra:harness")

        # 6. catalog loading works against the REAL shared kitsoki-ui-review
        # catalog (reuse, not reinvention — plan G1).
        real_catalog = ux.load_catalog(ux.DEFAULT_CATALOG_PATH)
        _check("real heuristics.yaml loads a non-empty checks list", len(real_catalog) > 0)
        _check(
            "real catalog entries have the expected fields",
            all({"id", "severity", "look_for"} <= set(entry) for entry in real_catalog),
        )
        prompt = ux.build_prompt(check, real_catalog, [tmp / "frames" / "fix-bug-tui.png"])
        _check("prompt embeds a real catalog id", real_catalog[0]["id"] in prompt)
        _check("prompt embeds the frame path", "fix-bug-tui.png" in prompt)

        # 7. verdict-file shape validates against completion-state.schema.json.
        try:
            import jsonschema

            schema = json.loads((REPO_ROOT / "schemas" / "completion-state.schema.json").read_text())
            validator = jsonschema.Draft7Validator(schema)
            dispatch = fake_dispatch_factory({"findings": [], "overall_pass": True, "summary": "clean"})
            payload = ux.run_check(check, tmp, dispatch, catalog_path=catalog_path)
            errors = list(validator.iter_errors(payload))
            _check("verdict validates against completion-state.schema.json", errors == [])
        except ImportError:
            print("NOTE: jsonschema not installed — schema-conformance check skipped, not failed.")

        # 8. run_all() writes one file per check; filename matches
        # generate.py's scan key shape.
        with tempfile.TemporaryDirectory() as vdir_str:
            vdir = Path(vdir_str) / "verdicts"
            dispatch = fake_dispatch_factory({"findings": [], "overall_pass": True, "summary": "clean"})
            results = ux.run_all([check], tmp, vdir, dispatch=dispatch, catalog_path=catalog_path)
            _check("run_all returns one result", len(results) == 1)
            written = sorted(p.name for p in vdir.glob("*.json"))
            _check("run_all writes the expected filename", written == ["fix-bug__tui__kitsoki-dev__ux-heuristic.json"])

            scanned = gen.scan_verdicts_dir(vdir)
            key = ("fix-bug", "tui", "kitsoki-dev", "ux-heuristic")
            _check("generate.py reads the ux-heuristic verdict back by its cell key", key in scanned)
            _check("scanned verdict is solved", scanned[key].verdict == "solved")

            # 9. downgrade path: a failed ux-heuristic verdict flips a
            # manifest `works` cell's effective status.
            failing_dispatch = fake_dispatch_factory(
                {
                    "findings": [
                        {
                            "id": "unreadable-or-broken-content",
                            "frame": "frames/fix-bug-tui.png",
                            "severity": "error",
                            "summary": "banner text is unreadable",
                        }
                    ],
                    "overall_pass": False,
                    "summary": "one blocking finding",
                }
            )
            ux.run_all([check], tmp, vdir, dispatch=failing_dispatch, catalog_path=catalog_path)
            scanned = gen.scan_verdicts_dir(vdir)
            cell = {
                "workflow": "fix-bug",
                "surface": "tui",
                "repo": "kitsoki-dev",
                "status": "works",
                "reason": "bugfix TUI works end to end",
            }
            status, reason = gen.effective_status(cell, scanned, stale_days=14)
            _check("failed ux-heuristic verdict downgrades a `works` cell", status != "works")
            _check("downgrade reason cites the experience proof class", reason is not None and "experience" in reason)

        # 10. dry-run writes nothing.
        with tempfile.TemporaryDirectory() as vdir_str:
            vdir = Path(vdir_str) / "verdicts"
            ux.run_all(
                [check], tmp, vdir, dispatch=fake_dispatch_factory({}), catalog_path=catalog_path, dry_run=True
            )
            _check("dry-run leaves the verdicts dir empty", list(vdir.glob("*.json")) == [])

    # 11. --list enumerates the declared checks without dispatching.
    exit_code = ux.main(["--list"])
    _check("--list exits 0", exit_code == 0)

    # 12. --dry-run + --only filters the declared suite and never dispatches.
    with tempfile.TemporaryDirectory() as tmpdir:
        exit_code = ux.main(["--dry-run", "--only", "fix-bug", "--repo-root", tmpdir])
        _check("--dry-run + --only runs cleanly", exit_code == 0)

    # 13. the real declared CHECKS list has unique verdict filenames.
    names = [ux.verdict_filename(c) for c in ux.CHECKS]
    _check("declared checks have unique verdict filenames", len(names) == len(set(names)))

    print("PASS")


if __name__ == "__main__":
    main()
