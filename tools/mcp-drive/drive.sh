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
#   MCP_DRIVE_TIMEOUT           hard wall-clock ceiling in seconds for ONE attempt
#                               (default: 2400 = 40m). A single legitimate
#                               full_pipeline drive polls session_status ~60s
#                               apart for many minutes — this is a backstop
#                               against a genuine hang (e.g. a silently-denied
#                               MCP tool call deadlocking the orchestrator), not
#                               a normal-case budget. On expiry the attempt is
#                               killed (SIGTERM, then SIGKILL after 30s) and
#                               reported distinctly from a real command failure
#                               so callers don't confuse "stalled" with "tried
#                               and failed".
#   MCP_DRIVE_TOOLS             override the allowlist (default: kitsoki studio MCP tools only)
#   MCP_DRIVE_MAX_ATTEMPTS      max attempts for retryable transient failures (default: 12)
#   MCP_DRIVE_BACKOFF_BASE      initial backoff seconds (default: 10)
#   MCP_DRIVE_BACKOFF_MAX       max backoff cap seconds (default: 600 = 10m)
#   MCP_DRIVE_RETRY_VERBOSE      print retry progress to stderr (default: 1)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
MCP_CONFIG="${MCP_DRIVE_MCP_CONFIG:-$HERE/kitsoki-mcp.json}"

MODEL="${MCP_DRIVE_MODEL:-sonnet}"
TIMEOUT_S="${MCP_DRIVE_TIMEOUT:-2400}"
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

# The Studio server discovers `.kitsoki.yaml` / `.kitsoki.local.yaml` from its
# process cwd. A paired-task runner (or any caller outside this checkout) must
# therefore not accidentally launch it from the task worktree or `/opt` and
# silently lose the selected harness profile.
cd "$REPO_ROOT"

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

# The kitsoki studio surface. The orchestrator drives, reads, gates, and
# integrates through MCP tools only; direct host Bash/Read/Grep/Glob are
# deliberately absent.
TOOLS="${MCP_DRIVE_TOOLS:-mcp__kitsoki__studio_ping,mcp__kitsoki__studio_handles,mcp__kitsoki__studio_work,mcp__kitsoki__story_read,mcp__kitsoki__story_write,mcp__kitsoki__story_validate,mcp__kitsoki__story_graph,mcp__kitsoki__story_test,mcp__kitsoki__story_list,mcp__kitsoki__story_search,mcp__kitsoki__story_turn,mcp__kitsoki__session_new,mcp__kitsoki__session_attach,mcp__kitsoki__session_drive,mcp__kitsoki__session_submit,mcp__kitsoki__session_continue,mcp__kitsoki__session_answer,mcp__kitsoki__session_status,mcp__kitsoki__session_world,mcp__kitsoki__session_inspect,mcp__kitsoki__session_trace,mcp__kitsoki__session_close,mcp__kitsoki__render_tui,mcp__kitsoki__render_tui_png,mcp__kitsoki__render_web,mcp__kitsoki__visual_open,mcp__kitsoki__visual_observe,mcp__kitsoki__visual_snapshot,mcp__kitsoki__visual_act,mcp__kitsoki__visual_diff,mcp__kitsoki__visual_git_diff,mcp__kitsoki__visual_record,mcp__kitsoki__host_run,mcp__kitsoki__trace_read,mcp__kitsoki__trace_to_flow,mcp__kitsoki__vcs_status,mcp__kitsoki__vcs_diff,mcp__kitsoki__vcs_log,mcp__kitsoki__vcs_commit,mcp__kitsoki__vcs_integrate,mcp__kitsoki__worktree_list,mcp__kitsoki__worktree_create,mcp__kitsoki__worktree_remove,mcp__kitsoki__gh_issues,mcp__kitsoki__gh_pr_view,mcp__kitsoki__gh_comment,mcp__kitsoki__issue_create}"

# Pure-bash wall-clock ceiling — no dependency on GNU coreutils `timeout`/
# `gtimeout` (absent by default on macOS, where this also runs). Backgrounds
# the command and polls `kill -0` once a second from the foreground (a single
# background job, no sibling watchdog subshell to race against — bash 3.2's
# `wait` on a killed subshell PID was observed to hang indefinitely on this
# box, so this deliberately avoids that shape). On expiry: SIGTERM, a short
# grace period, then SIGKILL, and exit 124 (the GNU `timeout` convention) so
# callers can tell "the process hung" apart from "the process ran and failed".
#
# Deliberately does NOT touch `set -e`/`set +e`: a function that flips
# errexit back on right before `return <nonzero>` aborts an unprotected
# caller (`f; rc=$?`) the instant it returns — verified directly, this is
# not a corner case. Callers must already run this with errexit off for the
# same reason `run_once` wraps its whole backend dispatch in `set +e` today.
run_with_soft_timeout() {
  local timeout_s="$1" out_file="$2" err_file="$3"; shift 3
  "$@" >"$out_file" 2>"$err_file" &
  local cmd_pid=$!
  local waited=0
  local kill_grace=30
  while kill -0 "$cmd_pid" 2>/dev/null; do
    if [[ $waited -ge $timeout_s ]]; then
      kill -TERM "$cmd_pid" 2>/dev/null
      local graced=0
      while [[ $graced -lt $kill_grace ]] && kill -0 "$cmd_pid" 2>/dev/null; do
        sleep 1
        graced=$((graced + 1))
      done
      kill -KILL "$cmd_pid" 2>/dev/null
      wait "$cmd_pid" 2>/dev/null
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$cmd_pid"
  return $?
}

is_retryable_error() {
  local payload="$1"
  local lower
  lower="$(printf '%s' "$payload" | tr '[:upper:]' '[:lower:]')"

  # Obvious client/config errors should fail fast. A watchdog kill also fails
  # fast: a genuine hang/deadlock (e.g. a silently-denied MCP tool call) is
  # deterministic and will very likely time out again on retry, so blindly
  # retrying just multiplies MCP_DRIVE_TIMEOUT by MAX_ATTEMPTS into a much
  # longer silent-looking wait — exactly the failure mode this timeout exists
  # to prevent.
  case "$lower" in
    *"usage:"*|*"unknown option"*|*"unknown arg"*|*"invalid or missing argument"*|\
    *"mcp-config not found"*|*"usage error"*|*"command not found"*|\
    *"watchdog"*)
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
    # acceptEdits equivalent for unattended MCP calls — the orchestrator only
    # clicks studio tools, shell_tool is disabled below, and app connectors are
    # disabled so this scoped MCP launch does not inherit unrelated capabilities.
    # --skip-git-repo-check: the cwd may be a worktree.
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
      codex_mcp_args+=(-c mcp_servers.kitsoki.enabled=true)
      codex_mcp_args+=(-c 'mcp_servers.kitsoki.args=["mcp","--stories-dir","stories"]')
      # A machine's own ~/.codex/config.toml may already declare mcp_servers.kitsoki
      # (pointing `cwd` at THAT machine's checkout, e.g. a developer's primary
      # checkout path). If CODEX_HOME happens to carry that file (bind-mounted
      # into a container, say), the pre-existing `cwd` silently wins over the -c
      # command/args overrides above, the kitsoki subprocess fails to start in a
      # directory that doesn't exist there, and codex reports zero MCP tools
      # with NO error surfaced — it just looks like the server was never
      # registered. Force `cwd` to THIS repo checkout so the override is total.
      codex_mcp_args+=(-c "mcp_servers.kitsoki.cwd=$REPO_ROOT")
      # codex's default per-tool-call timeout is far shorter than a real
      # session.drive full_pipeline turn (kitsoki dispatches its OWN live
      # agent calls inside that one MCP tool call — verified live: an
      # 11-minute drive got cut off mid-pipeline with the session left
      # "stuck" at bf.idle, no fix produced, no error). Both keys are accepted
      # by this codex version under --strict-config.
      codex_mcp_args+=(-c "mcp_servers.kitsoki.startup_timeout_sec=${MCP_DRIVE_MCP_STARTUP_TIMEOUT_SEC:-120}")
      codex_mcp_args+=(-c "mcp_servers.kitsoki.tool_timeout_sec=${MCP_DRIVE_MCP_TOOL_TIMEOUT_SEC:-1800}")
      local _fwd _fwd_list="${MCP_DRIVE_FORWARD_ENV:-}"
      for _fwd in ${_fwd_list//,/ }; do
        # An empty override is materially different from inheritance: in
        # particular `CODEX_HOME=` makes the nested live worker lose the
        # caller's subscription credentials. Forward only values that exist.
        if [[ -n "${!_fwd-}" ]]; then
          codex_mcp_args+=(-c "mcp_servers.kitsoki.env.${_fwd}=${!_fwd}")
        fi
      done
    fi
    # codex (>=0.142) DEFERS MCP server tools behind its `tool_search` tool —
    # studio.ping/session.new/etc. are NOT in the eager tool list, so without
    # this the orchestrator reports them "not available" and bounces instead of
    # driving the pipeline (see MEMORY codex-defers-mcp-tools-toolsearch; the
    # same fix already ships inside kitsoki's own codex agent backend at
    # internal/host/agent_backend_codex.go's codexMCPToolSearchPreamble — this
    # is the external orchestrator invocation, so it needs its own copy). Tool
    # names here MUST be the server's raw dotted names (session.new, not
    # session_new) — codex's tool_search indexes the MCP server's own name, not
    # the mcp__kitsoki__session_new alias the CLAUDE backend's client presents.
    local codex_tool_search_preamble="TOOL ACCESS (codex): Some tools provided to you — including the kitsoki studio tools (studio.ping, session.new, session.drive, session.status, etc. — note the DOTTED names) — are NOT listed in your default tool set. They are reachable only via the \`tool_search\` tool. BEFORE you conclude that a tool the task asks for is unavailable, you MUST call \`tool_search\` to locate it, then call it. Never emulate such a tool by printing its name or arguments as text — a printed call does nothing."
    run_with_soft_timeout "$TIMEOUT_S" "$out_file" "$err_file" \
      codex exec "${codex_tool_search_preamble}"$'\n\n---\n\n'"$PROMPT" \
      --model "$MODEL" \
      --dangerously-bypass-approvals-and-sandbox \
      --ignore-user-config \
      --ephemeral \
      --disable=shell_tool \
      --disable=apps \
      --skip-git-repo-check \
      "${codex_mcp_args[@]}" \
      --json
    rc=$?
  else
    run_with_soft_timeout "$TIMEOUT_S" "$out_file" "$err_file" \
      claude -p "$PROMPT" \
      --mcp-config "$MCP_CONFIG" \
      --strict-mcp-config \
      --model "$MODEL" \
      --permission-mode acceptEdits \
      --allowedTools "$TOOLS" \
      --output-format json
    rc=$?
  fi

  if [[ $rc -eq 124 ]]; then
    printf 'drive.sh: WATCHDOG — backend=%s model=%s exceeded MCP_DRIVE_TIMEOUT=%ss with no result; killed. This is a hang/stall, not a model failure.\n' "$BACKEND" "$MODEL" "$TIMEOUT_S" >>"$err_file"
  elif [[ $rc -ne 0 && ! -s "$out_file" && ! -s "$err_file" ]]; then
    printf 'drive.sh: backend=%s model=%s cmd failed with empty output (exit=%s)\n' "$BACKEND" "$MODEL" "$rc" >>"$err_file"
  fi
  set -e
  return "$rc"
}

attempt=1
wait_seconds="$BACKOFF_BASE"

while true; do
  out_file="$(mktemp)"
  err_file="$(mktemp)"

  # Pre-existing bug, fixed here: `run_once` restores `set -e` right before
  # its own `return "$rc"`. Calling it as a bare statement under this
  # script's top-level `set -euo pipefail` meant a nonzero return (ANY real
  # backend failure) aborted the whole script immediately via errexit —
  # before the retry/backoff logic below ever ran. Wrapping the call in an
  # `if` makes it a protected context, so errexit does not fire on it.
  if run_once "$out_file" "$err_file"; then
    rc=0
  else
    rc=$?
  fi

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
