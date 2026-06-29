#!/usr/bin/env bash
# drive.sh — the canonical HEADLESS kitsoki-MCP delegation primitive.
#
# THE PATTERN (and the bug it fixes). Dispatching a kitsoki-driving agent through
# the in-process subagent / Agent-tool path does NOT attach the kitsoki MCP. An
# in-process subagent inherits the PARENT session's MCP set, and a parent
# started without the kitsoki server has none to pass on, so the agent boots with
# "No MCP servers configured" and can call nothing (`session.new`,
# `story.read`, ... all absent). The fix is to delegate to a RAW headless CLI with
# the studio MCP attached fresh, so a stray worktree/project `.mcp.json` can't
# shadow or drop it (see MEMORY: maker-submit-strict-mcp).
#
# This script is that primitive. It launches one headless orchestrator CLI with
# the kitsoki studio MCP attached and the full studio toolset allowlisted, runs
# the given prompt to completion, and prints the CLI's result on stdout.
#
# ORCHESTRATOR BACKEND. Two backends are supported (MCP_DRIVE_BACKEND, or
# auto-detected from the model name):
#   - claude: `claude -p --mcp-config … --strict-mcp-config` (Anthropic models).
#   - codex:  `codex exec … -c mcp_servers.kitsoki.*` on ChatGPT subscription auth
#     (gpt-*/codex*/o[34]* models). The bake-off default — drive with GPT-5.5,
#     work inside the session with GLM-5.2/GPT-5.5.
# Only the EXIT CODE matters to callers (drive_cell.sh checks rc + scans text for
# retryable errors); the on-stdout envelope shape is backend-specific.
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
#   MCP_DRIVE_BACKEND           claude | codex (default: auto-detect from model)
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
command -v kitsoki >/dev/null || { echo "drive.sh: kitsoki not on PATH (run make install)" >&2; exit 2; }

# Resolve the orchestrator backend. Explicit MCP_DRIVE_BACKEND wins; otherwise
# infer from the model name (gpt-*/codex*/o3*/o4* → codex, else claude).
BACKEND="${MCP_DRIVE_BACKEND:-}"
if [[ -z "$BACKEND" ]]; then
  case "$MODEL" in
    gpt-*|codex*|o3*|o4*) BACKEND="codex" ;;
    *)                    BACKEND="claude" ;;
  esac
fi

case "$BACKEND" in
  claude)
    [[ -f "$MCP_CONFIG" ]] || { echo "drive.sh: mcp-config not found: $MCP_CONFIG" >&2; exit 2; }
    command -v claude >/dev/null || { echo "drive.sh: claude CLI not on PATH (orchestrator backend=claude)" >&2; exit 2; }
    ;;
  codex)
    command -v codex >/dev/null || { echo "drive.sh: codex CLI not on PATH (orchestrator backend=codex)" >&2; exit 2; }
    ;;
  *)
    echo "drive.sh: unknown MCP_DRIVE_BACKEND '$BACKEND' (want claude|codex)" >&2; exit 2 ;;
esac

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
  local rc

  set +e
  if [[ "$BACKEND" == "codex" ]]; then
    # Headless codex on ChatGPT subscription auth. The studio MCP is attached via
    # -c overrides (codex has no --mcp-config flag); --dangerously-bypass… is the
    # acceptEdits equivalent for unattended runs — the orchestrator only clicks
    # studio tools, and the kitsoki MCP it spawns forks the worker harness, so the
    # process needs full access. --skip-git-repo-check: the cwd may be a worktree.
    #
    # Env propagation: codex does NOT forward the parent environment to MCP
    # subprocesses, so a worker that needs env (IS_SANDBOX so claude accepts
    # --dangerously-skip-permissions as root; a provider API key the harness
    # profile interpolates) sees none of it and fast-fails. Two ways to supply it:
    #   MCP_DRIVE_CODEX_CONFIG_MCP=1 — the kitsoki server (command/args/env) is
    #     fully defined in ~/.codex/config.toml; emit NO -c mcp overrides so codex
    #     uses that definition (keeps secrets off argv). This is the VM default.
    #   else — pass command/args via -c, and forward each name in
    #     MCP_DRIVE_FORWARD_ENV (space/comma-separated) as -c …env.<NAME>=<value>
    #     (values come from this process's env; secrets land on argv — fine for a
    #     disposable single-tenant box, avoid otherwise).
    local codex_mcp_args=()
    if [[ "${MCP_DRIVE_CODEX_CONFIG_MCP:-0}" != "1" ]]; then
      codex_mcp_args+=(-c mcp_servers.kitsoki.command=kitsoki)
      codex_mcp_args+=(-c 'mcp_servers.kitsoki.args=["mcp","--stories-dir","stories"]')
      local _fwd
      for _fwd in ${MCP_DRIVE_FORWARD_ENV//,/ }; do
        codex_mcp_args+=(-c "mcp_servers.kitsoki.env.${_fwd}=${!_fwd-}")
      done
    fi
    codex exec "$PROMPT" \
      --model "$MODEL" \
      --dangerously-bypass-approvals-and-sandbox \
      --skip-git-repo-check \
      "${codex_mcp_args[@]}" \
      --json \
      >"$out_file" 2>"$err_file"
    rc=$?
  else
    claude -p "$PROMPT" \
      --mcp-config "$MCP_CONFIG" \
      --strict-mcp-config \
      --model "$MODEL" \
      --permission-mode acceptEdits \
      --allowedTools "$TOOLS" \
      --output-format json \
      >"$out_file" 2>"$err_file"
    rc=$?
  fi
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
