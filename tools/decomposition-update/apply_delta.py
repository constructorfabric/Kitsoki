#!/usr/bin/env python3
"""Apply a managed decomposition-graph delta.

This is the deterministic core of WM.7. It intentionally does not call an LLM:
review can be layered on top by the story, but the write transaction itself is
pure and testable.
"""

from __future__ import annotations

import argparse
import copy
import json
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any

try:
    import yaml  # type: ignore
except ModuleNotFoundError:
    sys.exit("apply_delta.py needs pyyaml")

REPO_ROOT = Path(__file__).resolve().parents[2]
VALIDATOR = REPO_ROOT / "tools" / "validate-decomposition-graph.py"
LOCKED_STATES = {"assigned", "in_flight", "reviewing", "verified"}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("base", help="current decomposition.yaml")
    parser.add_argument("delta", help="managed delta YAML")
    parser.add_argument("--out", required=True, help="output decomposition.yaml")
    parser.add_argument("--versions-dir", required=True, help="directory for prior graph versions")
    parser.add_argument("--event-log", required=True, help="append-only plan evolution JSONL")
    parser.add_argument("--ledger", help="optional folded ledger.json for no-orphan checks")
    parser.add_argument("--version-id", help="deterministic version suffix for tests")
    parser.add_argument(
        "--list-key",
        default="changes",
        help="top-level list key the base/delta documents use (default 'changes'; "
        "e.g. 'briefs' for a deliver-shaped decomposition manifest)",
    )
    parser.add_argument(
        "--skip-validate",
        action="store_true",
        help="skip the tools/validate-decomposition-graph.py candidate check — that "
        "validator is shaped for the dev-workflow 'changes' graph; callers with a "
        "different list-key (e.g. deliver's brief manifest) run their own "
        "deterministic validation downstream (deliver's lint room) instead",
    )
    args = parser.parse_args(argv)

    try:
        result = apply_delta(
            base=Path(args.base),
            delta=Path(args.delta),
            out=Path(args.out),
            versions_dir=Path(args.versions_dir),
            event_log=Path(args.event_log),
            ledger=Path(args.ledger) if args.ledger else None,
            version_id=args.version_id,
            list_key=args.list_key,
            skip_validate=args.skip_validate,
        )
    except TransactionError as exc:
        print(f"FAIL: {exc}")
        # A single-line JSON envelope with an explicit "route" field as the
        # LAST stdout line — host.run's stdout_json binder parses the last
        # non-empty line, and a story room branching on a bool alone (host.run
        # has no native tri-state) needs a string that is absent-by-default,
        # never a bool whose zero-value coincides with "failed" (see
        # stories/deliver/rooms/redecompose.yaml's comment on this).
        print(json.dumps({"route": "fail", "ok": False, "error": str(exc)}, sort_keys=True))
        return 1
    print(json.dumps({"route": "ok", "ok": True, **result}, sort_keys=True))
    return 0


class TransactionError(Exception):
    pass


def apply_delta(
    *,
    base: Path,
    delta: Path,
    out: Path,
    versions_dir: Path,
    event_log: Path,
    ledger: Path | None,
    version_id: str | None,
    list_key: str = "changes",
    skip_validate: bool = False,
) -> dict[str, Any]:
    base_doc = _load_yaml(base)
    delta_doc = _load_yaml(delta)
    _validate_delta_header(delta_doc)
    locked = _locked_change_ids(ledger)
    next_doc = _apply_operations(base_doc, delta_doc, locked, list_key=list_key)
    if not skip_validate:
        _validate_candidate(next_doc, out)

    version_id = version_id or _next_version_id(versions_dir)
    version_path = versions_dir / f"decomposition.{version_id}.yaml"

    # Commit point: all validation has passed. Writes happen only after this line.
    versions_dir.mkdir(parents=True, exist_ok=True)
    out.parent.mkdir(parents=True, exist_ok=True)
    event_log.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(base, version_path)
    _write_yaml(out, next_doc)
    event = {
        "kind": "plan_evolution",
        "trigger": delta_doc["trigger"],
        "provenance": delta_doc["provenance"],
        "version_path": str(version_path),
        "delta_path": str(delta),
        "added": [op["change"]["id"] for op in delta_doc.get("operations", []) if op.get("op") == "add_change"],
    }
    _append_jsonl(event_log, event)
    return {
        "out": str(out),
        "version_path": str(version_path),
        "event_log": str(event_log),
        "added": event["added"],
    }


def _load_yaml(path: Path) -> dict[str, Any]:
    if not path.exists():
        raise TransactionError(f"missing file: {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise TransactionError(f"{path} must contain a mapping")
    return data


def _write_yaml(path: Path, data: dict[str, Any]) -> None:
    path.write_text(yaml.safe_dump(data, sort_keys=False), encoding="utf-8")


def _validate_delta_header(delta: dict[str, Any]) -> None:
    trigger = str(delta.get("trigger") or "").strip()
    provenance = delta.get("provenance")
    if not trigger:
        raise TransactionError("delta.trigger is required")
    if not isinstance(provenance, dict):
        raise TransactionError("delta.provenance mapping is required")
    if not str(provenance.get("kind") or "").strip():
        raise TransactionError("delta.provenance.kind is required")
    if not str(provenance.get("ref") or "").strip():
        raise TransactionError("delta.provenance.ref is required")
    operations = delta.get("operations")
    if not isinstance(operations, list) or not operations:
        raise TransactionError("delta.operations must be a non-empty list")


def _locked_change_ids(ledger: Path | None) -> set[str]:
    if ledger is None:
        return set()
    data = json.loads(ledger.read_text(encoding="utf-8"))
    out = set()
    for row in data.get("changes", []):
        if row.get("state") in LOCKED_STATES:
            out.add(str(row.get("change_id")))
    return out


def _apply_operations(
    base: dict[str, Any], delta: dict[str, Any], locked: set[str], *, list_key: str = "changes"
) -> dict[str, Any]:
    doc = copy.deepcopy(base)
    changes = doc.get(list_key)
    if not isinstance(changes, list):
        raise TransactionError(f"base document must contain {list_key!r} list")
    by_id = {str(c.get("id")): c for c in changes if isinstance(c, dict)}
    for op in delta.get("operations", []):
        if not isinstance(op, dict):
            raise TransactionError("each operation must be a mapping")
        opname = op.get("op")
        if opname == "add_change":
            change = op.get("change")
            if not isinstance(change, dict):
                raise TransactionError("add_change.change must be a mapping")
            cid = str(change.get("id") or "").strip()
            if not cid:
                raise TransactionError("add_change.change.id is required")
            if cid in by_id:
                raise TransactionError(f"change {cid!r} already exists")
            changes.append(change)
            by_id[cid] = change
            continue
        if opname in {"remove_change", "replace_change"}:
            cid = str(op.get("id") or "").strip()
            if cid in locked:
                raise TransactionError(f"change {cid!r} is {opname}-locked by active ledger state")
        raise TransactionError(f"unsupported operation {opname!r}")
    return doc


def _validate_candidate(doc: dict[str, Any], out_path: Path) -> None:
    tmp = out_path.with_name(out_path.name + ".candidate.yaml")
    try:
        _write_yaml(tmp, doc)
        proc = subprocess.run(
            [sys.executable, str(VALIDATOR), str(tmp)],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
        if proc.returncode != 0:
            detail = (proc.stdout + proc.stderr).strip()
            raise TransactionError(f"candidate failed decomposition validation: {detail}")
    finally:
        tmp.unlink(missing_ok=True)


def _next_version_id(versions_dir: Path) -> str:
    existing = sorted(versions_dir.glob("decomposition.v*.yaml"))
    return f"v{len(existing) + 1:04d}"


def _append_jsonl(path: Path, event: dict[str, Any]) -> None:
    prior = path.read_text(encoding="utf-8") if path.exists() else ""
    if prior and not prior.endswith("\n"):
        prior += "\n"
    path.write_text(prior + json.dumps(event, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__":
    raise SystemExit(main())
