#!/usr/bin/env bash
# fake-claude.sh — emulates `claude -p` running with file-edit tools
# enabled inside the shadow story directory. Tests place the binary
# via $KITSOKI_ORACLE_CLAUDE_BIN.
#
# Behaviour is dispatched by sentinel tokens in the user's PROPOSAL
# section of the prompt (read from stdin). cwd is the shadow dir so
# any file edits land where authoring.diffDirs will see them.
#
#   FAIL_EXEC      → exit 2, write to stderr (exec failure)
#   REFUSE         → emit ERROR: line, no file edits, exit 0
#   NO_CHANGES     → emit only SUMMARY:, no file edits
#   ADD_LINE       → append a YAML comment to ./app.yaml
#   ADD_FILE       → write a new file ./prompts/new_prompt.md
#   BREAK_YAML     → corrupt ./app.yaml (test the validation gate)
#   default        → no edits, just emit SUMMARY:
set -euo pipefail
while [ $# -gt 0 ]; do shift; done
stdin="$(cat /dev/stdin)"

if printf '%s' "$stdin" | grep -q FAIL_EXEC; then
  printf 'simulated exec failure\n' >&2
  exit 2
fi

if printf '%s' "$stdin" | grep -q REFUSE; then
  printf 'ERROR: proposal is ambiguous and does not name a specific room.\n'
  printf 'The right place to edit is likely prompts/example.md.\n'
  exit 0
fi

if printf '%s' "$stdin" | grep -q ADD_LINE; then
  printf '\n# fake-claude appended this comment\n' >> ./app.yaml
fi

if printf '%s' "$stdin" | grep -q ADD_FILE; then
  mkdir -p ./prompts
  printf 'hello from fake-claude\n' > ./prompts/new_prompt.md
fi

if printf '%s' "$stdin" | grep -q BREAK_YAML; then
  printf 'this: is: not: valid: yaml: at: all:\n  - [unbalanced\n' > ./app.yaml
fi

# NO_CHANGES sentinel falls through with no edits.

printf 'SUMMARY: applied test edit\n'
