#!/usr/bin/env bash
# drive_cell.sh — run ONE external bake-off cell end-to-end from the manifest.
#
# Codifies the live-drive recipe (Capsule workspace prep + every load-bearing
# initial_world knob + the headless MCP delegation) so a cell is one command instead of a
# hand-assembled prompt. COST-BEARING (real LLM) — operator-run, never in CI.
#
#   drive_cell.sh --project <name> --bug <id> --candidate <key> [--score] [--no-drive] [--no-docker-score] [--keep-workspace]
#                 [--repo-dir <local-checkout>] [--completion-state <path>] [--story <repo-relative app.yaml>]
#                 [--implementation-mode <agent_task|codeact>]
#
#   --score      after the drive, grade the workspace with bench.py + extract cost
#   --no-drive   only prepare the workspace + print the prompt (free; for inspection)
#   --no-docker-score  force host-based scoring (docker default)
#   --keep-workspace  pin a scored workspace for explicit debugging instead of closing it
#   --story      repo-relative story app.yaml the orchestrator drives (default: stories/bench-bugfix/app.yaml)
#   --implementation-mode  world.implementation_mode forwarded to stories/bugfix's
#                          implementing room: agent_task (host.agent.task, default)
#                          or codeact (host.agent.codeact). Non-default values
#                          suffix the cellkey/branch so the two modes never share
#                          a workspace or result file for the same candidate.
#
# Reads projects/<name>/manifest.yaml (per-bug facts via `bench.py meta --bug`) and
# candidates.yaml (the model/profile axis). Materialization is delegated to a
# pinned native Capsule definition and every Git mutation stays behind Capsule's
# declared-command boundary. The worker model is whatever the candidate's
# profile selects (codex-native → GPT-5.5, synthetic-claude → GLM-5.2); the
# orchestrator (default GPT-5.5) only clicks the pipeline forward.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE" && git rev-parse --show-toplevel)"
CACHE="${EXTERNAL_BAKEOFF_CACHE:-$REPO_ROOT/.artifacts/external-bakeoff}"      # gitignored work area
CAPSULE_PROJECT="${EXTERNAL_BAKEOFF_CAPSULE_PROJECT:-$REPO_ROOT/.capsules/projects/external-bakeoff}"
MAX_ATTEMPTS="${MCP_DRIVE_MAX_ATTEMPTS:-12}"
BACKOFF_BASE="${MCP_DRIVE_BACKOFF_BASE:-10}"
BACKOFF_MAX="${MCP_DRIVE_BACKOFF_MAX:-600}"

capsule_workspace_cli() {
  if [[ -n "${KITSOKI_CAPSULE_BIN:-}" ]]; then
    "$KITSOKI_CAPSULE_BIN" capsule workspace "$@"
    return
  fi
  (cd "$REPO_ROOT" && go run ./cmd/kitsoki capsule workspace "$@")
}

project=""; bug=""; cand=""; repo_dir=""; completion_state=""; do_score=0; no_drive=0; keep_workspace=0; orch="${MCP_DRIVE_MODEL:-gpt-5.5}"; use_docker_score="${EXTERNAL_BAKEOFF_USE_DOCKER_SCORE:-1}"; story="stories/bench-bugfix/app.yaml"; implementation_mode="agent_task"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --bug) bug="$2"; shift 2;;
    --candidate) cand="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --completion-state) completion_state="$2"; shift 2;;
    --orchestrator) orch="$2"; shift 2;;
    --no-docker-score) use_docker_score=0; shift;;
    --keep-workspace) keep_workspace=1; shift;;
    --score) do_score=1; shift;;
    --no-drive) no_drive=1; shift;;
    --story) story="$2"; shift 2;;
    --implementation-mode) implementation_mode="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[[ -n "$project" && -n "$bug" && -n "$cand" ]] || {
  echo "usage: drive_cell.sh --project <name> --bug <id> --candidate <key> [--repo-dir <local-checkout>] [--score] [--no-drive] [--no-docker-score] [--keep-workspace] [--completion-state <path>] [--story <repo-relative app.yaml>] [--implementation-mode <agent_task|codeact>]" >&2; exit 2; }
case "$implementation_mode" in
  agent_task|codeact) ;;
  *) echo "--implementation-mode must be agent_task or codeact, got '$implementation_mode'" >&2; exit 2;;
esac
if [[ "$keep_workspace" == 1 && "$do_score" != 1 ]]; then
  echo "--keep-workspace is a debug override for scored cells; use --no-drive for a prepared handoff" >&2
  exit 2
fi

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
    # codex does not forward the parent environment to the kitsoki MCP
    # subprocess it spawns (tools/mcp-drive/drive.sh's own header comment).
    # Any harness_profiles.*.env ${VAR} the DRIVEN session's profile needs
    # (synthetic.new-backed profiles: glm-5.2, glm-5.2-high, and every
    # syn-* GX10 candidate all reference ${SYNTHETIC_API_KEY}) must be
    # forwarded explicitly or webconfig silently drops that profile from the
    # usable set at load (internal/webconfig/webconfig.go's expandEnvVar
    # path) and session.new fails with "unknown harness profile" -- looking
    # exactly like a config/typo bug rather than a missing env var. Claude
    # orchestrator backend inherits the parent env normally and is
    # unaffected; this only matters for the codex orchestrator path.
    MCP_DRIVE_MODEL="$orch" PATH="$drive_path" MCP_DRIVE_FORWARD_ENV="${MCP_DRIVE_FORWARD_ENV:-SYNTHETIC_API_KEY}" "$REPO_ROOT/tools/mcp-drive/drive.sh" --prompt-file "$pf" >"$log_file" 2>"$err_file"
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
# bug id (e.g. both define "bug9") can't collide on the same workspace/trace/branch.
cellkey="$project-$bug-$cand"
# Non-default implementation_mode is a distinct cell, not a rerun of the default
# one -- suffix so agent_task and codeact never share a workspace/trace/branch/
# result file for the same candidate. Default stays unsuffixed for backward
# compatibility with already-committed result filenames.
if [[ "$implementation_mode" != "agent_task" ]]; then
  cellkey="$cellkey-$implementation_mode"
  short="$short-$implementation_mode"
fi

# --- fail fast (before any materialization/spend) -----------------------------
# local_only projects drive against an explicit local checkout. kitsoki-self can
# default to this repository; external/private repos must use --repo-dir or one
# of the manifest's generic repo_envs.
local_only=""
[[ "$(jget local_only)" == "True" || "$(jget local_only)" == "true" ]] && local_only=1

# rich ticket_title: pack the full bug description (the reproducer is fed only
# ticket_id + ticket_title; no ticket file). One line, quotes stripped.
desc="$(printf '%s — %s' "$title" "$(printf '%s' "$ticket" | tr '\n' ' ' | sed 's/  */ /g')" | sed 's/"/\\"/g')"

# --- immutable source authority ------------------------------------------------
if [[ -n "$local_only" || -n "$repo_dir" ]]; then
  repo_path_args=(repo-path --project "$project")
  [[ -n "$repo_dir" ]] && repo_path_args+=(--repo-dir "$repo_dir")
  repo_path_json="$CACHE/preflight/$cellkey-repo-path.json"; repo_path_err="$repo_path_json.err"; mkdir -p "$(dirname "$repo_path_json")"
  if ! python3 "$HERE/bench.py" "${repo_path_args[@]}" >"$repo_path_json" 2>"$repo_path_err"; then
    echo "[cell] could not resolve the declared source checkout for '$project'; pass --repo-dir <checkout-or-meta-root> or set one of the manifest repo_envs." >&2
    cat "$repo_path_json" >&2 2>/dev/null || true
    cat "$repo_path_err" >&2 2>/dev/null || true
    exit 2
  fi
  source_ref="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("path",""))' "$repo_path_json")"
else
  source_ref="$repo"
fi

# The sentinel-owned child project is a real Capsule control plane: definitions are
# immutable, instances carry leases + generations, and Git source caching stays
# behind the pinned provider. Hash the source as well as the cell so a manifest
# retarget cannot silently reacquire an older checkout with the same display key.
capsule_project="$CAPSULE_PROJECT"
workspace_identity="$(python3 -c 'import hashlib,re,sys
raw=":".join(sys.argv[1:])
slug=re.sub(r"[^A-Za-z0-9._-]+","-",sys.argv[1]).strip("-.")[:72] or "cell"
print("cell-"+slug+"-"+hashlib.sha256(raw.encode()).hexdigest()[:16])' "$cellkey" "$source_ref" "$baseline")"
definition_id="$workspace_identity"
capsule_owner="external-bakeoff-$cellkey"
branch="bench-$project-$bug-$short"
base_branch="bench-base-$project-$bug-$short"
definition_path="$capsule_project/.kitsoki/capsules/$definition_id.yaml"
mkdir -p "$CACHE/capsule-logs"
python3 - "$capsule_project/.kitsoki-capsule-project" "$REPO_ROOT" <<'PY'
import json
import os
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = {
    "schema": "capsule-project/v1",
    "kind": "external-bakeoff",
    "parent_project": os.path.realpath(sys.argv[2]),
    "managed_by": "tools/bugfix-bakeoff/external/drive_cell.sh",
}
if path.parent.is_symlink():
    raise SystemExit(
        f"refusing Capsule project root {path.parent}: expected a real directory, not a symlink"
    )
if path.is_symlink():
    raise SystemExit(
        f"refusing Capsule project sentinel {path}: expected a regular non-symlink file"
    )
if path.exists():
    if not path.is_file():
        raise SystemExit(
            f"refusing Capsule project sentinel {path}: expected a regular non-symlink file"
        )
    try:
        current = json.loads(path.read_text())
    except Exception as exc:
        raise SystemExit(f"refusing malformed Capsule project sentinel {path}: {exc}")
    for field in ("schema", "kind", "parent_project", "managed_by"):
        if current.get(field) != expected[field]:
            raise SystemExit(
                f"refusing Capsule project sentinel {path}: {field}="
                f"{current.get(field)!r}, want {expected[field]!r}"
            )
else:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(expected, indent=2) + "\n")
PY
mkdir -p "$(dirname "$definition_path")"
python3 - "$definition_path" "$definition_id" "$source_ref" "$baseline" "$project" "$bug" "$cand" "$base_branch" "$branch" <<'PY'
import json
import sys
from pathlib import Path

path, definition_id, source_ref, baseline, project, bug, candidate, base_branch, branch = sys.argv[1:]
definition = {
    "schema": "capsule-definition/v1",
    "id": definition_id,
    "description": f"External bake-off cell {project}/{bug}/{candidate} at its immutable baseline.",
    "source": {"kind": "pinned", "ref": source_ref, "commit": baseline},
    "policy": {
        "network": "none",
        "commands": {
            "prepare_base": {
                "argv": ["git", "branch", base_branch, baseline],
                "timeout": "30s",
            },
            "prepare_branch": {
                "argv": ["git", "switch", "-c", branch, baseline],
                "timeout": "30s",
            },
            "debug_pin": {
                "argv": ["touch", ".kitsoki-capsule-pin"],
                "timeout": "10s",
            },
        },
    },
}
Path(path).write_text(json.dumps(definition, indent=2) + "\n")
PY

create_json="$CACHE/capsule-logs/$cellkey-create.json"
create_err="$create_json.err"
if ! run_with_retry "capsule workspace create --project $capsule_project --id $workspace_identity" "$create_json" "$create_err" \
  capsule_workspace_cli create --project "$capsule_project" --id "$workspace_identity" \
  --definition "$definition_id" --owner "$capsule_owner" --json; then
  echo "[cell] Capsule materialization failed; see $create_json and $create_err" >&2
  exit 2
fi
cell_rel="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("path",""))' "$create_json")"
capsule_generation="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("generation",0))' "$create_json")"
capsule_provider="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("provider",""))' "$create_json")"
[[ "$capsule_provider" == "git" ]] || {
  echo "[cell] Capsule returned provider '$capsule_provider', want native git" >&2; exit 2; }
case "$cell_rel" in
  ""|/*|..|../*) echo "[cell] Capsule returned an invalid project-relative workspace path: $cell_rel" >&2; exit 2;;
esac
cell="$capsule_project/$cell_rel"
[[ -d "$cell/.git" && -f "$cell/.kitsoki-capsule" && -f "$cell/capsule-manifest.json" ]] || {
  echo "[cell] Capsule workspace is missing its checkout or ownership sentinels: $cell" >&2; exit 2; }

current_head="$(git -C "$cell" rev-parse HEAD)"
current_branch="$(git -C "$cell" branch --show-current)"
current_dirty="$(git -C "$cell" status --porcelain)"
if [[ "$current_head" != "$baseline" || -n "$current_dirty" ]]; then
  echo "[cell] refusing to overwrite existing candidate work in managed Capsule $workspace_identity." >&2
  echo "[cell] score/resume that workspace, or close it through 'kitsoki capsule workspace close' and choose a new cell attempt." >&2
  exit 2
fi
if ! git -C "$cell" show-ref --verify --quiet "refs/heads/$base_branch"; then
  exec_json="$CACHE/capsule-logs/$cellkey-prepare-base.json"
  exec_err="$exec_json.err"
  if ! capsule_workspace_cli exec --project "$capsule_project" --id "$workspace_identity" \
    --generation "$capsule_generation" --owner "$capsule_owner" --command prepare_base --json \
    >"$exec_json" 2>"$exec_err"; then
    echo "[cell] Capsule base preparation failed; see $exec_json and $exec_err" >&2
    exit 2
  fi
  capsule_generation="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("generation",0))' "$exec_json")"
fi
if [[ -z "$current_branch" ]]; then
  exec_json="$CACHE/capsule-logs/$cellkey-prepare-branch.json"
  exec_err="$exec_json.err"
  if ! capsule_workspace_cli exec --project "$capsule_project" --id "$workspace_identity" \
    --generation "$capsule_generation" --owner "$capsule_owner" --command prepare_branch --json \
    >"$exec_json" 2>"$exec_err"; then
    echo "[cell] Capsule branch preparation failed; see $exec_json and $exec_err" >&2
    exit 2
  fi
  capsule_generation="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("generation",0))' "$exec_json")"
  current_branch="$(git -C "$cell" branch --show-current)"
fi
if [[ "$current_branch" != "$branch" ]]; then
  echo "[cell] managed Capsule branch mismatch: got '$current_branch', want '$branch'" >&2
  exit 2
fi

# Remote projects retain their dependency install in the managed cell so a
# prepared no-drive handoff and the later live drive see the same exact tree.
# local_only projects keep their established toolchain/oracle setup behavior.
if [[ -z "$local_only" && ! -d "$cell/node_modules" ]]; then
  echo "[cell] $install in managed Capsule $workspace_identity" >&2
  (cd "$cell" && eval "$install")
fi
src="$cell"

# Keep drive_cell and the repo-bakeoff story on the same readiness contract. For
# `--no-drive`, report preflight failures but still prepare the inspection prompt;
# real drives fail here before an MCP session is created.
preflight_args=(preflight --project "$project" --bug "$bug" --candidate "$cand" --repo-dir "$src")
preflight_json="$CACHE/preflight/$cellkey.json"; preflight_err="$preflight_json.err"; mkdir -p "$(dirname "$preflight_json")"
if ! run_with_retry "preflight --project $project --bug $bug --candidate $cand" "$preflight_json" "$preflight_err" python3 "$HERE/bench.py" "${preflight_args[@]}"; then
  if [[ "$no_drive" == 1 ]]; then
    echo "[cell] warning: preflight failed; --no-drive will still prepare the prompt. See $preflight_json" >&2
  else
    echo "[cell] preflight failed; see $preflight_json" >&2
    exit 2
  fi
fi

# --- per-cell managed Capsule at baseline on its own branch -------------------
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
   - story_path: "$REPO_ROOT/$story"
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
       base_branch: "$base_branch"
       bugfix_mode: "full"
       implementation_mode: "$implementation_mode"
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
python3 - "$prep" "$project" "$bug" "$cand" "$profile" "$src" "$cell" "$branch" "$baseline" "$trace" "$thread_file" "$pf" "$preflight_json" "$CACHE/results/cells/$cellkey-kitsoki.json" "$capsule_project" "$workspace_identity" "$definition_id" "$capsule_owner" "$capsule_generation" "$base_branch" "$capsule_provider" <<'PY'
import json
import sys
from pathlib import Path

keys = [
    "project", "bug", "candidate", "profile", "repo_dir", "worktree",
    "branch", "baseline_sha", "trace", "thread", "prompt", "preflight",
    "score_result", "capsule_project", "capsule_workspace_id",
    "capsule_definition_id", "capsule_owner", "capsule_generation",
    "base_branch", "capsule_provider",
]
payload = dict(zip(keys, sys.argv[2:]))
payload["workspace"] = payload["worktree"]
payload["workspace_provider"] = "pinned"
payload["capsule_generation"] = int(payload["capsule_generation"])
Path(sys.argv[1]).write_text(json.dumps(payload, indent=2) + "\n")
PY
echo "[cell] project=$project bug=$bug candidate=$cand profile=$profile implementation_mode=$implementation_mode" >&2
echo "[cell] capsule=$workspace_identity generation=$capsule_generation workspace=$cell branch=$branch trace=$trace" >&2

record_workspace_cleanup() {
  local status="$1"
  local receipt="${2:-}"
  local error_path="${3:-}"
  python3 - "$prep" "$status" "$receipt" "$error_path" <<'PY'
import datetime
import json
import os
import sys
from pathlib import Path

path = Path(sys.argv[1])
payload = json.loads(path.read_text())
status, receipt_path, error_path = sys.argv[2:]
payload["workspace_cleanup"] = status
payload["workspace_cleanup_at"] = datetime.datetime.now(
    datetime.timezone.utc
).isoformat().replace("+00:00", "Z")
if receipt_path:
    payload["workspace_cleanup_receipt"] = receipt_path
    receipt = json.loads(Path(receipt_path).read_text())
    if status == "closed":
        payload["capsule_close_generation"] = int(receipt["generation"])
    elif status == "kept-debug":
        payload["capsule_generation"] = int(receipt["generation"])
if error_path:
    payload["workspace_cleanup_error_path"] = error_path
    if os.path.exists(error_path):
        payload["workspace_cleanup_error"] = Path(error_path).read_text()[-4096:].strip()
tmp = path.with_suffix(path.suffix + ".tmp")
tmp.write_text(json.dumps(payload, indent=2) + "\n")
tmp.replace(path)
PY
}

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
    record_workspace_cleanup "retained-incomplete" "" "$err"
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

  # Reuse dependencies from the managed Capsule for scoring; local_only repos
  # install (if any) via the manifest's per-bug oracle.setup in the scratch tree.
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

  if ! python3 - "$out" "$project" "$bug" "$cand" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
payload = json.loads(path.read_text())
if payload.get("project") != sys.argv[2] or payload.get("bug") != sys.argv[3] or payload.get("candidate") != sys.argv[4]:
    raise SystemExit("score result identity does not match the cell")
if (payload.get("outcome") or {}).get("quality") not in {"solved", "partial", "failed"}:
    raise SystemExit("score result has no completed quality verdict")
PY
  then
    record_workspace_cleanup "retained-score-failure" "" "$host_score_err"
    echo "[cell] score did not produce a durable, identity-matched result; workspace retained for diagnosis" >&2
    exit 1
  fi

  echo "[cell] cost: $(python3 "$HERE/bench.py" cost --trace "$trace")"
  echo "[cell] verdict: $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["outcome"]["quality"])' "$out")"

  if [[ "$keep_workspace" == 1 ]]; then
    pin_json="$CACHE/capsule-logs/$cellkey-debug-pin.json"
    pin_err="$pin_json.err"
    if ! capsule_workspace_cli exec --project "$capsule_project" --id "$workspace_identity" \
      --generation "$capsule_generation" --owner "$capsule_owner" --command debug_pin --json \
      >"$pin_json" 2>"$pin_err"; then
      record_workspace_cleanup "debt" "" "$pin_err"
      echo "[cell] failed to pin debug workspace; cleanup debt recorded in $prep" >&2
      exit 1
    fi
    record_workspace_cleanup "kept-debug" "$pin_json" ""
    echo "[cell] debug override: kept and pinned managed workspace $cell" >&2
  else
    close_json="$CACHE/capsule-logs/$cellkey-close.json"
    close_err="$close_json.err"
    if ! capsule_workspace_cli close --project "$capsule_project" --id "$workspace_identity" \
      --generation "$capsule_generation" --owner "$capsule_owner" --json \
      >"$close_json" 2>"$close_err"; then
      record_workspace_cleanup "debt" "" "$close_err"
      echo "[cell] score is durable, but Capsule close failed; cleanup debt recorded in $prep and $close_err" >&2
      exit 1
    fi
    record_workspace_cleanup "closed" "$close_json" ""
    echo "[cell] closed managed Capsule workspace after durable grading -> $close_json" >&2
  fi
fi
