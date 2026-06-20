#!/usr/bin/env bash
# fake-oneshot-mcp.sh — emulates `claude -p` for host.agent.ask_with_mcp tests.
#
# Inspects argv for --mcp-config and --output-format. Echoes back a JSON object
# containing both the stdin text and the path of any --mcp-config file it
# received, so tests can assert that the handler materialized the temp file
# correctly.
#
# Sentinel "FAIL" in stdin → exit 2 with stderr, same convention as
# fake-oneshot.sh.
set -uo pipefail

# Capture the original argv before the case-shift loop consumes it; the
# retry-loop tests need to see --resume / --session-id.
orig_argv=("$@")

# When KITSOKI_FAKE_ARGV_DUMP is set, append the argv (space-joined) as ONE
# line per invocation to that path. Tests use this to assert which session flag
# (--session-id vs --resume) the host passed on a given call. Newlines/tabs
# inside an arg value (the composed --system-prompt is multi-line) are collapsed
# to spaces so each invocation stays a single line for the test's line split.
if [ -n "${KITSOKI_FAKE_ARGV_DUMP:-}" ]; then
  printf '%s' "${orig_argv[*]}" | tr '\n\t' '  ' >> "$KITSOKI_FAKE_ARGV_DUMP"
  printf '\n' >> "$KITSOKI_FAKE_ARGV_DUMP"
fi

mcp_config=""
output_format="text"
while [ $# -gt 0 ]; do
  case "$1" in
    --mcp-config)
      mcp_config="$2"
      shift 2
      ;;
    --output-format)
      output_format="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

stdin="$(cat /dev/stdin)"
if printf '%s' "$stdin" | grep -q FAIL; then
  printf 'simulated failure\n' >&2
  exit 2
fi

# Read the MCP config file content (so tests can assert the JSON body).
mcp_body=""
if [ -n "$mcp_config" ] && [ -r "$mcp_config" ]; then
  mcp_body="$(cat "$mcp_config")"
fi

# If the prompt contains the sentinel "SIMULATE_SUBMIT={...}" the fake
# binary writes that JSON to the validator's --output path, simulating
# what claude would do when it calls the validator's submit() tool.
# This exercises host.agent.ask_with_mcp's read-back of Result.Data["submitted"]
# without needing a real MCP roundtrip.
sentinel="$(printf '%s' "$stdin" | grep -o 'SIMULATE_SUBMIT=.*' || true)"
if [ -n "$sentinel" ] && [ -n "$mcp_body" ]; then
  payload="${sentinel#SIMULATE_SUBMIT=}"
  paths_json="$(python3 -c '
import json, sys
cfg = json.loads(sys.argv[1])
v = cfg.get("mcpServers", {}).get("validator", {})
args = v.get("args", [])
out = {"output": "", "state": ""}
for i, a in enumerate(args):
    if a == "--output" and i + 1 < len(args):
        out["output"] = args[i + 1]
    if a == "--state-file" and i + 1 < len(args):
        out["state"] = args[i + 1]
print(json.dumps(out))
' "$mcp_body")"
  output_path="$(printf '%s' "$paths_json" | python3 -c 'import json,sys;print(json.load(sys.stdin)["output"])')"
  state_path="$(printf '%s' "$paths_json" | python3 -c 'import json,sys;print(json.load(sys.stdin)["state"])')"
  if [ -n "$output_path" ]; then
    printf '%s' "$payload" > "$output_path"
  fi
  # When the host runs the retry loop it reads the validator state file
  # to compute Outcome. Synthesize a successful-submit state so the loop
  # exits after one iteration with Result.submitted bound to the payload.
  if [ -n "$state_path" ]; then
    printf '{"attempts":1,"successful_submits":1,"last_error":""}' > "$state_path"
  fi
fi

# "SIMULATE_ABANDON" — exit cleanly without writing anything to the
# validator's output or state file. From the host's point of view this
# looks like the LLM ended without calling submit at all (Outcome ==
# Abandoned). The retry loop should re-engage on subsequent iterations.
# No-op here; we just drop through.

# "SIMULATE_REJECT_THEN_OK" — the first iteration writes a non-success
# state file ("rejected"); a second iteration with the resume marker
# writes a successful state. The fake recognises the resume marker by
# scanning argv for the sentinel "--resume" (set by the host's retry
# loop on iter > 0).
resume_marker=""
if [ ${#orig_argv[@]} -gt 0 ] && printf '%s\n' "${orig_argv[@]}" | grep -q -- '--resume'; then
  resume_marker="resume"
fi
# The reject-then-ok scenario is sticky across iterations because the
# nudge prompt (iter 1+) won't carry the SIMULATE_REJECT_THEN_OK
# sentinel — only iter 0's user-authored prompt does. Tests opt in
# via env var KITSOKI_FAKE_REJECT_THEN_OK=1 (set with t.Setenv) so the
# behaviour persists across the host's claude restarts.
reject_then_ok="${KITSOKI_FAKE_REJECT_THEN_OK:-}"
if [ -z "$reject_then_ok" ] && printf '%s' "$stdin" | grep -q SIMULATE_REJECT_THEN_OK; then
  reject_then_ok="1"
fi
if [ "$reject_then_ok" = "1" ]; then
  state_path="$(printf '%s' "$mcp_body" | python3 -c '
import json, sys
try:
  cfg = json.loads(sys.stdin.read())
except Exception:
  print("")
  sys.exit(0)
v = cfg.get("mcpServers", {}).get("validator", {})
for i, a in enumerate(v.get("args", [])):
  if a == "--state-file" and i + 1 < len(v["args"]):
    print(v["args"][i + 1]); break
')"
  if [ -n "$state_path" ]; then
    if [ -z "$resume_marker" ]; then
      # First iteration: pretend abandoned (no successful submit, low
      # attempt count) so the host retry loop nudges.
      printf '{"attempts":1,"successful_submits":0,"last_error":"first attempt rejected"}' > "$state_path"
    else
      # Second iteration (after --resume nudge): pretend success.
      printf '{"attempts":2,"successful_submits":1,"last_error":""}' > "$state_path"
      output_path="$(printf '%s' "$mcp_body" | python3 -c '
import json, sys
try:
  cfg = json.loads(sys.stdin.read())
except Exception:
  print("")
  sys.exit(0)
v = cfg.get("mcpServers", {}).get("validator", {})
for i, a in enumerate(v.get("args", [])):
  if a == "--output" and i + 1 < len(v["args"]):
    print(v["args"][i + 1]); break
')"
      if [ -n "$output_path" ]; then
        printf '{"summary":"resume worked","resumed":true}' > "$output_path"
      fi
    fi
  fi
fi

# "SIMULATE_EXHAUST" — write a state file showing the validator
# rejected MaxRetries times so the host loop classifies the outcome as
# RetriesExhausted (regardless of how many outer iterations remain).
if printf '%s' "$stdin" | grep -q SIMULATE_EXHAUST; then
  state_path="$(printf '%s' "$mcp_body" | python3 -c '
import json, sys
try:
  cfg = json.loads(sys.stdin.read())
except Exception:
  print("")
  sys.exit(0)
v = cfg.get("mcpServers", {}).get("validator", {})
for i, a in enumerate(v.get("args", [])):
  if a == "--state-file" and i + 1 < len(v["args"]):
    print(v["args"][i + 1]); break
')"
  if [ -n "$state_path" ]; then
    printf '{"attempts":5,"successful_submits":0,"last_error":"verifier rejected: file foo.go did not change"}' > "$state_path"
  fi
fi

# KITSOKI_FAKE_RECORD=<path> — append the stdin prompt and argv to
# <path>, one entry per invocation. Used by retry-loop tests to assert
# the nudge-prompt content on iteration 1 (we can't carry a sentinel
# in the prompt because iter 1's prompt is the host-rendered nudge,
# not the original prompt). Set via t.Setenv.
record_path="${KITSOKI_FAKE_RECORD:-}"
if [ -n "$record_path" ]; then
  {
    printf '%s\n' '----iteration----'
    printf '%s\n' "$stdin"
    printf '%s\n' '----argv----'
    if [ ${#orig_argv[@]} -gt 0 ]; then
      printf '%s\n' "${orig_argv[@]}"
    fi
  } >> "$record_path"
fi

if [ "$output_format" = "json" ]; then
  # Emit a JSON envelope that includes everything the test needs to verify.
  python3 -c '
import json, sys, os
prompt = sys.argv[1]
mcp_path = sys.argv[2]
mcp_body = sys.argv[3]
print(json.dumps({
    "prompt": prompt,
    "mcp_config_path": mcp_path,
    "mcp_body": json.loads(mcp_body) if mcp_body else None,
}))
' "$stdin" "$mcp_config" "$mcp_body"
else
  printf 'prompt=%s\nmcp_config=%s\n' "$stdin" "$mcp_config"
  if [ -n "$mcp_body" ]; then
    printf 'mcp_body=%s\n' "$mcp_body"
  fi
fi
