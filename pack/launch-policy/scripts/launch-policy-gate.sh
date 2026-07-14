#!/usr/bin/env bash
# Red-team K1 enforcement gate. Run from an installed consumer repository.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
hook="$root/.claude/hooks/block-bare-checkout.sh"
[ -x "$hook" ] || { echo "FAIL: missing executable Claude guard" >&2; exit 1; }

attempt_blocked() {
  local label="$1" command="$2"
  if CLAUDE_PROJECT_DIR="$root" "$hook" <<EOF
{"tool_name":"Bash","tool_input":{"command":"$command","cwd":"$root"}}
EOF
  then
    echo "FAIL: $label was allowed" >&2
    exit 1
  fi
  echo "PASS: $label blocked"
}

attempt_blocked "bare checkout" "git checkout -b main-adjacent"
attempt_blocked "direct main commit" "git commit -m direct-main"
attempt_blocked "bare agent launch" "claude -p unsafe"

parent="$(cd "$root/.." && pwd -P)"
sibling="$parent/launch-policy-sibling-gate"
cleanup() { rm -rf "$sibling"; }
trap cleanup EXIT
git init -q "$sibling"
attempt_blocked "sibling repository write" "printf nope > ../$(basename "$sibling")/blocked.txt"

echo "PASS: K1 launch policy red-team gate"
