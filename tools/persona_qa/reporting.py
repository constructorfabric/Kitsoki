"""Shared, job-type-agnostic rollup: aggregate graded records into a leaderboard.

This is the SINGLE rollup/deck-building implementation for both arena
(bugfix, and any future arena job type) and product-journey/persona-qa. It was
extracted from `tools/arena/arena/rollup.py`'s private `_bucket` /
`build_rollup` / `write_rollup` functions, which mirrored product-journey's own
rollup logic without importing it — two rollup brains drifting independently.
`tools/arena/arena/rollup.py` is now a thin delegating shim over this module
(see that file).

Records: anything with `verdict`, `health`, `metrics` and, for the grouped
views, `variant_id` / `target_id` / `job_type` / `axis` — either as a mapping
(e.g. a raw completion-state dict per schemas/completion-state.schema.json,
merged with its cell coordinates) or as an object exposing those as attributes
(arena's `CellResult` dataclass). `_field`/`_axis`/`_as_dict` below read either
shape uniformly so callers never need to convert.

Bucketing is generalized beyond arena's original variant/target/job_type axes:
any `axis` key present on at least one record — e.g. persona-qa's `persona` /
`scenario` axes — gets its own `by_<axis>` bucket automatically. A rollup over
records whose axis never carries that key (e.g. arena's bugfix shape, whose
only axis is `bug`) omits the bucket entirely, which is what keeps this
module's output byte-identical to the original arena-only rollup for that
shape (see tools/persona_qa/tests/test_shared_rollup.py's golden test).
"""

from __future__ import annotations

import json
from collections import defaultdict
from pathlib import Path
from typing import Any, Callable, Mapping, Sequence

# Verdicts counted as a "win" for win-rate purposes. "armed" is arena's no-LLM
# skeleton success state; "solved" is the shared bugfix/persona-qa verdict
# vocabulary (schemas/completion-state.schema.json + arena/model.py VERDICTS).
_SOLVED = {"solved", "armed"}

# Axis keys that, when present on ANY record's `axis` mapping, get their own
# `by_<name>` bucket in the rollup — beyond the always-present variant/target/
# job_type buckets. Extend this list as new job types add axes worth reporting
# on; a name absent from every record's axis produces no bucket (see module
# docstring re: byte-compatibility with arena's original bugfix-only rollup).
_OPTIONAL_AXES: tuple[str, ...] = ("persona", "scenario")


def _as_dict(record: Any) -> dict[str, Any]:
    """Normalize a record (mapping or `.to_dict()`-able object) to a plain dict."""
    to_dict = getattr(record, "to_dict", None)
    if callable(to_dict):
        return to_dict()
    return dict(record)


def _field(record: Any, name: str, default: Any = None) -> Any:
    """Read `name` off a record whether it's a mapping or an attribute-bearing object."""
    if isinstance(record, Mapping):
        return record.get(name, default)
    return getattr(record, name, default)


def _axis(record: Any) -> Mapping[str, Any]:
    return _field(record, "axis", {}) or {}


def _bucket(records: Sequence[Any]) -> dict[str, Any]:
    n = len(records)
    counts: dict[str, int] = defaultdict(int)
    for r in records:
        counts[_field(r, "verdict")] += 1
    costs = [
        m.get("cost_usd")
        for r in records
        for m in [_field(r, "metrics", {}) or {}]
        if isinstance(m.get("cost_usd"), (int, float))
    ]
    won = sum(1 for r in records if _field(r, "verdict") in _SOLVED)
    infra = sum(1 for r in records if str(_field(r, "health", "")).startswith("infra:"))
    return {
        "n": n,
        "verdicts": dict(counts),
        "win_rate": round(won / n, 4) if n else None,
        "infra_failures": infra,
        "avg_cost_usd": round(sum(costs) / len(costs), 6) if costs else None,
    }


def _group(records: Sequence[Any], key: Callable[[Any], str]) -> dict[str, Any]:
    groups: dict[str, list[Any]] = defaultdict(list)
    for r in records:
        groups[key(r)].append(r)
    return {name: _bucket(rs) for name, rs in sorted(groups.items())}


def build_rollup(
    records: Sequence[Any],
    *,
    optional_axes: Sequence[str] = _OPTIONAL_AXES,
) -> dict[str, Any]:
    """Aggregate `records` (CellResult-shaped or completion-state-shaped) into a rollup.

    Always buckets by variant/target/job_type (arena's original axes). Any
    `optional_axes` name (default: persona, scenario) present in at least one
    record's `axis` mapping additionally gets a `by_<name>` bucket — the
    generalization that lets a persona-qa completion-state rollup (bucketed by
    persona/scenario) flow through the same function as arena's bugfix rollup
    (bucketed by variant/target), with no shape-specific branching.
    """
    rollup: dict[str, Any] = {
        "summary": _bucket(records),
        "by_variant": _group(records, lambda r: _field(r, "variant_id")),
        "by_target": _group(records, lambda r: _field(r, "target_id")),
        "by_job_type": _group(records, lambda r: _field(r, "job_type")),
    }
    for axis_name in optional_axes:
        if any(axis_name in _axis(r) for r in records):
            rollup[f"by_{axis_name}"] = _group(
                records, lambda r, a=axis_name: _axis(r).get(a, "")
            )
    rollup["cells"] = [_as_dict(r) for r in records]
    return rollup


def write_rollup(
    records: Sequence[Any],
    out_dir: str | Path,
    *,
    title: str = "Rollup",
    optional_axes: Sequence[str] = _OPTIONAL_AXES,
) -> dict[str, str]:
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    rollup = build_rollup(records, optional_axes=optional_axes)
    (out / "rollup.json").write_text(json.dumps(rollup, indent=2, sort_keys=True), encoding="utf-8")
    (out / "rollup.md").write_text(_markdown(rollup, title=title), encoding="utf-8")
    return {"rollup": str(out / "rollup.json"), "summary": str(out / "rollup.md")}


def _markdown(rollup: dict[str, Any], *, title: str = "Rollup") -> str:
    s = rollup["summary"]
    lines = [
        f"# {title}",
        "",
        f"- cells: **{s['n']}**  · win-rate: **{s['win_rate']}**  · infra failures: {s['infra_failures']}",
        "",
        "## By variant",
        "",
        "| variant | n | win-rate | avg cost | verdicts |",
        "|---|---|---|---|---|",
    ]
    for name, b in rollup["by_variant"].items():
        cost = "—" if b["avg_cost_usd"] is None else f"${b['avg_cost_usd']:.4f}"
        lines.append(f"| {name} | {b['n']} | {b['win_rate']} | {cost} | {b['verdicts']} |")
    lines += ["", "## By target", "", "| target | n | win-rate | verdicts |", "|---|---|---|---|"]
    for name, b in rollup["by_target"].items():
        lines.append(f"| {name} | {b['n']} | {b['win_rate']} | {b['verdicts']} |")
    lines.append("")
    for axis_name in ("persona", "scenario"):
        key = f"by_{axis_name}"
        if key not in rollup:
            continue
        lines += [f"## By {axis_name}", "", f"| {axis_name} | n | win-rate | verdicts |", "|---|---|---|---|"]
        for name, b in rollup[key].items():
            lines.append(f"| {name} | {b['n']} | {b['win_rate']} | {b['verdicts']} |")
        lines.append("")
    return "\n".join(lines)
