#!/usr/bin/env python3
"""No-LLM, no-docker test for the swarm arena plugin.

Covers: plugin registration alongside bugfix/persona-qa, image() selection
(the arena-browser-image tag, plus target/variant meta override), drive_command
argv/env composition (axis-driven user count, interactive concurrency, persona
mix, and fixture all reach the command line), score() reading a REAL on-disk
swarm results JSON (tools/swarm/results.ts's `SwarmResults` shape) via the
stdout `[swarm] wrote <path>` pointer — solved/partial/failed/negative-control-
failed/infra cases — and a concurrent FakeBackend sweep across a `users` axis
with axis coords carried through to CellResult/rollup. Never launches docker
or Playwright.
"""

from __future__ import annotations

import json
import sys
import tempfile
import threading
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.executor import CellExecutor, ContainerRun, FakeBackend  # noqa: E402
from arena.model import JobSpec  # noqa: E402
from arena.placement import run_sweep  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402
from arena.plugins.swarm import KITSOKI_MNT, REPO_ROOT  # noqa: E402
from arena.rollup import build_rollup  # noqa: E402

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


# ---- 1. Registration --------------------------------------------------------

check("swarm is registered", "swarm" in plugins.known(), True)
check("bugfix still registered alongside swarm", "bugfix" in plugins.known(), True)
check("persona-qa still registered alongside swarm", "persona-qa" in plugins.known(), True)
plugin = plugins.get("swarm")

# ---- 2. image() selection ---------------------------------------------------

SPEC = {
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier1", "backend": "replay"}],
    "axes": {"users": ["24"], "fixture": ["stories/prd/flows/happy_path.yaml"]},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
}
spec = JobSpec.from_dict(SPEC)
cell = spec.cells()[0]

check("default image is the arena browser image", plugin.image(cell), "kitsoki-arena-repo-runtime-browser:latest")

SPEC_OVERRIDE = json.loads(json.dumps(SPEC))
SPEC_OVERRIDE["targets"][0]["image"] = "kitsoki-arena-repo-runtime-browser:pinned"
override_cell = JobSpec.from_dict(SPEC_OVERRIDE).cells()[0]
check(
    "target.meta.image overrides the default",
    plugin.image(override_cell),
    "kitsoki-arena-repo-runtime-browser:pinned",
)

# ---- 3. drive_command argv/env composition ---------------------------------

SPEC_FULL_AXES = {
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier1", "backend": "replay"}],
    "axes": {
        "users": ["32"],
        "interactive_concurrency": ["4"],
        "persona_mix": ["core-maintainer:heavy"],
        "fixture": ["stories/prd/flows/happy_path.yaml"],
    },
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
}
axes_cell = JobSpec.from_dict(SPEC_FULL_AXES).cells()[0]

argv = plugin.drive_command(axes_cell, live=False)
check("drive_command shells out via bash -lc", argv[0:2], ["bash", "-lc"])
script = argv[2]
check("cd's into the runstatus dir inside the container", f"cd {KITSOKI_MNT}/tools/runstatus" in script, True)
check("runs the swarm-replay-users spec", "tests/playwright/swarm-replay-users.spec.ts" in script, True)
check("runs it via npx playwright test", "npx playwright test" in script, True)
check("SWARM_USERS carries the users axis value", "SWARM_USERS=32" in script, True)
check(
    "SWARM_INTERACTIVE_CONCURRENCY carries the axis value",
    "SWARM_INTERACTIVE_CONCURRENCY=4" in script,
    True,
)
check("SWARM_PERSONA_MIX carries the persona_mix axis value", "SWARM_PERSONA_MIX=" in script and "core-maintainer:heavy" in script, True)
check("SWARM_FIXTURE carries the fixture axis value", "SWARM_FIXTURE=" in script and "happy_path.yaml" in script, True)

live_argv = plugin.drive_command(axes_cell, live=True)
check("live and non-live drive the identical command (swarm never calls an LLM)", live_argv, argv)

# SPEC's cell (users axis = "24") still threads that explicit value through.
default_cell = spec.cells()[0]
default_argv = plugin.drive_command(default_cell, live=False)
default_script = default_argv[2]
check("threads the explicit users axis value through", "SWARM_USERS=24" in default_script, True)

no_axes_spec = JobSpec.from_dict({
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier1", "backend": "replay"}],
    "axes": {},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
})
no_axes_cell = no_axes_spec.cells()[0]
no_axes_script = plugin.drive_command(no_axes_cell, live=False)[2]
check("falls back to the DEFAULT_USERS default with no axis at all", "SWARM_USERS=24" in no_axes_script, True)
check("does not emit SWARM_PERSONA_MIX with no persona_mix axis", "SWARM_PERSONA_MIX=" in no_axes_script, False)
check("still threads the default fixture", "happy_path.yaml" in no_axes_script, True)

# ---- 3b. tier-2 cell selection (task 3.2, docs/proposals/scenario-foundry.md) --
# A variant with meta.tier == "tier2" drives swarm-cassette-users.spec.ts
# instead, and SWARM_FIXTURE/SWARM_PERSONA_MIX are genuinely consumed there
# (tools/swarm/tiers/tier2.ts's buildTier2RecordingAuto) rather than merely
# recorded — the DEFAULT_FIXTURE flow-fixture default must NOT leak onto a
# tier-2 cell, since tier2.ts's own fixture semantics are a scenario-IR
# path/dir, not a flow-fixture path.

TIER2_SPEC = {
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier2", "backend": "replay", "tier": "tier2"}],
    "axes": {
        "persona_mix": ["core-maintainer"],
        "fixture": ["tools/session-mining/calibration"],
    },
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
}
tier2_cell = JobSpec.from_dict(TIER2_SPEC).cells()[0]
tier2_argv = plugin.drive_command(tier2_cell, live=False)
tier2_script = tier2_argv[2]
check(
    "tier2 variant drives swarm-cassette-users.spec.ts, not tier 1's spec",
    "tests/playwright/swarm-cassette-users.spec.ts" in tier2_script,
    True,
)
check(
    "tier2 cell still runs via npx playwright test",
    "npx playwright test" in tier2_script,
    True,
)
check(
    "tier2 SWARM_FIXTURE carries the scenario-IR axis value",
    "SWARM_FIXTURE=" in tier2_script and "tools/session-mining/calibration" in tier2_script,
    True,
)
check(
    "tier2 SWARM_PERSONA_MIX carries the axis value",
    "SWARM_PERSONA_MIX=" in tier2_script and "core-maintainer" in tier2_script,
    True,
)

TIER2_NO_FIXTURE_AXIS = {
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier2", "backend": "replay", "tier": "tier2"}],
    "axes": {},
    "placement": {"hosts": ["local"], "concurrency": 1, "retry": 0},
}
tier2_no_fixture_cell = JobSpec.from_dict(TIER2_NO_FIXTURE_AXIS).cells()[0]
tier2_no_fixture_script = plugin.drive_command(tier2_no_fixture_cell, live=False)[2]
check(
    "tier2 with no fixture axis does NOT leak tier-1's DEFAULT_FIXTURE (happy_path.yaml)",
    "happy_path.yaml" in tier2_no_fixture_script,
    False,
)
check(
    "tier2 with no fixture axis emits no SWARM_FIXTURE at all",
    "SWARM_FIXTURE=" in tier2_no_fixture_script,
    False,
)

# A default (unset tier) variant is untouched by the tier2 branch.
check("default variant (no tier meta) is not tier2", plugin._is_tier2(cell), False)
check("default variant drives the tier-1 spec", plugin._spec_for(cell), "tests/playwright/swarm-replay-users.spec.ts")
check("tier2-meta variant IS tier2", plugin._is_tier2(tier2_cell), True)
check("tier2-meta variant resolves to the tier-2 spec", plugin._spec_for(tier2_cell), "tests/playwright/swarm-cassette-users.spec.ts")

# ---- 4. score() reads a REAL swarm results JSON via the stdout pointer -----

with tempfile.TemporaryDirectory() as tmp:
    results_dir = Path(tmp) / "swarm"
    results_dir.mkdir()
    results_path = results_dir / "results-1234.json"

    def make_results(**overrides):
        base_result = {
            "run_id": "1234",
            "started_at": "2026-07-04T00:00:00Z",
            "ended_at": "2026-07-04T00:05:00Z",
            "server": {"addr": "127.0.0.1:7799", "flow": "happy_path.yaml"},
            "user_count": 24,
            "users": [
                {
                    "index": i,
                    "persona_id": "core-maintainer",
                    "session_id": f"sess-{i}",
                    "marker": f"marker-{i}",
                    "completed": True,
                    "states_visited": ["idle", "drafting"],
                    "console_errors": 0,
                    "console_error_samples": [],
                    "audit_error_count": 0,
                    "audit_error_samples": [],
                    "audit_a11y_advisory_count": 0,
                    "audit_a11y_advisory_samples": [],
                    "isolation_ok": True,
                    "isolation_leaked": [],
                    "duration_ms": 1000,
                }
                for i in range(24)
            ],
            "all_completed": True,
            "all_isolated": True,
            "all_console_clean": True,
            "all_audit_clean": True,
            "rss": {},
            "negative_control": {
                "description": "seeded cross-talk fault",
                "shared_session_id": "shared-1",
                "injected_marker": "neg-marker",
                "detected": True,
                "leaked": ["neg-marker"],
            },
        }
        base_result.update(overrides)
        return base_result

    results_path.write_text(json.dumps(make_results()), encoding="utf-8")
    stdout_ok = f"[swarm] wrote {results_path} (24 users)\n"

    solved = plugin.score(cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("all-clean bundle verdict is solved", solved.verdict, "solved")
    check("all-clean bundle health is model:result", solved.health, "model:result")
    check("metrics carry user_count", solved.metrics["user_count"], 24)
    check("metrics carry completed_count", solved.metrics["completed_count"], 24)
    check("trace_ref is the results path", solved.trace_ref, str(results_path))
    check("evidence_refs include the results path", solved.evidence_refs, [str(results_path)])

    # Partial: one user failed isolation, rest clean.
    partial_users = make_results()["users"]
    partial_users[0]["isolation_ok"] = False
    partial_users[0]["isolation_leaked"] = ["marker-1"]
    results_path.write_text(json.dumps(make_results(
        users=partial_users, all_isolated=False,
    )), encoding="utf-8")
    partial = plugin.score(cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("one leaked user makes the cell partial, not solved", partial.verdict, "partial")
    check("partial metrics reflect the isolation miss", partial.metrics["isolated_count"], 23)

    # Failed: nobody completed.
    failed_users = [dict(u, completed=False) for u in make_results()["users"]]
    results_path.write_text(json.dumps(make_results(
        users=failed_users, all_completed=False,
    )), encoding="utf-8")
    zero_completed = plugin.score(cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("zero completions is failed", zero_completed.verdict, "failed")

    # Failed: the negative control itself did not detect the seeded fault —
    # the isolation gate is broken, a real failure even if every user's own
    # journey looked clean.
    results_path.write_text(json.dumps(make_results(
        negative_control={
            "description": "seeded cross-talk fault",
            "shared_session_id": "shared-1",
            "injected_marker": "neg-marker",
            "detected": False,
            "leaked": [],
        },
    )), encoding="utf-8")
    neg_control_failed = plugin.score(cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("undetected negative control fails the cell even if users look clean", neg_control_failed.verdict, "failed")
    check(
        "notes mention the negative control failure",
        "negative control" in neg_control_failed.notes.lower(),
        True,
    )

    # An UNEXERCISED negative control (e.g. results file written before that
    # test ran) must not itself fail an otherwise-clean cell.
    results_path.write_text(json.dumps(make_results(
        negative_control={
            "description": "populated by the negative-control test below",
            "shared_session_id": "",
            "injected_marker": "",
            "detected": False,
            "leaked": [],
        },
    )), encoding="utf-8")
    neg_control_unrun = plugin.score(cell, exit_code=0, stdout=stdout_ok, stderr="")
    check("an unexercised negative control does not fail an otherwise-clean cell", neg_control_unrun.verdict, "solved")

# score() with no results-path pointer at all is an infra failure, not a guessed verdict.
missing = plugin.score(cell, exit_code=0, stdout="not the expected line", stderr="")
check("missing results pointer is infra", missing.health, "infra:missing-results-path")
check("missing results pointer is blocked, not a model verdict", missing.verdict, "blocked")

crash = plugin.score(cell, exit_code=1, stdout="", stderr="Traceback (most recent call last):\n  boom")
check("crash before any results file is infra:harness", crash.health, "infra:harness")

# ---- 5. host<->container path mapping --------------------------------------

with tempfile.TemporaryDirectory() as tmp3:
    # Simulate the results file existing at REPO_ROOT/.artifacts/swarm/... (the
    # `local`-placement mount target) and being reported at its KITSOKI_MNT
    # container path in stdout.
    rel = Path(".artifacts") / "swarm" / "results-9999-mapping-test.json"
    host_path = REPO_ROOT / rel
    host_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        host_path.write_text(json.dumps({
            "run_id": "9999",
            "started_at": "", "ended_at": "",
            "server": {"addr": "", "flow": ""},
            "user_count": 1,
            "users": [{
                "index": 0, "persona_id": "p", "session_id": "s", "marker": "m",
                "completed": True, "states_visited": [], "console_errors": 0,
                "console_error_samples": [], "audit_error_count": 0,
                "audit_error_samples": [], "audit_a11y_advisory_count": 0,
                "audit_a11y_advisory_samples": [], "isolation_ok": True,
                "isolation_leaked": [], "duration_ms": 1,
            }],
            "all_completed": True, "all_isolated": True,
            "all_console_clean": True, "all_audit_clean": True,
            "rss": {}, "negative_control": {
                "description": "", "shared_session_id": "", "injected_marker": "",
                "detected": False, "leaked": [],
            },
        }), encoding="utf-8")
        container_stdout = f"[swarm] wrote {KITSOKI_MNT}/{rel} (1 users)\n"
        mapped = plugin.score(cell, exit_code=0, stdout=container_stdout, stderr="")
        check("container path maps back to the real host path", mapped.trace_ref, str(host_path))
        check("mapped read scores solved", mapped.verdict, "solved")
    finally:
        host_path.unlink(missing_ok=True)

# ---- 6. A concurrent FakeBackend sweep over a `users` axis -----------------

sweep_spec = JobSpec.from_dict({
    "job_type": "swarm",
    "targets": [{"id": "kitsoki", "label": "kitsoki web UI"}],
    "variants": [{"id": "tier1", "backend": "replay"}],
    "axes": {"users": ["24", "32"]},
    "placement": {"hosts": ["local"], "concurrency": 2, "retry": 0},
})
sweep_cells = sweep_spec.cells()
check("sweep enumerates one cell per users value", len(sweep_cells), 2)

lock = threading.Lock()
release_first = threading.Event()
in_flight = 0
max_in_flight = 0
paused_once = False


def responder(sweep_cell, host, sweep_argv):
    global in_flight, max_in_flight, paused_once
    should_pause = False
    with lock:
        in_flight += 1
        max_in_flight = max(max_in_flight, in_flight)
        if not paused_once:
            paused_once = True
            should_pause = True
        elif not release_first.is_set():
            release_first.set()
    try:
        if should_pause:
            release_first.wait(timeout=5)
        n = int(sweep_cell.axis["users"])
        bundle_dir = Path(tmp2) / sweep_cell.id.replace("/", "_")
        bundle_dir.mkdir(parents=True, exist_ok=True)
        results_file = bundle_dir / f"results-{sweep_cell.id}.json"
        results_file.write_text(json.dumps({
            "run_id": sweep_cell.id, "started_at": "", "ended_at": "",
            "server": {"addr": "", "flow": ""},
            "user_count": n,
            "users": [{
                "index": i, "persona_id": "p", "session_id": f"s{i}", "marker": f"m{i}",
                "completed": True, "states_visited": [], "console_errors": 0,
                "console_error_samples": [], "audit_error_count": 0,
                "audit_error_samples": [], "audit_a11y_advisory_count": 0,
                "audit_a11y_advisory_samples": [], "isolation_ok": True,
                "isolation_leaked": [], "duration_ms": 1,
            } for i in range(n)],
            "all_completed": True, "all_isolated": True,
            "all_console_clean": True, "all_audit_clean": True,
            "rss": {}, "negative_control": {
                "description": "", "shared_session_id": "", "injected_marker": "",
                "detected": False, "leaked": [],
            },
        }), encoding="utf-8")
        stdout_payload = f"[swarm] wrote {results_file} ({n} users)\n"
        return ContainerRun(exit_code=0, stdout=stdout_payload, stderr="", host=host)
    finally:
        with lock:
            in_flight -= 1


with tempfile.TemporaryDirectory() as tmp2:
    backend = FakeBackend(responder)
    executor = CellExecutor(backend, mounts_for=lambda c, h: {"/repo": "/workspace/kitsoki"})
    sweep_results = run_sweep(sweep_spec, executor, live=False)

check("sweep completed every cell", len(sweep_results), 2)
check("sweep proved real concurrency (>=2 in flight at once)", max_in_flight >= 2, True)
check("sweep all solved", {r.verdict for r in sweep_results}, {"solved"})

rollup = build_rollup(sweep_results)
check("rollup counts every sweep cell", sum(rollup["summary"]["verdicts"].values()), 2)

for r in sweep_results:
    check(f"{r.cell_id} axis carries users", "users" in r.axis, True)
    check(f"{r.cell_id} metrics user_count matches its users axis", r.metrics["user_count"], int(r.axis["users"]))

if failures:
    print("FAIL: swarm arena plugin")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: swarm arena plugin (registration, image selection, argv/env composition, results-json scoring, concurrent sweep)")
