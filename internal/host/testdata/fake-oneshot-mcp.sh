#!/usr/bin/env bash
# fake-oneshot-mcp.sh — emulates `claude -p` for host.oracle.ask_with_mcp tests.
#
# Inspects argv for --mcp-config and --output-format. Echoes back a JSON object
# containing both the stdin text and the path of any --mcp-config file it
# received, so tests can assert that the handler materialized the temp file
# correctly.
#
# Sentinel "FAIL" in stdin → exit 2 with stderr, same convention as
# fake-oneshot.sh.
set -euo pipefail

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
# This exercises host.oracle.ask_with_mcp's read-back of Result.Data["submitted"]
# without needing a real MCP roundtrip.
sentinel="$(printf '%s' "$stdin" | grep -o 'SIMULATE_SUBMIT=.*' || true)"
if [ -n "$sentinel" ] && [ -n "$mcp_body" ]; then
  payload="${sentinel#SIMULATE_SUBMIT=}"
  output_path="$(python3 -c '
import json, sys
cfg = json.loads(sys.argv[1])
v = cfg.get("mcpServers", {}).get("validator", {})
args = v.get("args", [])
for i, a in enumerate(args):
    if a == "--output" and i + 1 < len(args):
        print(args[i + 1])
        break
' "$mcp_body")"
  if [ -n "$output_path" ]; then
    printf '%s' "$payload" > "$output_path"
  fi
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
