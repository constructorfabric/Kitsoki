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
import json
from dataclasses import dataclass, field, asdict
from pathlib import Path
from typing import Any

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
VERDICTS = ("solved", "partial", "failed", "armed", "blocked", "pending")

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
