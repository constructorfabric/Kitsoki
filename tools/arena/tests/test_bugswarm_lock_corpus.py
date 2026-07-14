#!/usr/bin/env python3
"""No-LLM/no-Docker checks for immutable BugSwarm corpus locking."""
from __future__ import annotations
import json, subprocess, sys, tempfile
from pathlib import Path
import yaml  # type: ignore

ROOT = Path(__file__).resolve().parents[3]
LOCK = ROOT / "tools/arena/scripts/bugswarm_lock_corpus.py"
failures: list[str] = []
def check(label: str, got, want):
    if got != want: failures.append(f"{label}: got {got!r}, want {want!r}")

def task(i: int, repo: str, *, digest_value: str = "sha256:abc") -> dict:
    return {"id": f"bugswarm-{i}", "repo_label": repo, "source": "bugswarm", "image_tag": f"image-{i}", "failed_job_id": f"f{i}", "passed_job_id": f"p{i}", "ticket": f"public ticket {i}", "verified_red": True, "verified_green": True, "oracle": {"kind": "hidden", "reference": f"oracle-{i}"}, "meta": {"language": "Java", "selection_note": "predeclared filter", "failed_commit_sha": f"f{i}" * 20, "passed_commit_sha": f"p{i}" * 20, "bugswarm_verification": {"mode": "execute", "image_digest": digest_value, "report": "verify.json", "report_sha256": "a" * 64, "source_snapshot": "source.yaml", "source_snapshot_sha256": "b" * 64}}}

with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp); source = root / "source.yaml"; out = root / "lock.json"
    source.write_text(yaml.safe_dump({"kind": "arena_bugswarm_source", "tasks": [task(i, f"org/repo-{i}") for i in range(12)]}, sort_keys=False))
    run = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out)], text=True, capture_output=True)
    check("ready lock exits zero", run.returncode, 0)
    payload = json.loads(out.read_text())
    check("ready status", payload["status"], "ready")
    check("four learning", sum(t["split"] == "learning" for t in payload["tasks"]), 4)
    check("eight confirmation", sum(t["split"] == "confirmation" for t in payload["tasks"]), 8)
    check("repository split separation", len({t["repository"] for t in payload["tasks"]}), 12)
    check("content lock present", len(payload["lock_sha256"]), 64)
    check("oracle hash hidden", len(payload["tasks"][0]["hidden_oracle"]["sha256"]), 64)
    check("verification receipt is required", bool(payload["tasks"][0]["verification"]["receipt"]), True)
    check("verification source snapshot is required", bool(payload["tasks"][0]["verification"]["source_snapshot"]), True)
    # Same source must produce exactly the same receipt.
    out2 = root / "lock2.json"; run2 = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out2)], text=True, capture_output=True)
    check("repeat exits zero", run2.returncode, 0); check("deterministic bytes", out2.read_bytes(), out.read_bytes())
    source.write_text(yaml.safe_dump({"kind": "arena_bugswarm_source", "tasks": [task(1, "one/repo")]}, sort_keys=False))
    blocked = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out)], text=True, capture_output=True)
    check("undersized exits explicit blocked", blocked.returncode, 2)
    payload = json.loads(out.read_text())
    check("undersized status", payload["status"], "blocked")
    check("no partial selected corpus", payload["tasks"], [])
    check("durable split reason", any("repository-distinct" in b for b in payload["blockers"]), True)
    source.write_text(yaml.safe_dump({"kind": "arena_bugswarm_source", "tasks": [task(i, f"org/repo-{i}", digest_value="" if i == 0 else "sha256:abc") for i in range(12)]}, sort_keys=False))
    missing = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out)], text=True, capture_output=True)
    check("missing provenance blocked", missing.returncode, 2)
    payload = json.loads(out.read_text()); check("image digest named", any("image_digest" in b for b in payload["blockers"]), True)
    source.write_text(yaml.safe_dump({"kind": "arena_bugswarm_source", "tasks": [task(i, f"org/repo-{i}") for i in range(12)]}, sort_keys=False))
    no_receipt = yaml.safe_load(source.read_text()); no_receipt["tasks"][0]["meta"]["bugswarm_verification"].pop("report_sha256")
    source.write_text(yaml.safe_dump(no_receipt, sort_keys=False))
    missing_receipt = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out)], text=True, capture_output=True)
    check("missing receipt hash blocked", missing_receipt.returncode, 2)
    payload = json.loads(out.read_text()); check("receipt hash named", any("verification_receipt_sha256" in b for b in payload["blockers"]), True)
    source.write_text(yaml.safe_dump({"kind": "arena_bugswarm_source", "tasks": [task(i, f"org/repo-{i}") for i in range(12)]}, sort_keys=False))
    no_snapshot = yaml.safe_load(source.read_text()); no_snapshot["tasks"][0]["meta"]["bugswarm_verification"].pop("source_snapshot_sha256")
    source.write_text(yaml.safe_dump(no_snapshot, sort_keys=False))
    missing_snapshot = subprocess.run([sys.executable, str(LOCK), "--source", str(source), "--out", str(out)], text=True, capture_output=True)
    check("missing snapshot hash blocked", missing_snapshot.returncode, 2)
    payload = json.loads(out.read_text()); check("snapshot hash named", any("verification_source_snapshot_sha256" in b for b in payload["blockers"]), True)
if failures:
    print("FAIL: bugswarm corpus lock"); print("\n".join("  - " + f for f in failures)); sys.exit(1)
print("PASS: BugSwarm corpus lock (no LLM, no Docker)")
