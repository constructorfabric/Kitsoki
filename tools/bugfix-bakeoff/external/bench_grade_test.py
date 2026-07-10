#!/usr/bin/env python3
"""Free, no-LLM regression tests for the deterministic grade + the cell→deck seam.

Run: python3 bench_grade_test.py   (exit 0 = pass). Guards two dogfood finds:
  1. decide_quality: a suite-DISABLED project (kitsoki/gears-rust) reaches `solved`
     on the oracle alone — otherwise the escalation ladder never stops.
  2. aggregate.py consumes a bench.py-format cell (None metrics / unmeasured
     compliance) without KeyError.
"""
import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
from contextlib import redirect_stdout
from pathlib import Path

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


bench = _load("bench", os.path.join(HERE, "bench.py"))
aggregate = _load("aggregate", os.path.join(HERE, "..", "aggregate.py"))


def test_decide_quality():
    dq = bench.decide_quality
    # suite DISABLED: oracle alone decides solved (the escalation-ladder fix).
    assert dq(True, None, False) == "solved", "suite-disabled + oracle GREEN must be solved"
    assert dq(False, None, False) == "failed"
    # suite ENABLED: oracle GREEN but suite RED ⇒ partial; both GREEN ⇒ solved.
    assert dq(True, False, True) == "partial"
    assert dq(True, True, True) == "solved"
    assert dq(False, True, True) == "failed"


def test_aggregate_tolerates_bench_cell():
    # A bench.py-shaped cell: None metrics, unmeasured compliance.
    cell = {
        "project": "kitsoki", "bug": "bug9", "candidate": "glm-5.2", "treatment": "kitsoki",
        "model": "GLM-5.2", "effort": "medium", "provider": "synthetic.new",
        "outcome": {"quality": "solved"},
        "compliance": {"rate": None},
        "metrics": {"cost_usd": 0.6, "total_tokens": 2900, "wall_time_s": None,
                    "guidance_turns": 0, "agent_calls": 2},
    }
    manifest = {"project": {"id": "kitsoki"}, "bugs": [], "candidates": [], "treatments": ["kitsoki"]}
    summary = aggregate.build_summary(manifest, [cell], "2026-06-26T00:00:00Z")
    bucket = summary["rollup"]["by_candidate"]["glm-5.2"]
    assert bucket["solved"] == 1 and bucket["solve_rate"] == 1.0
    assert bucket["avg_cost_usd"] == 0.6
    # also the agenteval report path (the deck source) must not KeyError
    aggregate.build_agenteval_reports(manifest, [cell], "2026-06-26T00:00:00Z")


def test_preflight_candidate_profile_and_local_repo_checks():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: cheap\n"
            "    profile: missing-profile\n"
            "    model: test-model\n"
            "    effort: low\n"
        )
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        oracle = oracle_dir / "bug1.test"
        oracle.write_text("oracle")
        manifest = {
            "project": {"id": "demo", "local_only": True, "repo": "local"},
            "bugs": [{
                "id": "bug1",
                "baseline_sha": "abc123",
                "fix_sha": "def456",
                "oracle_test": "oracles/bug1.test",
            }],
            "_dir": manifest_dir,
        }
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            with redirect_stdout(out):
                rc = bench.preflight(manifest, candidate="cheap", candidates_path=str(candidates))
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 1
        text = out.getvalue()
        assert "missing-profile" in text
        assert "DEMO_REPO" in text


def test_resolve_repo_dir_accepts_meta_repo_subdir_or_standalone_checkout():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        meta = root / "meta-repo"
        sub = meta / "src" / "cyberfabric" / "gears-rust"
        sub.mkdir(parents=True)
        subprocess.run(["git", "init", "-q", str(sub)], check=True)
        (sub / "case.txt").write_text("baseline\n")
        subprocess.run(["git", "-C", str(sub), "add", "case.txt"], check=True)
        subprocess.run([
            "git", "-C", str(sub),
            "-c", "user.name=Test",
            "-c", "user.email=test@example.com",
            "commit", "-q", "-m", "baseline",
        ], check=True)
        baseline = subprocess.check_output(["git", "-C", str(sub), "rev-parse", "HEAD"], text=True).strip()
        (sub / "case.txt").write_text("fix\n")
        subprocess.run(["git", "-C", str(sub), "add", "case.txt"], check=True)
        subprocess.run([
            "git", "-C", str(sub),
            "-c", "user.name=Test",
            "-c", "user.email=test@example.com",
            "commit", "-q", "-m", "fix",
        ], check=True)
        fix = subprocess.check_output(["git", "-C", str(sub), "rev-parse", "HEAD"], text=True).strip()
        manifest = {
            "project": {
                "id": "gears-rust",
                "local_only": True,
                "repo_envs": ["BUGFIX_BAKEOFF_REPO", "BUGFIX_BAKEOFF_META_REPO"],
                "repo_subdir": "src/cyberfabric/gears-rust",
            },
            "bugs": [{
                "id": "bug1",
                "baseline_sha": baseline,
                "fix_sha": fix,
            }],
            "_dir": root,
        }
        resolved = bench.resolve_repo_dir(manifest, str(meta))
        assert resolved["ok"] is True
        assert resolved["path"] == str(sub)
        assert resolved["repo_subdir"] == "src/cyberfabric/gears-rust"

        standalone = root / "gears-rust"
        subprocess.run(["git", "clone", "-q", str(sub), str(standalone)], check=True)
        resolved = bench.resolve_repo_dir(manifest, str(standalone))
        assert resolved["ok"] is True
        assert resolved["path"] == str(standalone)

        missing_submodule = root / "missing-submodule"
        subprocess.run(["git", "init", "-q", str(missing_submodule)], check=True)
        resolved = bench.resolve_repo_dir(manifest, str(missing_submodule))
        assert resolved["ok"] is False
        assert "repo_subdir does not exist" in resolved["errors"][0]


def test_repo_history_capsule_catalog_has_the_promoted_ten():
    projects = ["query-string", "gears-rust", "kitsoki"]
    manifests = bench.load_project_manifests(projects)
    capsules = [
        bench.repo_history_capsule_record(m, bug)
        for m in manifests
        for bug in bench.selected_bugs(m)
    ]
    ids = {item["id"] for item in capsules}
    assert len(capsules) == 10
    assert {"query-string/qs1", "gears-rust/bug1", "kitsoki/bug9"} <= ids
    gears = next(item for item in capsules if item["id"] == "gears-rust/bug1")
    assert gears["capsule_ref"] == "repo-history/gears-rust/bug1"
    assert gears["kind"] == "repo-history"
    assert gears["materializer"] == "bugfix-bakeoff"
    assert gears["repo_modes"] == ["single-repo", "meta-repo"]
    assert gears["repo_envs"][:2] == ["BUGFIX_BAKEOFF_REPO", "BUGFIX_BAKEOFF_META_REPO"]
    assert gears["green_red_green"][1].startswith("RED:")


def test_preflight_scopes_to_selected_bugs():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: ready\n"
            "    profile: ready-profile\n"
        )
        (root / ".kitsoki.yaml").write_text(
            "harness_profiles:\n"
            "  ready-profile:\n"
            "    backend: replay\n"
        )
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        (oracle_dir / "bug1.test").write_text("oracle")
        manifest = {
            "project": {"id": "demo"},
            "bugs": [
                {"id": "bug1", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug1.test"},
                {"id": "bug2", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/missing.test"},
            ],
            "_dir": manifest_dir,
        }
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            with redirect_stdout(out):
                rc = bench.preflight(manifest, candidate="ready", candidates_path=str(candidates), bug_ids="bug1")
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        text = out.getvalue()
        assert '"bugs": [\n    "bug1"\n  ]' in text
        assert "missing.test" not in text


def test_summarize_empty_results_fails_loudly_by_default():
    with tempfile.TemporaryDirectory() as td:
        manifest = {"project": {"id": "demo"}, "bugs": [], "_dir": Path(td)}
        rel = os.path.relpath(Path(td) / "empty-results", bench.HERE)
        out = io.StringIO()
        with redirect_stdout(out):
            rc = bench.summarize(manifest, rel)
        assert rc == 1
        text = out.getvalue()
        assert '"error": "no scored cells"' in text
        assert "drive_cell.sh --score" in text


def test_drive_plan_renders_exact_matrix_commands():
    manifest = {
        "project": {"id": "demo"},
        "bugs": [
            {"id": "bug1", "baseline_sha": "abc", "oracle_test": "oracles/bug1"},
            {"id": "bug2", "baseline_sha": "def", "oracle_test": "oracles/bug2"},
        ],
    }
    out = io.StringIO()
    with redirect_stdout(out):
        rc = bench.drive_plan(manifest, bug_ids="bug1,bug2", candidate="cheap,strong", repo_dir="/tmp/demo")
    assert rc == 0
    text = out.getvalue()
    assert "--bug bug1 --candidate cheap --repo-dir /tmp/demo --score" in text
    assert "--bug bug2 --candidate strong --repo-dir /tmp/demo --score" in text
    assert '"commands": [' in text


def test_pending_cell_rolls_up_separately_from_failures():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: cheap\n"
            "    profile: cheap-profile\n"
        )
        (root / ".kitsoki.yaml").write_text(
            "harness_profiles:\n"
            "  cheap-profile:\n"
            "    backend: replay\n"
        )
        (root / "oracles").mkdir()
        (root / "oracles" / "bug1").write_text("oracle")
        manifest = {
            "project": {"id": "demo"},
            "bugs": [{"id": "bug1", "baseline_sha": "abc", "oracle_test": "oracles/bug1"}],
            "_dir": root,
        }
        results = root / "results"
        out_cell = results / "cells" / "demo-bug1-cheap-kitsoki.json"
        out = io.StringIO()
        with redirect_stdout(out):
            rc = bench.pending_cell(manifest, "bug1", "cheap", "provider rate limited", str(out_cell))
        assert rc == 0
        assert out_cell.exists()
        summary_out = io.StringIO()
        rel = os.path.relpath(results, bench.HERE)
        markdown = root / "report.md"
        with redirect_stdout(summary_out):
            rc = bench.summarize(manifest, rel, markdown=str(markdown))
        assert rc == 0
        summary = json_load(summary_out.getvalue())
        bucket = summary["rollup"]["by_candidate"]["cheap"]
        assert bucket["pending"] == 1
        assert bucket["failed"] == 0
        assert bucket["attempted"] == 0
        assert bucket["solve_rate"] == 0.0
        text = markdown.read_text()
        assert "demo bake-off: 0/0 attempted solved; 1 pending" in text
        assert "| cheap | 1 | 0 | 0 | 0 | 1 | 0% |" in text
        assert "| bug1 | cheap | pending | pending |" in text

        completion_out = io.StringIO()
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            with redirect_stdout(completion_out):
                rc = bench.completion(
                    manifest,
                    candidate="cheap",
                    candidates_path=str(candidates),
                    bug_ids="bug1",
                    results_dir=rel,
                    armed=True,
                    require_result_evidence=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        completion = json_load(completion_out.getvalue())
        assert completion["ok"] is True
        assert completion["status"] == "complete-with-pending"
        assert completion["checks"]["result_evidence_complete"] is True
        assert completion["checks"]["live_scored"] is False
        assert completion["results"]["pending_cells"] == 1
        assert completion["results"]["attempted_cells"] == 0
        assert any("pending" in b for b in completion["blockers"])

        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            with redirect_stdout(io.StringIO()):
                rc = bench.completion(
                    manifest,
                    candidate="cheap",
                    candidates_path=str(candidates),
                    bug_ids="bug1",
                    results_dir=rel,
                    armed=True,
                    require_live_scored=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 1


def test_readiness_reports_missing_and_scored_cells():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        candidates = root / "candidates.yaml"
        candidates.write_text(
            "candidates:\n"
            "  - key: ready\n"
            "    profile: ready-profile\n"
        )
        (root / ".kitsoki.yaml").write_text(
            "harness_profiles:\n"
            "  ready-profile:\n"
            "    backend: replay\n"
        )
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        (oracle_dir / "bug1.test").write_text("oracle")
        (oracle_dir / "bug2.test").write_text("oracle")
        manifest = {
            "project": {"id": "demo"},
            "bugs": [
                {"id": "bug1", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug1.test"},
                {"id": "bug2", "baseline_sha": "abc123", "fix_sha": "def456", "oracle_test": "oracles/bug2.test"},
            ],
            "_dir": manifest_dir,
        }
        results = root / "results"
        out_cell = results / "cells" / "demo-bug1-ready-kitsoki.json"
        with redirect_stdout(io.StringIO()):
            bench.pending_cell(manifest, "bug1", "ready", "provider blocked", str(out_cell))
        stale_cell = results / "cells" / "demo-bug2-ready-kitsoki.json"
        stale_cell.write_text(json.dumps({
            "project": "demo",
            "bug": "bug2",
            "candidate": "ready",
            "treatment": "kitsoki",
            "outcome": {"quality": "solved"},
        }))
        prepared = root / "prepared" / "demo-bug2-ready.json"
        prepared.parent.mkdir(parents=True)
        (root / "cells" / "demo-bug2-ready").mkdir(parents=True)
        (root / "prompts").mkdir()
        (root / "prompts" / "demo-bug2-ready.md").write_text("prompt")
        (root / "preflight").mkdir()
        (root / "preflight" / "demo-bug2-ready.json").write_text("{}")
        prepared.write_text(json.dumps({
            "project": "demo",
            "bug": "bug2",
            "candidate": "ready",
            "worktree": str(root / "cells" / "demo-bug2-ready"),
            "prompt": str(root / "prompts" / "demo-bug2-ready.md"),
            "preflight": str(root / "preflight" / "demo-bug2-ready.json"),
            "trace": str(root / "traces" / "demo-bug2-ready.jsonl"),
        }))
        stale = root / "prepared" / "demo-bug1-ready.json"
        stale.write_text(json.dumps({
            "project": "demo",
            "bug": "bug1",
            "candidate": "ready",
            "worktree": str(root / "cells" / "missing"),
            "prompt": str(root / "prompts" / "missing.md"),
            "preflight": str(root / "preflight" / "missing.json"),
        }))
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            out = io.StringIO()
            markdown = root / "ready.md"
            rel = os.path.relpath(results, bench.HERE)
            with redirect_stdout(out):
                rc = bench.readiness(
                    manifest,
                    candidate="ready",
                    candidates_path=str(candidates),
                    bug_ids="bug1,bug2",
                    results_dir=rel,
                    markdown=str(markdown),
                    armed=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        report = json_load(out.getvalue())
        assert report["results"]["selected_cells"] == 2
        assert report["results"]["scored_cells"] == 1
        assert report["results"]["missing_cells"] == 1
        assert report["results"]["stale_result_cells"] == 1
        assert report["results"]["prepared_cells"] == 1
        assert report["results"]["stale_prepared_cells"] == 1
        assert report["results"]["unprepared_cells"] == 1
        assert report["arming"]["verified"] is True
        assert report["prepared"]["cells"][0]["bug"] == "bug2"
        assert report["unprepared"][0]["bug"] == "bug1"
        assert "--no-drive" in report["unprepared"][0]["command"]
        assert "--no-drive" in report["prepared"]["stale"][0]["command"]
        assert "/../" not in report["prepared"]["cells"][0]["_path"]
        assert report["missing"][0]["bug"] == "bug2"
        assert report["stale_results"][0]["bug"] == "bug2"
        assert "missing baseline_sha" in report["stale_results"][0]["stale_reason"]
        assert "bench.py pending" in report["missing"][0]["pending_command"]
        text = markdown.read_text()
        assert "Preflight: ready" in text
        assert "Arming: verified" in text
        assert "Stale result cells: 1" in text
        assert "Prepared cells: 1" in text
        assert "Stale prepared cells: 1" in text
        assert "Unprepared cells: 1" in text
        assert "## What This Proves" in text
        assert "verified RED at the historical baseline and GREEN at the real fix" in text
        assert "not attempted or not recorded, not failed" in text
        assert "## Prepared Cells" in text
        assert "metadata `" in text
        assert "demo-bug2-ready.json" in text
        assert "## Stale Prepared Metadata" in text
        assert "demo-bug1-ready.json" in text
        assert "refresh with `" in text
        assert "## Unprepared Cells" in text
        assert "--no-drive" in text
        assert "`bug2` x `ready`" in text
        assert "## Pending Alternatives" in text
        assert "--reason \"<reason>\"" in text
        assert "## Stale Result Artifacts" in text
        assert "demo-bug2-ready-kitsoki.json" in text

        out = io.StringIO()
        completion_md = root / "completion.md"
        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            with redirect_stdout(out):
                rc = bench.completion(
                    manifest,
                    candidate="ready",
                    candidates_path=str(candidates),
                    bug_ids="bug1,bug2",
                    results_dir=rel,
                    markdown=str(completion_md),
                    armed=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 0
        completion = json_load(out.getvalue())
        assert completion["status"] == "blocked"
        assert completion["checks"]["no_cost_ready"] is True
        assert completion["checks"]["ready_to_drive"] is False
        assert completion["checks"]["result_evidence_complete"] is False
        assert completion["checks"]["live_scored"] is False
        assert completion["results"]["selected_cells"] == 2
        assert completion["results"]["result_cells"] == 1
        assert completion["results"]["missing_cells"] == 1
        assert completion["results"]["stale_result_cells"] == 1
        assert any("prepared handoff" in b for b in completion["blockers"])
        assert any("stale" in b for b in completion["blockers"])
        assert any("need a scored or pending result" in b for b in completion["blockers"])
        completion_text = completion_md.read_text()
        assert "No-cost ready: yes" in completion_text
        assert "Ready to drive live: no" in completion_text
        assert "Result evidence complete: no" in completion_text
        assert "Live scored capability result: no" in completion_text
        assert "## Drive Commands" in completion_text
        assert "## Pending Commands" in completion_text

        old_root = bench.REPO_ROOT
        bench.REPO_ROOT = root
        try:
            with redirect_stdout(io.StringIO()):
                rc = bench.completion(
                    manifest,
                    candidate="ready",
                    candidates_path=str(candidates),
                    bug_ids="bug1,bug2",
                    results_dir=rel,
                    armed=True,
                    require_result_evidence=True,
                )
        finally:
            bench.REPO_ROOT = old_root
        assert rc == 1


def test_readiness_markdown_uses_singular_missing_cell_wording():
    report = {
        "project": "demo",
        "preflight": {"ok": True, "errors": [], "warnings": []},
        "drive_plan": {"markdown": "- `bug1` x `ready`: `drive`"},
        "results": {
            "selected_cells": 1,
            "scored_cells": 0,
            "missing_cells": 1,
            "prepared_cells": 0,
            "stale_prepared_cells": 0,
            "unprepared_cells": 1,
        },
        "arming": {"verified": True},
        "missing": [{
            "bug": "bug1",
            "candidate": "ready",
            "command": "drive",
            "pending_command": "pending",
        }],
        "prepared": {"cells": []},
        "unprepared": [{
            "bug": "bug1",
            "candidate": "ready",
            "command": "drive --no-drive",
        }],
        "next_actions": ["drive it"],
    }
    with tempfile.TemporaryDirectory() as td:
        markdown = Path(td) / "ready.md"
        bench.write_readiness_markdown(report, markdown)
        text = markdown.read_text()
        assert "1 of 1 selected cell has no result artifact yet" in text
        assert "1 of 1 selected cells have no result artifacts yet" not in text


def test_drive_cell_preflight_scopes_to_requested_bug():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        repo = root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        (repo / "lib.txt").write_text("baseline\n")
        subprocess.run(["git", "add", "lib.txt"], cwd=repo, check=True)
        subprocess.run(
            ["git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "baseline"],
            cwd=repo,
            check=True,
        )
        baseline = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        project = "drive-scope-demo"
        manifest_dir = Path(HERE) / "projects" / project
        cache = root / "cache"
        try:
            (manifest_dir / "oracles").mkdir(parents=True)
            (manifest_dir / "oracles" / "bug1.test").write_text("oracle")
            (manifest_dir / "manifest.yaml").write_text(
                "project:\n"
                f"  id: {project}\n"
                "  repo: local\n"
                "  local_only: true\n"
                "bugs:\n"
                "  - id: bug1\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/bug1.test\n"
                "  - id: bug2\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/missing.test\n"
            )
            env = {**os.environ, "EXTERNAL_BAKEOFF_CACHE": str(cache)}
            r = subprocess.run(
                [
                    str(Path(HERE) / "drive_cell.sh"),
                    "--project", project,
                    "--bug", "bug1",
                    "--candidate", "opus-4.8",
                    "--repo-dir", str(repo),
                    "--no-drive",
                ],
                cwd=Path(HERE).parents[2],
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            assert r.returncode == 0, r.stdout + r.stderr
            preflight = json_load((cache / "preflight" / f"{project}-bug1-opus-4.8.json").read_text())
            assert preflight["bugs"] == ["bug1"]
            assert preflight["errors"] == []
            prepared = json_load((cache / "prepared" / f"{project}-bug1-opus-4.8.json").read_text())
            assert prepared["project"] == project
            assert prepared["bug"] == "bug1"
            assert prepared["candidate"] == "opus-4.8"
            assert Path(prepared["worktree"]).exists()
            assert Path(prepared["prompt"]).exists()
            assert prepared["preflight"].endswith(f"{project}-bug1-opus-4.8.json")
            old_here = bench.HERE
            bench.HERE = Path(HERE)
            try:
                out = io.StringIO()
                rel = os.path.relpath(cache / "results", bench.HERE)
                with redirect_stdout(out):
                    rc = bench.audit_handoffs(
                        bench.load(project),
                        results_dir=rel,
                        candidate="opus-4.8",
                        bug_ids="bug1",
                    )
            finally:
                bench.HERE = old_here
            assert rc == 0
            audit = json_load(out.getvalue())
            assert audit["ok"] is True
            assert audit["prepared_cells"] == 1
        finally:
            shutil.rmtree(manifest_dir, ignore_errors=True)


def test_drive_cell_score_writes_completion_state_without_live_drive():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        repo = root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        (repo / "lib.txt").write_text("baseline\n")
        subprocess.run(["git", "add", "lib.txt"], cwd=repo, check=True)
        subprocess.run(
            ["git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "baseline"],
            cwd=repo,
            check=True,
        )
        baseline = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        project = "drive-completion-demo"
        manifest_dir = Path(HERE) / "projects" / project
        cache = root / "cache"
        completion_state = root / "completion-state.json"
        try:
            (manifest_dir / "oracles").mkdir(parents=True)
            (manifest_dir / "oracles" / "bug1.txt").write_text("pass\n")
            (manifest_dir / "manifest.yaml").write_text(
                "project:\n"
                f"  id: {project}\n"
                "  repo: local\n"
                "  local_only: true\n"
                "  suite: false\n"
                "  oracle:\n"
                "    target: oracle.txt\n"
                "    inject: write\n"
                "    run: \"test \\\"$(cat {target})\\\" = pass\"\n"
                "bugs:\n"
                "  - id: bug1\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/bug1.txt\n"
            )
            env = {
                **os.environ,
                "EXTERNAL_BAKEOFF_CACHE": str(cache),
                "EXTERNAL_BAKEOFF_FAKE_DRIVE_SUCCESS": "1",
            }
            r = subprocess.run(
                [
                    str(Path(HERE) / "drive_cell.sh"),
                    "--project", project,
                    "--bug", "bug1",
                    "--candidate", "opus-4.8",
                    "--repo-dir", str(repo),
                    "--score",
                    "--no-docker-score",
                    "--completion-state", str(completion_state),
                ],
                cwd=Path(HERE).parents[2],
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            assert r.returncode == 0, r.stdout + r.stderr
            payload = json_load(completion_state.read_text())
            assert payload["schema_version"] == bench.COMPLETION_STATE_SCHEMA_VERSION
            assert payload["verdict"] == "solved"
            assert payload["health"] == "model:result"
            assert payload["target_id"] == project
            assert payload["variant_id"] == "opus-4.8"
            assert payload["axis"] == {"bug": "bug1"}
        finally:
            shutil.rmtree(manifest_dir, ignore_errors=True)


def test_prepare_handoffs_wraps_no_drive_and_audit():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        repo = root / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        (repo / "lib.txt").write_text("baseline\n")
        subprocess.run(["git", "add", "lib.txt"], cwd=repo, check=True)
        subprocess.run(
            ["git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "baseline"],
            cwd=repo,
            check=True,
        )
        baseline = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

        project = "prepare-handoffs-demo"
        manifest_dir = Path(HERE) / "projects" / project
        cache = root / "cache"
        markdown = root / "handoffs.md"
        try:
            (manifest_dir / "oracles").mkdir(parents=True)
            (manifest_dir / "oracles" / "bug1.test").write_text("oracle")
            (manifest_dir / "manifest.yaml").write_text(
                "project:\n"
                f"  id: {project}\n"
                "  repo: local\n"
                "  local_only: true\n"
                "bugs:\n"
                "  - id: bug1\n"
                "    title: wrapped no-drive prep\n"
                "    ticket: prepare a scoped handoff without leaking the oracle\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/bug1.test\n"
                "  - id: bug2\n"
                f"    baseline_sha: {baseline}\n"
                f"    fix_sha: {baseline}\n"
                "    oracle_test: oracles/missing.test\n"
            )
            rel_results = os.path.relpath(cache / "results", Path(HERE))
            env = {**os.environ, "EXTERNAL_BAKEOFF_CACHE": str(cache)}
            r = subprocess.run(
                [
                    str(Path(HERE) / "prepare_handoffs.sh"),
                    "--project", project,
                    "--bug", "bug1",
                    "--candidate", "opus-4.8",
                    "--repo-dir", str(repo),
                    "--results", rel_results,
                    "--markdown", str(markdown),
                ],
                cwd=Path(HERE).parents[2],
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            assert r.returncode == 0, r.stdout + r.stderr
            audit = json_load(r.stdout)
            assert audit["ok"] is True
            assert audit["selected_cells"] == 1
            assert audit["prepared_cells"] == 1
            assert audit["missing_prepared_cells"] == 0
            assert markdown.exists()
            preflight = json_load((cache / "preflight" / f"{project}-bug1-opus-4.8.json").read_text())
            assert preflight["bugs"] == ["bug1"]
            prepared = json_load((cache / "prepared" / f"{project}-bug1-opus-4.8.json").read_text())
            assert Path(prepared["prompt"]).exists()
            assert Path(prepared["worktree"]).exists()
            assert not (cache / "prepared" / f"{project}-bug2-opus-4.8.json").exists()
        finally:
            shutil.rmtree(manifest_dir, ignore_errors=True)


def test_audit_handoffs_rejects_oracle_and_real_fix_leaks():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        manifest_dir = root / "projects" / "demo"
        oracle_dir = manifest_dir / "oracles"
        oracle_dir.mkdir(parents=True)
        oracle_line = "assert_eq!(loaded_config.modules.len(), 1, \"duplicate module was rejected\");"
        (oracle_dir / "bug1.rs").write_text(f"#[test]\nfn oracle() {{\n    {oracle_line}\n}}\n")
        worktree = root / "cells" / "demo-bug1-ready"
        worktree.mkdir(parents=True)
        preflight = root / "preflight" / "demo-bug1-ready.json"
        preflight.parent.mkdir()
        preflight.write_text("{}")
        prompt = root / "prompts" / "demo-bug1-ready.md"
        prompt.parent.mkdir()
        prompt.write_text(
            "Drive ONE kitsoki bug-fix pipeline cell\n"
            'profile: "ready-profile"\n'
            'ticket_id: "bug1"\n'
            'ticket_title: "Leaky bug — reproduce it"\n'
            'workdir: "' + str(worktree) + '"\n'
            'workspace_id: ""\n'
            "Do not use shell\n"
            "Hidden hints: oracles/bug1.rs abcdef12 src/fix.rs cargo test oracle_bug1\n"
            f"{oracle_line}\n"
        )
        prepared_dir = root / "prepared"
        prepared_dir.mkdir()
        (prepared_dir / "demo-bug1-ready.json").write_text(json.dumps({
            "project": "demo",
            "bug": "bug1",
            "candidate": "ready",
            "profile": "ready-profile",
            "worktree": str(worktree),
            "branch": "bench-demo-bug1",
            "baseline_sha": "1234567",
            "trace": str(root / "traces" / "demo-bug1-ready.jsonl"),
            "thread": str(root / "threads" / "demo-bug1-ready.md"),
            "prompt": str(prompt),
            "preflight": str(preflight),
            "score_result": str(root / "results" / "cells" / "demo-bug1-ready-kitsoki.json"),
        }))
        manifest = {
            "project": {"id": "demo"},
            "bugs": [{
                "id": "bug1",
                "title": "Leaky bug",
                "ticket": "reproduce it",
                "baseline_sha": "1234567",
                "fix_sha": "abcdef12",
                "fix_source": "src/fix.rs",
                "oracle_test": "oracles/bug1.rs",
                "oracle": {"target": "tests/oracle_bug1.rs", "run": "cargo test oracle_bug1"},
            }],
            "_dir": manifest_dir,
        }
        out = io.StringIO()
        rel = os.path.relpath(root / "results", bench.HERE)
        with redirect_stdout(out):
            rc = bench.audit_handoffs(manifest, results_dir=rel, candidate="ready", bug_ids="bug1")
        assert rc == 1
        report = json_load(out.getvalue())
        assert report["ok"] is False
        joined = "\n".join(report["errors"])
        assert "prompt leaks hidden oracle_test" in joined
        assert "prompt leaks hidden fix_sha" in joined
        assert "prompt leaks hidden fix_source" in joined
        assert "prompt leaks hidden oracle.run" in joined
        assert "prompt leaks hidden oracle content" in joined


def test_score_cli_exits_zero_on_failed_verdict():
    """A completed grade exits 0 whether the oracle PASSES or FAILS — the verdict
    is DATA in the result JSON, not the process exit status. Regression for the
    VM bake-off hang: score() returns 1 on oracle-fail (correct, for in-process
    verify()), but the `score` CLI propagating that made a legitimate `failed`
    cell look like a transient error to drive_cell.sh's run_with_retry, which
    then burned the whole docker+host backoff ladder (~hours) on an
    already-successful score."""
    for run_cmd, want_oracle, want_quality in (("false", False, "failed"),
                                               ("true", True, "solved")):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            (root / "oracles").mkdir()
            # Trivial standalone oracle file (content is irrelevant — the `run`
            # command decides pass/fail). inject: write drops it at `target`.
            (root / "oracles" / "t.txt").write_text("oracle marker\n")
            manifest = root / "manifest.yaml"
            manifest.write_text(
                "kind: bugfix_bakeoff_external\nversion: 1\n"
                "project:\n  id: tinyproj\n  repo: .\n  local_only: true\n"
                "  install: \"\"\n  suite: false\n"
                "  oracle:\n    inject: write\n    target: t_oracle.txt\n"
                f"    run: '{run_cmd}'\n"
                "bugs:\n  - id: b1\n    title: tiny\n    fix_sha: \"\"\n"
                "    baseline_sha: \"\"\n    fix_source: \".\"\n"
                "    oracle_test: oracles/t.txt\n    oracle_match: \"\"\n"
            )
            tree = root / "tree"
            tree.mkdir()
            (tree / "keep.txt").write_text("candidate work\n")
            out = root / "result.json"
            r = subprocess.run(
                [sys.executable, os.path.join(HERE, "bench.py"), "score",
                 "--project", str(manifest), "--bug", "b1",
                 "--tree", str(tree), "--out", str(out)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            assert r.returncode == 0, (
                f"score CLI must exit 0 on a completed grade (run={run_cmd}); "
                f"got {r.returncode}\nstderr:\n{r.stderr}")
            res = json.loads(out.read_text())
            assert res["outcome"]["oracle_pass"] is want_oracle, res["outcome"]
            assert res["outcome"]["quality"] == want_quality, res["outcome"]
    # The in-process function still surfaces the verdict for verify().
    # (oracle_pass=False ⇒ returns 1) — covered implicitly by verify()'s
    # RED/GREEN logic; here we only guard the CLI exit contract.


def _write_trace(path, events):
    path.write_text("\n".join(json.dumps(e) for e in events) + "\n")


def test_classify_cell_separates_infra_from_model():
    bench = _load("bench_classify", os.path.join(HERE, "bench.py"))
    with tempfile.TemporaryDirectory() as td:
        tdp = Path(td)

        # Worker never ran: no streams, no completed calls.
        never = tdp / "never.jsonl"
        _write_trace(never, [
            {"kind": "session.header", "payload": {}},
            {"kind": "harness.dispatched", "payload": {}},
        ])
        assert bench.classify_cell(str(never))["class"] == "infra:worker-never-ran"

        # Silent stall: a start with no matching complete, no terminal, ends there.
        stall = tdp / "stall.jsonl"
        _write_trace(stall, [
            {"kind": "agent.call.start", "call_id": "r1", "state_path": "bf.reproducing", "payload": {"agent": "bf__reproducer"}},
            {"kind": "agent.stream", "state_path": "bf.reproducing", "payload": {}},
            {"kind": "agent.call.complete", "call_id": "r1", "state_path": "bf.reproducing", "payload": {"agent": "bf__reproducer"}},
            {"kind": "agent.call.start", "call_id": "j1", "state_path": "bf.reproducing", "payload": {"agent": "bf__judge"}},
        ])
        st = bench.classify_cell(str(stall))
        assert st["class"] == "infra:stall", st
        assert st["evidence"]["stalled_agent"] == "bf__judge", st

        # Host/env error: complete calls then a git-identity commit failure, no terminal.
        host = tdp / "host.jsonl"
        _write_trace(host, [
            {"kind": "agent.call.start", "call_id": "i1", "state_path": "bf.implementing", "payload": {"agent": "bf__implementer"}},
            {"kind": "agent.stream", "state_path": "bf.implementing", "payload": {}},
            {"kind": "agent.call.complete", "call_id": "i1", "state_path": "bf.implementing", "payload": {"agent": "bf__implementer"}},
            {"kind": "harness.returned", "state_path": "bf.idle", "payload": {"error": "git.commit: Author identity unknown\n*** Please tell me who you are."}},
        ])
        h = bench.classify_cell(str(host))
        assert h["class"] == "infra:host-error", h
        assert h["evidence"]["error_label"] == "git-identity-missing", h

        # Real model result: reached a terminal state with completed work.
        model = tdp / "model.jsonl"
        _write_trace(model, [
            {"kind": "agent.call.start", "call_id": "i1", "state_path": "bf.implementing", "payload": {"agent": "bf__implementer"}},
            {"kind": "agent.stream", "state_path": "bf.implementing", "payload": {}},
            {"kind": "agent.call.complete", "call_id": "i1", "state_path": "bf.implementing", "payload": {"agent": "bf__implementer"}},
            {"kind": "machine.state_entered", "state_path": "bf.done", "payload": {}},
            {"kind": "machine.state_entered", "state_path": "finished", "payload": {}},
        ])
        assert bench.classify_cell(str(model))["class"] == "model:result"

        # Absent trace.
        assert bench.classify_cell(str(tdp / "nope.jsonl"))["class"] == "infra:no-trace"


def test_mine_failures_aggregates_and_prioritizes(capsys=None):
    bench = _load("bench_mine", os.path.join(HERE, "bench.py"))
    with tempfile.TemporaryDirectory() as td:
        tdp = Path(td)
        cells = tdp / "results" / "cells"
        traces = tdp / "traces"
        cells.mkdir(parents=True)
        traces.mkdir()

        def cell(bug, cand, oracle_pass):
            (cells / f"demo-{bug}-{cand}-kitsoki.json").write_text(json.dumps({
                "project": "demo", "bug": bug, "candidate": cand,
                "outcome": {"oracle_pass": oracle_pass},
            }))

        def trace(bug, cand, events):
            (traces / f"demo-{bug}-{cand}.jsonl").write_text(
                "\n".join(json.dumps(e) for e in events) + "\n")

        # A genuine model miss (terminal + oracle fail).
        cell("bug1", "glm", False)
        trace("bug1", "glm", [
            {"kind": "agent.call.start", "call_id": "i", "payload": {"agent": "impl"}},
            {"kind": "agent.call.complete", "call_id": "i", "payload": {"agent": "impl"}},
            {"kind": "machine.state_entered", "state_path": "bf.done", "payload": {}},
        ])
        # An infra host-error cell (must not count against the model).
        cell("bug2", "glm", False)
        trace("bug2", "glm", [
            {"kind": "agent.call.start", "call_id": "i", "payload": {"agent": "impl"}},
            {"kind": "agent.call.complete", "call_id": "i", "payload": {"agent": "impl"}},
            {"kind": "harness.returned", "payload": {"error": "git.commit: Author identity unknown"}},
        ])

        import io
        from contextlib import redirect_stdout
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = bench.mine_failures(str(tdp / "results"), str(traces))
        assert rc == 0
        rep = json.loads(buf.getvalue())
        assert rep["cells_scanned"] == 2, rep
        assert rep["by_class"].get("model:result") == 1, rep
        assert rep["by_class"].get("infra:host-error") == 1, rep
        assert len(rep["model_misses"]) == 1 and rep["model_misses"][0]["bug"] == "bug1", rep
        assert rep["host_error_labels"].get("git-identity-missing") == 1, rep
        assert any("INFRASTRUCTURE" in a for a in rep["feedback_actions"]), rep


def test_oracle_linter_flags_brittle_prose_not_messages():
    bench = _load("bench_lint", os.path.join(HERE, "bench.py"))
    # A brittle match on exact error prose — must be flagged.
    brittle = 'if !strings.Contains(resB.Error, "already checked out by session") {'
    # A behavior-style match on short synonyms + a failure MESSAGE with %q — must NOT be flagged.
    good = (
        'ok := strings.Contains(e, "in use by") || strings.Contains(e, "owned by")\n'
        't.Fatalf("session B error did not name the owning session as expected: %q", e)\n'
        'if !strings.Contains(e, "session-A") { t.Fatal("missing owner") }'
    )
    bf = bench._brittle_prose_literals(brittle)
    assert len(bf) == 1 and "already checked out by session" in bf[0][1], bf
    assert bench._brittle_prose_literals(good) == [], bench._brittle_prose_literals(good)


def json_load(raw):
    import json
    return json.loads(raw)


def main():
    fails = 0
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            try:
                fn()
                print(f"PASS {name}")
            except AssertionError as e:
                fails += 1
                print(f"FAIL {name}: {e}")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
