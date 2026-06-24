#!/usr/bin/env bash
# integrate_room_hardening.sh — REAL regression guard for the three
# dt-integrate-buildcheck defects in stories/delivery-tail/rooms/integrate.yaml.
#
# The flow fixtures (bug_gate_string_collision.yaml, bug_blank_last_error.yaml)
# MOCK integrate_exec, so they prove ROUTING but cannot exercise the bash. This
# script guards the bash mechanisms directly:
#
#   #8  gate-string collision — STRUCTURAL: the gate must be passed as a
#       POSITIONAL parameter to `bash -c`, never pasted into a shell-parsed
#       assignment (`BUILD_CMD="{{ world.gate_command }}"`). FUNCTIONAL: the
#       positional mechanism runs a quote-bearing gate VERBATIM.
#   #9a blank last_error — FUNCTIONAL: the EXIT trap emits JSON with a non-blank
#       last_error even when the script aborts via `set -e` before any emit.
#   #checkout — STRUCTURAL: the checkout exit is captured (CO_EXIT), not left to
#       a bare `set -e` abort.
#
# Run from repo root: bash stories/delivery-tail/tests/integrate_room_hardening.sh
# Exit 0 → all guards hold (bugs fixed). Exit 1 → a defect is present.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ROOM="$REPO_ROOT/stories/delivery-tail/rooms/integrate.yaml"
fail=0
note() { echo "  $1"; }

echo "=== integrate room hardening guard: $ROOM ==="

# ── #8 STRUCTURAL ───────────────────────────────────────────────────────────
echo "--- #8: gate passed positionally, never pasted into an assignment ---"
if grep -Eq 'BUILD_CMD="\{\{[[:space:]]*world\.gate_command' "$ROOM"; then
  note "FAIL: gate is interpolated into a shell-parsed assignment (the bug)."
  fail=1
else
  note "ok: no BUILD_CMD=\"{{ world.gate_command }}\" assignment."
fi
if grep -q 'GATE_CMD="$1"' "$ROOM" && grep -q 'bash -c "$GATE_CMD"' "$ROOM"; then
  note "ok: gate taken from positional \$1 and run via bash -c \"\$GATE_CMD\"."
else
  note "FAIL: gate is not taken from the positional \$1 / run via bash -c \"\$GATE_CMD\"."
  fail=1
fi
# The gate must appear as a trailing argv element (positional) to bash -c.
if grep -q '"{{ world.gate_command }}"' "$ROOM"; then
  note "ok: gate present as a verbatim argv element."
else
  note "FAIL: gate is not passed as an argv element."
  fail=1
fi

# ── #8 FUNCTIONAL: positional mechanism preserves quotes verbatim ───────────
echo "--- #8 functional: a quote-bearing gate runs verbatim via positional \$1 ---"
# Mirror the runtime: exec.CommandContext(ctx,"bash","-c",SCRIPT,"kitsoki",GATE).
# GATE has load-bearing inner double-quotes + a space; it must survive intact.
SCRIPT='GATE_CMD="$1"; bash -c "$GATE_CMD"'
GATE='printf "%s" "A B"'              # only prints "A B" if the quotes survive
OUT=$(bash -c "$SCRIPT" kitsoki "$GATE" 2>&1)
if [ "$OUT" = "A B" ]; then
  note "ok: positional gate ran verbatim (got: '$OUT')."
else
  note "FAIL: positional gate mangled (got: '$OUT', want 'A B')."
  fail=1
fi

# ── #9a FUNCTIONAL: trap emits JSON on an unexpected set -e abort ────────────
echo "--- #9a functional: EXIT trap emits non-blank last_error on abort ---"
ABORT_SCRIPT='
set -e
EMITTED=0
emit() { jq -n "$@"; EMITTED=1; exit 0; }
trap '"'"'rc=$?; if [ "$EMITTED" != "1" ]; then jq -n --arg rc "$rc" "{outcome:\"error\",integrated_sha:\"\",last_op_ok:false,last_error:(\"integrate aborted before completing (exit \"+\$rc+\")\")}"; fi; exit 0'"'"' EXIT
false                                  # unguarded failure → set -e abort
emit '"'"'{outcome:"integrated"}'"'"'  # never reached
'
AOUT=$(bash -c "$ABORT_SCRIPT" 2>&1)
if echo "$AOUT" | grep -q '"outcome": "error"' && \
   echo "$AOUT" | grep -q 'aborted before completing'; then
  note "ok: trap emitted JSON with a non-blank last_error on abort."
else
  note "FAIL: trap did not emit a parseable error JSON (got: $AOUT)."
  fail=1
fi
# And the room itself must install the trap + emit helper.
if grep -q "trap 'rc=\$?" "$ROOM" && grep -q "emit() { jq -n" "$ROOM"; then
  note "ok: room installs the EXIT trap + emit helper."
else
  note "FAIL: room is missing the EXIT trap / emit helper."
  fail=1
fi

# ── #checkout STRUCTURAL: checkout exit captured ────────────────────────────
echo "--- #checkout: the integration checkout exit is captured ---"
if grep -q 'CO_EXIT=$?' "$ROOM"; then
  note "ok: checkout exit captured (CO_EXIT)."
else
  note "FAIL: checkout exit not captured — a failed checkout aborts blindly."
  fail=1
fi

echo "==================================================================="
if [ "$fail" -ne 0 ]; then
  echo "FAIL: one or more integrate-room hardening guards regressed."
  exit 1
fi
echo "PASS: integrate room is hardened (#8 / #9a / #checkout)."
