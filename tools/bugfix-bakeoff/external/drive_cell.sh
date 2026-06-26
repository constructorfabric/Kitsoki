#!/usr/bin/env bash
# drive_cell.sh — run ONE external bake-off cell end-to-end from the manifest.
#
# Codifies the live-drive recipe (worktree prep + every load-bearing initial_world
# knob + the headless MCP delegation) so a cell is one command instead of a
# hand-assembled prompt. COST-BEARING (real LLM) — operator-run, never in CI.
#
#   drive_cell.sh --project <name> --bug <id> --candidate <key> [--score] [--no-drive]
#                 [--repo-dir <local-checkout>]
#
#   --score      after the drive, grade the worktree with bench.py + extract cost
#   --no-drive   only prepare the worktree + print the prompt (free; for inspection)
#
# Reads projects/<name>/manifest.yaml (per-bug facts via `bench.py meta --bug`) and
# candidates.yaml (the model/profile axis). Clones the repo ONCE into a cache and
# reuses node_modules across cells. The worker model is whatever the candidate's
# profile selects (codex-native → GPT-5.5, synthetic-claude → GLM-5.2); the
# orchestrator (cheap sonnet) only clicks the pipeline forward.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE" && git rev-parse --show-toplevel)"
CACHE="${EXTERNAL_BAKEOFF_CACHE:-$REPO_ROOT/.artifacts/external-bakeoff}"      # gitignored work area

project=""; bug=""; cand=""; repo_dir=""; do_score=0; no_drive=0; orch="${MCP_DRIVE_MODEL:-opus}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --bug) bug="$2"; shift 2;;
    --candidate) cand="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --orchestrator) orch="$2"; shift 2;;
    --score) do_score=1; shift;;
    --no-drive) no_drive=1; shift;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[[ -n "$project" && -n "$bug" && -n "$cand" ]] || {
  echo "usage: drive_cell.sh --project <name> --bug <id> --candidate <key> [--repo-dir <local-checkout>] [--score] [--no-drive]" >&2; exit 2; }

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
# default to this repository; external/private repos such as gears-rust must use
# --repo-dir or <PROJECT>_REPO / GEARS_RUST_REPO.
local_only=""
[[ "$(jget local_only)" == "True" || "$(jget local_only)" == "true" ]] && local_only=1

# rich ticket_title: pack the full bug description (the reproducer is fed only
# ticket_id + ticket_title; no ticket file). One line, quotes stripped.
desc="$(printf '%s — %s' "$title" "$(printf '%s' "$ticket" | tr '\n' ' ' | sed 's/  */ /g')" | sed 's/"/\\"/g')"

# --- source repo: clone (remote) or the local checkout (local_only) -----------
if [[ -n "$local_only" ]]; then
  env_name="$(printf '%s_REPO' "$project" | tr '[:lower:]-' '[:upper:]_')"
  src="${repo_dir:-${!env_name:-${GEARS_RUST_REPO:-}}}"
  if [[ -z "$src" && "$project" == "kitsoki" ]]; then
    src="$REPO_ROOT"
  fi
  [[ -n "$src" ]] || {
    echo "[cell] project '$project' is local_only; pass --repo-dir <checkout> or set $env_name." >&2
    exit 2
  }
  [[ -d "$src/.git" ]] || {
    echo "[cell] local repo '$src' is not a git checkout." >&2
    exit 2
  }
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
preflight_json="$CACHE/preflight/$cellkey.json"; mkdir -p "$(dirname "$preflight_json")"
if ! python3 "$HERE/bench.py" "${preflight_args[@]}" > "$preflight_json"; then
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

trace="$CACHE/traces/$cellkey.jsonl"; rm -f "$trace"
thread_file="$CACHE/threads/$cellkey.md"
mkdir -p "$CACHE/traces" "$CACHE/drive-logs" "$CACHE/results/cells" "$CACHE/threads"

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
4. STOP at a terminal state, ~25 forward turns, or a repeated stuck state. If a
   host_error bounces you to idle, read world.last_error, report it verbatim, STOP.
5. Then inspect the session status/world/trace through MCP. Do not use shell,
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
echo "[cell] driving (orchestrator=$orch, worker=$cand)…" >&2
MCP_DRIVE_MODEL="$orch" "$REPO_ROOT/tools/mcp-drive/drive.sh" --prompt-file "$pf" > "$log" 2>"${log%.json}.err" || true
echo "[cell] drive done -> $log" >&2

if [[ "$do_score" == 1 ]]; then
  out="$CACHE/results/cells/$cellkey-kitsoki.json"
  # Reuse the cloned JS node_modules for scoring; local_only repos install (if any)
  # via the manifest's per-bug oracle.setup inside the scratch tree.
  [[ -z "$local_only" && -d "$src/node_modules" ]] && export QS_NODE_MODULES="$src/node_modules"
  python3 "$HERE/bench.py" score \
    --project "$project" --bug "$bug" --tree "$cell" \
    --candidate "$cand" --treatment kitsoki --out "$out" \
    --trace "$trace" --candidates "$HERE/candidates.yaml" || true
  echo "[cell] cost: $(python3 "$HERE/bench.py" cost --trace "$trace")"
  echo "[cell] verdict: $(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["outcome"]["quality"])' "$out" 2>/dev/null || echo "?")"
fi
