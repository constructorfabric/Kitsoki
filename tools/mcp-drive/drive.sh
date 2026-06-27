#!/usr/bin/env bash
# drive.sh — the canonical HEADLESS kitsoki-MCP delegation primitive.
#
# THE PATTERN (and the bug it fixes). Dispatching a kitsoki-driving agent through
# the in-process subagent / Agent-tool path does NOT attach the kitsoki MCP. An
# in-process subagent inherits the PARENT session's MCP set, and a parent
# started without the kitsoki server has none to pass on, so the agent boots with
# "No MCP servers configured" and can call nothing (`session.new`,
# `story.read`, ... all absent). The fix is to delegate to a RAW `claude -p` with
# an explicit `--mcp-config` so the studio server is attached fresh, and
# `--strict-mcp-config` so a stray worktree/project `.mcp.json` can't shadow or
# drop it (see MEMORY: maker-submit-strict-mcp).
#
# This script is that primitive. It launches one headless `claude -p` with the
# kitsoki studio MCP attached and the full studio toolset allowlisted, runs the
# given prompt to completion, and prints the JSON result envelope on stdout.
#
#   tools/mcp-drive/drive.sh "<prompt>"                 # inline prompt
#   tools/mcp-drive/drive.sh --prompt-file <path>         # prompt from a file
#   tools/mcp-drive/drive.sh --model sonnet "<prompt>"    # pin orchestrator model
#
# The ORCHESTRATOR model (this claude -p) only *drives* the studio — it clicks
# session.new / session.drive / session.submit. The model that actually does the
# work runs inside the kitsoki session and is chosen by
# `session.new {profile, harness: "live"}` (e.g. profile=codex-native ->
# GPT-5.5, profile=synthetic-claude -> GLM-5.2). So "drive with Claude, fix
# with GPT/GLM" is the intended split.
#
# Env:
#   MCP_DRIVE_MODEL             orchestrator model (default: sonnet)
#   MCP_DRIVE_TIMEOUT           not enforced here; wrap with your own timeout if needed
#   MCP_DRIVE_TOOLS             override the allowlist (default: all kitsoki studio tools)
#   MCP_DRIVE_MAX_ATTEMPTS      max attempts for retryable transient failures (default: 12)
#   MCP_DRIVE_BACKOFF_BASE      initial backoff seconds (default: 10)
#   MCP_DRIVE_BACKOFF_MAX       max backoff cap seconds (default: 600 = 10m)
#   MCP_DRIVE_RETRY_VERBOSE      print retry progress to stderr (default: 1)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MCP_CONFIG="$HERE/kitsoki-mcp.json"

MODEL="${MCP_DRIVE_MODEL:-sonnet}"
MAX_ATTEMPTS="${MCP_DRIVE_MAX_ATTEMPTS:-12}"
BACKOFF_BASE="${MCP_DRIVE_BACKOFF_BASE:-10}"
BACKOFF_MAX="${MCP_DRIVE_BACKOFF_MAX:-600}"
RETRY_VERBOSE="${MCP_DRIVE_RETRY_VERBOSE:-1}"

PROMPT=""
PROMPT_FILE=""
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

# The full kitsoki studio surface. The orchestrator needs session.* driving
# tools + story.*/render.* introspection; Bash/Read let it stage tickets + verify
# the worktree between turns.
TOOLS="${MCP_DRIVE_TOOLS:-mcp__kitsoki__studio_ping,mcp__kitsoki__story_read,mcp__kitsoki__story_graph,mcp__kitsoki__story_validate,mcp__kitsoki__session_new,mcp__kitsoki__session_attach,mcp__kitsoki__session_drive,mcp__kitsoki__session_submit,mcp__kitsoki__session_continue,mcp__kitsoki__session_answer,mcp__kitsoki__session_inspect,mcp__kitsoki__session_trace,mcp__kitsoki__session_close,mcp__kitsoki__render_tui,Bash,Read,Glob,Grep}"

is_retryable_error() {
  local payload="$1"
  local lower
  lower="$(printf '%s' "$payload" | tr '[:upper:]' '[:lower:]')"

  # Obvious client/config errors should fail fast.
  case "$lower" in
    *"usage:"*|*"unknown option"*|*"unknown arg"*|*"invalid or missing argument"*|\
    *"mcp-config not found"*|*"usage error"*|*"command not found"*)
      return 1
      ;;
  esac

  case "$lower" in
    *"429"*|*"too many requests"*|*"rate limit"*|*"rate-limited"*|*"quota"*|*"quota exceeded"*|\
    *"temporarily unavailable"*|*"service unavailable"*|*"gateway timeout"*|*"bad gateway"*|\
    *"connection reset"*|*"connection timed out"*|*"network error"*|*"econn"*|*"retry-after"*)
      return 0
      ;;
  esac

  # Default to retry for non-obvious failures to be resilient to new provider
  # strings. This keeps quota/retry loops robust when wording changes.
  return 0
}

run_once() {
  local out_file="$1"
  local err_file="$2"

  set +e
  claude -p "$PROMPT" \
    --mcp-config "$MCP_CONFIG" \
    --strict-mcp-config \
    --model "$MODEL" \
    --permission-mode acceptEdits \
    --allowedTools "$TOOLS" \
    --output-format json \
    >"$out_file" 2>"$err_file"
  local rc=$?
  set -e
  return "$rc"
}

attempt=1
wait_seconds="$BACKOFF_BASE"

while true; do
  out_file="$(mktemp)"
  err_file="$(mktemp)"

  run_once "$out_file" "$err_file"
  rc=$?

  if [[ $rc -eq 0 ]]; then
    cat "$out_file"
    rm -f "$out_file" "$err_file"
    exit 0
  fi

  payload="$(cat "$out_file" "$err_file")"
  rm -f "$out_file" "$err_file"

  if is_retryable_error "$payload"; then
    if [[ "$attempt" -lt "$MAX_ATTEMPTS" ]]; then
      if [[ "$RETRY_VERBOSE" == "1" ]]; then
        echo "[drive.sh] attempt ${attempt} failed (exit ${rc}); retrying in ${wait_seconds}s" >&2
      fi
      sleep "$wait_seconds"
      attempt=$((attempt + 1))
      wait_seconds=$((wait_seconds * 2))
      if [[ "$wait_seconds" -gt "$BACKOFF_MAX" ]]; then
        wait_seconds="$BACKOFF_MAX"
      fi
      continue
    fi

    echo "[drive.sh] reached max attempts (${MAX_ATTEMPTS}) for retryable failure" >&2
    if [[ -n "$payload" ]]; then
      echo "$payload" >&2
    fi
    exit "$rc"
  fi

  if [[ -n "$payload" ]]; then
    echo "$payload" >&2
  fi
  echo "[drive.sh] non-retryable failure; stop immediately" >&2
  exit "$rc"
done
