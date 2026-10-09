# plan_legs.star — resolve the scenario-qa run bundle's driver-plan.json into
# the per-transport legs this story loops over.
#
# Catalog mode: driver-plan.json's "scenarios" list already IS the per-leg
# list (tools/product-journey/run.py's --transport axis expands one entry per
# scenario x transport leg, each carrying transport/leg_id/visual_surface/
# evidence_dir/transport_evidence_contract) — just read it back.
#
# Ad-hoc mode (a free-text description, no catalog scenario): run.py was
# still invoked (against a generic carrier scenario, "bugfix", which allows
# the reusable transport set) purely to get real project/persona resolution and a
# transport-shaped leg skeleton. This drafts the actual scenario (id/label/
# task/success_criteria) from the description and splices it onto those
# carrier legs, replacing every carrier-specific field but keeping the
# transport-mechanical ones (transport, visual_surface, transport_evidence_
# contract, required_mcp) — those came from the SAME --transport flag this
# story already passed to run.py, so they already match what was requested.
#
# Takes `run_id` (a plain scalar, already fresh at machine-time from this
# room's FIRST invoke) rather than a pre-computed run_dir string -- deriving
# the repo-relative run dir HERE, inside this invoke's own script, is what
# lets the engine's dispatch-time re-render supply the fresh run_id even
# though this invoke is queued right after the one that binds it. A `set:`
# in the calling room computing the same string would bake in a stale value
# instead (see plan.yaml's header comment).
#
# Flow fixtures and replay cassettes may provide an inline driver_plan object on
# the emit-run result. Prefer it when present; production still reads the
# canonical driver-plan.json from the run directory.
#
# Fail-closed harness selection (issue group B / bug #105 "replay-miss
# silently goes live"): the MCP session runtime itself already hard-fails a
# replay session that hits a host.agent.* call with no matching cassette
# episode instead of silently falling through to a live agent
# (internal/mcp/studio/session_runtime.go's replayAgentMissHandler, landed
# 616bdeae). What that engine-level fix cannot cover is the OTHER silent
# fallback this brief calls out: an operator running `check` without
# `profile=` gets no visible warning before the driver quietly stays
# replay-only and any missing cassette becomes a bare `degraded-evidence`
# verdict with no stated cause. This script computes that warning
# deterministically (Starlark, not an LLM judgment call) for every leg that
# looks like it needs live/interpretive drive (a scenario with
# `natural_utterances` — free-text routing a cassette can't cover — an
# ad-hoc description, or a `harness` hint containing "live"), and attaches it
# to each leg (`live_authorization_note`, also visible to the driver agent
# since the leg is spliced verbatim into `leg_json`) plus a flat
# `live_authorization_summary` list surfaced by `rooms/plan.yaml` and
# `rooms/execute.yaml` up front, before any leg is driven.
#
# Interface (authoritative in plan_legs.star.yaml):
#   inputs:  mode (string: catalog|adhoc), run_id (string),
#            description (string), primary_story (string),
#            live_profile (string, optional), last_result (object, optional)
#   outputs: legs (object {items:[...]}), leg_count (int),
#            driver_agent (string), run_dir_rel (string, repo-relative),
#            live_authorization_summary (list of string)

_ALPHANUM = "abcdefghijklmnopqrstuvwxyz0123456789"

def _slug(text):
    lowered = text.lower()
    out = ""
    last_dash = False
    for ch in lowered.elems():
        if ch in _ALPHANUM:
            out += ch
            last_dash = False
        elif len(out) > 0 and not last_dash:
            out += "-"
            last_dash = True
    if len(out) > 0 and out[-1] == "-":
        out = out[:-1]
    if len(out) > 40:
        out = out[:40]
    if out == "":
        out = "adhoc"
    return out


def _label(description):
    if len(description) <= 64:
        return description
    return description[:61] + "..."


def _needs_live(leg, mode):
    # Deterministic proxy for "this leg's flow needs interpretive behavior a
    # cassette can't replay" — the exact condition drive_leg.md/product-
    # journey-qa-driver.md already gate live authorization on in prose.
    # natural_utterances (free-text phrasing the persona would actually type)
    # is the strongest available signal; an ad-hoc description is by
    # definition unscripted operator prose with no catalog contract
    # guaranteeing a scripted-only flow, so it defaults to needing live too.
    utterances = leg.get("natural_utterances", [])
    if type(utterances) == "list" and len(utterances) > 0:
        return True
    harness = leg.get("harness", "")
    if type(harness) == "string" and "live" in harness:
        return True
    if mode == "adhoc":
        return True
    return False


def _live_authorization_note(leg, mode, live_profile):
    transport = leg.get("transport", "")
    if not _needs_live(leg, mode):
        return ""
    if live_profile != "":
        return "leg " + transport + ": live drive authorized (profile=" + live_profile + ")."
    return (
        "leg " + transport + ": needs `profile=<name>` for live drive — will run " +
        "replay-only; missing cassettes will be reported as degraded-evidence with cause."
    )


def _annotate_legs(legs, mode, live_profile):
    annotated = []
    summary = []
    for leg in legs:
        drafted = dict(leg)
        note = _live_authorization_note(drafted, mode, live_profile)
        drafted["live_authorization_note"] = note
        drafted["needs_live_hint"] = _needs_live(drafted, mode)
        annotated.append(drafted)
        if note != "" and live_profile == "":
            summary.append(note)
    return annotated, summary


def _draft_scenario(description, primary_story):
    scenario_id = "adhoc-" + _slug(description)
    return {
        "id": scenario_id,
        "label": _label(description),
        "primary_story": primary_story if primary_story != "" else "stories/dev-story/app.yaml",
        "task": description,
        "success_criteria": [
            "The described behavior is observable on the requested transport(s).",
            "Any broken or confusing behavior is captured as a concrete finding, not papered over.",
        ],
    }


def main(ctx):
    mode = ctx.inputs.get("mode", "catalog")
    run_id = ctx.inputs.get("run_id", "")
    live_profile = ctx.inputs.get("live_profile", "")
    last_result = ctx.inputs.get("last_result", {})
    if type(last_result) != "dict":
        last_result = {}
    # Prefer the runner-returned path. In live scenario-qa runs the bundle may
    # live in an automatically managed capsule workspace, so the path is the
    # workspace-local run dir rather than a primary-checkout artifact path.
    run_dir_rel = last_result.get("run_dir_rel", "")
    if run_dir_rel == "":
        # Backward-compatible fallback for old flow fixtures and cassettes.
        run_dir_rel = ".artifacts/product-journey/" + run_id
    plan = last_result.get("driver_plan", {})
    if type(plan) != "dict" or len(plan) == 0:
        content = ctx.fs.read(run_dir_rel + "/driver-plan.json")
        plan = json.decode(content)
    carrier_legs = plan.get("scenarios", [])
    driver_agent = plan.get("driver_agent", ".agents/agents/product-journey-qa-driver.md")

    if mode != "adhoc":
        annotated_legs, summary = _annotate_legs(carrier_legs, mode, live_profile)
        return {
            "legs": {"items": annotated_legs},
            "leg_count": len(annotated_legs),
            "driver_agent": driver_agent,
            "run_dir_rel": run_dir_rel,
            "plan_status": {"resolved": True},
            "live_authorization_summary": summary,
        }

    description = ctx.inputs.get("description", "")
    primary_story = ctx.inputs.get("primary_story", "")
    scenario = _draft_scenario(description, primary_story)

    legs = []
    for leg in carrier_legs:
        drafted = dict(leg)
        transport = leg.get("transport", "tui")
        drafted["scenario"] = scenario["id"]
        drafted["label"] = scenario["label"]
        drafted["primary_story"] = scenario["primary_story"]
        drafted["task_prompt"] = scenario["task"]
        drafted["success_criteria"] = scenario["success_criteria"]
        drafted["evidence_dir"] = "evidence/" + scenario["id"] + "/" + transport
        drafted["leg_id"] = scenario["id"] + "::" + transport
        legs.append(drafted)

    annotated_legs, summary = _annotate_legs(legs, mode, live_profile)
    return {
        "legs": {"items": annotated_legs},
        "leg_count": len(annotated_legs),
        "driver_agent": driver_agent,
        "run_dir_rel": run_dir_rel,
        "plan_status": {"resolved": True},
        "live_authorization_summary": summary,
    }
