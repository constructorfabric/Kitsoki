#!/usr/bin/env bash
# fake-claude.sh — emulates `claude -p` for authoring package tests.
#
# Reads the prompt from stdin and dispatches based on sentinel tokens
# present in the user's proposal section:
#
#   PROPOSAL contains "FAIL_EXEC"     -> exit 2, write to stderr (exec failure)
#   PROPOSAL contains "REFUSE"        -> emit `ERROR: ...` (no yaml fence)
#   PROPOSAL contains "RETURN_INVALID"-> emit a yaml fence with broken YAML
#   PROPOSAL contains "MALFORMED"     -> emit prose only, no SUMMARY/yaml
#   default                           -> echo back the original yaml (no-op edit)
#                                       OR if proposal contains "ADD_LINE",
#                                       append a comment so diff is non-empty.
set -euo pipefail
while [ $# -gt 0 ]; do shift; done
stdin="$(cat /dev/stdin)"

# Extract the original yaml between the first ```yaml and the matching ```
# in the prompt template's "current app.yaml" section. We use awk because
# bash regex over multi-line input is painful.
yaml="$(printf '%s' "$stdin" | awk '
  /^```yaml/ && !seen { seen=1; next }
  seen && /^```/ { exit }
  seen { print }
')"

if printf '%s' "$stdin" | grep -q FAIL_EXEC; then
  printf 'simulated exec failure\n' >&2
  exit 2
fi

if printf '%s' "$stdin" | grep -q REFUSE; then
  printf 'ERROR: proposal is ambiguous and does not name a specific room.\n'
  exit 0
fi

if printf '%s' "$stdin" | grep -q MALFORMED; then
  printf 'I would change the foyer message but I am not formatting this correctly.\n'
  exit 0
fi

if printf '%s' "$stdin" | grep -q RETURN_INVALID; then
  printf 'SUMMARY: invalid edit\n\n```yaml\nthis: is: not: valid yaml: at all:\n  - [missing\n```\n'
  exit 0
fi

if printf '%s' "$stdin" | grep -q ADD_LINE; then
  yaml="${yaml}"$'\n''# fake-claude appended this comment'
fi

printf 'SUMMARY: applied test edit\n\n```yaml\n%s\n```\n' "$yaml"
