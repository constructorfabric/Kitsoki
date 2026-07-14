#!/usr/bin/env bash
# Claude Code PreToolUse guard for the operator-owned checkout.
set -euo pipefail

input="$(cat)"
json_string() {
  # The hook payload is JSON. The enforced command forms are intentionally
  # simple; the launch-policy preflight remains the authoritative boundary.
  printf '%s' "$input" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" | head -n 1
}

command="$(json_string command)"
cwd="$(json_string cwd)"
root="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
root="$(cd "$root" && pwd -P)"
[ -n "$cwd" ] || cwd="$root"
case "$cwd" in /*) ;; *) cwd="$root/$cwd" ;; esac
cwd="$(cd "$cwd" 2>/dev/null && pwd -P || printf '%s' "$cwd")"

case "$cwd" in "$root"/.capsules/workspaces|"$root"/.capsules/workspaces/*) exit 0 ;; esac

deny() {
  printf '%s\n' "Blocked by Kitsoki launch policy: $1" >&2
  printf '%s\n' "Use scripts/dev-workspace.sh create --id <id> --branch <branch> --bootstrap, then launch through kitsoki agent launch --exec from that capsule." >&2
  exit 2
}

case " $command " in
  *" git checkout "*|*" git switch "*|*" git restore "*) deny "the primary checkout is pinned to its default branch" ;;
  *" git commit "*|*" git commit-"*) deny "direct commits in the protected checkout are not allowed" ;;
esac

case " $command " in
  *" claude "*|*" codex "*|*" copilot "*|*" agy "*) deny "bare agent launch bypasses policy; use kitsoki agent launch --exec" ;;
esac

parent="$(cd "$root/.." && pwd -P)"
for sibling in "$parent"/*; do
  [ -d "$sibling/.git" ] || continue
  sibling="$(cd "$sibling" && pwd -P)"
  [ "$sibling" = "$root" ] && continue
  base="$(basename "$sibling")"
  case "$command" in
    *"$sibling"*|*"../$base"*) deny "sibling repository $base is not authorized; propose work to that repository instead" ;;
  esac
done

exit 0
