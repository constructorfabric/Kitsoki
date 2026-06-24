#!/usr/bin/env bash
# fake-oneshot.sh — emulates `claude -p` for host.agent.ask handler tests.
# Reads the templated prompt from stdin and echoes it back verbatim.
# If stdin contains the sentinel token "FAIL", exits non-zero with a stderr
# message so the non-zero-exit path can be tested.
#
# When --append-system-prompt is supplied, the system prompt text is appended
# to the echoed stdout as `system=[...]` so tests covering the named-agent
# threading on host.agent.ask can assert the round-trip. --model is echoed
# the same way. Both are optional to keep backward compat with existing
# tests that only inspect the templated prompt body.
set -euo pipefail

system_prompt=""
model=""
while [ $# -gt 0 ]; do
  case "$1" in
    --append-system-prompt) system_prompt="$2"; shift 2 ;;
    --model) model="$2"; shift 2 ;;
    *) shift ;;
  esac
done

stdin="$(cat /dev/stdin)"
if printf '%s' "$stdin" | grep -q FAIL; then
  printf 'simulated failure\n' >&2
  exit 2
fi
out="$stdin"
if [ -n "$system_prompt" ]; then
  out="${out} system=[${system_prompt}]"
fi
if [ -n "$model" ]; then
  out="${out} model=[${model}]"
fi
printf '%s\n' "$out"
