#!/usr/bin/env python3
"""No-network regression coverage for fail-closed BugSwarm provenance enrichment."""
from __future__ import annotations

import hashlib
import importlib.util
import json
import subprocess
import sys
import tempfile
import types
from unittest.mock import patch
from pathlib import Path

import yaml  # type: ignore

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/arena/scripts/bugswarm_enrich_source.py"
failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def source() -> dict:
    # Keep one existing value: the repository's stdlib YAML fallback renders an
    # empty mapping as a null scalar, and source validation rightly rejects it.
    return {"kind": "arena_bugswarm_source", "version": 1, "tasks": [{"id": "bugswarm-one", "image_tag": "org-project-1", "meta": {"selection_note": "seed"}}]}


def metadata(*, passed: str = "b" * 40) -> dict:
    return {"kind": "bugswarm_database_api_export/v1", "provider": "bugswarm-common DatabaseAPI", "artifacts": [{"image_tag": "org-project-1", "failed_job": {"commit": "a" * 40}, "passed_job": {"commit_sha": passed}}]}


def load_enricher_module():
    """Load the helper without invoking its CLI entry point."""
    spec = importlib.util.spec_from_file_location("bugswarm_enrich_source_test", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("could not load BugSwarm enricher")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def mock_database_api_module():
    """Return only the documented bugswarm-common import hierarchy.

    In particular, ``bugswarm.common`` deliberately has no ``DatabaseAPI``
    re-export.  This makes the test fail if the helper regresses to the old,
    unsupported top-level import while remaining entirely offline.
    """
    calls: list[str] = []

    class FakeDatabaseAPI:
        def filter_artifacts(self, query):
            if not isinstance(query, str):
                raise TypeError("DatabaseAPI.filter_artifacts requires a MongoDB query string")
            calls.append(query)
            parsed = json.loads(query)
            return [{"image_tag": parsed["image_tag"], "failed_job": {"commit": "d" * 40}, "passed_job": {"commit": "e" * 40}}]

    bugswarm = types.ModuleType("bugswarm")
    common = types.ModuleType("bugswarm.common")
    rest_api = types.ModuleType("bugswarm.common.rest_api")
    database_api = types.ModuleType("bugswarm.common.rest_api.database_api")
    bugswarm.__path__ = []
    common.__path__ = []
    rest_api.__path__ = []
    database_api.DatabaseAPI = FakeDatabaseAPI
    return calls, {
        "bugswarm": bugswarm,
        "bugswarm.common": common,
        "bugswarm.common.rest_api": rest_api,
        "bugswarm.common.rest_api.database_api": database_api,
    }


enricher = load_enricher_module()
fetch_calls, fake_modules = mock_database_api_module()
with patch.dict(sys.modules, fake_modules):
    fetched = enricher.fetch_database_api([{"image_tag": "official-import-path"}])
check("documented DatabaseAPI module path fetches offline", fetched["artifacts"][0]["image_tag"], "official-import-path")
check("DatabaseAPI receives canonical MongoDB query string", fetch_calls, ['{"image_tag":"official-import-path"}'])


with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    src, raw, out = root / "source.yaml", root / "metadata.json", root / "out.yaml"
    src.write_text(yaml.safe_dump(source(), sort_keys=False), encoding="utf-8")
    raw.write_text(json.dumps(metadata()), encoding="utf-8")
    run = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out), "--metadata-json", str(raw)], text=True, capture_output=True)
    check("offline enrichment exits zero", run.returncode, 0)
    if run.returncode:
        failures.append(f"offline enrichment output: {run.stdout}{run.stderr}")
        payload = {"tasks": [{"meta": {}}]}
    else:
        payload = yaml.safe_load(out.read_text(encoding="utf-8"))
    task = payload["tasks"][0]
    check("failed SHA copied only from metadata", task["meta"]["failed_commit_sha"], "a" * 40)
    check("passed SHA copied only from metadata", task["meta"]["passed_commit_sha"], "b" * 40)
    check("raw metadata digest retained", task["meta"]["bugswarm_provenance"]["metadata_sha256"], hashlib.sha256(raw.read_bytes()).hexdigest())
    check("source is not mutated", "failed_commit_sha" in yaml.safe_load(src.read_text())["tasks"][0]["meta"], False)
    check("no verification invented", task.get("verified_red"), None)
    check("enrichment receipt names task", payload["provenance_enrichment"]["enriched_task_ids"], ["bugswarm-one"])

    out.unlink()
    missing = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out), "--metadata-json", str(raw), "--task-id", "missing"], text=True, capture_output=True)
    check("unknown task rejected", missing.returncode, 1)
    check("unknown task writes nothing", out.exists(), False)

    raw.write_text(json.dumps(metadata(passed="not-a-sha")), encoding="utf-8")
    invalid = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out), "--metadata-json", str(raw)], text=True, capture_output=True)
    check("invalid metadata fails closed", invalid.returncode, 2)
    check("invalid metadata writes nothing", out.exists(), False)

    raw.write_text(json.dumps(metadata()), encoding="utf-8")
    no_opt_in = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out)], text=True, capture_output=True)
    check("network is never default", no_opt_in.returncode, 2)
    check("network default writes nothing", out.exists(), False)

    fetch_without_receipt = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out), "--fetch-database-api"], text=True, capture_output=True)
    check("live fetch requires raw receipt path", fetch_without_receipt.returncode, 2)

    conflict = source(); conflict["tasks"][0]["meta"]["failed_commit_sha"] = "c" * 40
    src.write_text(yaml.safe_dump(conflict, sort_keys=False), encoding="utf-8")
    conflict_run = subprocess.run([sys.executable, str(SCRIPT), "--source", str(src), "--out", str(out), "--metadata-json", str(raw)], text=True, capture_output=True)
    check("conflicting history rejected", conflict_run.returncode, 2)
    check("conflict writes nothing", out.exists(), False)

if failures:
    print("FAIL: BugSwarm provenance enrichment")
    print("\n".join("  - " + failure for failure in failures))
    sys.exit(1)
print("PASS: BugSwarm provenance enrichment (no network, no Docker, no LLM)")
