#!/usr/bin/env bash
# drive_cell.sh — run ONE external bake-off cell end-to-end from the manifest.
#
# Codifies the live-drive recipe (worktree prep + every load-bearing initial_world
# knob + the headless MCP delegation) so a cell is one command instead of a
# hand-assembled prompt. COST-BEARING (real LLM) — operator-run, never in CI.
#
#   drive_cell.sh --project <name> --bug <id> --candidate <key> [--score] [--no-drive] [--no-docker-score]
#                 [--repo-dir <local-checkout>] [--completion-state <path>]
#
#   --score      after the drive, grade the worktree with bench.py + extract cost
#   --no-drive   only prepare the worktree + print the prompt (free; for inspection)
#   --no-docker-score  force host-based scoring (docker default)
#
# Reads projects/<name>/manifest.yaml (per-bug facts via `bench.py meta --bug`) and
# candidates.yaml (the model/profile axis). Clones the repo ONCE into a cache and
# reuses node_modules across cells. The worker model is whatever the candidate's
# profile selects (codex-native → GPT-5.5, synthetic-claude → GLM-5.2); the
# orchestrator (default GPT-5.5) only clicks the pipeline forward.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE" && git rev-parse --show-toplevel)"
CACHE="${EXTERNAL_BAKEOFF_CACHE:-$REPO_ROOT/.artifacts/external-bakeoff}"      # gitignored work area
MAX_ATTEMPTS="${MCP_DRIVE_MAX_ATTEMPTS:-12}"
BACKOFF_BASE="${MCP_DRIVE_BACKOFF_BASE:-10}"
BACKOFF_MAX="${MCP_DRIVE_BACKOFF_MAX:-600}"

project=""; bug=""; cand=""; repo_dir=""; completion_state=""; do_score=0; no_drive=0; orch="${MCP_DRIVE_MODEL:-gpt-5.5}"; use_docker_score="${EXTERNAL_BAKEOFF_USE_DOCKER_SCORE:-1}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --bug) bug="$2"; shift 2;;
    --candidate) cand="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --completion-state) completion_state="$2"; shift 2;;
    --orchestrator) orch="$2"; shift 2;;
    --no-docker-score) use_docker_score=0; shift;;
    --score) do_score=1; shift;;
    --no-drive) no_drive=1; shift;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[[ -n "$project" && -n "$bug" && -n "$cand" ]] || {
  echo "usage: drive_cell.sh --project <name> --bug <id> --candidate <key> [--repo-dir <local-checkout>] [--score] [--no-drive] [--no-docker-score] [--completion-state <path>]" >&2; exit 2; }

is_retryable_error() {
  local payload="$1"
  local lower
  lower="$(printf '%s' "$payload" | tr '[:upper:]' '[:lower:]')"

  case "$lower" in
    *"usage:"*|*"unknown option"*|*"unknown arg"*|*"invalid or missing argument"*|\
    *"unknown candidate"*|*"unknown project"*|*"no such project"*|\
    *"candidate not found"*|*"not configured"*|*"baseline is not a commit"*|\
    *"not a git repository"*|*"no such file"*|*"command not found"*|\
    *"permission denied"*)
      return 1
      ;;
  esac
  return 0
}

run_with_retry() {
  local desc="$1"; local stdout_file="$2"; local stderr_file="$3"; shift 3
  local attempt=1
  local wait_seconds="$BACKOFF_BASE"
  while true; do
    set +e
    "$@" >"$stdout_file" 2>"$stderr_file"
    local rc=$?
    set -e
    local payload
    payload="$(cat "$stdout_file" "$stderr_file" 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g')"
    if [[ $rc -eq 0 ]]; then
      return 0
    fi
    if is_retryable_error "$payload" && [[ $attempt -lt "$MAX_ATTEMPTS" ]]; then
      echo "[cell] ${desc} failed (attempt ${attempt}) — retrying in ${wait_seconds}s" >&2
      sleep "$wait_seconds"
      attempt=$((attempt + 1))
      wait_seconds=$((wait_seconds * 2))
      if [[ $wait_seconds -gt $BACKOFF_MAX ]]; then
        wait_seconds=$BACKOFF_MAX
      fi
      continue
    fi
    echo "[cell] ${desc} failed" >&2
    return "$rc"
  done
}

drive_with_retry() {
  local log_file="$1"; local err_file="$2"
  local attempt=1
  local wait_seconds="$BACKOFF_BASE"
  local drive_path="/root/go/bin:$PATH"
  while true; do
    set +e
    MCP_DRIVE_MODEL="$orch" PATH="$drive_path" "$REPO_ROOT/tools/mcp-drive/drive.sh" --prompt-file "$pf" >"$log_file" 2>"$err_file"
    local rc=$?
    set -e
    local payload
    payload="$(cat "$log_file" "$err_file" 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g')"
    if [[ $rc -eq 0 ]]; then
      return 0
    fi
    if is_retryable_error "$payload" && [[ $attempt -lt "$MAX_ATTEMPTS" ]]; then
      echo "[cell] drive failed (attempt ${attempt}) — retrying in ${wait_seconds}s" >&2
      sleep "$wait_seconds"
      attempt=$((attempt + 1))
      wait_seconds=$((wait_seconds * 2))
      if [[ $wait_seconds -gt $BACKOFF_MAX ]]; then
        wait_seconds=$BACKOFF_MAX
      fi
      continue
    fi
    return "$rc"
  done
}

# --- read manifest (per-bug) + candidate --------------------------------------
meta="$(cd "$HERE" && python3 bench.py meta --project "$project" --bug "$bug")"
jget() { python3 -c 'import json,sys; print(json.load(sys.stdin).get(sys.argv[1],""))' "$1" <<<"$meta"; }
repo="$(jget repo)"; install="$(jget install)"; test_cmd="$(jget test_cmd)"
baseline="$(jget baseline_sha)"; title="$(jget title)"; ticket="$(jget ticket)"

cand_field() { python3 -c '
import sys,yaml
d=yaml.safe_load(open(sys.argv[1]))
for c in d["candidates"]:
    if c["key"]==sys.argv[2]: print(c.get(sys.argv[3],"")); break
' "$HERE/candidates.yaml" "$cand" "$1"; }
profile="$(cand_field profile)"; short="$(cand_field short)"
[[ -n "$profile" ]] || { echo "unknown candidate '$cand' in candidates.yaml" >&2; exit 2; }
# Key every per-cell path/identifier by project too, so two projects that reuse a
# bug id (e.g. both define "bug9") can't collide on the same worktree/trace/branch.
cellkey="$project-$bug-$cand"

# --- fail fast (before any clone/spend) ---------------------------------------
# local_only projects drive against an explicit local checkout. kitsoki-self can
# default to this repository; external/private repos must use --repo-dir or one
# of the manifest's generic repo_envs.
local_only=""
[[ "$(jget local_only)" == "True" || "$(jget local_only)" == "true" ]] && local_only=1

# rich ticket_title: pack the full bug description (the reproducer is fed only
# ticket_id + ticket_title; no ticket file). One line, quotes stripped.
desc="$(printf '%s — %s' "$title" "$(printf '%s' "$ticket" | tr '\n' ' ' | sed 's/  */ /g')" | sed 's/"/\\"/g')"

# --- source repo: clone (remote) or the local checkout (local_only) -----------
if [[ -n "$local_only" ]]; then
  repo_path_args=(repo-path --project "$project")
  [[ -n "$repo_dir" ]] && repo_path_args+=(--repo-dir "$repo_dir")
  repo_path_json="$CACHE/preflight/$cellkey-repo-path.json"; repo_path_err="$repo_path_json.err"; mkdir -p "$(dirname "$repo_path_json")"
  if ! python3 "$HERE/bench.py" "${repo_path_args[@]}" >"$repo_path_json" 2>"$repo_path_err"; then
    echo "[cell] project '$project' is local_only; pass --repo-dir <checkout-or-meta-root> or set one of the manifest repo_envs." >&2
    cat "$repo_path_json" >&2 2>/dev/null || true
    cat "$repo_path_err" >&2 2>/dev/null || true
    exit 2
  fi
  src="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("path",""))' "$repo_path_json")"
  # Drive against a worktree of the target checkout; the baseline must be a real
  # commit there. No clone, no install — language toolchains are cached locally;
  # a nested package install can still run at score time via oracle.setup.
  git -C "$src" cat-file -e "${baseline}^{commit}" 2>/dev/null || {
    echo "[cell] baseline '$baseline' is not a commit in the local repo ($src)." >&2; exit 2; }
else
  src="$CACHE/clone/$project"
  if [[ ! -d "$src/.git" ]]; then
    echo "[cell] cloning $repo -> $src" >&2
    mkdir -p "$(dirname "$src")"
    git clone -q "$repo" "$src"
  fi
  git -C "$src" cat-file -e "$baseline" 2>/dev/null || git -C "$src" fetch -q origin "$baseline"
  if [[ ! -d "$src/node_modules" ]]; then
    echo "[cell] $install (once) in $src" >&2
    ( cd "$src" && git checkout -q "$baseline" && eval "$install" )
  fi
fi

# Keep drive_cell and the repo-bakeoff story on the same readiness contract. For
# `--no-drive`, report preflight failures but still prepare the inspection prompt;
# real drives fail here before a worktree or MCP session is created.
preflight_args=(preflight --project "$project" --bug "$bug" --candidate "$cand")
[[ -n "${repo_dir:-}" ]] && preflight_args+=(--repo-dir "$repo_dir")
if [[ -n "$local_only" && -z "${repo_dir:-}" ]]; then
  preflight_args+=(--repo-dir "$src")
fi
preflight_json="$CACHE/preflight/$cellkey.json"; preflight_err="$preflight_json.err"; mkdir -p "$(dirname "$preflight_json")"
if ! run_with_retry "preflight --project $project --bug $bug --candidate $cand" "$preflight_json" "$preflight_err" python3 "$HERE/bench.py" "${preflight_args[@]}"; then
  if [[ "$no_drive" == 1 ]]; then
    echo "[cell] warning: preflight failed; --no-drive will still prepare the prompt. See $preflight_json" >&2
  else
    echo "[cell] preflight failed; see $preflight_json" >&2
    exit 2
  fi
fi

# --- per-cell worktree at baseline on its own branch --------------------------
cell="$CACHE/cells/$cellkey"
branch_suffix="$(python3 -c 'import hashlib,sys; print(hashlib.sha1(sys.argv[1].encode()).hexdigest()[:8])' "$cell")"
branch="bench-$project-$bug-$short-$branch_suffix"
git -C "$src" worktree prune
if [[ -d "$cell" ]]; then
  git -C "$cell" reset --hard -q "$baseline"; git -C "$cell" clean -fdq
else
  git -C "$src" worktree add -q --detach "$cell" "$baseline"
fi
git -C "$cell" checkout -q -B "$branch"
# Link a prebuilt node_modules only for cloned JS repos; local_only repos have
# their toolchain in place (and a root node_modules symlink would be wrong here).
[[ -z "$local_only" && -d "$src/node_modules" ]] && ln -sfn "$src/node_modules" "$cell/node_modules"
if [[ "${BAKEOFF_CLAUDE_ALLOW_VALIDATOR:-0}" == "1" ]]; then
  mkdir -p "$cell/.claude"
  cat >"$cell/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "allow": [
      "mcp__kitsoki__session_status",
      "mcp__kitsoki__session_world",
      "mcp__kitsoki__session_inspect",
      "mcp__kitsoki__session_trace",
      "mcp__validator__submit",
      "mcp__operator__ask"
    ]
  }
}
JSON
fi

trace="$CACHE/traces/$cellkey.jsonl"; rm -f "$trace"
thread_file="$CACHE/threads/$cellkey.md"
mkdir -p "$CACHE/traces" "$CACHE/drive-logs" "$CACHE/results/cells" "$CACHE/threads" "$CACHE/score-logs"

# --- the orchestrator prompt (all tuning knobs baked in) ----------------------
prompt="$(cat <<EOF
Drive ONE kitsoki bug-fix pipeline cell to completion via the kitsoki studio MCP.
The fix MUST be generated by the live worker model inside the session (profile
**$profile** = $cand); you (orchestrator) only click studio tools — do NOT edit source.

1. studio_ping.
2. session_new EXACTLY:
   - story_path: "$REPO_ROOT/stories/bench-bugfix/app.yaml"
   - harness: "live"
   - profile: "$profile"
   - trace: "$trace"
   - initial_world:
       ticket_id: "$bug"
       thread: "$thread_file"
       ticket_title: "$desc"
       workdir: "$cell"
       workspace_id: ""
       feature_branch: "$branch"
       base_branch: "$branch"
       bugfix_mode: "full"
       judge_mode: "llm"
       test_cmd: "$test_cmd"
       bf_autostart_attempted: true
       escalate_low_value: true
   (workspace_id EMPTY ⇒ implementer edits the prepared workdir directly + commits.)
3. Drive **full_pipeline** ONCE, then only advance explicit gates (accept/continue/
   confirm/proceed) and answer ask-gates affirmatively ("looks correct, proceed").
   Do NOT re-drive start — the LLM judge auto-emits accept/refine. Give each
   on_enter step time ($cand does the real work there).
4. IMPORTANT — async turns are not stuck: session_submit returns a \`running\`
   handle and the story executes INSIDE that one turn for many minutes. While
   session_status shows a \`running\` field, the reported state stays at the
   resting state (e.g. bf.idle) the whole time — that is NORMAL and is NOT a
   stuck state. Keep polling (~60s apart, patiently, dozens of polls if needed)
   until \`running\` clears or the state advances. Never stop, re-drive, or
   close the session while \`running\` is present.
5. STOP at a terminal state, ~25 forward turns, or a repeated stuck state
   (same resting state, NO \`running\` field, across several polls). If a
   host_error bounces you to idle, read world.last_error, report it verbatim, STOP.
6. Then inspect the session status/world/trace through MCP. Do not use shell,
   filesystem, git, GitHub, or non-kitsoki tools during the delegated drive.
Report: final state; trace path; source modified (y/n) + fix SHA; 1-line fix; reproduction bug_verified (t/f); forward turns; last_error if any.
EOF
)"
pf="$CACHE/drive-prompts/$cellkey.md"; mkdir -p "$(dirname "$pf")"; printf '%s\n' "$prompt" > "$pf"
prep="$CACHE/prepared/$cellkey.json"; mkdir -p "$(dirname "$prep")"
python3 - "$prep" "$project" "$bug" "$cand" "$profile" "$src" "$cell" "$branch" "$baseline" "$trace" "$thread_file" "$pf" "$preflight_json" "$CACHE/results/cells/$cellkey-kitsoki.json" <<'PY'
import json
import sys
from pathlib import Path

keys = [
    "project", "bug", "candidate", "profile", "repo_dir", "worktree",
    "branch", "baseline_sha", "trace", "thread", "prompt", "preflight",
    "score_result",
]
Path(sys.argv[1]).write_text(json.dumps(dict(zip(keys, sys.argv[2:])), indent=2) + "\n")
PY
echo "[cell] project=$project bug=$bug candidate=$cand profile=$profile" >&2
echo "[cell] worktree=$cell branch=$branch trace=$trace" >&2

if [[ "$no_drive" == 1 ]]; then echo "[cell] --no-drive: prompt at $pf"; echo "[cell] prepared metadata at $prep"; exit 0; fi

# --- drive (COST) -------------------------------------------------------------
log="$CACHE/drive-logs/$cellkey.json"
err="${log%.json}.err"
if [[ "${EXTERNAL_BAKEOFF_FAKE_DRIVE_SUCCESS:-0}" == 1 ]]; then
  echo "[cell] fake drive enabled by EXTERNAL_BAKEOFF_FAKE_DRIVE_SUCCESS=1" >&2
  printf '{"status":"fake-drive-success"}\n' >"$log"
  : >"$err"
  drive_exit=0
else
  echo "[cell] driving (orchestrator=$orch, worker=$cand)…" >&2
  if drive_with_retry "$log" "$err"; then
    echo "[cell] drive done -> $log" >&2
    drive_exit=0
  else
    drive_exit=$?
    echo "[cell] drive failed with exit $drive_exit -> $err" >&2
  fi
fi

# Cell-health classification from the trace: separate an INFRA failure (worker
# never ran, silent stall, host/env error) from a real MODEL result the oracle
# should judge. A bare `failed` verdict lies — it looks identical whether the
# worker never got a turn or genuinely produced a wrong fix. Print the label so
# a sweep is readable and so infra regressions are caught loudly.
health_json="$(python3 "$HERE/bench.py" classify --trace "$trace" 2>/dev/null || echo '{}')"
health_class="$(printf '%s' "$health_json" | python3 -c 'import sys,json;
try: print(json.load(sys.stdin).get("class",""))
except Exception: print("")' 2>/dev/null)"
echo "[cell] health: ${health_class:-unknown} -- $(printf '%s' "$health_json" | python3 -c 'import sys,json;
try: print(json.load(sys.stdin).get("reason",""))
except Exception: print("")' 2>/dev/null)" >&2
case "$health_class" in
  infra:*) echo "[cell] WARNING: this cell is an INFRASTRUCTURE failure, not a model miss — do not score it against the model." >&2 ;;
esac

if [[ "$do_score" == 1 ]]; then
  out="$CACHE/results/cells/$cellkey-kitsoki.json"
  if [[ "$drive_exit" != 0 ]]; then
    reason="driver transient failure after retries"
    if [[ -s "$err" ]]; then
      reason="$(tail -n 30 "$err" | tr '\n' ' ' | sed 's/  */ /g')"
    fi
    python3 "$HERE/bench.py" pending \
      --project "$project" --bug "$bug" --candidate "$cand" --reason "$reason" \
      --out "$out"
    echo "[cell] wrote pending result -> $out" >&2
    exit 1
  fi

  to_docker_path() {
    local p="$1"
    if [[ "$p" == "$CACHE/"* ]]; then
      printf '/workspace/.artifacts/%s\n' "${p#$CACHE/}"
      return 0
    fi
    if [[ "$p" == "$CACHE" ]]; then
      printf '/workspace/.artifacts\n'
      return 0
    fi
    printf '%s\n' "$p"
  }

  # Reuse the cloned JS node_modules for scoring; local_only repos install (if any)
  # via the manifest's per-bug oracle.setup inside the scratch tree.
  bench_host=(
    python3 "$HERE/bench.py" score
    --project "$project"
    --bug "$bug"
    --tree "$cell"
    --candidate "$cand"
    --treatment kitsoki
    --out "$out"
    --trace "$trace"
    --candidates "$HERE/candidates.yaml"
  )
  if [[ -n "$completion_state" ]]; then
    bench_host+=(--completion-state "$completion_state")
  fi
  host_score_log="$CACHE/score-logs/$cellkey-host.log"
  host_score_err="$CACHE/score-logs/$cellkey-host.err"
  if [[ "$use_docker_score" == 1 ]] && command -v docker >/dev/null 2>&1; then
    image="${BAKEOFF_DOCKER_IMAGE_PREFIX:-kitsoki-bakeoff-repo}/${project}:${BAKEOFF_DOCKER_IMAGE_SUFFIX:-workspace-latest}"
    if docker image inspect "$image" >/dev/null 2>&1; then
      [[ -z "$local_only" && -d "$src/node_modules" ]] && export QS_NODE_MODULES="$src/node_modules"
      docker_bench_tree="$(to_docker_path "$cell")"
      docker_bench_out="$(to_docker_path "$out")"
      docker_bench_trace="$(to_docker_path "$trace")"
      docker_completion_state=""
      docker_bench_completion_state=""
      if [[ -n "$completion_state" ]]; then
        docker_completion_state="$CACHE/completion-state/$cellkey.json"
        mkdir -p "$(dirname "$docker_completion_state")"
        rm -f "$docker_completion_state"
        docker_bench_completion_state="$(to_docker_path "$docker_completion_state")"
      fi
      docker_score_log="$CACHE/score-logs/$cellkey-docker.log"
      docker_score_err="$CACHE/score-logs/$cellkey-docker.err"
      docker_bench_args=(
        python3 /workspace/kitsoki/tools/bugfix-bakeoff/external/bench.py score
        --project "$project" --bug "$bug" --tree "$docker_bench_tree"
        --candidate "$cand" --treatment kitsoki --out "$docker_bench_out"
        --trace "$docker_bench_trace" --candidates /workspace/kitsoki/tools/bugfix-bakeoff/external/candidates.yaml
      )
      if [[ -n "$docker_bench_completion_state" ]]; then
        docker_bench_args+=(--completion-state "$docker_bench_completion_state")
      fi
      if run_with_retry "docker score --project $project --bug $bug --candidate $cand" "$docker_score_log" "$docker_score_err" \
        "$HERE/run_repo_docker.sh" --project "$project" --repo-dir "$src" -- \
        "${docker_bench_args[@]}"; then
        if [[ -n "$completion_state" && -f "$docker_completion_state" ]]; then
          mkdir -p "$(dirname "$completion_state")"
          cp "$docker_completion_state" "$completion_state"
        fi
      else
        echo "[cell] docker score failed; falling back to host scoring" >&2
        run_with_retry "host score --project $project --bug $bug --candidate $cand" "$host_score_log" "$host_score_err" "${bench_host[@]}" || true
      fi
    else
      echo "[cell] docker image not found (${image}); scoring on host" >&2
      run_with_retry "host score --project $project --bug $bug --candidate $cand" "$host_score_log" "$host_score_err" "${bench_host[@]}" || true
    fi
  else
    [[ -z "$local_only" && -d "$src/node_modules" ]] && export QS_NODE_MODULES="$src/node_modules"
    run_with_retry "host score --project $project --bug $bug --candidate $cand" "$host_score_log" "$host_score_err" "${bench_host[@]}" || true
  fi

  echo "[cell] cost: $(python3 "$HERE/bench.py" cost --trace "$trace")"
  echo "[cell] verdict: $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["outcome"]["quality"])' "$out" 2>/dev/null || echo "?")"
fi
