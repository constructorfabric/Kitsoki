#!/usr/bin/env python3
"""flow_fixture_compiler.py — task 3.1: project a `kind: conversation` scenario
IR document (schema/scenario_ir.schema.json, compiled by scenario_compiler.py)
onto a runnable `kitsoki test flows` flow fixture + host cassette pair.

## Why not `internal/testrunner/fromtrace.go`'s shape directly

`trace to-flow` (cmd/kitsoki/trace.go, internal/testrunner/fromtrace.go)
converts a recorded kitsoki ENGINE trace, where every turn already carries a
resolved `intent: {name, slots}` (a real router decision). A mined scenario
IR turn only has free-text `turns[].text` — there is no resolved intent/slots
upstream, and resolving each mined utterance against some arbitrary story's
real intents is a much bigger, story-specific problem this compiler does not
attempt (see docs/proposals/scenario-foundry.md's flow-fixture compiler note).

Instead this compiler targets ONE fixed, purpose-built harness app —
`stories/scenario-foundry-harness/` — that exposes a single explicit `ask`
intent (`slots.text`) which hands the verbatim utterance to a stubbed
`host.agent.converse` and stays put. This mirrors stories/off-ramp-demo's
free-text-answered-in-place mechanic, but uses an explicit intent instead of
a clarify/off-ramp no-match: internal/testrunner's orchestrator-backed runner
(required for `host_cassette:`) does not support recording-resolved `input:`
turns today (see internal/testrunner/flows.go's runOneFlowOrchestrator —
"recording routing not yet supported on orchestrator path"). Explicit
`intent:` turns sidestep that gap entirely and are fully supported combined
with `host_cassette:`.

## Shape emitted

- One flow turn per mined `turns[]` entry: `intent: {name: ask, slots: {text:
  <verbatim>}}`, `display_input: <verbatim>` (mirrors fromtrace.go's own
  display_input convention — the trace/replay UI shows the operator's real
  words even though a fixed intent drives routing).
- One host_cassette episode per turn, matched on `handler: host.agent.converse`
  (sequential, non-replay:any — matches the i-th call to the i-th episode,
  exactly like fromtrace.go's converter), whose canned `answer` is derived
  deterministically from the scenario's own fields (corrective_ops on a
  corrected turn; expected_effects on the scenario's final turn; a generic
  acknowledgement otherwise) — never invented content, never a live call.

## Determinism

Pure function of the scenario IR document's bytes: same input, same output
bytes (byte-for-byte), so the compiled fixture set is itself a regression
artifact a reviewer can regenerate and diff (mirrors scenario_compiler.py's
own determinism contract).

## The real-workbench target (S6, room-workbench Task "workbench-target")

The fixed-`ask`-intent harness above proves the schema/join/rollup machinery
end to end, but it is not a `workbench:` room
(`internal/orchestrator/workbench_gate_signal.go` returns nil for any
dispatching state that isn't one), so replaying its fixtures produces zero
`usable_kitsoki_gate` turn signals — a wiring proof, not a parity
measurement (see `tools/usable-kitsoki-gate/flow_gate_runner.py`'s module
docstring for that honest gap in full).

`compile_scenario_onto_workbench` closes that gap WITHOUT solving the
general "resolve arbitrary free text onto an arbitrary story's real intents"
problem this module's docstring above says it deliberately avoids: it
projects onto the ONE synthesized capture intent every `workbench:` room
gets for free (`docs/proposals/room-workbench.md`'s `<room>_capture`
macro, `internal/app/workbench.go`), which — unlike a story's other,
bespoke intents — has the exact same fixed shape (`slots: {request:
<free text>}`) everywhere a `workbench:` room exists. `WORKBENCH_TARGETS`
below lists the three story instances this project actually ships a real
`workbench:` room in: `stories/dev-story/rooms/landing.yaml` (the
hand-authored primary, importable as `@kitsoki/dev-story`) and its two thin
downstream instances `stories/pets-dev/` / `stories/slidey-dev/`, which
import dev-story as alias `core` and inherit its `landing` room (with the
workbench macro's already-desugared `on_enter`/capture-intent/world-var
shape) unmodified through kitsoki's import-folding (every non-reserved
child world key and intent name gets renamed `core__<key>` —
`internal/app/imports_rewriter.go` — which is why this registry's `pets-dev`
/ `slidey-dev` entries carry the `core__`-prefixed names while `dev-story`
itself does not). No story YAML needed changing for this — the fold already
carries `landing_capture` -> `core__landing_capture` and (once
`landing_expected_effects` was added below to `stories/dev-story/app.yaml`'s
world:) `landing_expected_effects` -> `core__landing_expected_effects`
automatically, verified directly by loading both thin instances.

Per turn this compiles:
  - `intent: {name: <capture_intent>, slots: {request: <verbatim mined
    text>}}`, `display_input: <verbatim text>` — same fidelity contract as
    the `ask`-intent path above; the workbench's synthesized capture arc
    self-targets, so `expect_state` is the same room on every turn.
  - one `host.agent.task` cassette episode (not `host.agent.converse` — the
    workbench's synthesized on_enter always dispatches `host.agent.task`),
    whose `submitted.summary` is `canned_answer(scenario, turn_index)` —
    the SAME deterministic derivation the ask-intent path already uses
    (corrective_ops on a corrected turn; expected_effects joined on the
    scenario's own last turn; a bare abandonment acknowledgement when the
    scenario never reached a satisfying outcome; a generic ack otherwise).
    Reusing it here is what makes the engine's own expected_effects join
    honest either way: a non-abandoned scenario's final note actually
    states its expected_effects (so the join finds them and reports
    candidate_completed = true), while an abandoned scenario's final note
    deliberately does NOT claim them (so the join reports candidate_completed
    = false) — never a value hardcoded by this compiler.
  - ONLY on the scenario's LAST turn, a `world_override:` seeding the
    target's `expected_effects_key` with `scenario["expected_effects"]`
    verbatim. Earlier turns carry no such override, so
    `workbenchGateSignal`'s join input is absent for them and they fall back
    to the plain dispatch-success proxy — exactly right, since a mid-scenario
    close-out note isn't expected to already state a goal that hasn't been
    reached yet. Putting the join input only on the final turn is what makes
    a real multi-turn scenario's overall `candidate_completed` (S6's
    `build_parity_record` ANDs every turn's signal) hinge on whether the
    scenario's ACTUAL outcome was reached, not on incidental earlier-turn
    phrasing.

## Determinism

Same contract as `compile_scenario` above: a pure function of the scenario
IR document's bytes (plus the fixed, in-repo target registry) — byte-
identical reruns.

Usage:
  python3 flow_fixture_compiler.py --scenarios-dir calibration --out calibration/flows
  python3 flow_fixture_compiler.py --scenario calibration/scn-foo-0000.json --out /tmp/out
  python3 flow_fixture_compiler.py --scenario calibration/scn-foo-0000.json --out /tmp/out --target dev-story
"""
import argparse
import glob
import json
import os
import sys

HARNESS_APP_REL = "stories/scenario-foundry-harness/app.yaml"

FLOW_HEADER = (
    "# Generated by tools/session-mining/flow_fixture_compiler.py (task 3.1).\n"
    "# DO NOT EDIT BY HAND — regenerate from the source scenario IR document.\n"
    "# Turns are one-per-mined-user-turn, each projected onto the fixed `ask`\n"
    "# intent of stories/scenario-foundry-harness (slots.text drives the\n"
    "# stubbed host.agent.converse call; display_input carries the operator's\n"
    "# verbatim words for the trace/replay UI). See the module docstring for\n"
    "# why this app rather than the source story's own intents.\n"
)

CASSETTE_HEADER = (
    "# Generated by tools/session-mining/flow_fixture_compiler.py (task 3.1).\n"
    "# DO NOT EDIT BY HAND — regenerate from the source scenario IR document.\n"
    "# One episode per mined turn, matched on handler (sequential — the i-th\n"
    "# converse call consumes the i-th episode). Canned answers are derived\n"
    "# deterministically from the scenario's own corrective_ops/expected_effects\n"
    "# fields, never invented content.\n"
)


def _yaml_str(s):
    """Render a Python string as a YAML double-quoted scalar (valid on any
    single-line string; mined turn text has already been through prep.py's
    whitespace-collapsing distillation, so embedded newlines are not expected,
    but we escape defensively via json.dumps, which produces valid YAML
    double-quoted-scalar syntax for any string)."""
    return json.dumps(s)


def canned_answer(scenario, turn_index):
    """Deterministically derive the canned converse/task answer for
    turn_index (0-based) in scenario. Never invents content: only reflects
    fields already present on the (redacted) scenario IR document.

    Order matters (S6 "workbench-target" fix): `abandoned` is checked BEFORE
    `expected_effects` on the last turn, so a scenario whose mined span ended
    without a satisfying outcome never has its stub note claim the
    (unreached) expected_effects — matching schema/scenario_ir.schema.json's
    own `abandoned` doc ("such a scenario should assert the workbench does
    NOT silently claim success"). This is what lets
    compile_scenario_onto_workbench's real expected_effects join
    (internal/orchestrator/workbench_gate_signal.go) come out honestly
    incomplete for an abandoned scenario that DOES carry expected_effects
    (representing what should have happened, not what the note claims did).
    Previously this checked expected_effects first, which — harmlessly for
    every existing fixture (none combine abandoned=true with a non-empty
    expected_effects) — would have overridden the abandonment phrasing.
    """
    turn = scenario["turns"][turn_index]
    is_last = turn_index == len(scenario["turns"]) - 1
    if turn.get("corrected") and turn.get("corrective_ops"):
        ops = "; ".join(turn["corrective_ops"])
        return "noted — a correction followed (%s)" % ops
    if scenario.get("abandoned") and is_last:
        return "acknowledged (scenario ends unresolved in the source session)"
    if is_last and scenario.get("expected_effects"):
        effects = "; ".join(scenario["expected_effects"])
        return "acknowledged — expected effects: %s" % effects
    return "acknowledged"


def compile_scenario(scenario):
    """Compile one scenario IR document (already-parsed dict) into
    (flow_yaml_bytes, cassette_yaml_bytes). Pure function of `scenario`.
    """
    if scenario.get("kind") != "conversation":
        raise ValueError("flow_fixture_compiler: not a kind:conversation document: %r" % scenario.get("kind"))
    turns = scenario.get("turns") or []
    if not turns:
        raise ValueError("flow_fixture_compiler: scenario %s has no turns" % scenario.get("id"))

    scenario_id = scenario["id"]
    cassette_name = "%s.cassette.yaml" % scenario_id

    flow_lines = [
        FLOW_HEADER,
        "test_kind: flow",
        "app: ../../../../stories/scenario-foundry-harness/app.yaml",
        "host_cassette: %s" % cassette_name,
        "initial_state: desk",
        "initial_world: {}",
        "turns:",
    ]
    cassette_lines = [
        CASSETTE_HEADER,
        "kind: host_cassette",
        "app_id: scenario-foundry-harness",
        'app_version: "0.1.0"',
        "match_on: [handler]",
        "",
        "episodes:",
    ]

    for i, turn in enumerate(turns):
        text = turn["text"]
        flow_lines.append("  - intent: { name: ask, slots: { text: %s } }" % _yaml_str(text))
        flow_lines.append("    display_input: %s" % _yaml_str(text))
        flow_lines.append("    expect_state: desk")
        answer = canned_answer(scenario, i)
        flow_lines.append("    expect_world: { last_answer: %s }" % _yaml_str(answer))

        cassette_lines.append("  - id: converse_%d" % (i + 1))
        cassette_lines.append("    match: { handler: host.agent.converse }")
        cassette_lines.append("    response:")
        cassette_lines.append("      data:")
        cassette_lines.append("        answer: %s" % _yaml_str(answer))

    flow_lines.append("expect_no_errors: true")

    flow_yaml = "\n".join(flow_lines) + "\n"
    cassette_yaml = "\n".join(cassette_lines) + "\n"
    return flow_yaml.encode("utf-8"), cassette_yaml.encode("utf-8")


# The real `workbench:` rooms this project ships (see module docstring's
# "real-workbench target" section for why these three and no others, and why
# the pets-dev/slidey-dev entries carry `core__`-prefixed names). Keyed by a
# short target name usable on the CLI (--target).
WORKBENCH_TARGETS = {
    "dev-story": {
        "app_rel": "../../../../stories/dev-story/app.yaml",
        "app_id": "dev-story",
        "app_version": "0.1.0",
        "initial_state": "landing",
        "capture_intent": "landing_capture",
        "capture_slot_key": "landing_request",
        "note_key": "landing_note",
        "expected_effects_key": "landing_expected_effects",
    },
    "pets-dev": {
        "app_rel": "../../../../stories/pets-dev/app.yaml",
        "app_id": "pets-dev",
        "app_version": "0.1.0",
        "initial_state": "core.landing",
        "capture_intent": "core__landing_capture",
        "capture_slot_key": "core__landing_request",
        "note_key": "core__landing_note",
        "expected_effects_key": "core__landing_expected_effects",
    },
    "slidey-dev": {
        "app_rel": "../../../../stories/slidey-dev/app.yaml",
        "app_id": "slidey-dev",
        "app_version": "0.1.0",
        "initial_state": "core.landing",
        "capture_intent": "core__landing_capture",
        "capture_slot_key": "core__landing_request",
        "note_key": "core__landing_note",
        "expected_effects_key": "core__landing_expected_effects",
    },
}

WORKBENCH_FLOW_HEADER = (
    "# Generated by tools/session-mining/flow_fixture_compiler.py\n"
    "# (compile_scenario_onto_workbench, room-workbench S6 \"workbench-target\").\n"
    "# DO NOT EDIT BY HAND — regenerate from the source scenario IR document.\n"
    "# Turns are one-per-mined-user-turn, each projected onto the target's\n"
    "# real workbench: room's synthesized <room>_capture intent (slots.request\n"
    "# drives the room's own on_enter host.agent.task dispatch; display_input\n"
    "# carries the operator's verbatim words for the trace/replay UI). Only\n"
    "# the LAST turn carries a world_override seeding the target's\n"
    "# expected_effects world var — see the module docstring for why.\n"
)

WORKBENCH_CASSETTE_HEADER = (
    "# Generated by tools/session-mining/flow_fixture_compiler.py\n"
    "# (compile_scenario_onto_workbench, room-workbench S6 \"workbench-target\").\n"
    "# DO NOT EDIT BY HAND — regenerate from the source scenario IR document.\n"
    "# One host.agent.task episode per mined turn (sequential — the i-th\n"
    "# dispatch consumes the i-th episode), whose submitted.summary is\n"
    "# canned_answer(scenario, i) — the same deterministic derivation the\n"
    "# ask-intent harness path uses, never invented content.\n"
)


def compile_scenario_onto_workbench(scenario, target_name):
    """Compile one scenario IR document onto WORKBENCH_TARGETS[target_name]'s
    real `workbench:` room. Pure function of (scenario, target_name); see the
    module docstring's "real-workbench target" section for the full
    contract. Returns (flow_yaml_bytes, cassette_yaml_bytes).
    """
    if scenario.get("kind") != "conversation":
        raise ValueError("flow_fixture_compiler: not a kind:conversation document: %r" % scenario.get("kind"))
    turns = scenario.get("turns") or []
    if not turns:
        raise ValueError("flow_fixture_compiler: scenario %s has no turns" % scenario.get("id"))
    if target_name not in WORKBENCH_TARGETS:
        raise ValueError(
            "flow_fixture_compiler: unknown workbench target %r (known: %s)"
            % (target_name, ", ".join(sorted(WORKBENCH_TARGETS)))
        )
    target = WORKBENCH_TARGETS[target_name]

    scenario_id = scenario["id"]
    cassette_name = "%s.%s.cassette.yaml" % (scenario_id, target_name)
    last_index = len(turns) - 1

    flow_lines = [
        WORKBENCH_FLOW_HEADER,
        "test_kind: flow",
        "app: %s" % target["app_rel"],
        "host_cassette: %s" % cassette_name,
        "initial_state: %s" % target["initial_state"],
        "initial_world: {}",
        "turns:",
    ]
    cassette_lines = [
        WORKBENCH_CASSETTE_HEADER,
        "kind: host_cassette",
        "app_id: %s" % target["app_id"],
        'app_version: "%s"' % target["app_version"],
        "match_on: [handler]",
        "",
        "episodes:",
    ]

    for i, turn in enumerate(turns):
        text = turn["text"]
        answer = canned_answer(scenario, i)

        flow_lines.append("  - intent: { name: %s, slots: { request: %s } }" % (target["capture_intent"], _yaml_str(text)))
        flow_lines.append("    display_input: %s" % _yaml_str(text))
        if i == last_index:
            # Only the final turn's join input is meaningful — see module
            # docstring. `json.dumps` on a plain list of strings produces
            # valid YAML flow-sequence syntax too, so this is safe even for
            # an empty list ("[]").
            flow_lines.append(
                "    world_override: { %s: %s }"
                % (target["expected_effects_key"], json.dumps(list(scenario.get("expected_effects") or [])))
            )
        flow_lines.append("    expect_state: %s" % target["initial_state"])
        flow_lines.append(
            "    expect_world: { %s: %s, %s: { summary: %s } }"
            % (target["capture_slot_key"], _yaml_str(text), target["note_key"], _yaml_str(answer))
        )

        cassette_lines.append("  - id: dispatch_%d" % (i + 1))
        cassette_lines.append("    match: { handler: host.agent.task }")
        cassette_lines.append("    response:")
        cassette_lines.append("      data:")
        cassette_lines.append("        ok: true")
        cassette_lines.append("        submitted:")
        cassette_lines.append("          summary: %s" % _yaml_str(answer))

    flow_lines.append("expect_no_errors: true")

    flow_yaml = "\n".join(flow_lines) + "\n"
    cassette_yaml = "\n".join(cassette_lines) + "\n"
    return flow_yaml.encode("utf-8"), cassette_yaml.encode("utf-8")


def load_scenarios(paths):
    docs = []
    for p in paths:
        with open(p, "r", encoding="utf-8") as f:
            docs.append(json.load(f))
    return docs


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--scenarios-dir", help="directory of scn-*.json scenario IR documents")
    ap.add_argument("--scenario", action="append", default=[], help="a single scenario IR file (repeatable)")
    ap.add_argument("--out", required=True, help="output directory for compiled .flow.yaml/.cassette.yaml pairs")
    ap.add_argument(
        "--target",
        default="harness",
        choices=["harness"] + sorted(WORKBENCH_TARGETS),
        help="'harness' (default): project onto stories/scenario-foundry-harness's fixed ask intent "
             "(see module docstring). Any other value: project onto that real workbench: room "
             "(WORKBENCH_TARGETS) instead, for the S6 usable-kitsoki-gate parity measurement.",
    )
    args = ap.parse_args(argv)

    paths = list(args.scenario)
    if args.scenarios_dir:
        paths.extend(sorted(glob.glob(os.path.join(args.scenarios_dir, "scn-*.json"))))
    if not paths:
        print("flow_fixture_compiler: no scenario IR documents given (use --scenarios-dir or --scenario)", file=sys.stderr)
        return 2

    os.makedirs(args.out, exist_ok=True)
    compiled = 0
    for scenario in load_scenarios(paths):
        scenario_id = scenario["id"]
        if args.target == "harness":
            flow_yaml, cassette_yaml = compile_scenario(scenario)
            flow_path = os.path.join(args.out, "%s.flow.yaml" % scenario_id)
            cassette_path = os.path.join(args.out, "%s.cassette.yaml" % scenario_id)
        else:
            flow_yaml, cassette_yaml = compile_scenario_onto_workbench(scenario, args.target)
            flow_path = os.path.join(args.out, "%s.%s.flow.yaml" % (scenario_id, args.target))
            cassette_path = os.path.join(args.out, "%s.%s.cassette.yaml" % (scenario_id, args.target))
        with open(flow_path, "wb") as f:
            f.write(flow_yaml)
        with open(cassette_path, "wb") as f:
            f.write(cassette_yaml)
        compiled += 1
    print("flow_fixture_compiler: compiled %d scenario(s) into %s (target=%s)" % (compiled, args.out, args.target))
    return 0


if __name__ == "__main__":
    sys.exit(main())
