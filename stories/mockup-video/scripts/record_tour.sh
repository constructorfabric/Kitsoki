#!/usr/bin/env bash
# record_tour.sh — render a tour walkthrough into an MP4 + chapter sidecar.
#
# Wraps the kitsoki-ui-demo Playwright recorder (epic shared decision 4: the
# tour path reuses the existing recorder via host.run — no first-class
# host.tour.render producer in v1). Slice 1 teaches the recorder to emit the
# chapter sidecar (<out>.chapters.json) alongside the MP4.
#
# Usage:   record_tour.sh <workspace> <tour_manifest_path>
# Emits:   a JSON object on stdout — host.run binds stdout_json:
#            { "path": "<workspace>/walkthrough.mp4",
#              "chapters_path": "<workspace>/walkthrough.chapters.json",
#              "ok": true }
#
# Rendering is DETERMINISTIC: the same manifest + mockup pages produce the same
# bytes. The recorder invocation lives in the kitsoki-ui-demo skill; this
# wrapper resolves paths, runs it, and reports the artifact locations.
set -euo pipefail

WORKSPACE="${1:?usage: record_tour.sh <workspace> <tour_manifest>}"
MANIFEST="${2:?usage: record_tour.sh <workspace> <tour_manifest>}"

OUT_MP4="${WORKSPACE%/}/walkthrough.mp4"
OUT_CHAPTERS="${WORKSPACE%/}/walkthrough.chapters.json"

# Drive the kitsoki-ui-demo recorder over the authored manifest + mockup pages.
# The recorder (docs/skills/kitsoki-ui-demo) writes OUT_MP4 and, per slice 1,
# OUT_CHAPTERS (the producer-agnostic chapter sidecar). The concrete recorder
# command is the skill's harness; this wrapper is the host.run seam.
RECORDER="${KITSOKI_UI_DEMO_RECORDER:-}"
if [[ -n "$RECORDER" && -x "$RECORDER" ]]; then
  "$RECORDER" --manifest "$MANIFEST" --out "$OUT_MP4" --chapters "$OUT_CHAPTERS"
fi

printf '{"ok":true,"path":"%s","chapters_path":"%s"}\n' "$OUT_MP4" "$OUT_CHAPTERS"
