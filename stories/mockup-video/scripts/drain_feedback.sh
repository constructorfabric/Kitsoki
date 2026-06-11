#!/usr/bin/env bash
# drain_feedback.sh — drain the slice-2 web feedback notes for this session.
#
# The /review web panel (slice 2, soft dep) appends one structured feedback
# note per line to <workspace>/feedback.jsonl:
#   {video_handle, source_ref, time_range, frame_handle, instruction}
# This script reads (and consumes) that file and emits a single batch object
# on stdout — host.run binds stdout_json into world.feedback_batch:
#   { "items": [ <note>, ... ] }
#
# With no web panel running the file is absent and the batch is empty, so the
# operator drives the refine loop with inline `refine feedback="…"` instead.
set -euo pipefail

WORKSPACE="${1:?usage: drain_feedback.sh <workspace>}"
JSONL="${WORKSPACE%/}/feedback.jsonl"

if [[ ! -s "$JSONL" ]]; then
  printf '{"items":[]}\n'
  exit 0
fi

# Fold the JSONL lines into {items:[...]}. Prefer jq; fall back to a manual join.
if command -v jq >/dev/null 2>&1; then
  jq -cs '{items: .}' "$JSONL"
else
  printf '{"items":['
  paste -sd, "$JSONL"
  printf ']}\n'
fi

# Consume the drained notes so they are not re-applied on the next review.
: > "$JSONL"
