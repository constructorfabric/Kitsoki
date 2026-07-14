"""Core data model for the arena comparison-job runner.

A *job* is a big comparison sweep (bug-fixing, onboarding, persona QA, …). It is
declared as a `JobSpec` and enumerates into `Cell`s — the unit of work. Each cell
runs in a container (placed on a Docker host) and produces a job-type-agnostic
`CellResult`. The `candidate` of bugfix-bakeoff and the `persona/scenario` of
product-journey both collapse here into a cell's **variant + axis coords**.

Pure stdlib + dataclasses; no LLM, no docker — those live behind interfaces in
executor.py so this layer stays deterministic and trivially testable.
"""

from __future__ import annotations

import itertools
import hashlib
import json
import subprocess
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import Any

from .task_optimization_receipt import validate_scored_attempt_receipt

# tools/arena/arena/model.py -> parents[3] is the repo root. `targets_from` /
# `persona_axis_from` / `target_proof_from` paths in a spec are resolved
# relative to this so specs stay portable regardless of cwd.
REPO_ROOT = Path(__file__).resolve().parents[3]


def _resolve_repo_path(path: str | Path) -> Path:
    p = Path(path)
    return p if p.is_absolute() else REPO_ROOT / p


@dataclass(frozen=True)
class Target:
    """One thing being compared *across* — e.g. a repo. (github-targets shape.)"""

    id: str
    label: str = ""
    repo: str = ""
    stack: str = ""
    meta: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class Variant:
    """One competitor in the comparison — a model/tool/treatment (candidate shape)."""

    id: str
    backend: str = ""          # claude | codex | synthetic | …
    model: str = ""
    effort: str = ""
    meta: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class Cell:
    """The unit of work: target × variant × axis-coords, for one job_type."""

    id: str
    job_type: str
    target: Target
    variant: Variant
    axis: dict[str, str] = field(default_factory=dict)   # e.g. {"bug": "qs1"}
    status: str = "planned"
    options: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        out = {
            "id": self.id,
            "job_type": self.job_type,
            "target": asdict(self.target),
            "variant": asdict(self.variant),
            "axis": dict(self.axis),
            "status": self.status,
        }
        if self.options:
            out["options"] = dict(self.options)
        return out


# Job-type-agnostic verdicts. Plugins map their native grade into these so the
# rollup can aggregate across any job type. (bugfix oracle, persona review gate,
# onboarding assertions all land in this vocabulary.)
VERDICTS = ("solved", "partial", "failed", "armed", "blocked", "pending", "unsupported")

# A task-optimization cell moves through these durable scheduler states. They
# deliberately are not verdicts: a valid scored attempt can be failed, while a
# pending/unsupported candidate has not supplied a model result at all.
TASK_OPTIMIZATION_CELL_STATES = (
    "planned", "armed", "ready", "running", "scored", "blocked", "pending", "unsupported",
)
TASK_OPTIMIZATION_SCHEMA = "task-optimization/v1"
TASK_OPTIMIZATION_PREFLIGHT_SCHEMA = "task-optimization/preflight/v1"
TASK_OPTIMIZATION_ATTEMPT_SCHEMA = "task-optimization/attempt/v1"
TASK_OPTIMIZATION_CHAMPION_SCHEMA = "task-optimization/champion/v1"
# ``blocked`` is deliberately not always terminal.  A placement/harness outage
# must be recorded (so the evidence is not lost) but remains resumable while
# it declares ``retryable: true`` or an ``infra:*`` health.  The lifecycle
# reducer below is the sole place which makes that distinction; receipt schema
# validation remains owned by the receipt contract.
TASK_OPTIMIZATION_TERMINAL_STATES = ("scored", "blocked", "unsupported")


def _sha256(value: bytes | str) -> str:
    if isinstance(value, str):
        value = value.encode("utf-8")
    return hashlib.sha256(value).hexdigest()


def canonical_json(value: Any) -> bytes:
    """Return the canonical bytes used by task-optimization receipts."""
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode("utf-8")


def receipt_sha256(value: Any) -> str:
    return _sha256(canonical_json(value))


BUGSWARM_CORPUS_LOCK_SCHEMA = "arena_bugswarm_corpus_lock/v1"


def bugswarm_lock_sha256(value: dict[str, Any]) -> str:
    """Return the locker's canonical digest, excluding its self-reference."""
    payload = dict(value)
    payload.pop("lock_sha256", None)
    return _sha256(json.dumps(payload, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8"))


@dataclass(frozen=True)
class TaskOptimizationStudy:
    """Immutable-input declaration for a task x model x treatment campaign.

    The study file is intentionally separate from the generic ``JobSpec``:
    it owns split discipline, pinned corpus/profile inputs and repeat policy,
    while Arena remains the scheduler and report surface.
    """

    study_id: str
    boundary: str
    corpus_lock: str
    splits: dict[str, str]
    candidates: list[dict[str, Any]]
    treatments: list[str]
    repeats: dict[str, int]
    stop: dict[str, Any]
    live_gate_env: str
    blocked_by: list[str] = field(default_factory=list)
    path: str = ""

    @staticmethod
    def load(path: str | Path) -> "TaskOptimizationStudy":
        p = Path(path).resolve()
        text = p.read_text(encoding="utf-8")
        try:
            import yaml  # type: ignore
            data = yaml.safe_load(text)
        except ModuleNotFoundError:
            data = json.loads(text)
        if not isinstance(data, dict):
            raise ValueError("study manifest must be a mapping")
        if data.get("schema") != TASK_OPTIMIZATION_SCHEMA:
            raise ValueError(f"unsupported study schema {data.get('schema')!r}")
        blocked_by = [str(reason) for reason in data.get("blocked_by", []) or []]
        is_blocked = data.get("live_status") == "blocked"
        required = ("study_id", "boundary", "corpus_lock", "splits", "candidates", "treatments", "repeats", "stop")
        missing = [key for key in required if not data.get(key)]
        if missing and not is_blocked:
            raise ValueError(f"study manifest missing required fields: {', '.join(missing)}")
        if missing:
            if not blocked_by:
                raise ValueError("blocked study manifests require non-empty blocked_by")
            return TaskOptimizationStudy(
                study_id=str(data["study_id"]), boundary=str(data["boundary"]), corpus_lock=str(data.get("corpus_lock") or ""),
                splits={}, candidates=[], treatments=[], repeats={}, stop={},
                live_gate_env=str(data.get("live_gate_env") or "KITSOKI_TASK_OPT_LIVE"), blocked_by=blocked_by, path=str(p),
            )
        candidates = data["candidates"]
        if not isinstance(candidates, list) or not all(isinstance(c, dict) for c in candidates):
            raise ValueError("candidates must be a non-empty list of mappings")
        # The checked-in Stage-0 declaration uses the more explicit
        # key/requested_* spelling.  Normalize both it and the compact test
        # spelling here, so a corpus unlock cannot silently change candidate
        # identity or discard the requested model/effort contract.
        normalized_candidates: list[dict[str, Any]] = []
        for raw in candidates:
            candidate = dict(raw)
            candidate["id"] = str(candidate.get("id") or candidate.get("key") or "")
            candidate["model"] = str(candidate.get("model") or candidate.get("requested_model") or "")
            candidate["effort"] = str(candidate.get("effort") or candidate.get("requested_effort") or "")
            candidate["requested_model"] = str(candidate.get("requested_model") or candidate["model"])
            candidate["requested_effort"] = str(candidate.get("requested_effort") or candidate["effort"])
            normalized_candidates.append(candidate)
        ids = [str(c.get("id") or "") for c in normalized_candidates]
        if not all(ids) or len(ids) != len(set(ids)):
            raise ValueError("candidates require unique non-empty ids")
        for candidate in normalized_candidates:
            if not candidate.get("profile") or not candidate.get("model"):
                raise ValueError(f"candidate {candidate.get('id')!r} requires profile and model")
            if "effort" not in candidate:
                raise ValueError(f"candidate {candidate.get('id')!r} requires effort (use 'n/a' when unsupported)")
        treatments = [str(t) for t in data["treatments"]]
        if not all(treatments) or len(treatments) != len(set(treatments)):
            raise ValueError("treatments require unique non-empty ids")
        repeats = dict(data["repeats"])
        if any(not isinstance(value, int) or value < 1 for value in repeats.values()):
            raise ValueError("repeat values must be positive integers")
        lock_path = Path(str(data["corpus_lock"]))
        if not lock_path.is_absolute():
            lock_path = (p.parent / lock_path).resolve()
        return TaskOptimizationStudy(
            study_id=str(data["study_id"]), boundary=str(data["boundary"]), corpus_lock=str(lock_path),
            splits={str(k): str(v) for k, v in dict(data["splits"]).items()},
            candidates=normalized_candidates, treatments=treatments, repeats=repeats,
            stop=dict(data["stop"]), live_gate_env=str(data.get("live_gate_env") or "KITSOKI_TASK_OPT_LIVE"),
            blocked_by=blocked_by, path=str(p),
        )

    def corpus(self) -> dict[str, Any]:
        path = Path(self.corpus_lock)
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            raise ValueError(f"corpus lock must be JSON: {exc}") from exc
        if not isinstance(data, dict):
            raise ValueError("corpus lock must be a JSON object")
        if data.get("schema") != BUGSWARM_CORPUS_LOCK_SCHEMA:
            raise ValueError("corpus lock has an unsupported schema")
        if data.get("status") != "ready":
            raise ValueError("corpus lock is not ready")
        actual_digest = bugswarm_lock_sha256(data)
        if data.get("lock_sha256") != actual_digest:
            raise ValueError("corpus lock_sha256 does not match its contents")
        source_sha = str(data.get("source_sha256") or "")
        if len(source_sha) != 64 or any(char not in "0123456789abcdef" for char in source_sha.lower()):
            raise ValueError("corpus lock requires a SHA-256 source_sha256")
        selection = data.get("selection")
        if not isinstance(selection, dict) or selection.get("repository_separated") is not True:
            raise ValueError("corpus lock requires repository_separated selection")
        expected = {"learning": selection.get("learning_count"), "confirmation": selection.get("confirmation_count")}
        if any(not isinstance(count, int) or count < 1 for count in expected.values()):
            raise ValueError("corpus lock selection requires positive learning_count and confirmation_count")
        tasks = data.get("tasks")
        if not isinstance(tasks, list) or not tasks:
            raise ValueError("corpus lock requires a non-empty tasks list")
        if len(tasks) != sum(expected.values()):
            raise ValueError("corpus lock task count does not match its selection")
        seen: set[str] = set()
        repositories: set[str] = set()
        split_counts = {"learning": 0, "confirmation": 0}
        for task in tasks:
            if not isinstance(task, dict) or not task.get("id") or not task.get("split"):
                raise ValueError("every corpus task requires id and split")
            if task["id"] in seen:
                raise ValueError(f"duplicate corpus task id {task['id']!r}")
            seen.add(task["id"])
            if task["split"] not in self.splits:
                raise ValueError(f"corpus task {task['id']!r} has unknown split {task['split']!r}")
            if task["split"] not in split_counts:
                raise ValueError(f"corpus task {task['id']!r} has unsupported lock split {task['split']!r}")
            split_counts[task["split"]] += 1
            repository = str(task.get("repository") or "")
            if not repository or repository in repositories:
                raise ValueError(f"corpus task {task['id']!r} lacks a repository-distinct provenance record")
            repositories.add(repository)
            provenance = ("image_digest", "commits", "verification", "public_task", "hidden_oracle")
            if any(not task.get(key) for key in provenance):
                raise ValueError(f"corpus task {task['id']!r} lacks required provenance")
        if split_counts != expected:
            raise ValueError("corpus lock split counts do not match its selection")
        return data

    def plan(self, *, repeat_phase: str = "screening") -> dict[str, Any]:
        if repeat_phase not in self.repeats:
            raise ValueError(f"unknown repeat phase {repeat_phase!r}; known: {sorted(self.repeats)}")
        corpus = self.corpus()
        cells: list[dict[str, Any]] = []
        for task in sorted(corpus["tasks"], key=lambda t: str(t["id"])):
            for candidate in sorted(self.candidates, key=lambda c: str(c["id"])):
                for treatment in sorted(self.treatments):
                    for repeat in range(1, self.repeats[repeat_phase] + 1):
                        cell_id = f"{task['id']}--{candidate['id']}--{treatment}--r{repeat}"
                        cells.append({"id": cell_id, "task_id": task["id"], "split": task["split"],
                                      "candidate_id": candidate["id"], "treatment": treatment,
                                      "repeat": repeat, "status": "planned"})
        policy = self.stop.get("promotion_policy", {}) if isinstance(self.stop, dict) else {}
        retry_policy = self.stop.get("retry_policy", {}) if isinstance(self.stop, dict) else {}
        if not isinstance(policy, dict) or not isinstance(retry_policy, dict):
            raise ValueError("stop promotion_policy and retry_policy must be objects")
        return {"schema": TASK_OPTIMIZATION_SCHEMA, "study_id": self.study_id, "repeat_phase": repeat_phase,
                "cell_count": len(cells), "promotion_policy": policy, "retry_policy": retry_policy,
                # The dispatcher consumes the frozen plan rather than re-reading
                # a mutable study manifest before each cell.  Keep the spend gate
                # in that frozen boundary too.
                "live_gate_env": self.live_gate_env, "cells": cells}

    def lock(self) -> dict[str, Any]:
        corpus_bytes = Path(self.corpus_lock).read_bytes()
        study_bytes = Path(self.path).read_bytes()
        runner_commit = ""
        try:
            runner_commit = subprocess.run(["git", "rev-parse", "HEAD"], cwd=REPO_ROOT, text=True,
                                           capture_output=True, check=True).stdout.strip()
        except (OSError, subprocess.CalledProcessError):
            runner_commit = "unknown"
        return {"schema": TASK_OPTIMIZATION_SCHEMA, "study_id": self.study_id, "boundary": self.boundary,
                "study_manifest": self.path, "study_manifest_sha256": _sha256(study_bytes),
                "corpus_lock": self.corpus_lock, "corpus_lock_sha256": _sha256(corpus_bytes),
                "splits": self.splits, "candidates": self.candidates, "treatments": sorted(self.treatments),
                "repeats": self.repeats, "stop": self.stop, "live_gate_env": self.live_gate_env,
                "runner_commit": runner_commit}

    def preflight(self, *, config_path: str | Path | None = None,
                  launch_agent: str = "task-optimization-preflight", working_dir: str | Path | None = None) -> dict[str, Any]:
        """Resolve every candidate through the real local dry-run launcher.

        This boundary intentionally does *not* accept operator-authored model,
        effort, hash, auth, or quota fields.  ``kitsoki agent launch`` loads
        the effective local profile catalog (including the ignored local
        overlay) and returns a redacted no-provider launch plan.  An absent
        profile is an explicit ``unsupported`` outcome; malformed context or a
        launcher error is ``invalid``.  Auth readiness is configuration-only:
        no remote credential or quota probe is performed here.
        """
        config = Path(config_path or (REPO_ROOT / ".kitsoki.yaml")).resolve()
        context_dir = Path(working_dir or REPO_ROOT).resolve()
        records: list[dict[str, Any]] = []
        for candidate in self.candidates:
            record = {
                "candidate_id": candidate["id"],
                "profile": candidate["profile"],
                "requested_model": candidate["requested_model"],
                "requested_effort": candidate["requested_effort"],
                "context": {"working_dir": str(context_dir), "launch_agent": launch_agent},
            }
            resolved, failure = _resolve_task_optimization_launch_plan(
                profile=str(candidate["profile"]), config_path=config, launch_agent=launch_agent,
                working_dir=context_dir,
            )
            if failure:
                record["status"] = "unsupported" if "unknown harness profile" in failure else "invalid"
                record["reason"] = failure
            else:
                assert resolved is not None
                profile_resolution = resolved.get("profile_resolution")
                if not isinstance(profile_resolution, dict):
                    record["status"] = "invalid"
                    record["reason"] = "launch plan omitted secret-free profile_resolution"
                    records.append(record)
                    continue
                record.update({
                    "effective_model": resolved.get("model"),
                    "effective_effort": resolved.get("effort") or "n/a",
                    "provider": _provider_for_launch_plan(resolved),
                    "backend": resolved.get("backend"),
                    "profile_hash": receipt_sha256(profile_resolution),
                    "launch_plan_hash": receipt_sha256(resolved),
                    "launch_plan": resolved,
                    "auth": profile_resolution.get("auth"),
                    "quota": _quota_readiness(profile_resolution.get("quota")),
                })
                if record["context"]["working_dir"] != str(resolved.get("working_dir") or ""):
                    record["status"] = "invalid"
                    record["reason"] = "launch plan working_dir differs from requested context"
                elif record["effective_model"] != record["requested_model"]:
                    record["status"] = "invalid"
                    record["reason"] = "effective_model differs from requested_model; fallback is forbidden"
                elif record["effective_effort"] != record["requested_effort"]:
                    record["status"] = "invalid"
                    record["reason"] = "effective_effort differs from requested_effort"
                else:
                    record["status"] = "ready"
            if record["status"] == "unsupported":
                record["status"] = "unsupported"
            records.append(record)
        result = {"schema": TASK_OPTIMIZATION_PREFLIGHT_SCHEMA, "study_id": self.study_id,
                  "study_lock_sha256": receipt_sha256(self.lock()), "candidates": records}
        result["preflight_sha256"] = receipt_sha256(result)
        return result


def _resolve_task_optimization_launch_plan(*, profile: str, config_path: Path,
                                           launch_agent: str, working_dir: Path) -> tuple[dict[str, Any] | None, str]:
    """Return a normalized native dry-run plan without ever executing a CLI provider."""
    if not config_path.is_file():
        return None, f"profile config does not exist: {config_path}"
    if not working_dir.is_dir():
        return None, f"working directory does not exist: {working_dir}"
    command = ["go", "run", "./cmd/kitsoki", "agent", "launch", "--agent", launch_agent,
               "--profile", profile, "--config", str(config_path), "--working-dir", str(working_dir),
               "--task", "Task-optimization preflight only. Do not execute this task."]
    try:
        completed = subprocess.run(command, cwd=REPO_ROOT, text=True, capture_output=True, check=False)
    except OSError as exc:
        return None, f"could not start native launch-plan resolver: {exc}"
    if completed.returncode != 0:
        detail = (completed.stderr or completed.stdout).strip().replace("\n", " ")
        return None, f"native launch-plan resolution failed: {detail or f'exit {completed.returncode}'}"
    try:
        plan = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        return None, f"native launch-plan resolver returned invalid JSON: {exc}"
    if not isinstance(plan, dict):
        return None, "native launch-plan resolver returned a non-object"
    # The launcher safely creates an ephemeral MCP config for some agents. Its
    # random filesystem name is not semantic evidence, so canonicalize it
    # before hashing or persisting the plan receipt.
    command_value = plan.get("command")
    if isinstance(command_value, list):
        normalized_command = [str(value) for value in command_value]
        for index, value in enumerate(normalized_command[:-1]):
            if value == "--mcp-config":
                normalized_command[index + 1] = "<ephemeral-mcp-config>"
        for index, value in enumerate(normalized_command):
            if value.startswith("model_instructions_file="):
                normalized_command[index] = "model_instructions_file=<ephemeral-prompt-file>"
        plan["command"] = normalized_command
    plan["dry_run"] = True
    return plan, ""


def _provider_for_launch_plan(plan: dict[str, Any]) -> str:
    model = str(plan.get("model") or "")
    if model.startswith("hf:"):
        return "synthetic"
    return {"codex": "openai", "claude": "anthropic", "agy": "google", "copilot": "github"}.get(
        str(plan.get("backend") or ""), str(plan.get("backend") or "unknown"))


def _quota_readiness(quota: Any) -> dict[str, str]:
    if not isinstance(quota, dict):
        return {"status": "not-configured"}
    return {"status": "configured-unverified"}


def load_task_optimization_receipts(path: str | Path, *, plan: dict[str, Any], preflight: dict[str, Any]) -> list[dict[str, Any]]:
    """Load immutable result receipts and reject mismatched/corrupt evidence."""
    root = Path(path)
    if root.is_file():
        files = [root]
    elif root.exists():
        # .leases are dispatch inputs, not scored attempts. New attempts are
        # immutable directories containing receipt.json; retain support for
        # legacy cell/*.json records so existing campaigns remain resumable.
        files = sorted(file for file in root.rglob("*.json")
                       if ".leases" not in file.parts and (file.name == "receipt.json" or file.parent == root / file.parent.name))
    else:
        files = []
    plan_ids = {str(cell["id"]) for cell in plan.get("cells", [])}
    plan_cells = {str(cell["id"]): cell for cell in plan.get("cells", [])}
    preflight_candidates = {str(record.get("candidate_id")): record for record in preflight.get("candidates", []) if isinstance(record, dict)}
    plan_digest = receipt_sha256(plan)
    preflight_digest = str(preflight.get("preflight_sha256") or receipt_sha256(preflight))
    receipts: list[dict[str, Any]] = []
    seen: set[str] = set()
    for file in files:
        data = json.loads(file.read_text(encoding="utf-8"))
        if data.get("schema") != TASK_OPTIMIZATION_ATTEMPT_SCHEMA:
            raise ValueError(f"{file}: unsupported attempt receipt schema")
        attempt_id = str(data.get("attempt_id") or "")
        cell_id = str(data.get("cell_id") or "")
        if not attempt_id or attempt_id in seen:
            raise ValueError(f"{file}: attempt_id must be unique and non-empty")
        if cell_id not in plan_ids:
            raise ValueError(f"{file}: cell_id {cell_id!r} is absent from the plan")
        candidate_id = str(data.get("candidate_id") or "")
        if candidate_id != str(plan_cells[cell_id].get("candidate_id") or ""):
            raise ValueError(f"{file}: candidate_id does not match the planned cell")
        preflight_candidate = preflight_candidates.get(candidate_id, {})
        if preflight_candidate.get("status") != "ready":
            raise ValueError(f"{file}: candidate {candidate_id!r} lacks a ready preflight receipt")
        boundary_missing = [key for key in ("profile_hash", "launch_plan_hash") if not preflight_candidate.get(key)]
        if boundary_missing:
            raise ValueError(f"{file}: ready candidate {candidate_id!r} lacks preflight boundary fields: {', '.join(boundary_missing)}")
        if data.get("plan_sha256") != plan_digest:
            raise ValueError(f"{file}: plan_sha256 does not match immutable plan")
        if data.get("preflight_sha256") != preflight_digest:
            raise ValueError(f"{file}: preflight_sha256 does not match immutable preflight")
        status = str(data.get("status") or "")
        if status not in TASK_OPTIMIZATION_CELL_STATES:
            raise ValueError(f"{file}: invalid task-optimization status {status!r}")
        validate_scored_attempt_receipt(
            data,
            receipt_path=file,
            preflight_candidate=preflight_candidate,
            requires_codeact_runtime="codeact" in str(plan_cells[cell_id].get("treatment") or ""),
        )
        seen.add(attempt_id)
        receipts.append(data)
    return receipts


def _retryable_infra(receipt: dict[str, Any]) -> bool:
    """Whether a recorded blocked attempt is an infrastructure retry, not a verdict."""
    if str(receipt.get("status") or "") != "blocked":
        return False
    if receipt.get("retryable") is True:
        return True
    return str(receipt.get("health") or "").startswith("infra:") and receipt.get("retryable") is not False


def _cell_lifecycle(attempts: list[dict[str, Any]], *, max_infra_retries: int) -> dict[str, Any]:
    """Reduce append-only attempts without allowing an infra failure to grade a model."""
    retryable = [item for item in attempts if _retryable_infra(item)]
    model_results = [item for item in attempts if str(item.get("status") or "") == "scored"]
    terminal_blocks = [item for item in attempts if str(item.get("status") or "") == "blocked" and not _retryable_infra(item)]
    unsupported = [item for item in attempts if str(item.get("status") or "") == "unsupported"]
    if model_results:
        return {"status": "scored", "result": model_results[-1], "retryable_infra_attempts": len(retryable)}
    if terminal_blocks:
        return {"status": "blocked", "result": terminal_blocks[-1], "retryable_infra_attempts": len(retryable)}
    if unsupported:
        return {"status": "unsupported", "result": unsupported[-1], "retryable_infra_attempts": len(retryable)}
    if len(retryable) > max_infra_retries:
        return {"status": "blocked", "result": None, "retryable_infra_attempts": len(retryable), "reason": "infra retry budget exhausted"}
    if any(str(item.get("status") or "") in {"running", "armed"} for item in attempts):
        return {"status": "running", "result": None, "retryable_infra_attempts": len(retryable)}
    if retryable:
        return {"status": "retryable_infra", "result": None, "retryable_infra_attempts": len(retryable)}
    return {"status": "planned", "result": None, "retryable_infra_attempts": 0}


def _promotion_policy(plan: dict[str, Any]) -> dict[str, Any]:
    """Read a small, explicit selection policy with conservative defaults."""
    declared = plan.get("promotion_policy") or {}
    if not isinstance(declared, dict):
        raise ValueError("plan promotion_policy must be an object")
    policy = {
        "min_scored": int(declared.get("min_scored", 1)),
        "min_solved": int(declared.get("min_solved", 1)),
        "min_solve_rate": float(declared.get("min_solve_rate", 0.0)),
        "require_matched_tasks": bool(declared.get("require_matched_tasks", True)),
    }
    if policy["min_scored"] < 1 or policy["min_solved"] < 0 or not 0 <= policy["min_solve_rate"] <= 1:
        raise ValueError("invalid promotion_policy thresholds")
    return policy


def task_optimization_status(plan: dict[str, Any], receipts: list[dict[str, Any]], *, champion: dict[str, Any] | None = None) -> dict[str, Any]:
    """Aggregate attempts into a retry-safe, champion-aware resume state."""
    max_infra_retries = int((plan.get("retry_policy") or {}).get("max_infra_retries", 2))
    if max_infra_retries < 0:
        raise ValueError("retry_policy.max_infra_retries must be non-negative")
    by_cell: dict[str, list[dict[str, Any]]] = {str(cell["id"]): [] for cell in plan["cells"]}
    for receipt in receipts:
        by_cell[receipt["cell_id"]].append(receipt)
    cells: list[dict[str, Any]] = []
    for cell in plan["cells"]:
        attempts = by_cell[cell["id"]]
        lifecycle = _cell_lifecycle(attempts, max_infra_retries=max_infra_retries)
        state = str(lifecycle["status"])
        cells.append({**cell, "status": state, "attempt_count": len(attempts),
                      "attempt_ids": sorted(str(item["attempt_id"]) for item in attempts),
                      "retryable_infra_attempts": lifecycle["retryable_infra_attempts"],
                      **({"reason": lifecycle["reason"]} if lifecycle.get("reason") else {})})
    learning = [cell for cell in cells if cell["split"] == "learning"]
    confirmation = [cell for cell in cells if cell["split"] == "confirmation"]
    if champion is not None:
        confirmation = [cell for cell in confirmation if cell["candidate_id"] == champion.get("candidate_id") and cell["treatment"] == champion.get("treatment")]
    learning_complete = bool(learning) and all(cell["status"] in TASK_OPTIMIZATION_TERMINAL_STATES for cell in learning)
    confirmation_complete = bool(confirmation) and all(cell["status"] in TASK_OPTIMIZATION_TERMINAL_STATES for cell in confirmation)
    phase = "learning"
    if learning_complete:
        phase = "champion_selection"
    if champion is not None:
        phase = "complete" if confirmation_complete else "confirmation"
    return {"schema": "task-optimization/status/v1", "study_id": plan["study_id"], "plan_sha256": receipt_sha256(plan),
            "phase": phase, "learning_complete": learning_complete, "confirmation_complete": confirmation_complete,
            "resume_cell_ids": [cell["id"] for cell in cells if cell["status"] in {"planned", "running", "retryable_infra"}
                                and (champion is None or cell["split"] != "confirmation" or cell in confirmation)], "cells": cells}


def analyze_task_optimization(plan: dict[str, Any], receipts: list[dict[str, Any]]) -> dict[str, Any]:
    """Return treatment-aware learning metrics and a promotion decision input."""
    status = task_optimization_status(plan, receipts)
    if not status["learning_complete"]:
        raise ValueError("cannot analyze champion until every learning cell has a terminal receipt")
    policy = _promotion_policy(plan)
    scores: dict[tuple[str, str], dict[str, Any]] = {}
    for cell in status["cells"]:
        if cell["split"] != "learning":
            continue
        key = (str(cell["candidate_id"]), str(cell["treatment"]))
        score = scores.setdefault(key, {"candidate_id": key[0], "treatment": key[1], "scored": 0, "solved": 0, "tokens": 0, "task_ids": [], "solve_task_ids": []})
        terminal = [item for item in receipts if item["cell_id"] == cell["id"] and item.get("status") == "scored"]
        score["task_ids"].append(cell["task_id"])
        if terminal:
            latest = terminal[-1]
            score["scored"] += 1
            if latest.get("verdict") == "solved":
                score["solved"] += 1
                score["solve_task_ids"].append(cell["task_id"])
            score["tokens"] += int((latest.get("metrics") or {}).get("total_tokens") or 0)
    if not scores:
        raise ValueError("learning plan contains no candidate cells")
    all_tasks = sorted({str(cell["task_id"]) for cell in status["cells"] if cell["split"] == "learning"})
    arms: list[dict[str, Any]] = []
    for score in scores.values():
        score["task_ids"] = sorted(set(score["task_ids"]))
        score["solve_task_ids"] = sorted(set(score["solve_task_ids"]))
        score["solve_rate"] = round(score["solved"] / score["scored"], 6) if score["scored"] else 0.0
        score["matched_task_ids"] = sorted(set(score["task_ids"]) & set(all_tasks))
        score["tokens_per_solved"] = round(score["tokens"] / score["solved"], 6) if score["solved"] else None
        reasons: list[str] = []
        if score["scored"] < policy["min_scored"]: reasons.append("min_scored unmet")
        if score["solved"] < policy["min_solved"]: reasons.append("min_solved unmet")
        if score["solve_rate"] < policy["min_solve_rate"]: reasons.append("min_solve_rate unmet")
        if policy["require_matched_tasks"] and score["matched_task_ids"] != all_tasks: reasons.append("matched task set incomplete")
        score["promotion_eligible"] = not reasons
        score["promotion_reasons"] = reasons
        arms.append(score)
    return {"schema": "task-optimization/analysis/v1", "study_id": plan["study_id"], "plan_sha256": receipt_sha256(plan),
            "policy": policy, "learning_task_ids": all_tasks,
            "arms": sorted(arms, key=lambda arm: (arm["candidate_id"], arm["treatment"]))}


def compare_task_optimization(plan: dict[str, Any], receipts: list[dict[str, Any]]) -> dict[str, Any]:
    """Build exhaustive pairwise arm deltas from the immutable analysis input."""
    analysis = analyze_task_optimization(plan, receipts)
    comparisons: list[dict[str, Any]] = []
    arms = list(analysis["arms"])
    for left_index, left in enumerate(arms):
        for right in arms[left_index + 1:]:
            left_set, right_set = set(left["solve_task_ids"]), set(right["solve_task_ids"])
            union = left_set | right_set
            comparisons.append({
                "left": {"candidate_id": left["candidate_id"], "treatment": left["treatment"]},
                "right": {"candidate_id": right["candidate_id"], "treatment": right["treatment"]},
                "matched_task_ids": sorted(set(left["task_ids"]) & set(right["task_ids"])),
                "left_only_solved_task_ids": sorted(left_set - right_set),
                "right_only_solved_task_ids": sorted(right_set - left_set),
                "solve_set_jaccard": round(len(left_set & right_set) / len(union), 6) if union else None,
                "solved_delta_left_minus_right": left["solved"] - right["solved"],
                "tokens_delta_left_minus_right": left["tokens"] - right["tokens"],
            })
    return {"schema": "task-optimization/comparison/v1", "study_id": plan["study_id"],
            "plan_sha256": receipt_sha256(plan), "analysis_sha256": receipt_sha256(analysis),
            "arms": analysis["arms"], "comparisons": comparisons}


def select_task_optimization_champion(plan: dict[str, Any], receipts: list[dict[str, Any]]) -> dict[str, Any]:
    """Freeze one candidate+treatment arm only after its policy promotion bar passes."""
    analysis = analyze_task_optimization(plan, receipts)
    eligible = [arm for arm in analysis["arms"] if arm["promotion_eligible"]]
    if not eligible:
        raise ValueError("no candidate+treatment arm satisfies the promotion policy")
    winner = min(eligible, key=lambda arm: (-arm["solved"], -arm["solve_rate"], arm["tokens"], arm["candidate_id"], arm["treatment"]))
    champion = {"schema": TASK_OPTIMIZATION_CHAMPION_SCHEMA, "study_id": plan["study_id"],
                "plan_sha256": receipt_sha256(plan), "candidate_id": winner["candidate_id"], "treatment": winner["treatment"],
                "policy": analysis["policy"], "analysis_sha256": receipt_sha256(analysis), "scores": analysis["arms"]}
    champion["champion_sha256"] = receipt_sha256(champion)
    return champion

# Check-type discriminator (WS-G G1): a cell declares a check SUITE, not one
# verdict shape, and every check emits a completion-state verdict tagged with
# its type (schemas/completion-state.schema.json `check_type`; absent == the
# default "replay"). `replay` is the container/plugin path below. `journey-
# verdict`/`ux-heuristic` (WS-G G6) are FILE-ADAPTER checks: they read an
# already-written kitsoki-ui-qa / kitsoki-ui-review `verdict.json` off disk via
# tools.persona_qa.ui_verdict — no container spawn, no LLM call of their own
# (see arena/checks.py's `run_ui_verdict_check`). `docs-fidelity` remains
# declared-but-not-implemented: specs may declare it (validation accepts it)
# and execution reports an honest `pending` verdict, never a fake green.
CHECK_TYPES = ("replay", "docs-fidelity", "ux-heuristic", "journey-verdict")
DEFAULT_CHECK_TYPE = "replay"
# Check types execution never falls back to `unimplemented_check_result` for.
# `replay` runs a container; `journey-verdict`/`ux-heuristic` run the file
# adapter (arena/checks.py FILE_ADAPTER_CHECK_TYPES) — both can still yield an
# honest `pending` verdict at the CellResult level (e.g. no verdict.json
# configured/found yet), but the check_type itself is implemented.
IMPLEMENTED_CHECK_TYPES = ("replay", "journey-verdict", "ux-heuristic")


@dataclass(frozen=True)
class CheckSpec:
    """One declared check in a cell's check suite: a type + how to run/collect.

    `options` is the check-type-specific "how" (e.g. a future docs-fidelity
    check's docs path or persona id); the replay check needs none — its "how"
    is the job-type plugin itself.
    """

    check_type: str = DEFAULT_CHECK_TYPE
    options: dict[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        if self.check_type not in CHECK_TYPES:
            raise ValueError(
                f"unknown check_type {self.check_type!r}; known: {list(CHECK_TYPES)}"
            )


def _check_spec_from(entry: Any) -> CheckSpec:
    """Parse one spec-file `checks:` entry — a bare type string or a mapping."""
    if isinstance(entry, str):
        return CheckSpec(check_type=entry)
    if isinstance(entry, dict):
        data = dict(entry)
        check_type = data.pop("check_type", data.pop("type", DEFAULT_CHECK_TYPE))
        options = data.pop("options", None)
        if options is None:
            options = data  # remaining keys fold into options (least surprise)
        return CheckSpec(check_type=check_type, options=dict(options))
    raise ValueError(f"checks entry must be a string or mapping, got {entry!r}")


@dataclass
class CellResult:
    """A graded cell. `verdict` is from VERDICTS; `health` separates INFRA from MODEL."""

    cell_id: str
    job_type: str
    target_id: str
    variant_id: str
    axis: dict[str, str] = field(default_factory=dict)
    verdict: str = "pending"
    health: str = "incomplete"          # infra:* | model:result | incomplete
    metrics: dict[str, Any] = field(default_factory=dict)   # cost_usd, tokens, wall_s
    evidence_refs: list[str] = field(default_factory=list)
    trace_ref: str = ""
    notes: str = ""
    # Which proof class this verdict grades (WS-G G1). Default "replay" — the
    # only value pre-check-suite consumers ever meant.
    check_type: str = DEFAULT_CHECK_TYPE

    def to_dict(self) -> dict[str, Any]:
        d = asdict(self)
        # Schema semantics: absent check_type == "replay". Omitting the default
        # keeps every pre-check-suite consumer (and the golden rollup bytes)
        # unchanged; only non-replay checks carry the discriminator explicitly.
        if self.check_type == DEFAULT_CHECK_TYPE:
            d.pop("check_type")
        return d


@dataclass
class Placement:
    """Where cell containers run. A host is a docker context; `local` = local daemon."""

    hosts: list[str] = field(default_factory=lambda: ["local"])
    concurrency: int = 1
    retry: int = 0
    # Where the kitsoki checkout lives on each host's daemon. A remote docker
    # context resolves `-v` paths on the REMOTE filesystem, so a VM host must
    # point at the checkout ON THE VM. `local` defaults to the local REPO_ROOT
    # (filled in by the CLI); any host absent here falls back to that default.
    host_repo: dict[str, str] = field(default_factory=dict)


def _target_from_dict(t: dict[str, Any]) -> Target:
    return Target(
        id=t["id"],
        label=t.get("label", t["id"]),
        repo=t.get("repo", ""),
        stack=t.get("stack", ""),
        meta={k: v for k, v in t.items() if k not in {"id", "label", "repo", "stack"}},
    )


def load_targets_from_corpus(path: str | Path) -> list[Target]:
    """Materialize Targets from a corpus, in either of two shapes.

    - A single file: the github-targets.json shape,
      `{"targets": [{"id", "label", "repo", "stack", ...}, ...]}` (product-
      journey remains its owner, read-only here).
    - A directory: one JSON document per file, each document itself already
      target-shaped (requires an `"id"` field; everything else — however
      many extra keys — folds into `Target.meta` via `_target_from_dict`,
      same as an inlined target would). This is how `usable-kitsoki-gate`
      (S6) consumes S4's scenario-foundry IR corpus
      (`tools/session-mining/calibration/`, schema
      `tools/session-mining/schema/scenario_ir.schema.json`) as its cell
      enumeration: each `scn-*.json` scenario IR doc becomes one Target
      (`id` = the scenario id, `meta` carries `persona`, `goal`,
      `expected_effects`, `abandoned`, `provenance`, etc. verbatim) without
      this module needing any scenario-IR-specific parsing — it is already
      a generic "directory of target-shaped JSON docs" loader. Non-`.json`
      siblings (e.g. a `MANIFEST.md`) and subdirectories (e.g. a `flows/`
      compiled-fixture dir) are ignored; files are read in sorted order so
      enumeration is deterministic.
    """
    p = _resolve_repo_path(path)
    if p.is_dir():
        docs = [
            json.loads(f.read_text(encoding="utf-8")) for f in sorted(p.glob("*.json"))
        ]
        return [_target_from_dict(t) for t in docs]
    data = json.loads(p.read_text(encoding="utf-8"))
    return [_target_from_dict(t) for t in data.get("targets", [])]


def load_target_proof_checks(path: str | Path) -> dict[str, dict[str, Any]]:
    """Load a product-journey target-proof.json's per-target checks by target id.

    Accepts either the proof file itself or its containing directory (the
    proof dir convention used by `.artifacts/product-journey/target-proofs/<id>/`).
    Returns {} if no proof is found — proof is optional metadata, never required.
    """
    p = _resolve_repo_path(path)
    if p.is_dir():
        p = p / "target-proof.json"
    if not p.exists():
        return {}
    data = json.loads(p.read_text(encoding="utf-8"))
    return {c["target"]: c for c in data.get("checks", []) if "target" in c}


def load_persona_axis_values(path: str | Path) -> list[str]:
    """Load persona ids from a personas.json-shaped file for use as axis values."""
    data = json.loads(_resolve_repo_path(path).read_text(encoding="utf-8"))
    return [p["id"] for p in data.get("personas", [])]


@dataclass
class JobSpec:
    """Declarative comparison job. Enumerates into cells (target × variant × axes)."""

    job_type: str
    targets: list[Target]
    variants: list[Variant]
    axes: dict[str, list[str]] = field(default_factory=dict)   # extra per-job axes
    placement: Placement = field(default_factory=Placement)
    options: dict[str, Any] = field(default_factory=dict)      # job-type-specific knobs
    # The cell's check suite (WS-G G1). Every cell in the job runs every
    # declared check; aggregation is one verdict per cell per check_type.
    # Default: the single replay check — exactly the pre-check-suite behavior.
    checks: list[CheckSpec] = field(default_factory=lambda: [CheckSpec()])
    # Corpus provenance (informational; the fields above are already resolved).
    targets_from: str = ""
    persona_axis_from: str = ""
    target_proof_from: str = ""

    # ---- loading -------------------------------------------------------------
    @staticmethod
    def from_dict(data: dict[str, Any]) -> "JobSpec":
        targets_from = data.get("targets_from", "") or ""
        if data.get("targets"):
            # Hand-inlined targets take precedence and keep working unchanged.
            targets = [_target_from_dict(t) for t in data["targets"]]
        elif targets_from:
            targets = load_targets_from_corpus(targets_from)
        else:
            targets = []

        target_proof_from = data.get("target_proof_from", "") or ""
        if target_proof_from:
            proof_checks = load_target_proof_checks(target_proof_from)
            if proof_checks:
                targets = [
                    Target(
                        id=t.id,
                        label=t.label,
                        repo=t.repo,
                        stack=t.stack,
                        meta={**t.meta, "target_proof": proof_checks[t.id]}
                        if t.id in proof_checks
                        else t.meta,
                    )
                    for t in targets
                ]

        variants = [
            Variant(
                id=v["id"],
                backend=v.get("backend", ""),
                model=v.get("model", ""),
                effort=v.get("effort", ""),
                meta={k: val for k, val in v.items() if k not in {"id", "backend", "model", "effort"}},
            )
            for v in data.get("variants", [])
        ]

        axes = dict(data.get("axes", {}) or {})
        persona_axis_from = data.get("persona_axis_from", "") or ""
        if persona_axis_from and "persona" not in axes:
            # Hand-inlined `axes.persona` (if any) always wins — this only
            # fills in the axis when the spec didn't hand-copy persona ids.
            axes["persona"] = load_persona_axis_values(persona_axis_from)

        checks_data = data.get("checks") or []
        checks = [_check_spec_from(c) for c in checks_data] or [CheckSpec()]
        # A duplicated check_type would double-report a verdict per type —
        # reject it at load time rather than surprising the rollup.
        seen_types = [c.check_type for c in checks]
        if len(seen_types) != len(set(seen_types)):
            raise ValueError(f"duplicate check_type in checks: {seen_types}")

        place = data.get("placement", {}) or {}
        return JobSpec(
            job_type=data["job_type"],
            targets=targets,
            variants=variants,
            axes=axes,
            placement=Placement(
                hosts=place.get("hosts", ["local"]),
                concurrency=int(place.get("concurrency", 1)),
                retry=int(place.get("retry", 0)),
                host_repo=dict(place.get("host_repo", {}) or {}),
            ),
            options=data.get("options", {}) or {},
            checks=checks,
            targets_from=targets_from,
            persona_axis_from=persona_axis_from,
            target_proof_from=target_proof_from,
        )

    @staticmethod
    def load(path: str | Path) -> "JobSpec":
        text = Path(path).read_text(encoding="utf-8")
        try:
            import yaml  # type: ignore

            data = yaml.safe_load(text)
        except ModuleNotFoundError:
            data = json.loads(text)
        return JobSpec.from_dict(data)

    # ---- enumeration ---------------------------------------------------------
    def cells(self) -> list[Cell]:
        """Cross-product targets × variants × every axis → deterministic cell list."""
        axis_names = sorted(self.axes)
        axis_value_lists = [self.axes[name] for name in axis_names]
        combos = list(itertools.product(*axis_value_lists)) if axis_value_lists else [()]
        out: list[Cell] = []
        for target in self.targets:
            for variant in self.variants:
                for combo in combos:
                    axis = {name: val for name, val in zip(axis_names, combo)}
                    cid = "--".join(
                        [target.id, variant.id] + [f"{n}:{axis[n]}" for n in axis_names]
                    )
                    out.append(
                        Cell(
                            id=cid,
                            job_type=self.job_type,
                            target=target,
                            variant=variant,
                            axis=axis,
                            options=dict(self.options),
                        )
                    )
        return out
