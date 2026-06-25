#!/usr/bin/env bash
# drive.sh — the canonical HEADLESS kitsoki-MCP delegation primitive.
#
# THE PATTERN (and the bug it fixes). Dispatching a kitsoki-driving agent through
# the in-process subagent / Agent-tool path does NOT attach the kitsoki studio
# MCP: an in-process subagent inherits the PARENT session's MCP set, and a parent
# that was started without the kitsoki server has none to pass on — the agent
# boots with "No MCP servers configured" and can call nothing. The fix is to
# delegate to a RAW `claude -p` with an explicit `--mcp-config` so the studio
# server is attached fresh, and `--strict-mcp-config` so a stray worktree/project
# `.mcp.json` can't shadow or drop it (see MEMORY: maker-submit-strict-mcp).
#
# This script is that primitive. It launches one headless `claude -p` with the
# kitsoki studio MCP attached and the full studio toolset allowlisted, runs the
# given prompt to completion, and prints the JSON result envelope on stdout.
#
#   tools/mcp-drive/drive.sh "<prompt>"                 # inline prompt
#   tools/mcp-drive/drive.sh --prompt-file <path>       # prompt from a file
#   tools/mcp-drive/drive.sh --model sonnet "<prompt>"  # pin the ORCHESTRATOR model
#
# The ORCHESTRATOR model (this claude -p) only *drives* the studio — it clicks
# session.new / session.drive / etc. The model that actually does the work runs
# INSIDE the kitsoki session and is chosen by `session.new {profile, harness:
# "live"}` (e.g. profile=codex-native → GPT-5.5, profile=synthetic-claude →
# GLM-5.2). So "drive with Claude, fix with GPT/GLM" is expected and correct.
#
# Env:
#   MCP_DRIVE_MODEL     orchestrator model (default: sonnet — cheap; it only clicks)
#   MCP_DRIVE_TIMEOUT   not enforced here; wrap with your own timeout if needed
#   MCP_DRIVE_TOOLS     override the allowlist (default: all kitsoki studio tools)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MCP_CONFIG="$HERE/kitsoki-mcp.json"

MODEL="${MCP_DRIVE_MODEL:-sonnet}"
PROMPT=""; PROMPT_FILE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --model) MODEL="$2"; shift 2;;
    --prompt-file) PROMPT_FILE="$2"; shift 2;;
    --mcp-config) MCP_CONFIG="$2"; shift 2;;
    *) PROMPT="$1"; shift;;
  esac
done
[[ -n "$PROMPT_FILE" && -f "$PROMPT_FILE" ]] && PROMPT="$(cat "$PROMPT_FILE")"
[[ -n "$PROMPT" ]] || { echo "usage: drive.sh [--model M] (\"<prompt>\" | --prompt-file <path>)" >&2; exit 2; }
[[ -f "$MCP_CONFIG" ]] || { echo "drive.sh: mcp-config not found: $MCP_CONFIG" >&2; exit 2; }
command -v kitsoki >/dev/null || { echo "drive.sh: kitsoki not on PATH (run make install)" >&2; exit 2; }

# The full kitsoki studio surface. The orchestrator needs the session.* driving
# tools + story.*/render.* introspection; Bash/Read let it stage tickets + verify
# the worktree between turns.
TOOLS="${MCP_DRIVE_TOOLS:-mcp__kitsoki__studio_ping,mcp__kitsoki__studio_handles,mcp__kitsoki__story_read,mcp__kitsoki__story_graph,mcp__kitsoki__story_validate,mcp__kitsoki__session_new,mcp__kitsoki__session_attach,mcp__kitsoki__session_drive,mcp__kitsoki__session_submit,mcp__kitsoki__session_continue,mcp__kitsoki__session_answer,mcp__kitsoki__session_inspect,mcp__kitsoki__session_trace,mcp__kitsoki__session_close,mcp__kitsoki__render_tui,Bash,Read,Glob,Grep}"

exec claude -p "$PROMPT" \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  --model "$MODEL" \
  --permission-mode acceptEdits \
  --allowedTools "$TOOLS" \
  --output-format json
