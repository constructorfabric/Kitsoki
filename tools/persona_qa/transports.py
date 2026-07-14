"""Canonical transport profiles for the story-owned Persona QA harness.

The scenario-qa story treats transport as data: a scenario says which surfaces
it can be driven on, and this module defines the deterministic entrypoints and
proof contract for each surface. The product-journey runner, compatibility
adapter, story tests, and validation gates import the same catalog so the
operator-facing story cannot drift from retained kit/schema checks.
"""

from __future__ import annotations

from copy import deepcopy
from typing import Any


TRANSPORT_PROFILES: dict[str, dict[str, Any]] = {
    "tui": {
        "id": "tui",
        "label": "TUI",
        "visual_surface": "tui",
        "open_capabilities": ["session.open"],
        "observe_capabilities": ["render.tui", "session.trace", "session.inspect"],
        "act_capabilities": ["session.submit", "session.drive", "session.trace"],
        "preflight": "Call render.tui_png or render.tui before the first action and confirm the frame is real.",
        "recording_rule": "Persist pre-action and post-action TUI frames under the leg evidence directory.",
        "evidence_contract": {
            "primary_tool": "render.tui_png",
            "evidence_kind": "rendered_tui_frame",
            "level": "frame-level",
            "capture_hint": "Capture render.tui_png frames before and after the interaction under test.",
        },
    },
    "web": {
        "id": "web",
        "label": "Web UI",
        "visual_surface": "web",
        "open_capabilities": ["session.open", "visual.open"],
        "observe_capabilities": ["visual.observe", "session.trace", "session.inspect"],
        "act_capabilities": ["visual.act", "session.submit", "session.drive", "session.trace"],
        "preflight": "Call visual.open and visual.observe before the first action and confirm the browser frame is real.",
        "recording_rule": "Persist screenshots or rrweb clips that show the browser state before and after the interaction.",
        "evidence_contract": {
            "primary_tool": "visual.snapshot",
            "evidence_kind": "browser_screenshot",
            "level": "frame-level",
            "capture_hint": "Capture a visual.snapshot or rrweb recording of the browser surface.",
        },
    },
    "vscode": {
        "id": "vscode",
        "label": "VS Code bridge",
        "visual_surface": "vscode",
        "open_capabilities": ["session.open", "visual.open"],
        "observe_capabilities": ["visual.observe", "session.trace", "session.inspect"],
        "act_capabilities": ["session.submit", "session.drive", "visual.act", "session.trace"],
        "preflight": "Call visual.open kind=vscode and visual.observe before driving, then capture the bridge again after the scenario reaches its target state.",
        "recording_rule": "Persist distinct preflight and post-drive VS Code bridge captures; never reuse the preflight as outcome proof.",
        "evidence_contract": {
            "primary_tool": "visual.open (kind=vscode)",
            "evidence_kind": "screenshot_or_tui_png",
            "level": "bridge-level",
            "capture_hint": (
                "Open the VS Code bridge surface with visual.open kind=vscode; label "
                "this evidence bridge-level, not editor-level. A preflight capture "
                "alone is NOT sufficient: after driving the live session forward to "
                "its target state, capture visual.open kind=vscode again against the "
                "SAME session handle (a distinct post-drive capture, never reusing "
                "the preflight file/slot) before the leg can be scored anything "
                "other than degraded-evidence."
            ),
        },
        # editor_evidence_contract is a SECOND, stronger tier a vscode leg can
        # opportunistically reach on top of the mandatory bridge-level floor
        # above -- see docs/persona-qa.md's long-standing note that "VS Code
        # checks are bridge-level proof unless a future native editor
        # integration raises the contract". That integration is the
        # host.ide.* verb family (internal/host/ide_handlers.go): it queries
        # the REAL editor over the live internal/ide.Link the VS Code
        # extension opens (the same link the TUI's `/ide` command drives),
        # not the runstatus-webview stand-in visual.open kind=vscode renders.
        # It only produces evidence when BOTH (a) a genuine VS Code + kitsoki
        # extension is linked to this process, AND (b) the leg's driven
        # primary_story issues a host.ide.* call (e.g. stories/bugfix's
        # `validating` room already calls host.ide.get_diagnostics) while
        # advancing the leg -- so this tier is opportunistic, not guaranteed,
        # and MUST NOT be treated as a substitute for the bridge-level floor.
        # No visual pixels are captured this way (that would need a real VS
        # Code window screenshot, which the dev-workflows matrix's Mode A
        # investigation found requires extension launch+packaging no story
        # host call can reach deterministically today -- tracked as the
        # remaining gap, not faked here).
        "editor_evidence_contract": {
            "primary_tool": "session.trace (reads the ide.context_captured event a host.ide.* call appends)",
            "evidence_kind": "ide_context_capture",
            "level": "editor-level",
            "capture_hint": (
                "After driving the leg's session to its target state, call "
                "session.trace and look for a POST-drive `ide.context_captured` "
                "event (kind emitted by host.ide.get_open_editors / "
                "get_diagnostics / get_selection) whose payload has "
                "`connected: true`. Attach that event's JSON (never the raw "
                "selection/diagnostic text -- the handler already redacts it, "
                "keeping only file path, counts, and provenance) as "
                "post_drive_editor_evidence_ref, and its turn/verb as "
                "post_drive_editor_trace_ref. This is genuine editor-level "
                "proof -- it can only exist when a real editor was connected "
                "AND actually queried during the drive, unlike the "
                "always-available bridge-level capture above. Not every "
                "primary_story queries host.ide.* today and no live VS Code "
                "editor is attached in replay/CI, so this tier is "
                "opportunistic: when no such event is found, report it "
                "honestly (leave post_drive_editor_evidence_ref empty) rather "
                "than fabricating one -- the bridge-level capture above stays "
                "the mandatory floor either way."
            ),
            "requires": (
                "A real internal/ide.Link connection (VS Code + the kitsoki "
                "extension, matched by workspace) AND the leg's driven "
                "primary_story issuing host.ide.get_open_editors / "
                "get_diagnostics / get_selection during or after the drive."
            ),
        },
    },
    "cli": {
        "id": "cli",
        "label": "CLI",
        "visual_surface": "cli",
        "open_capabilities": ["session.open"],
        "observe_capabilities": ["session.status", "session.trace", "session.inspect"],
        "act_capabilities": ["session.submit", "session.drive", "session.trace"],
        "preflight": "Run the declared deterministic CLI/session entrypoint once and capture exit code plus stdout/stderr before treating command output as proof.",
        "recording_rule": "Persist command transcript, exit code, cwd, and relevant trace references under the leg evidence directory.",
        "evidence_contract": {
            "primary_tool": "command transcript",
            "evidence_kind": "command_output",
            "level": "terminal-level",
            "capture_hint": "Capture deterministic CLI stdout/stderr with exit code, cwd, and command line.",
        },
    },
}

TRANSPORT_IDS: list[str] = list(TRANSPORT_PROFILES)
TRANSPORT_EVIDENCE_CONTRACTS: dict[str, dict[str, Any]] = {
    transport: profile["evidence_contract"]
    for transport, profile in TRANSPORT_PROFILES.items()
}


def transport_profile(transport: str) -> dict[str, Any]:
    """Return a copy of a known transport profile."""

    profile = TRANSPORT_PROFILES.get(transport)
    if profile is None:
        raise ValueError(f"unknown transport profile: {transport}")
    return deepcopy(profile)


def compact_transport_profile(profile: dict[str, Any]) -> dict[str, Any]:
    """Return the stable profile shape embedded in run bundles."""

    contract = profile.get("evidence_contract", {})
    return {
        "id": profile.get("id", ""),
        "label": profile.get("label", ""),
        "visual_surface": profile.get("visual_surface", ""),
        "primary_tool": contract.get("primary_tool", ""),
        "evidence_kind": contract.get("evidence_kind", ""),
        "level": contract.get("level", ""),
        "preflight": profile.get("preflight", ""),
        "recording_rule": profile.get("recording_rule", ""),
    }


def normalize_transport_filter(transport_filter: str) -> list[str]:
    """Validate and normalize a comma-separated transport filter.

    An empty filter returns [] for legacy runner compatibility. `all` expands
    to the complete ordered catalog, even when mixed with explicit ids.
    """

    requested = [item.strip() for item in transport_filter.split(",") if item.strip()]
    if not requested:
        return []
    if "all" in requested:
        return list(TRANSPORT_IDS)

    duplicates = sorted({item for item in requested if requested.count(item) > 1})
    if duplicates:
        raise ValueError(f"duplicate transport id(s): {', '.join(duplicates)}")

    unknown = [item for item in requested if item not in TRANSPORT_IDS]
    if unknown:
        known = ", ".join(TRANSPORT_IDS + ["all"])
        raise ValueError(f"unknown transport id(s): {', '.join(unknown)}. Known transports: {known}")

    return requested
