"""persona-qa job-type plugin — product-journey personas/scenarios as arena cells.

A persona-qa cell grades one (target repo, persona, scenario) triple. The
non-live path drives `tools/persona_qa/kit.py replay-smoke`, the public
persona-QA CLI facade over the deterministic cassette-backed smoke. It builds a
real run bundle (cassette-backed evidence, review.json, driver journal) and
reports the run bundle's directory in its `--json-output` payload's `run_dir`
field. The live path (gated behind `--live`, cost-bearing) instead emits a fresh
run bundle through the same CLI and dispatches the `product-journey-qa-driver`
agent headlessly against it before reviewing it.

Scoring never regexes stdout for a verdict. It only reads the `run_dir` pointer
out of the container's structured JSON output, then hands that directory to
`tools.persona_qa.load_product_journey_run` (the same completion-state bridge
`unify-contract` landed for bench.py / bugfix.py) — that module reads the run
bundle's real artifacts (`review.json`, `scenario-outcomes.json`,
`driver-handoff.json`) off disk and derives the job-agnostic verdict/health.
Stdout/stderr text is only used as an INFRA fallback when no run_dir pointer
is found at all (crash before the runner ever printed JSON).
"""

from __future__ import annotations

import json
import re
import shlex
import sys
from pathlib import Path

# tools/arena/arena/plugins/persona_qa.py -> parents[4] == REPO_ROOT (mirrors
# bugfix.py's REPO_ROOT derivation).
ROOT = Path(__file__).resolve().parents[4]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from tools.completion_state import CompletionStateError  # noqa: E402

from ..artifact_adapters import adapt_artifact  # noqa: E402
from ..completion_state import apply_completion_state  # noqa: E402
from ..model import Cell, CellResult  # noqa: E402
from . import base  # noqa: E402

# Path *inside the container* where the kitsoki checkout is mounted (mirrors
# bugfix.py's KITSOKI_MNT convention).
KITSOKI_MNT = "/workspace/kitsoki"
PERSONA_QA = f"{KITSOKI_MNT}/tools/persona_qa/kit.py"

# Infra signals — a harness crash before any JSON was printed, not a model
# verdict. Kept ONLY as the fallback for a missing run_dir pointer.
_INFRA_RE = re.compile(
    r"traceback|no such file|permission denied|connection refused|"
    r"command not found|provider 5\d\d",
    re.I,
)


class PersonaQAPlugin:
    name = "persona-qa"

    def image(self, cell: Cell) -> str:
        # persona-qa cells need a browser for eventual visual evidence capture
        # (arena-browser-image); falls back to a per-target override via meta.
        return cell.target.meta.get("image") or "kitsoki-arena-repo-runtime-browser:latest"

    def _coords(self, cell: Cell) -> tuple[str, str, str, str]:
        target = cell.target.id
        persona = cell.axis.get("persona") or str(cell.variant.meta.get("persona", "")) or cell.variant.id
        scenario = cell.axis.get("scenario") or str(cell.variant.meta.get("scenario", "")) or "project-onboarding"
        seed = cell.axis.get("seed") or f"arena-{target}-{cell.variant.id}-{scenario}"
        return target, persona, scenario, seed

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        target, persona, scenario, seed = self._coords(cell)
        if not live:
            # No-LLM path: the public Persona QA CLI's deterministic replay smoke.
            # Prints a JSON payload (incl. `run_dir`) that `score()` reads back.
            return [
                "python3", PERSONA_QA, "replay-smoke",
                "--project", target,
                "--persona", persona,
                "--scenario", scenario,
                "--seed", seed,
                "--json-output",
            ]
        # Live (gated, cost-bearing): emit a fresh run bundle, dispatch the
        # product-journey-qa-driver agent headlessly against it (per
        # .agents/agents/product-journey-qa-driver.md — the run bundle's own
        # agent-brief.md/execution-plan.md brief the agent), then review it.
        # AskUserQuestion is hard-denied headless (see AGENTS.md); the agent
        # proceeds solo. This branch is never exercised by tests/CI — it is
        # only reached behind `arena run --live`.
        return [
            "bash", "-lc",
            (
                f"set -euo pipefail; "
                f"run_json=$(python3 {PERSONA_QA} emit-run --project {shlex.quote(target)} "
                f"--persona {shlex.quote(persona)} --scenario {shlex.quote(scenario)} "
                f"--seed {shlex.quote(seed)} --json-output); "
                f"echo \"$run_json\"; "
                f"run_dir=$(python3 -c 'import json,sys; "
                f"print(json.loads(sys.argv[1])[\"run_dir\"])' \"$run_json\"); "
                f"claude -p --agent product-journey-qa-driver --dangerously-skip-permissions "
                f"\"Drive the product-journey run bundle at $run_dir per its "
                f"agent-brief.md and execution-plan.md, capturing evidence through "
                f"the studio MCP, then leave it ready for --review-run.\"; "
                f"python3 {PERSONA_QA} review --run-dir \"$run_dir\" --json-output"
            ),
        ]

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(
            cell_id=cell.id,
            job_type=self.name,
            target_id=cell.target.id,
            variant_id=cell.variant.id,
            axis=dict(cell.axis),
        )
        blob = f"{stdout}\n{stderr}"
        run_dir = _extract_run_dir(stdout)
        if run_dir is None:
            if _INFRA_RE.search(blob):
                result.verdict = "blocked"
                result.health = "infra:harness"
                result.notes = _first_line(blob)
            else:
                result.verdict = "blocked"
                result.health = "infra:missing-run-dir"
                result.notes = (
                    f"no run_dir found in product-journey output (exit_code={exit_code}); "
                    "the driver did not honor the persona-qa contract"
                )
            return result

        try:
            payload = adapt_artifact("product-journey-review", run_dir)
        except (OSError, json.JSONDecodeError, CompletionStateError) as exc:
            result.verdict = "blocked"
            result.health = "infra:completion-state-malformed"
            result.notes = f"could not load run bundle at {run_dir}: {exc}"
            return result

        result = apply_completion_state(result, payload)
        deck_path = payload.get("deck_path")
        if deck_path:
            result.evidence_refs.append(str(deck_path))
        return result


def _extract_run_dir(stdout: str) -> str | None:
    """Pull the `run_dir` pointer out of the last JSON object printed to stdout.

    This is the only thing read from stdout — a path, not a verdict. Everything
    that determines verdict/health is then read off disk via the completion.py
    bridge (see module docstring).
    """
    for line in reversed(stdout.splitlines()):
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            data = json.loads(line)
        except json.JSONDecodeError:
            continue
        run_dir = data.get("run_dir") or (data.get("run") or {}).get("run_dir")
        if run_dir:
            return str(run_dir)
    return None


def _first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:200]
    return ""


base.register(PersonaQAPlugin())
