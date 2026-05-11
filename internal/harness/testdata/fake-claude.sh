#!/usr/bin/env bash
# fake-claude.sh — stub for ClaudeCLIHarness exec-plumbing tests.
#
# Reads stdin (the prompt) and discards it. Parses the --mcp-config path
# from argv, extracts the validator's --output path from that config, and
# writes a fixed validated payload there to simulate a successful
# `mcp__kitsoki-validator__submit` round trip.
#
# Outputs a minimal claude-style JSON envelope on stdout.
set -e

# Drain stdin.
cat /dev/stdin > /dev/null

# Find --mcp-config <path> in argv.
config_path=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mcp-config)
      config_path="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

# Extract the validator --output path. The mcp-config json is shaped:
# {"mcpServers": {"kitsoki-validator": {"command": "...", "args": [..., "--output", "<path>", ...]}}}
output_path=""
if [[ -n "$config_path" && -f "$config_path" ]]; then
  # Use python for a robust JSON walk; available everywhere Go tests run.
  output_path=$(python3 -c '
import json, sys
with open(sys.argv[1]) as f:
    cfg = json.load(f)
servers = cfg.get("mcpServers", {})
for name, entry in servers.items():
    args = entry.get("args", [])
    for i, a in enumerate(args):
        if a == "--output" and i + 1 < len(args):
            print(args[i + 1])
            break
' "$config_path")
fi

# Write the canned validated payload to the capture file.
if [[ -n "$output_path" ]]; then
  printf '{"intent":"go","slots":{"direction":"south"},"confidence":0.9}' > "$output_path"
fi

# Emit a claude-style envelope so callers parsing stdout don't choke.
cat <<'ENVELOPE'
{"type":"result","subtype":"success","is_error":false,"result":"OK: payload validated and captured.","session_id":"fake-session","total_cost_usd":0}
ENVELOPE
