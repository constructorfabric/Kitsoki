#!/usr/bin/env python3
"""Tests for docs_fidelity.py — the WS-G G1 `docs-fidelity` check runner.

Run directly: python3 tools/dev-workflow-matrix/docs_fidelity_test.py

Uses a FAKE dispatch function (dependency injection, per AGENTS.md) so this
suite never shells out to `claude` — zero LLM, zero network, zero real
subprocess. Covers: truthful-claims -> solved, stale/missing claims -> failed,
missing doc -> failed (a missing canonical doc IS a docs-fidelity failure,
plan G3), a dispatch failure -> blocked/infra:harness, an unparsable agent
response -> blocked/infra:harness, verdict-file shape validates against
`schemas/completion-state.schema.json`, the writer/reader round-trip with
generate.py's scan_verdicts_dir, and the downgrade path: a failed
docs-fidelity verdict flips a manifest `works` cell's effective status.
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


df = _load("dwm_docs_fidelity", "docs_fidelity.py")
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
        doc_path = tmp / "doc.md"
        doc_path.write_text("# Onboarding\n\nRun `onboard .` to get started.\n", encoding="utf-8")

        check = df.DocsFidelityCheck(
            workflow="onboard",
            surface="tui",
            repo="gears-rust",
            doc_path="doc.md",
        )

        # 1. all claims truthful + overall_pass -> solved.
        dispatch = fake_dispatch_factory(
            {
                "claims": [{"claim": "run `onboard .`", "score": "truthful", "note": "works as written"}],
                "overall_pass": True,
                "summary": "doc held up end to end",
            }
        )
        payload = df.run_check(check, tmp, dispatch)
        _check("truthful claims -> solved", payload["verdict"] == "solved")
        _check("health is model:result", payload["health"] == "model:result")
        _check("check_type is docs-fidelity", payload["check_type"] == "docs-fidelity")
        _check("schema_version present", payload["schema_version"] == df.SCHEMA_VERSION)
        _check("axis carries workflow/surface", payload["axis"] == {"workflow": "onboard", "surface": "tui"})
        _check("target_id is the repo id", payload["target_id"] == "gears-rust")
        _check("evidence_refs cites the doc", payload["evidence_refs"] == ["doc.md"])
        _check("claims carried through", payload["claims"][0]["score"] == "truthful")

        # 2. a stale claim -> failed even if the agent claims overall_pass.
        dispatch = fake_dispatch_factory(
            {
                "claims": [{"claim": "run `onboard .`", "score": "stale", "note": "command was renamed"}],
                "overall_pass": True,
                "summary": "one stale claim",
            }
        )
        payload = df.run_check(check, tmp, dispatch)
        _check("stale claim -> failed (never trusts overall_pass alone)", payload["verdict"] == "failed")

        # 3. a missing claim -> failed.
        dispatch = fake_dispatch_factory(
            {
                "claims": [{"claim": "expected next step", "score": "missing", "note": "doc never explains it"}],
                "overall_pass": False,
                "summary": "missing a step",
            }
        )
        payload = df.run_check(check, tmp, dispatch)
        _check("missing claim -> failed", payload["verdict"] == "failed")

        # 4. missing doc -> failed / model:result (a missing canonical doc IS
        # a docs-fidelity failure per plan G3, not an infra signal).
        missing_check = df.DocsFidelityCheck(
            workflow="onboard", surface="tui", repo="gears-rust", doc_path="does-not-exist.md"
        )
        payload = df.run_check(missing_check, tmp, fake_dispatch_factory({}))
        _check("missing doc -> failed", payload["verdict"] == "failed")
        _check("missing doc -> model:result (not infra)", payload["health"] == "model:result")

        # 5. dispatch failure -> blocked/infra:harness, never a crash.
        def _boom(prompt, cwd):
            raise df.AgentDispatchError("agent process crashed")

        payload = df.run_check(check, tmp, _boom)
        _check("dispatch failure -> blocked", payload["verdict"] == "blocked")
        _check("dispatch failure -> infra:harness", payload["health"] == "infra:harness")

        # 6. unparsable agent response -> blocked/infra:harness, never a crash.
        payload = df.run_check(check, tmp, lambda prompt, cwd: "not json at all")
        _check("unparsable response -> blocked", payload["verdict"] == "blocked")
        _check("unparsable response -> infra:harness", payload["health"] == "infra:harness")

        # 7. verdict-file shape validates against completion-state.schema.json.
        try:
            import jsonschema

            schema = json.loads((REPO_ROOT / "schemas" / "completion-state.schema.json").read_text())
            validator = jsonschema.Draft7Validator(schema)
            dispatch = fake_dispatch_factory(
                {"claims": [], "overall_pass": True, "summary": "clean"}
            )
            payload = df.run_check(check, tmp, dispatch)
            errors = list(validator.iter_errors(payload))
            _check("verdict validates against completion-state.schema.json", errors == [])
        except ImportError:
            print("NOTE: jsonschema not installed — schema-conformance check skipped, not failed.")

        # 8. run_all() writes one file per check; filename matches generate.py's
        # scan key shape (workflow__surface__repo__check_type.json).
        with tempfile.TemporaryDirectory() as vdir_str:
            vdir = Path(vdir_str) / "verdicts"
            dispatch = fake_dispatch_factory(
                {"claims": [], "overall_pass": True, "summary": "clean"}
            )
            results = df.run_all([check], tmp, vdir, dispatch=dispatch)
            _check("run_all returns one result", len(results) == 1)
            written = sorted(p.name for p in vdir.glob("*.json"))
            _check("run_all writes the expected filename", written == ["onboard__tui__gears-rust__docs-fidelity.json"])

            scanned = gen.scan_verdicts_dir(vdir)
            key = ("onboard", "tui", "gears-rust", "docs-fidelity")
            _check("generate.py reads the docs-fidelity verdict back by its cell key", key in scanned)
            _check("scanned verdict is solved", scanned[key].verdict == "solved")

            # 9. downgrade path: a failed docs-fidelity verdict flips a
            # manifest `works` cell's effective status (generate.py's
            # experience-check-type ingestion, EXPERIENCE_CHECK_TYPES already
            # includes docs-fidelity).
            failing_dispatch = fake_dispatch_factory(
                {
                    "claims": [{"claim": "x", "score": "stale", "note": "rotted"}],
                    "overall_pass": False,
                    "summary": "docs rotted",
                }
            )
            df.run_all([check], tmp, vdir, dispatch=failing_dispatch)
            scanned = gen.scan_verdicts_dir(vdir)
            cell = {
                "workflow": "onboard",
                "surface": "tui",
                "repo": "gears-rust",
                "status": "works",
                "reason": "onboarding works end to end",
            }
            status, reason = gen.effective_status(cell, scanned, stale_days=14)
            _check("failed docs-fidelity verdict downgrades a `works` cell", status != "works")
            _check("downgrade reason cites the experience proof class", reason is not None and "experience" in reason)

        # 10. dry-run writes nothing.
        with tempfile.TemporaryDirectory() as vdir_str:
            vdir = Path(vdir_str) / "verdicts"
            df.run_all([check], tmp, vdir, dispatch=fake_dispatch_factory({}), dry_run=True)
            _check("dry-run leaves the verdicts dir empty", list(vdir.glob("*.json")) == [])

    # 11. --list enumerates the declared checks without dispatching (no doc
    # files exist at REPO_ROOT-relative paths in a temp cwd, so any dispatch
    # attempt would blow up loudly — --list must never reach dispatch).
    exit_code = df.main(["--list"])
    _check("--list exits 0", exit_code == 0)

    # 12. --dry-run + --only filters the declared suite and never dispatches.
    with tempfile.TemporaryDirectory() as tmpdir:
        exit_code = df.main(["--dry-run", "--only", "onboard", "--repo-root", tmpdir])
        _check("--dry-run + --only runs cleanly", exit_code == 0)

    # 13. the real declared CHECKS list has unique verdict filenames.
    names = [df.verdict_filename(c) for c in df.CHECKS]
    _check("declared checks have unique verdict filenames", len(names) == len(set(names)))

    print("PASS")


if __name__ == "__main__":
    main()
