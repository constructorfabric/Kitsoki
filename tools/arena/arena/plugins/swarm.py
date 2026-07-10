"""swarm job-type plugin — one arena cell = one whole tier-1 swarm run.

`tools/swarm/` + `tools/runstatus/tests/playwright/swarm-replay-users.spec.ts`
(swarm-tier1) already put N>=24 concurrent scripted Playwright browser
contexts on ONE `kitsoki web --flow ...` server, on a single machine. This
plugin does not reinvent that harness — it places the WHOLE run (server + N
drivers) as one cell in the browser-capable image `arena-browser-image`
landed, so the swarm scales out through arena's existing local|VM placement
exactly like a bugfix/persona-qa cell does. The swarm itself is the unit of
placement; the N scripted users inside it are internal to the cell, not
separate cells (unlike persona-qa, which fans personas out to one cell each).

Cost/live: the tier-1 harness never calls an LLM (every user is a scripted
replay driven by `tools/product-journey/personas.json` lenses over a flow
fixture), so unlike bugfix/persona-qa there is no separate paid `--live`
path here — `drive_command` runs the identical no-LLM Playwright spec either
way. `live` is still accepted (for interface parity with the other plugins
and so a future live-driven variant of the harness has somewhere to hook in)
but is otherwise a no-op today.

Axis-driven knobs: `axis["users"]` maps straight onto the harness's own
`SWARM_USERS` env var (tools/swarm/README.md, swarm-replay-users.spec.ts:46)
and `axis["interactive_concurrency"]` onto `SWARM_INTERACTIVE_CONCURRENCY`
(swarm-replay-users.spec.ts:152) — both already read by the standing spec.
`axis["persona_mix"]` and `axis["fixture"]` are threaded onto the command
line as `SWARM_PERSONA_MIX`/`SWARM_FIXTURE` env vars. On a default (tier-1)
cell these remain forward-compat only — `swarm-replay-users.spec.ts` always
rotates every persona in `personas.json` (`personaForIndex`) and always
drives the hardcoded `stories/prd/flows/happy_path.yaml` fixture, so setting
them there gets them recorded on the cell/result but doesn't change the run.
On a cell whose variant sets `meta.tier: tier2` (`_is_tier2`/`_spec_for`
below), the plugin instead drives `swarm-cassette-users.spec.ts`, where BOTH
env vars are genuinely consumed by `tools/swarm/tiers/tier2.ts`'s
`buildTier2RecordingAuto` (task 3.2, docs/proposals/scenario-foundry.md):
`SWARM_FIXTURE` names a mined scenario-IR document or directory (task 3.1's
`tools/session-mining/flow_fixture_compiler.py` inputs) and `SWARM_PERSONA_MIX`
selects/orders which of those scenarios' `persona` field backs each cassette
user, falling back to the shipped off-ramp trust question when unset — see
tier2.ts's own doc comment for the full contract.

Scoring reads the harness's own per-run results JSON
(`.artifacts/swarm/results-<run_id>.json`, tools/swarm/results.ts's
`SwarmResults` shape) instead of regexing stdout for a verdict. The only
thing pulled from stdout is the `[swarm] wrote <path> (...)` line the spec
logs right after `writeResults` returns (swarm-replay-users.spec.ts's final
`console.log`) — a path, not a verdict, mirroring persona_qa.py's
`_extract_run_dir` convention. The `swarm-results` artifact adapter normalizes
that file onto the shared completion-state contract
(schemas/completion-state.schema.json):
aggregate rule is all-journeys-complete AND isolation clean AND audit clean
=> solved; some-but-not-all clean/complete => partial; nothing completed (or
the negative control itself failed to fire) => failed; a harness crash
before any results file was ever written is `infra:*`, never a model verdict.
"""

from __future__ import annotations

import re
import shlex
import sys
from pathlib import Path

# tools/arena/arena/plugins/swarm.py -> parents[4] == REPO_ROOT (mirrors
# bugfix.py's / persona_qa.py's REPO_ROOT derivation).
REPO_ROOT = Path(__file__).resolve().parents[4]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from tools.completion_state import CompletionStateError  # noqa: E402

from ..artifact_adapters import adapt_artifact  # noqa: E402
from ..completion_state import apply_completion_state  # noqa: E402
from ..model import Cell, CellResult  # noqa: E402
from . import base  # noqa: E402

# Path *inside the container* where the kitsoki checkout is mounted (mirrors
# bugfix.py's / persona_qa.py's KITSOKI_MNT convention).
KITSOKI_MNT = "/workspace/kitsoki"
RUNSTATUS_DIR = f"{KITSOKI_MNT}/tools/runstatus"
SPEC_TIER1 = "tests/playwright/swarm-replay-users.spec.ts"
# tier-2 gate: a variant with meta.tier == "tier2" (see _spec_for below) drives
# this spec instead, where SWARM_FIXTURE/SWARM_PERSONA_MIX are now actually
# consumed (tools/swarm/tiers/tier2.ts's buildTier2RecordingAuto) rather than
# only recorded on the cell's command line — see _coords/drive_command below.
SPEC_TIER2 = "tests/playwright/swarm-cassette-users.spec.ts"
SPEC = SPEC_TIER1  # kept as the historical name for tier-1 (default) cells.

DEFAULT_USERS = "24"
DEFAULT_FIXTURE = "stories/prd/flows/happy_path.yaml"

# `[swarm] wrote /abs/path/results-172....json (24 users)` — the ONE line of
# stdout this plugin reads; everything else in score() comes off disk.
_RESULTS_LINE_RE = re.compile(r"\[swarm\]\s+wrote\s+(\S+\.json)")

# Infra signals — a harness crash before any results file was ever written is
# not a model verdict. Kept ONLY as the fallback for when no results-path
# pointer is found in stdout at all.
_INFRA_RE = re.compile(
    r"traceback|no such file|permission denied|connection refused|"
    r"command not found|econnrefused|provider 5\d\d",
    re.I,
)


class SwarmPlugin:
    name = "swarm"

    def image(self, cell: Cell) -> str:
        # The swarm always needs a real browser (N Playwright contexts against
        # a live kitsoki web server) — arena-browser-image's tag, unless a spec
        # opts a target/variant into an override.
        return (
            cell.target.meta.get("image")
            or cell.variant.meta.get("image")
            or "kitsoki-arena-repo-runtime-browser:latest"
        )

    def _is_tier2(self, cell: Cell) -> bool:
        # A cell opts into the tier-2 (cassette-agent) spec via
        # variant.meta.tier == "tier2" (mirrors bugfix.py's variant-driven
        # branching convention). Default (unset/anything else) stays tier 1 —
        # existing specs/tests are unaffected.
        return str(cell.variant.meta.get("tier", "")).strip().lower() == "tier2"

    def _spec_for(self, cell: Cell) -> str:
        return SPEC_TIER2 if self._is_tier2(cell) else SPEC_TIER1

    def _coords(self, cell: Cell) -> dict[str, str]:
        is_tier2 = self._is_tier2(cell)
        return {
            "users": cell.axis.get("users") or str(cell.variant.meta.get("users", "")) or DEFAULT_USERS,
            "interactive_concurrency": cell.axis.get("interactive_concurrency", ""),
            "persona_mix": cell.axis.get("persona_mix", ""),
            # Tier 1's fixture is a flow-fixture path and always has a default
            # (the standing PRD happy-path). Tier 2's fixture is a scenario-IR
            # path/dir (tools/session-mining/flow_fixture_compiler.py's
            # inputs) consumed by tier2.ts's buildTier2RecordingAuto, which has
            # its own built-in fallback (the shipped off-ramp question) when
            # unset — so no default is forced here for tier 2.
            "fixture": cell.axis.get("fixture") or ("" if is_tier2 else DEFAULT_FIXTURE),
        }

    def drive_command(self, cell: Cell, *, live: bool) -> list[str]:
        # No separate paid path: neither tier 1 nor tier 2 ever calls an LLM
        # (every user is a scripted replay or a cassette-answered free-text
        # turn — see module docstring), so `live` doesn't change what runs
        # here. Accepted only for interface parity with the other job-type
        # plugins.
        del live
        coords = self._coords(cell)
        spec = self._spec_for(cell)

        env: list[str] = [f"SWARM_USERS={shlex.quote(coords['users'])}"]
        if coords["interactive_concurrency"]:
            env.append(f"SWARM_INTERACTIVE_CONCURRENCY={shlex.quote(coords['interactive_concurrency'])}")
        if coords["persona_mix"]:
            # Consumed by tier2.ts's buildTier2RecordingAuto (task 3.2) when
            # the cell is tier 2 (SWARM_PERSONA_MIX selects/orders which mined
            # scenarios' personas back the cassette users); a no-op env var on
            # tier-1 cells (that spec doesn't read it).
            env.append(f"SWARM_PERSONA_MIX={shlex.quote(coords['persona_mix'])}")
        if coords["fixture"]:
            # Tier 1: still hardcoded FLOW in swarm-replay-users.spec.ts (this
            # remains forward-compat there). Tier 2: consumed by tier2.ts's
            # buildTier2RecordingAuto as the mined scenario-IR path/dir.
            env.append(f"SWARM_FIXTURE={shlex.quote(coords['fixture'])}")

        script = (
            f"set -euo pipefail; cd {shlex.quote(RUNSTATUS_DIR)} && "
            f"{' '.join(env)} npx playwright test {shlex.quote(spec)}"
        )
        return ["bash", "-lc", script]

    def score(self, cell: Cell, *, exit_code: int, stdout: str, stderr: str) -> CellResult:
        result = CellResult(
            cell_id=cell.id,
            job_type=self.name,
            target_id=cell.target.id,
            variant_id=cell.variant.id,
            axis=dict(cell.axis),
        )
        blob = f"{stdout}\n{stderr}"
        results_path = _extract_results_path(stdout)
        if results_path is None:
            if _INFRA_RE.search(blob):
                result.verdict = "blocked"
                result.health = "infra:harness"
                result.notes = _first_line(blob)
            else:
                result.verdict = "blocked"
                result.health = "infra:missing-results-path"
                result.notes = (
                    f"no swarm results path found in stdout (exit_code={exit_code}); "
                    "the harness did not honor the '[swarm] wrote <path>' contract"
                )
            return result

        host_path = _host_results_path(results_path)
        try:
            payload = adapt_artifact("swarm-results", host_path)
        except CompletionStateError as exc:
            result.verdict = "blocked"
            result.health = "infra:results-malformed"
            result.notes = f"could not load swarm results at {host_path}: {exc}"
            return result

        return apply_completion_state(result, payload)


def _extract_results_path(stdout: str) -> str | None:
    """Pull the results-file path out of the harness's own
    `[swarm] wrote <path> (...)` stdout line — a path, not a verdict (see
    module docstring).
    """
    match = None
    for line in stdout.splitlines():
        m = _RESULTS_LINE_RE.search(line)
        if m:
            match = m.group(1)
    return match


def _host_results_path(container_path: str) -> Path:
    """Map a results path as printed INSIDE the container back to the host
    path this process can read (mirrors bugfix.py's completion_state_path
    convention: for `local` placement the executor mounts REPO_ROOT at
    KITSOKI_MNT, so a file written under KITSOKI_MNT/... is readable here at
    the equivalent REPO_ROOT/... path). Paths that don't carry the
    KITSOKI_MNT prefix (e.g. a test calling score() directly against a temp
    dir) are returned unchanged.
    """
    if container_path.startswith(KITSOKI_MNT + "/"):
        return REPO_ROOT / container_path[len(KITSOKI_MNT) + 1:]
    return Path(container_path)


def _first_line(blob: str) -> str:
    for line in blob.splitlines():
        line = line.strip()
        if line:
            return line[:200]
    return ""


base.register(SwarmPlugin())
