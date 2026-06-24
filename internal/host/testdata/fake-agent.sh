#!/usr/bin/env bash
# fake-agent.sh — emulates `claude -p` for Agent handler tests.
# Echoes an answer that includes the session-id it was invoked with so tests
# can assert the handler forwarded the session correctly.
#
# When --append-system-prompt is supplied, the system prompt text is also
# embedded in the answer (after the sid) so tests can assert the persona
# threading. When --model is supplied, the model name is appended too so
# tests covering the named-agent Model field can assert the round-trip.
# Optional arguments to keep backward compat with existing tests that match
# only the leading "ANSWER for q=[...] sid=..." prefix.
set -euo pipefail

session_id=""
system_prompt=""
model=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session-id) session_id="$2"; shift 2 ;;
    --resume) session_id="$2"; shift 2 ;;
    --append-system-prompt) system_prompt="$2"; shift 2 ;;
    --system-prompt) system_prompt="$2"; shift 2 ;;
    --model) model="$2"; shift 2 ;;
    *) shift ;;
  esac
done

question="$(cat /dev/stdin)"
out="ANSWER for q=[${question}] sid=${session_id}"
if [ -n "$system_prompt" ]; then
  out="${out} system=[${system_prompt}]"
fi
if [ -n "$model" ]; then
  out="${out} model=[${model}]"
fi
printf '%s\n' "$out"
