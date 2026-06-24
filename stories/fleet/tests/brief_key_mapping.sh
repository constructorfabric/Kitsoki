#!/usr/bin/env bash
# brief_key_mapping.sh — regression guard: fleet_load.py must read the `brief`
# key from the decomposition manifest, not just agent_brief/goal/title.
#
# Bug: fleet_load.py built each entry's brief from
#   b.get("agent_brief") or b.get("goal") or b.get("title") or ""
# omitting b.get("brief") — so manifests using the contracted `brief:` key
# produced empty goal_text and dead-end slices.
#
# This test FAILS on the pre-fix script (brief comes out empty) and PASSES
# after the fix (brief key is read first in the fallback chain).
#
# Run from repo root: bash stories/fleet/tests/brief_key_mapping.sh
set -euo pipefail

TMPFILE=$(mktemp /tmp/fleet_brief_test_XXXXXX.json)
trap 'rm -f "$TMPFILE"' EXIT

cat > "$TMPFILE" <<'EOF'
{"briefs":[{"id":"x","brief":"a sufficiently long brief string","gate_command":"go build ./..."}]}
EOF

OUTPUT=$(python3 stories/fleet/scripts/fleet_load.py "$TMPFILE")

BRIEF=$(python3 -c "
import json, sys
data = json.loads(sys.stdin.read())
print(data['fleet_briefs'][0]['brief'])
" <<< "$OUTPUT")

if [ -z "$BRIEF" ]; then
  echo "FAIL: fleet_briefs[0].brief is empty — fleet_load.py does not read the 'brief' key."
  exit 1
fi

echo "PASS: fleet_briefs[0].brief = '$BRIEF'"
