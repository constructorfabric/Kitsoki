#!/usr/bin/env python3
"""Deterministic tests for the shared rollup module (tools/persona_qa/reporting.py).

Two things must hold with zero LLM/docker spend:

1. Golden byte-compatibility: the shared module, fed the exact bugfix-shaped
   `CellResult` fixture arena's rollup used to bucket privately, produces JSON
   and Markdown byte-identical to the golden output captured from arena's
   ORIGINAL private `_bucket`/`build_rollup`/`_markdown` before it was deleted
   in favor of this shared module (see testdata/golden_rollup.{json,md} and the
   capture script noted below). This is the proof the extraction changed
   nothing for arena's existing shape.

2. Data-completeness for persona-qa: the same module, fed persona-qa-shaped
   completion-state records (bucketed by variant/target as usual, PLUS a
   persona and a scenario axis), produces `by_persona` / `by_scenario` buckets
   with correct counts/win-rates — proving the shared module is a
   data-complete input for the persona-qa deck swap noted at
   arena/rollup.py's old header comment.

The golden fixture files under testdata/ were captured with this exact
snippet run against the pre-extraction tools/arena/arena/rollup.py (git
history: the commit immediately before this one has the private
_bucket/build_rollup/_markdown implementation being replaced here):

    from arena.model import CellResult
    from arena.rollup import build_rollup, _markdown
    rollup = build_rollup(FIXTURE)   # FIXTURE == BUGFIX_FIXTURE below
    json.dump(rollup, open("golden_rollup.json", "w"), indent=2, sort_keys=True)
    open("golden_rollup.md", "w").write(_markdown(rollup))
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "tools" / "arena"))

from arena.model import CellResult  # noqa: E402
from tools.persona_qa import reporting  # noqa: E402

TESTDATA = Path(__file__).resolve().parent / "testdata"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


# ---------------------------------------------------------------------------
# 1. Golden byte-compatibility over arena's bugfix shape.
# ---------------------------------------------------------------------------

BUGFIX_FIXTURE = [
    CellResult(cell_id="query-string--kitsoki-gpt-5.5--bug:qs1", job_type="bugfix",
               target_id="query-string", variant_id="kitsoki-gpt-5.5", axis={"bug": "qs1"},
               verdict="armed", health="model:result", metrics={"cost_usd": 0.12},
               evidence_refs=[], trace_ref="", notes="ok"),
    CellResult(cell_id="query-string--kitsoki-gpt-5.5--bug:qs2", job_type="bugfix",
               target_id="query-string", variant_id="kitsoki-gpt-5.5", axis={"bug": "qs2"},
               verdict="failed", health="model:result", metrics={"cost_usd": 0.30},
               evidence_refs=[], trace_ref="", notes="failed"),
    CellResult(cell_id="query-string--kitsoki-gpt-5.5--bug:qs3", job_type="bugfix",
               target_id="query-string", variant_id="kitsoki-gpt-5.5", axis={"bug": "qs3"},
               verdict="armed", health="model:result", metrics={"cost_usd": 0.09},
               evidence_refs=[], trace_ref="", notes="ok"),
    CellResult(cell_id="query-string--single-gpt-5.5--bug:qs1", job_type="bugfix",
               target_id="query-string", variant_id="single-gpt-5.5", axis={"bug": "qs1"},
               verdict="armed", health="model:result", metrics={"cost_usd": 0.5},
               evidence_refs=[], trace_ref="", notes="ok"),
    CellResult(cell_id="query-string--single-gpt-5.5--bug:qs2", job_type="bugfix",
               target_id="query-string", variant_id="single-gpt-5.5", axis={"bug": "qs2"},
               verdict="blocked", health="infra:harness", metrics={},
               evidence_refs=[], trace_ref="", notes="infra"),
    CellResult(cell_id="query-string--single-gpt-5.5--bug:qs3", job_type="bugfix",
               target_id="query-string", variant_id="single-gpt-5.5", axis={"bug": "qs3"},
               verdict="failed", health="model:result", metrics={"cost_usd": 0.4},
               evidence_refs=[], trace_ref="", notes="failed"),
]

golden_json = json.loads((TESTDATA / "golden_rollup.json").read_text(encoding="utf-8"))
golden_md = (TESTDATA / "golden_rollup.md").read_text(encoding="utf-8")

rollup = reporting.build_rollup(BUGFIX_FIXTURE)
check("golden rollup dict byte-identical", rollup, golden_json)
check("golden rollup json byte-identical", json.dumps(rollup, indent=2, sort_keys=True) + "\n",
      (TESTDATA / "golden_rollup.json").read_text(encoding="utf-8"))
md = reporting._markdown(rollup, title="Arena rollup")
check("golden rollup markdown byte-identical", md, golden_md)

# No persona/scenario buckets should appear for arena's bugfix shape (its axis
# only ever carries "bug") — this is what keeps the golden dict byte-identical
# without arena having to opt out of the shared module's generalization.
check("no by_persona bucket for bugfix shape", "by_persona" in rollup, False)
check("no by_scenario bucket for bugfix shape", "by_scenario" in rollup, False)

# write_rollup() end-to-end: same bytes land on disk.
with tempfile.TemporaryDirectory() as tmp:
    paths = reporting.write_rollup(BUGFIX_FIXTURE, tmp, title="Arena rollup")
    check("write_rollup rollup.json byte-identical",
          Path(paths["rollup"]).read_text(encoding="utf-8"),
          (TESTDATA / "golden_rollup.json").read_text(encoding="utf-8").rstrip("\n"))
    check("write_rollup rollup.md byte-identical",
          Path(paths["summary"]).read_text(encoding="utf-8"),
          golden_md)

# And arena/rollup.py's delegating shim reproduces the exact same thing.
sys.path.insert(0, str(ROOT / "tools" / "arena"))
from arena import rollup as arena_rollup  # noqa: E402

shim_rollup = arena_rollup.build_rollup(BUGFIX_FIXTURE)
check("shim build_rollup matches golden", shim_rollup, golden_json)
check("shim _markdown matches golden", arena_rollup._markdown(shim_rollup), golden_md)


# ---------------------------------------------------------------------------
# 2. Persona-qa-shaped completion-state rollup: persona/scenario buckets.
# ---------------------------------------------------------------------------

# Records shaped like a persona-qa arena cell: a CellResult whose axis carries
# persona/scenario coords (the arena-persona-qa plugin's cells) built from a
# completion state (schemas/completion-state.schema.json — verdict/health/
# metrics below mirror what tools/persona_qa/completion.py's CompletionState
# emits via .to_dict()).
PERSONA_FIXTURE = [
    CellResult(cell_id="onboarding--claude--persona:pm--scenario:first-run", job_type="persona-qa",
               target_id="onboarding", variant_id="claude",
               axis={"persona": "pm", "scenario": "first-run"},
               verdict="solved", health="model:result",
               metrics={"checks_passed": 5, "checks_total": 5}, evidence_refs=["e1"],
               trace_ref="", notes="completed: review=ready; validation=valid"),
    CellResult(cell_id="onboarding--claude--persona:pm--scenario:invite-team", job_type="persona-qa",
               target_id="onboarding", variant_id="claude",
               axis={"persona": "pm", "scenario": "invite-team"},
               verdict="partial", health="model:result",
               metrics={"checks_passed": 3, "checks_total": 5}, evidence_refs=[],
               trace_ref="", notes="incomplete: 2 blockers"),
    CellResult(cell_id="onboarding--claude--persona:eng--scenario:first-run", job_type="persona-qa",
               target_id="onboarding", variant_id="claude",
               axis={"persona": "eng", "scenario": "first-run"},
               verdict="solved", health="model:result",
               metrics={"checks_passed": 6, "checks_total": 6}, evidence_refs=["e2"],
               trace_ref="", notes="completed"),
    CellResult(cell_id="onboarding--codex--persona:eng--scenario:first-run", job_type="persona-qa",
               target_id="onboarding", variant_id="codex",
               axis={"persona": "eng", "scenario": "first-run"},
               verdict="blocked", health="infra:harness",
               metrics={}, evidence_refs=[],
               trace_ref="", notes="driver crashed before completion-state was written"),
]

persona_rollup = reporting.build_rollup(PERSONA_FIXTURE)

check("persona rollup has by_persona", "by_persona" in persona_rollup, True)
check("persona rollup has by_scenario", "by_scenario" in persona_rollup, True)
check("persona buckets", sorted(persona_rollup["by_persona"]), ["eng", "pm"])
check("scenario buckets", sorted(persona_rollup["by_scenario"]), ["first-run", "invite-team"])

check("persona pm n", persona_rollup["by_persona"]["pm"]["n"], 2)
check("persona pm win_rate", persona_rollup["by_persona"]["pm"]["win_rate"], 0.5)
check("persona eng n", persona_rollup["by_persona"]["eng"]["n"], 2)
check("persona eng win_rate", persona_rollup["by_persona"]["eng"]["win_rate"], 0.5)
check("scenario first-run n", persona_rollup["by_scenario"]["first-run"]["n"], 3)
check("scenario first-run win_rate", persona_rollup["by_scenario"]["first-run"]["win_rate"], 0.6667)
check("scenario invite-team n", persona_rollup["by_scenario"]["invite-team"]["n"], 1)
check("scenario invite-team infra_failures", persona_rollup["by_scenario"]["first-run"]["infra_failures"], 1)

check("by_variant still present", sorted(persona_rollup["by_variant"]), ["claude", "codex"])
check("by_target still present", sorted(persona_rollup["by_target"]), ["onboarding"])
check("summary n", persona_rollup["summary"]["n"], 4)
check("summary win_rate", persona_rollup["summary"]["win_rate"], 0.5)
check("cells is data-complete (full record dicts)", len(persona_rollup["cells"]), 4)
check("cell dict carries axis", persona_rollup["cells"][0]["axis"], {"persona": "pm", "scenario": "first-run"})

# Markdown renders persona/scenario sections when present.
persona_md = reporting._markdown(persona_rollup, title="Persona QA rollup")
check("markdown has persona section", "## By persona" in persona_md, True)
check("markdown has scenario section", "## By scenario" in persona_md, True)
check("markdown title honored", persona_md.startswith("# Persona QA rollup"), True)


if failures:
    print("FAIL: shared rollup")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: shared rollup (golden byte-compat + persona/scenario data-completeness)")
