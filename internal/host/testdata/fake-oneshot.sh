#!/usr/bin/env bash
# fake-oneshot.sh — emulates `claude -p` for host.oracle.ask handler tests.
# Reads the templated prompt from stdin and echoes it back verbatim.
# If stdin contains the sentinel token "FAIL", exits non-zero with a stderr
# message so the non-zero-exit path can be tested.
set -euo pipefail

# Ignore CLI flags — the real binary accepts --output-format, --permission-mode,
# etc. We don't care here; we just want to observe stdin.
while [ $# -gt 0 ]; do shift; done

stdin="$(cat /dev/stdin)"
if printf '%s' "$stdin" | grep -q FAIL; then
  printf 'simulated failure\n' >&2
  exit 2
fi
printf '%s\n' "$stdin"
