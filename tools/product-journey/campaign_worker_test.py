#!/usr/bin/env python3
"""Runner-level test for campaign worker receipts.

Run directly:  python3 tools/product-journey/campaign_worker_test.py

This creates local dry-run bundles only; it never launches a worker, LLM, or
GitHub operation.
"""

import importlib.util
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


def main():
    catalog = run.load_catalog(run.CATALOG)
    github_targets = run.load_github_targets(run.GITHUB_TARGETS)
    personas = run.load_personas(run.PERSONAS)
    scenarios = run.select_scenarios(run.load_scenarios(run.SCENARIOS), "remote-worker-campaign")

    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.MATRIX_ROOT = run.ARTIFACT_ROOT / "matrices"
        run.TARGET_PROOF_ROOT = run.ARTIFACT_ROOT / "target-proofs"
        run.DOGFOOD_ROOT = run.ARTIFACT_ROOT / "dogfood"
        run.PREFLIGHT_ROOT = run.ARTIFACT_ROOT / "preflights"

        run_dir, _ = run.build_run_bundle(
            catalog,
            github_targets,
            personas,
            scenarios,
            "gears-rust",
            "workflow-author",
            "campaign-worker-test",
            "dry-run",
            None,
            12,
        )
        artifact = run_dir / "worker" / "receipt.json"
        artifact.parent.mkdir(parents=True, exist_ok=True)
        artifact.write_text('{"status":"ready"}\n', encoding="utf-8")

        receipt = run.record_campaign_worker_receipt(
            run_dir,
            "vm",
            "qa-vm-1",
            "ready",
            "pass",
            "healthz ok; artifact import available",
            15,
            "vm://qa-vm-1/campaign-worker-test",
            str(artifact),
            "VM worker is ready for bounded campaign capture.",
        )
        summary = run.run_story_summary(run_dir)
        markdown = run.campaign_worker_receipt_markdown_path(run_dir).read_text(encoding="utf-8")

        _check("receipt records VM backend", receipt["backend"] == "vm")
        _check("receipt preserves scenario scope", receipt["scenario_scope"] == ["remote-worker-campaign"])
        _check("receipt imports existing artifact", receipt["artifact_import_status"] == "imported")
        _check("markdown names worker", "qa-vm-1" in markdown)
        _check("story summary exposes worker status", summary["campaign_worker_status"] == "ready")
        _check("story summary exposes worker receipt path", summary["campaign_worker_receipt_markdown_path"].endswith("campaign-worker-receipt.md"))

        blocked = run.record_campaign_worker_receipt(
            run_dir,
            "arena",
            "arena-local",
            "blocked",
            "fail",
            "VM credentials are not installed",
            0,
            "",
            str(run_dir / "missing-artifact.json"),
            "Arena worker cannot start yet.",
        )
        _check("blocked receipt stays conservative", blocked["ready_status"] == "fail" and blocked["artifact_import_status"] == "none")

        _expect_system_exit(
            "invalid backend is rejected",
            lambda: run.record_campaign_worker_receipt(run_dir, "ssh", "bad", "ready", "pass", "", 0, "", "", ""),
            "--worker-backend",
        )

    print("PASS")


if __name__ == "__main__":
    main()
