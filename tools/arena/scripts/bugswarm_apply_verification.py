#!/usr/bin/env python3
"""Apply a BugSwarm verification report to a converted arena source YAML.

`bugswarm_verify_source.py --execute` proves RED/GREEN behavior, but the source
YAML should not be hand-edited afterward. This script writes a new source file
with `verified_red` / `verified_green` set from the verification report and
records the evidence in each task's `meta.bugswarm_verification`.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib
import sys
from pathlib import Path
from typing import Any


def _import_pyyaml() -> Any:
    """Import the installed PyYAML package, never this directory's shim.

    ``tools/arena/scripts/yaml.py`` is a deliberately small offline parser for
    manifest tests.  Python puts a directly executed script's directory first
    on ``sys.path``, which previously made this command silently serialize via
    that shim.  Its emitter does not implement YAML's colon quoting rules, so
    the resulting corpus could not be read by a standard YAML implementation.
    Corpus evidence is exchanged outside this repository; require the real
    PyYAML serializer for that boundary.
    """
    arena_root = Path(__file__).resolve().parents[1]

    def is_arena_path(entry: str) -> bool:
        try:
            Path(entry or ".").resolve().relative_to(arena_root)
            return True
        except ValueError:
            return False

    previous_module = sys.modules.pop("yaml", None)
    previous_path = sys.path[:]
    sys.path[:] = [entry for entry in sys.path if not is_arena_path(entry)]
    try:
        module = importlib.import_module("yaml")
    except ModuleNotFoundError:
        if previous_module is not None:
            sys.modules["yaml"] = previous_module
        sys.exit("bugswarm_apply_verification.py needs pyyaml")
    finally:
        sys.path[:] = previous_path

    origin = Path(str(getattr(module, "__file__", ""))).resolve()
    if is_arena_path(str(origin)):
        raise RuntimeError(f"refusing local YAML shim for corpus serialization: {origin}")
    return module


yaml = _import_pyyaml()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="YAML from bugswarm_to_arena.py")
    parser.add_argument("--verification", required=True, help="JSON from bugswarm_verify_source.py")
    parser.add_argument("--out", required=True, help="updated source YAML")
    parser.add_argument(
        "--evidence-dir",
        help="durable directory for immutable source and receipt snapshots (default: <out parent>/bugswarm-evidence)",
    )
    parser.add_argument(
        "--allow-dry-run",
        action="store_true",
        help="carry dry-run evidence into meta without setting verified flags",
    )
    args = parser.parse_args(argv)

    source_path = Path(args.source)
    verification_path = Path(args.verification)
    out = Path(args.out)
    source = load_yaml(source_path)
    verification = load_yaml_or_json(verification_path)
    source_sha256 = hashlib.sha256(source_path.read_bytes()).hexdigest()
    if verification.get("source_sha256") != source_sha256:
        raise ValueError("verification source_sha256 does not match --source; rerun verification for this source revision")
    evidence_dir = Path(args.evidence_dir) if args.evidence_dir else out.parent / "bugswarm-evidence"
    require_durable_evidence_dir(evidence_dir)
    source_snapshot = write_immutable_snapshot(evidence_dir, "source", source_path, source_sha256)
    receipt_sha256 = hashlib.sha256(verification_path.read_bytes()).hexdigest()
    receipt_snapshot = write_immutable_snapshot(evidence_dir, "verification", verification_path, receipt_sha256)
    updated = apply_verification(
        source,
        verification,
        verification_path=str(receipt_snapshot),
        verification_sha256=receipt_sha256,
        source_snapshot_path=str(source_snapshot),
        source_snapshot_sha256=source_sha256,
        allow_dry_run=args.allow_dry_run,
    )
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(yaml.safe_dump(updated, sort_keys=False), encoding="utf-8")
    verified_count = sum(1 for task in updated.get("tasks", []) if task.get("verified_red") and task.get("verified_green"))
    print(f"wrote {out} ({verified_count} verified task(s)); immutable evidence: {evidence_dir}")
    return 0


def load_yaml(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    if data.get("kind") != "arena_bugswarm_source":
        raise ValueError(f"source kind must be arena_bugswarm_source: {path}")
    return data


def load_yaml_or_json(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected mapping: {path}")
    if data.get("kind") != "arena_bugswarm_verification":
        raise ValueError(f"verification kind must be arena_bugswarm_verification: {path}")
    return data


def managed_workspace_root(path: Path) -> Path | None:
    """Return a managed workspace ancestor, if this path would be torn down."""
    resolved = path.resolve()
    for candidate in (resolved, *resolved.parents):
        if (candidate / ".kitsoki-dev-workspace.json").is_file():
            return candidate
    return None


def require_durable_evidence_dir(path: Path) -> None:
    workspace = managed_workspace_root(path)
    if workspace is not None:
        raise ValueError(
            f"evidence directory is inside managed workspace {workspace}: {path}; "
            "use --evidence-dir under the primary checkout's .artifacts directory"
        )


def write_immutable_snapshot(evidence_dir: Path, kind: str, source: Path, sha256: str) -> Path:
    """Copy bytes once, content-addressed, so teardown cannot erase the evidence."""
    suffix = ".yaml" if kind == "source" else ".json"
    destination = evidence_dir / f"bugswarm-{kind}-{sha256}{suffix}"
    payload = source.read_bytes()
    if hashlib.sha256(payload).hexdigest() != sha256:
        raise ValueError(f"{kind} changed while applying verification: {source}")
    evidence_dir.mkdir(parents=True, exist_ok=True)
    if destination.exists():
        if destination.read_bytes() != payload:
            raise ValueError(f"immutable {kind} snapshot collision: {destination}")
        return destination.resolve()
    destination.write_bytes(payload)
    return destination.resolve()


def apply_verification(
    source: dict[str, Any],
    verification: dict[str, Any],
    *,
    verification_path: str,
    verification_sha256: str,
    source_snapshot_path: str,
    source_snapshot_sha256: str,
    allow_dry_run: bool,
) -> dict[str, Any]:
    mode = str(verification.get("mode") or "")
    if mode != "execute" and not allow_dry_run:
        raise ValueError("verification mode must be execute; pass --allow-dry-run to carry dry-run metadata only")
    results = {
        str(result.get("task_id") or ""): result
        for result in verification.get("results", [])
        if isinstance(result, dict)
    }
    if len(results) != len([result for result in verification.get("results", []) if isinstance(result, dict)]):
        raise ValueError("verification results require unique non-empty task_id values")
    source_ids = {str(task.get("id") or "") for task in source.get("tasks", []) if isinstance(task, dict)}
    unknown = sorted(set(results) - source_ids)
    if unknown:
        raise ValueError("verification contains task ids absent from source: " + ", ".join(unknown))
    out = dict(source)
    out["verification"] = {
        "path": verification_path,
        "sha256": verification_sha256,
        "source_snapshot": source_snapshot_path,
        "source_snapshot_sha256": source_snapshot_sha256,
        "mode": mode,
        "task_count": int(verification.get("task_count") or 0),
        "verified_count": int(verification.get("verified_count") or 0),
    }
    tasks = []
    for task in source.get("tasks", []):
        if not isinstance(task, dict):
            continue
        updated = dict(task)
        result = results.get(str(task.get("id") or ""))
        if result:
            red = bool(result.get("verified_red"))
            green = bool(result.get("verified_green"))
            if mode == "execute":
                updated["verified_red"] = red
                updated["verified_green"] = green
            meta = dict(updated.get("meta") or {})
            meta["bugswarm_verification"] = {
                "mode": mode,
                "verified_red": red,
                "verified_green": green,
                "failed_exit_code": result.get("failed_exit_code"),
                "passed_exit_code": result.get("passed_exit_code"),
                # These values are observed by `git rev-parse HEAD` inside the
                # separate failed/passed containers, not inferred from an API
                # export.  The corpus locker consumes them directly.
                "failed_commit_sha": result.get("failed_commit_sha"),
                "passed_commit_sha": result.get("passed_commit_sha"),
                "report": verification_path,
                "report_sha256": out["verification"]["sha256"],
                "source_snapshot": source_snapshot_path,
                "source_snapshot_sha256": source_snapshot_sha256,
                "image_digest": result.get("image_digest"),
            }
            if mode == "execute":
                for field in ("failed_commit_sha", "passed_commit_sha"):
                    value = result.get(field)
                    if isinstance(value, str) and value:
                        meta[field] = value.lower()
            updated["meta"] = meta
        tasks.append(updated)
    out["tasks"] = tasks
    out["task_count"] = len(tasks)
    return out


if __name__ == "__main__":
    raise SystemExit(main())
