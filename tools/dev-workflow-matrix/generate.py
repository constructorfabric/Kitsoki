#!/usr/bin/env python3
"""generate.py — render the dev-workflow surface matrix from its manifest.

WS-F F1 of .context/dev-workflows-surface-matrix-plan.md: the 5-workflow ×
4-surface × 2-repo matrix is a GENERATED artifact seeded from a hand-edited
manifest (tools/dev-workflow-matrix/manifest.yaml), so progress is visible
and regressions bite. This generator reads the manifest plus any standing
completion-state verdict files (schemas/completion-state.schema.json) the
cells point at, and emits the matrix as markdown with BOTH proof-class
verdicts per cell (mechanical = check_type `replay`; experience = one of the
judged check types) and each verdict's freshness (verdict-file mtime date).

A cell without a verdict pointer — or whose pointed file is missing — renders
as "no standing verdict". That is the honest steady state today; the WS-F
arena gate later fills these in.

WS-F gate wiring (`--verdicts-dir`): in addition to a cell's hand-maintained
manifest `verdicts.*.path` pointer, the generator can ingest a whole directory
of completion-state verdict JSON files keyed by `(workflow, surface, repo,
check_type)` (see `run_checks.py`'s `axis.workflow`/`axis.surface`/`target_id`/
`check_type` convention). A verdict found in that directory always wins over a
manifest pointer for the SAME cell + proof class (it is the live signal); a
missing verdict leaves the manifest's static status standing untouched — that
is the honest default, not an error. A `failed`/`blocked` verdict (or an
`infra:*` health) downgrades the cell's effective status to at least `gap`; a
verdict older than `--stale-days` downgrades a `works` cell to `proof-thin`.
`out-of-scope` cells are never downgraded (there is nothing to prove). This
ingestion is opt-in (`--verdicts-dir`) and never changes the checked-in
`docs/testing/dev-workflow-matrix.md`, which stays generated from the manifest
alone (deterministic, no timestamps) — see `make dev-workflow-gate` for where
the live, verdict-aware report is produced instead.

Usage:
  python3 tools/dev-workflow-matrix/generate.py                 # print to stdout
  python3 tools/dev-workflow-matrix/generate.py --out PATH      # write file
  python3 tools/dev-workflow-matrix/generate.py --manifest PATH --repo-root DIR
  python3 tools/dev-workflow-matrix/generate.py --verdicts-dir DIR --gate

The output is deterministic given the manifest + verdict files (no generation
timestamp), so regenerating without changes produces no diff.
"""
from __future__ import annotations

import argparse
import datetime as _dt
import json
import sys
import time
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = Path(__file__).resolve().parent / "manifest.yaml"
DEFAULT_OUT = REPO_ROOT / "docs" / "testing" / "dev-workflow-matrix.md"

STATUS_EMOJI = {
    "works": "✅",
    "proof-thin": "🟡",
    "gap": "🔴",
    "out-of-scope": "⬜",
}

# Severity order for downgrade-on-fail/stale (lower is healthier). Only these
# three participate — `out-of-scope` is exempt (nothing to prove there).
STATUS_SEVERITY = {"works": 0, "proof-thin": 1, "gap": 2}

# Proof classes and the completion-state check_type values each accepts
# (plan §2 principle 1 / WS-G G1; schemas/completion-state.schema.json).
MECHANICAL_CHECK_TYPES = {"replay"}
EXPERIENCE_CHECK_TYPES = {"docs-fidelity", "ux-heuristic", "journey-verdict"}
PROOF_CLASSES = ("mechanical", "experience")
DEFAULT_STALE_DAYS = 14


class ManifestError(ValueError):
    """Raised when the manifest is structurally invalid."""


def load_manifest(path: Path) -> dict:
    with open(path, encoding="utf-8") as fh:
        manifest = yaml.safe_load(fh)
    if not isinstance(manifest, dict):
        raise ManifestError(f"{path}: manifest must be a mapping")
    validate_manifest(manifest, path)
    return manifest


def _ids(manifest: dict, key: str) -> list[str]:
    entries = manifest.get(key) or []
    out = []
    for entry in entries:
        if not isinstance(entry, dict) or "id" not in entry or "title" not in entry:
            raise ManifestError(f"{key}: every entry needs id + title, got {entry!r}")
        out.append(entry["id"])
    if not out:
        raise ManifestError(f"{key}: must be non-empty")
    return out


def validate_manifest(manifest: dict, path: Path) -> None:
    workflows = _ids(manifest, "workflows")
    surfaces = _ids(manifest, "surfaces")
    repos = _ids(manifest, "repos")

    cells = manifest.get("cells") or []
    seen: set[tuple[str, str, str]] = set()
    for cell in cells:
        key = (cell.get("workflow"), cell.get("surface"), cell.get("repo"))
        if key in seen:
            raise ManifestError(f"{path}: duplicate cell {key}")
        seen.add(key)
        if cell.get("workflow") not in workflows:
            raise ManifestError(f"{path}: unknown workflow in cell {key}")
        if cell.get("surface") not in surfaces:
            raise ManifestError(f"{path}: unknown surface in cell {key}")
        if cell.get("repo") not in repos:
            raise ManifestError(f"{path}: unknown repo in cell {key}")
        status = cell.get("status")
        if status not in STATUS_EMOJI:
            raise ManifestError(
                f"{path}: cell {key} status {status!r} not in {sorted(STATUS_EMOJI)}"
            )
        if not cell.get("reason"):
            raise ManifestError(f"{path}: cell {key} needs a one-line reason")
        verdicts = cell.get("verdicts") or {}
        for proof_class, spec in verdicts.items():
            if proof_class not in PROOF_CLASSES:
                raise ManifestError(
                    f"{path}: cell {key} verdict class {proof_class!r} not in {PROOF_CLASSES}"
                )
            allowed = (
                MECHANICAL_CHECK_TYPES if proof_class == "mechanical" else EXPERIENCE_CHECK_TYPES
            )
            check_type = (spec or {}).get("check_type")
            if check_type not in allowed:
                raise ManifestError(
                    f"{path}: cell {key} {proof_class} check_type {check_type!r} "
                    f"must be one of {sorted(allowed)}"
                )
            if not (spec or {}).get("path"):
                raise ManifestError(
                    f"{path}: cell {key} {proof_class} verdict needs a repo-relative path "
                    "(omit the verdicts entry entirely when there is no standing verdict)"
                )

    missing = [
        (w, s, r)
        for r in repos
        for w in workflows
        for s in surfaces
        if (w, s, r) not in seen
    ]
    if missing:
        raise ManifestError(f"{path}: missing cells: {missing}")


def read_verdict(spec: dict | None, repo_root: Path) -> str:
    """One proof-class verdict summary line: verdict + check_type + freshness."""
    if not spec:
        return "no standing verdict"
    verdict_path = repo_root / spec["path"]
    if not verdict_path.exists():
        return f"no standing verdict (pointer `{spec['path']}` absent)"
    try:
        payload = json.loads(verdict_path.read_text(encoding="utf-8"))
        verdict = payload["verdict"]
    except (json.JSONDecodeError, KeyError, OSError) as err:
        return f"unreadable verdict at `{spec['path']}` ({type(err).__name__})"
    fresh = _dt.date.fromtimestamp(verdict_path.stat().st_mtime).isoformat()
    return f"**{verdict}** ({spec['check_type']}, as of {fresh})"


# ---------------------------------------------------------------------------
# WS-F gate wiring: ingest a directory of completion-state verdict files keyed
# to matrix cells by (workflow, surface, repo, check_type), independent of any
# manifest `verdicts.*.path` pointer. See run_checks.py for the writer side.
# ---------------------------------------------------------------------------


class VerdictEntry:
    """One parsed, located verdict file, keyed to a single matrix cell."""

    __slots__ = ("path", "payload", "check_type")

    def __init__(self, path: Path, payload: dict, check_type: str) -> None:
        self.path = path
        self.payload = payload
        self.check_type = check_type

    @property
    def verdict(self) -> str:
        return self.payload.get("verdict", "")

    @property
    def health(self) -> str:
        return self.payload.get("health", "")

    def age_days(self) -> float:
        return (time.time() - self.path.stat().st_mtime) / 86400.0

    def is_failing(self) -> bool:
        return self.verdict in {"failed", "blocked"} or self.health.startswith("infra:")

    def summary(self) -> str:
        fresh = _dt.date.fromtimestamp(self.path.stat().st_mtime).isoformat()
        return f"**{self.verdict}** ({self.check_type}, as of {fresh})"


def scan_verdicts_dir(verdicts_dir: Path) -> dict[tuple[str, str, str, str], VerdictEntry]:
    """Glob every `*.json` under `verdicts_dir`, key by (workflow, surface, repo, check_type).

    Each file is expected to carry the cell coordinates the same way an arena
    `CellResult` would: `axis.workflow` / `axis.surface`, `target_id` (the
    repo id), and an optional `check_type` (absent means `replay`, per the
    schema's discriminator). A file that doesn't parse, or is missing any of
    those coordinates, is skipped — never crashes the render (an unreadable
    verdict is a rendering-time concern for a cell that DOES point at it via
    the manifest; a directory scan silently ignoring a malformed sibling file
    is the right default so one bad write never blocks the whole matrix).
    """
    out: dict[tuple[str, str, str, str], VerdictEntry] = {}
    if not verdicts_dir.exists():
        return out
    for path in sorted(verdicts_dir.glob("*.json")):
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        axis = payload.get("axis") or {}
        workflow = axis.get("workflow") or payload.get("workflow")
        surface = axis.get("surface") or payload.get("surface")
        repo = payload.get("target_id") or payload.get("repo")
        if not (workflow and surface and repo):
            continue
        check_type = payload.get("check_type") or "replay"
        out[(workflow, surface, repo, check_type)] = VerdictEntry(path, payload, check_type)
    return out


def _lookup_verdict(
    verdicts_by_key: dict[tuple[str, str, str, str], VerdictEntry],
    workflow: str,
    surface: str,
    repo: str,
    allowed_check_types: set[str],
) -> VerdictEntry | None:
    for check_type in sorted(allowed_check_types):
        entry = verdicts_by_key.get((workflow, surface, repo, check_type))
        if entry is not None:
            return entry
    return None


def effective_status(
    cell: dict,
    verdicts_by_key: dict[tuple[str, str, str, str], VerdictEntry],
    stale_days: int,
) -> tuple[str, str | None]:
    """Downgrade a cell's manifest status against any live verdicts-dir entries.

    Returns `(effective_status, downgrade_reason_or_None)`. `out-of-scope`
    cells are never downgraded. A verdict this cell has no manifest pointer
    for (a fresh check the manifest hasn't been hand-edited to reference yet)
    still participates — the verdicts-dir is the live source of truth, the
    manifest pointer is just one (still-supported) way to surface it.
    """
    status = cell["status"]
    if status == "out-of-scope":
        return status, None

    worst = STATUS_SEVERITY[status]
    reason = None
    for proof_class, allowed in (
        ("mechanical", MECHANICAL_CHECK_TYPES),
        ("experience", EXPERIENCE_CHECK_TYPES),
    ):
        entry = _lookup_verdict(
            verdicts_by_key, cell["workflow"], cell["surface"], cell["repo"], allowed
        )
        if entry is None:
            continue
        if entry.is_failing():
            if STATUS_SEVERITY["gap"] > worst:
                worst = STATUS_SEVERITY["gap"]
                reason = f"{proof_class} {entry.check_type} verdict is {entry.verdict!r}"
        elif entry.age_days() > stale_days:
            if STATUS_SEVERITY["proof-thin"] > worst:
                worst = STATUS_SEVERITY["proof-thin"]
                reason = (
                    f"{proof_class} {entry.check_type} verdict is stale "
                    f"({entry.age_days():.0f}d > {stale_days}d)"
                )

    for name, sev in STATUS_SEVERITY.items():
        if sev == worst:
            effective = name
            break
    if effective == status:
        return status, None
    return effective, reason


def render(
    manifest: dict,
    repo_root: Path,
    verdicts_dir: Path | None = None,
    stale_days: int = DEFAULT_STALE_DAYS,
) -> str:
    workflows = manifest["workflows"]
    surfaces = manifest["surfaces"]
    repos = manifest["repos"]
    cells = {
        (c["workflow"], c["surface"], c["repo"]): c for c in manifest["cells"]
    }
    verdicts_by_key = scan_verdicts_dir(verdicts_dir) if verdicts_dir else {}

    manifest_rel = "tools/dev-workflow-matrix/manifest.yaml"
    lines = [
        "<!-- GENERATED FILE — DO NOT HAND-EDIT.",
        f"     Source of truth: {manifest_rel}",
        "     Regenerate with: make dev-workflow-matrix -->",
        "",
        "# Dev-workflow surface matrix",
        "",
        f"Generated from `{manifest_rel}` by `tools/dev-workflow-matrix/generate.py`.",
        f"Plan: `{manifest.get('plan', '')}` (§1 target matrix, WS-F F1).",
        "",
        "Legend: ✅ works + no-LLM proven · 🟡 works but proof thin or reliability"
        " suspect · 🔴 gap · ⬜ intentionally out of scope for the surface.",
        "",
        "Every cell carries two proof-class verdicts from"
        " `schemas/completion-state.schema.json`: **mechanical** (check_type"
        " `replay`; no-LLM, per-commit) and **experience** (check_type"
        " `docs-fidelity` / `ux-heuristic` / `journey-verdict`; persona-judged,"
        " budgeted — WS-G). \"no standing verdict\" is the honest state until the"
        " arena gate feeds this matrix.",
        "",
    ]

    for repo in repos:
        lines.append(f"## {repo['title']}")
        lines.append("")
        header = "| Workflow | " + " | ".join(s["title"] for s in surfaces) + " |"
        lines.append(header)
        lines.append("|---|" + "---|" * len(surfaces))
        for wf in workflows:
            row = [f"**{wf['title']}**"]
            for sf in surfaces:
                cell = cells[(wf["id"], sf["id"], repo["id"])]
                status, _ = effective_status(cell, verdicts_by_key, stale_days)
                row.append(f"{STATUS_EMOJI[status]} {status}")
            lines.append("| " + " | ".join(row) + " |")
        lines.append("")
        lines.append("### Cell detail")
        lines.append("")
        for wf in workflows:
            for sf in surfaces:
                cell = cells[(wf["id"], sf["id"], repo["id"])]
                verdicts = cell.get("verdicts") or {}
                status, downgrade_reason = effective_status(cell, verdicts_by_key, stale_days)
                lines.append(
                    f"- **{wf['title']} × {sf['title']}** — "
                    f"{STATUS_EMOJI[status]} {status}: {cell['reason']}"
                )
                if downgrade_reason:
                    lines.append(
                        f"  - downgraded from manifest status `{cell['status']}`: {downgrade_reason}"
                    )
                for proof_class, allowed in (
                    ("mechanical", MECHANICAL_CHECK_TYPES),
                    ("experience", EXPERIENCE_CHECK_TYPES),
                ):
                    entry = _lookup_verdict(
                        verdicts_by_key, cell["workflow"], cell["surface"], cell["repo"], allowed
                    )
                    if entry is not None:
                        summary = entry.summary()
                    else:
                        summary = read_verdict(verdicts.get(proof_class), repo_root)
                    lines.append(f"  - {proof_class}: {summary}")
        lines.append("")

    return "\n".join(lines).rstrip() + "\n"


def gate_failures(
    manifest: dict,
    verdicts_by_key: dict[tuple[str, str, str, str], VerdictEntry],
    stale_days: int,
) -> list[tuple[dict, str, str | None]]:
    """Cells the manifest marks `works` whose effective status is no longer `works`.

    This is WS-F F1's exit criterion: "a red cell blocks declaring the
    workflow supported." A cell the manifest doesn't yet claim as `works`
    can't regress the gate — only a claimed-supported cell whose live verdict
    disagrees blocks.
    """
    failures = []
    for cell in manifest["cells"]:
        if cell["status"] != "works":
            continue
        status, reason = effective_status(cell, verdicts_by_key, stale_days)
        if status != "works":
            failures.append((cell, status, reason))
    return failures


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--out", type=Path, default=None, help="write here instead of stdout")
    parser.add_argument(
        "--verdicts-dir",
        type=Path,
        default=None,
        help="directory of completion-state verdict JSON files (see run_checks.py)",
    )
    parser.add_argument(
        "--stale-days",
        type=int,
        default=DEFAULT_STALE_DAYS,
        help="a verdict older than this many days downgrades a `works` cell to `proof-thin`",
    )
    parser.add_argument(
        "--gate",
        action="store_true",
        help="exit non-zero if any `works` cell's effective status regresses (requires --verdicts-dir)",
    )
    args = parser.parse_args(argv)

    if args.gate and not args.verdicts_dir:
        print("[dev-workflow-matrix] --gate requires --verdicts-dir", file=sys.stderr)
        return 2

    try:
        manifest = load_manifest(args.manifest)
    except ManifestError as err:
        print(f"[dev-workflow-matrix] manifest invalid: {err}", file=sys.stderr)
        return 2

    markdown = render(manifest, args.repo_root, args.verdicts_dir, args.stale_days)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(markdown, encoding="utf-8")
        print(f"[dev-workflow-matrix] wrote {args.out}")
    else:
        sys.stdout.write(markdown)

    if args.gate:
        verdicts_by_key = scan_verdicts_dir(args.verdicts_dir)
        failures = gate_failures(manifest, verdicts_by_key, args.stale_days)
        if failures:
            print(
                f"[dev-workflow-matrix] GATE FAILED: {len(failures)} cell(s) marked "
                "`works` regressed:",
                file=sys.stderr,
            )
            for cell, status, reason in failures:
                print(
                    f"  - {cell['workflow']} x {cell['surface']} x {cell['repo']}: "
                    f"now {status} ({reason})",
                    file=sys.stderr,
                )
            return 1
        print("[dev-workflow-matrix] gate: all `works` cells still hold", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
