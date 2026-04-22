#!/usr/bin/env bash
# fake-claude.sh — echoes a canned JSON envelope for ClaudeCLIHarness tests.
# Used in TestClaudeCLIHarness_ExecPlumbing only.
# Reads stdin (the prompt) and discards it; outputs a fixed envelope.
cat /dev/stdin > /dev/null
# Use a heredoc to avoid shell escaping issues with nested JSON.
cat <<'ENVELOPE'
{"type":"result","subtype":"success","is_error":false,"result":"{\"intent\":\"go\",\"slots\":{\"direction\":\"south\"}}","session_id":"fake-session","total_cost_usd":0}
ENVELOPE
