#!/usr/bin/env bash
# brief_key_mapping.sh — regression guard: fleet_load.star must read the `brief`
# key from the decomposition manifest, not just agent_brief/goal/title.
#
# Bug: the old Python loader built each entry's brief from
#   b.get("agent_brief") or b.get("goal") or b.get("title") or ""
# omitting the contracted `brief:` key, so those manifests
# produced empty goal_text and dead-end slices.
#
# This test FAILS on the pre-fix script (brief comes out empty) and PASSES
# after the fix (brief key is read first in the fallback chain).
#
# Run from repo root: bash stories/fleet/tests/brief_key_mapping.sh
set -euo pipefail

OUTPUT=$(go test ./internal/host/starlark -run '^TestFleetLoadBriefKeyMapping$' -count=1 2>&1)

if ! grep -q "ok" <<< "$OUTPUT"; then
  echo "$OUTPUT"
  echo "FAIL: fleet_load.star does not read the 'brief' key."
  exit 1
fi

echo "PASS: fleet_load.star reads the brief key"
