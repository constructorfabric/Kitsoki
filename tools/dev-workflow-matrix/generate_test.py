#!/usr/bin/env python3
"""Tests for the dev-workflow-matrix generator.

Run directly:  python3 tools/dev-workflow-matrix/generate_test.py

Covers: seeded manifest -> expected markdown structure, missing verdict ->
honest "no standing verdict", standing verdict file -> verdict + freshness,
manifest validation (missing cell / bad status / bad check_type), that the
real checked-in manifest loads and stays in sync with the committed
docs/testing/dev-workflow-matrix.md, AND the WS-F gate wiring: ingesting a
verdicts-dir keyed by (workflow, surface, repo, check_type), downgrade-on-fail,
stale detection, and the `--gate` exit-code contract.
"""
from __future__ import annotations

import importlib.util
import json
import os
import sys
import tempfile
import time
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "dwm_generate", str(Path(__file__).with_name("generate.py"))
)
gen = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gen)

import yaml  # noqa: E402


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def seed_manifest(**cell_overrides) -> dict:
    cell = {
        "workflow": "wf1",
        "surface": "s1",
        "repo": "r1",
        "status": "proof-thin",
        "reason": "works but proof thin",
    }
    cell.update(cell_overrides)
    return {
        "schema_version": "1.0.0",
        "plan": ".context/plan.md",
        "workflows": [{"id": "wf1", "title": "Workflow One"}],
        "surfaces": [{"id": "s1", "title": "Surface One"}],
        "repos": [{"id": "r1", "title": "Repo One"}],
        "cells": [cell],
    }


def load(manifest: dict, tmp: Path) -> dict:
    path = tmp / "manifest.yaml"
    path.write_text(yaml.safe_dump(manifest), encoding="utf-8")
    return gen.load_manifest(path)


def main():
    with tempfile.TemporaryDirectory() as tmpdir:
        tmp = Path(tmpdir)

        # 1. seeded manifest -> expected markdown
        manifest = load(seed_manifest(), tmp)
        md = gen.render(manifest, tmp)
        _check("has DO-NOT-HAND-EDIT header", "DO NOT HAND-EDIT" in md)
        _check("points at the manifest", "tools/dev-workflow-matrix/manifest.yaml" in md)
        _check("repo section rendered", "## Repo One" in md)
        _check("table row rendered", "| **Workflow One** | 🟡 proof-thin |" in md)
        _check("reason in cell detail", "works but proof thin" in md)

        # 2. no verdict pointer -> honest "no standing verdict" for BOTH classes
        _check(
            "both proof classes say no standing verdict",
            "  - mechanical: no standing verdict" in md
            and "  - experience: no standing verdict" in md,
        )

        # 3. pointer to an absent file -> still honest, names the pointer
        manifest = load(
            seed_manifest(
                verdicts={
                    "mechanical": {"check_type": "replay", "path": "gone.json"}
                }
            ),
            tmp,
        )
        md = gen.render(manifest, tmp)
        _check(
            "absent verdict file named",
            "no standing verdict (pointer `gone.json` absent)" in md,
        )

        # 4. standing verdict file -> verdict + check_type + freshness date
        verdict_path = tmp / "verdicts" / "cell.json"
        verdict_path.parent.mkdir(parents=True)
        verdict_path.write_text(
            json.dumps(
                {
                    "schema_version": "1.0.0",
                    "verdict": "solved",
                    "health": "model:result",
                    "metrics": {},
                    "evidence_refs": [],
                }
            ),
            encoding="utf-8",
        )
        manifest = load(
            seed_manifest(
                verdicts={
                    "mechanical": {"check_type": "replay", "path": "verdicts/cell.json"},
                    "experience": {
                        "check_type": "journey-verdict",
                        "path": "verdicts/cell.json",
                    },
                }
            ),
            tmp,
        )
        md = gen.render(manifest, tmp)
        _check("mechanical verdict shown", "**solved** (replay, as of " in md)
        _check("experience verdict shown", "**solved** (journey-verdict, as of " in md)

        # 5. unreadable verdict file is flagged, not crashed on
        bad = tmp / "bad.json"
        bad.write_text("{not json", encoding="utf-8")
        manifest = load(
            seed_manifest(
                verdicts={"mechanical": {"check_type": "replay", "path": "bad.json"}}
            ),
            tmp,
        )
        md = gen.render(manifest, tmp)
        _check("unreadable verdict flagged", "unreadable verdict at `bad.json`" in md)

        # 6. validation: missing cell of the cross product
        m = seed_manifest()
        m["surfaces"].append({"id": "s2", "title": "Surface Two"})
        try:
            load(m, tmp)
            _check("missing cell rejected", False)
        except gen.ManifestError as err:
            _check("missing cell rejected", "missing cells" in str(err))

        # 7. validation: unknown status
        try:
            load(seed_manifest(status="meh"), tmp)
            _check("bad status rejected", False)
        except gen.ManifestError as err:
            _check("bad status rejected", "status" in str(err))

        # 8. validation: experience check_type must be a judged type
        try:
            load(
                seed_manifest(
                    verdicts={
                        "experience": {"check_type": "replay", "path": "x.json"}
                    }
                ),
                tmp,
            )
            _check("replay rejected as experience check_type", False)
        except gen.ManifestError as err:
            _check("replay rejected as experience check_type", "check_type" in str(err))

        # 9. validation: duplicate cell
        m = seed_manifest()
        m["cells"].append(dict(m["cells"][0]))
        try:
            load(m, tmp)
            _check("duplicate cell rejected", False)
        except gen.ManifestError as err:
            _check("duplicate cell rejected", "duplicate" in str(err))

        # ---- WS-F gate wiring: verdicts-dir ingestion ----------------------

        def write_verdict(vdir: Path, name: str, **overrides) -> Path:
            payload = {
                "schema_version": "1.1.0",
                "verdict": "solved",
                "health": "model:result",
                "check_type": "replay",
                "target_id": "r1",
                "axis": {"workflow": "wf1", "surface": "s1"},
                "metrics": {},
                "evidence_refs": [],
            }
            payload.update(overrides)
            vdir.mkdir(parents=True, exist_ok=True)
            path = vdir / name
            path.write_text(json.dumps(payload), encoding="utf-8")
            return path

        # 11. a `works` cell + a solved verdicts-dir entry -> stays `works`,
        # no downgrade reason, and the live verdict is shown (not "no
        # standing verdict") even though the manifest has no pointer for it.
        vdir = tmp / "verdicts-ok"
        write_verdict(vdir, "cell.json")
        manifest = load(seed_manifest(status="works"), tmp)
        md = gen.render(manifest, tmp, verdicts_dir=vdir)
        _check("live verdict shown without a manifest pointer", "**solved** (replay, as of " in md)
        _check("no downgrade noted for a solved verdict", "downgraded from manifest status" not in md)
        _check("table cell stays works", "| **Workflow One** | ✅ works |" in md)

        # 12. downgrade-on-fail: a `works` cell with a `failed` replay verdict
        # renders (and gates) as `gap`, not the manifest's static `works`.
        vdir = tmp / "verdicts-failed"
        write_verdict(vdir, "cell.json", verdict="failed")
        manifest = load(seed_manifest(status="works"), tmp)
        md = gen.render(manifest, tmp, verdicts_dir=vdir)
        _check("failed verdict downgrades table cell to gap", "| **Workflow One** | 🔴 gap |" in md)
        _check(
            "downgrade reason names the failing verdict",
            "downgraded from manifest status `works`: mechanical replay verdict is 'failed'" in md,
        )

        # 13. infra health also counts as failing (not just verdict=="failed").
        vdir = tmp / "verdicts-infra"
        write_verdict(vdir, "cell.json", verdict="blocked", health="infra:harness")
        manifest = load(seed_manifest(status="works"), tmp)
        eff_status, reason = gen.effective_status(
            manifest["cells"][0], gen.scan_verdicts_dir(vdir), gen.DEFAULT_STALE_DAYS
        )
        _check("infra:* health downgrades to gap", eff_status == "gap")
        _check("infra reason present", reason is not None and "blocked" in reason)

        # 14. stale detection: an old, otherwise-solved verdict downgrades
        # `works` to `proof-thin` (one notch), not all the way to `gap`.
        vdir = tmp / "verdicts-stale"
        stale_path = write_verdict(vdir, "cell.json")
        old_mtime = time.time() - (30 * 86400)
        os.utime(stale_path, (old_mtime, old_mtime))
        manifest = load(seed_manifest(status="works"), tmp)
        eff_status, reason = gen.effective_status(
            manifest["cells"][0], gen.scan_verdicts_dir(vdir), stale_days=14
        )
        _check("stale verdict downgrades works to proof-thin", eff_status == "proof-thin")
        _check("stale reason mentions staleness", reason is not None and "stale" in reason)

        # 15. a fresh verdict under the stale threshold does not downgrade.
        eff_status, reason = gen.effective_status(
            manifest["cells"][0], gen.scan_verdicts_dir(vdir), stale_days=60
        )
        _check("fresh-enough verdict keeps works", eff_status == "works" and reason is None)

        # 16. out-of-scope cells are never downgraded, even by a failing verdict.
        vdir = tmp / "verdicts-oos"
        write_verdict(vdir, "cell.json", verdict="failed")
        manifest = load(seed_manifest(status="out-of-scope"), tmp)
        eff_status, reason = gen.effective_status(
            manifest["cells"][0], gen.scan_verdicts_dir(vdir), gen.DEFAULT_STALE_DAYS
        )
        _check("out-of-scope is exempt from downgrade", eff_status == "out-of-scope" and reason is None)

        # 17. missing verdict (empty verdicts-dir) leaves the manifest's
        # static status standing untouched — the honest default.
        empty_vdir = tmp / "verdicts-empty"
        empty_vdir.mkdir()
        manifest = load(seed_manifest(status="proof-thin"), tmp)
        eff_status, reason = gen.effective_status(
            manifest["cells"][0], gen.scan_verdicts_dir(empty_vdir), gen.DEFAULT_STALE_DAYS
        )
        _check("no verdict -> manifest status stands", eff_status == "proof-thin" and reason is None)

        # 18. gate_failures() only reports cells the manifest claims `works`.
        vdir = tmp / "verdicts-gate"
        write_verdict(vdir, "cell.json", verdict="failed")
        works_manifest = load(seed_manifest(status="works"), tmp)
        failures = gen.gate_failures(works_manifest, gen.scan_verdicts_dir(vdir), gen.DEFAULT_STALE_DAYS)
        _check("gate_failures flags the regressed works cell", len(failures) == 1)
        thin_manifest = load(seed_manifest(status="proof-thin"), tmp)
        failures = gen.gate_failures(thin_manifest, gen.scan_verdicts_dir(vdir), gen.DEFAULT_STALE_DAYS)
        _check("gate_failures ignores a non-works cell even with a failing verdict", failures == [])

        # 19. main() --gate exit-code contract: 0 when nothing regressed, 1
        # when a `works` cell regressed, 2 when --gate is passed without
        # --verdicts-dir.
        gate_manifest_path = tmp / "gate-manifest.yaml"
        gate_manifest_path.write_text(yaml.safe_dump(seed_manifest(status="works")), encoding="utf-8")
        ok_vdir = tmp / "gate-verdicts-ok"
        write_verdict(ok_vdir, "cell.json")
        rc = gen.main([
            "--manifest", str(gate_manifest_path),
            "--repo-root", str(tmp),
            "--verdicts-dir", str(ok_vdir),
            "--gate",
            "--out", str(tmp / "gate-out-ok.md"),
        ])
        _check("gate passes (exit 0) when the works cell still holds", rc == 0)

        fail_vdir = tmp / "gate-verdicts-fail"
        write_verdict(fail_vdir, "cell.json", verdict="failed")
        rc = gen.main([
            "--manifest", str(gate_manifest_path),
            "--repo-root", str(tmp),
            "--verdicts-dir", str(fail_vdir),
            "--gate",
            "--out", str(tmp / "gate-out-fail.md"),
        ])
        _check("gate fails (exit 1) when a works cell regressed", rc == 1)

        rc = gen.main(["--manifest", str(gate_manifest_path), "--gate"])
        _check("--gate without --verdicts-dir is a usage error (exit 2)", rc == 2)

    # 10. the real checked-in manifest loads and matches the committed matrix
    real = gen.load_manifest(gen.DEFAULT_MANIFEST)
    _check(
        "real manifest covers 5x4x2 = 40 cells",
        len(real["cells"]) == 40,
    )
    rendered = gen.render(real, gen.REPO_ROOT)
    committed = gen.DEFAULT_OUT
    if committed.exists():
        _check(
            "committed matrix is fresh (rerun make dev-workflow-matrix if not)",
            committed.read_text(encoding="utf-8") == rendered,
        )
    else:
        print("skip: committed matrix not present yet")

    print("PASS")


if __name__ == "__main__":
    main()
