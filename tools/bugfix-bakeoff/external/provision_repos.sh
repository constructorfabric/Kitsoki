#!/usr/bin/env bash
set -euo pipefail

# provision_repos.sh
# Build cache-ready repo workspaces for bakeoff projects and (optionally) build
# the docker repo-runtime image used for deterministic, local-only preflight/arming
# checks.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && git rev-parse --show-toplevel)"

export EXTERNAL_BAKEOFF_CACHE="${EXTERNAL_BAKEOFF_CACHE:-$ROOT/.artifacts/external-bakeoff}"
export BAKEOFF_REPO_ROOT="${BAKEOFF_REPO_ROOT:-$ROOT/.artifacts/external-bakeoff/repos}"
export BAKEOFF_DOCKER_IMAGE_PREFIX="${BAKEOFF_DOCKER_IMAGE_PREFIX:-kitsoki-bakeoff-repo}"
export BAKEOFF_DOCKER_IMAGE_SUFFIX="${BAKEOFF_DOCKER_IMAGE_SUFFIX:-workspace-latest}"
DOCKERFILE="${HERE}/docker/Dockerfile.repo-runtime"

MAX_ATTEMPTS="${MCP_DRIVE_MAX_ATTEMPTS:-12}"
BACKOFF_BASE="${MCP_DRIVE_BACKOFF_BASE:-10}"
BACKOFF_MAX="${MCP_DRIVE_BACKOFF_MAX:-600}"

projects=()
local_repo_dir=""
do_build_images=1
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir) local_repo_dir="$2"; shift 2;;
    --project) projects+=("$2"); shift 2;;
    --no-build-images) do_build_images=0; shift;;
    --cache) EXTERNAL_BAKEOFF_CACHE="$2"; shift 2;;
    --repo-root) BAKEOFF_REPO_ROOT="$2"; shift 2;;
    --help|-h)
      cat <<'USAGE'
usage: provision_repos.sh [--project <name>] [--repo-dir <path>] [--no-build-images] [--cache <path>] [--repo-root <path>]

Defaults:
- project(s): all manifests under tools/bugfix-bakeoff/external/projects
- cache: <repo-root>/.artifacts/external-bakeoff
- repo-root: <repo-root>/.artifacts/external-bakeoff/repos
- image prefix: kitsoki-bakeoff-repo
- image suffix: workspace-latest
USAGE
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

if [[ "$do_build_images" == "1" ]]; then
  if [[ ! -x "$(command -v docker || true)" ]]; then
    echo "[provision] docker is required for this target; install docker or pass --no-build-images and run commands externally" >&2
    exit 2
  fi
else
  echo "[provision] --no-build-images set; skipping docker image build"
fi

mkdir -p "$BAKEOFF_REPO_ROOT" "$EXTERNAL_BAKEOFF_CACHE"

if [[ ${#projects[@]} -eq 0 ]]; then
  for manifest in "$HERE/projects"/*/manifest.yaml; do
    [[ -f "$manifest" ]] || continue
    pid="$(python3 -c 'import yaml,sys; print(yaml.safe_load(open(sys.argv[1]))["project"]["id"])' "$manifest")"
    [[ -n "$pid" ]] && projects+=("$pid")
  done
fi

if [[ ${#projects[@]} -eq 0 ]]; then
  echo "[provision] no project manifests found in $HERE/projects" >&2
  exit 1
fi

is_retryable_error() {
  local payload="$1"
  local lower
  lower="$(printf '%s' "$payload" | tr '[:upper:]' '[:lower:]')"

  case "$lower" in
    *"usage:"*|*"unknown option"*|*"unknown arg"*|*"invalid or missing argument"*|\
    *"not a git repository"*|*"no such file"*|*"cannot clone into"*|\
    *"command not found"*|*"permission denied"*)
      return 1
      ;;
  esac

  case "$lower" in
    *"429"*|*"too many requests"*|*"rate limit"*|*"quota"*|*"quota exceeded"*|\
    *"temporarily unavailable"*|*"service unavailable"*|*"gateway timeout"*|*"bad gateway"*|\
    *"connection reset"*|*"connection timed out"*|*"network error"*|*"econn"*|*"retry-after"*)
      return 0
      ;;
  esac
  # Default to retrying non-obvious failures for forward compatibility with provider
  # wording changes.
  return 0
}

run_with_retry() {
  local desc="$1"; shift
  local attempt=1
  local wait_seconds="$BACKOFF_BASE"
  while true; do
    set +e
    local output
    output="$("$@" 2>&1)"
    local rc=$?
    set -e
    if [[ $rc -eq 0 ]]; then
      printf '%s\n' "$output"
      return 0
    fi

    if is_retryable_error "$output" && [[ $attempt -lt "$MAX_ATTEMPTS" ]]; then
      echo "[provision] ${desc} failed (attempt ${attempt}) — retrying in ${wait_seconds}s" >&2
      sleep "$wait_seconds"
      attempt=$((attempt + 1))
      wait_seconds=$((wait_seconds * 2))
      if [[ $wait_seconds -gt $BACKOFF_MAX ]]; then
        wait_seconds=$BACKOFF_MAX
      fi
      continue
    fi

    echo "[provision] ${desc} failed" >&2
    printf '%s\n' "$output" >&2
    return "$rc"
  done
}

resolve_manifest() {
  local project_name="$1"
  local manifest
  for manifest in "$HERE/projects"/*/manifest.yaml; do
    [[ -f "$manifest" ]] || continue
    local pid
    pid="$(python3 -c 'import yaml,sys; print(yaml.safe_load(open(sys.argv[1]))["project"]["id"])' "$manifest")"
    if [[ "$pid" == "$project_name" ]]; then
      printf '%s\n' "$manifest"
      return 0
    fi
  done
  return 1
}

build_repo_image() {
  local project_name="$1"
  local image="${BAKEOFF_DOCKER_IMAGE_PREFIX}/${project_name}:${BAKEOFF_DOCKER_IMAGE_SUFFIX}"
  echo "[provision] building image: $image"
  run_with_retry "docker build for $project_name" \
    docker build -f "$DOCKERFILE" -t "$image" "$HERE/docker"
}

ensure_remote_repo() {
  local project_name="$1" repo_url="$2" target_dir="$3"
  if [[ -d "$target_dir/.git" ]]; then
    echo "[provision] updating $project_name checkout at $target_dir"
    run_with_retry "git fetch $project_name" git -C "$target_dir" fetch --all --prune
    return 0
  fi

  echo "[provision] cloning $project_name -> $target_dir"
  mkdir -p "$(dirname "$target_dir")"
  run_with_retry "git clone $project_name" git clone --origin origin "$repo_url" "$target_dir"
}

for project in "${projects[@]}"; do
  manifest="$(resolve_manifest "$project" || true)"
  if [[ -z "$manifest" ]]; then
    echo "[provision] no manifest for project '$project'" >&2
    continue
  fi

  readarray -t meta < <(python3 - <<'PY' "$manifest"
import yaml,sys
d = yaml.safe_load(open(sys.argv[1])) or {}
p = (d.get("project") or {})
print(p.get("id", ""))
print(p.get("repo", ""))
print(str(bool(p.get("local_only", False))).lower())
PY
)
  pid="${meta[0]}"
  repo="${meta[1]}"
  local_only="${meta[2]}"

  if [[ -z "$pid" ]]; then
    echo "[provision] manifest has empty project.id: $manifest" >&2
    continue
  fi

  if [[ "$local_only" == "true" ]]; then
    var_name="$(echo "${pid}_REPO" | tr '[:lower:]-' '[:upper:]_')"
    local_repo="${local_repo_dir:-${!var_name:-}}"
    if [[ -z "$local_repo" || ! -d "$local_repo/.git" ]]; then
      echo "[provision] local_only project '$pid' requires checkout in ${var_name}; set ${var_name} or --repo-dir" >&2
      continue
    fi
    target="$local_repo"
    echo "[provision] using local_only checkout for $pid: $target"
  else
    if [[ -z "$repo" ]]; then
      echo "[provision] missing repo URL for $pid" >&2
      continue
    fi
    target="$BAKEOFF_REPO_ROOT/$pid"
    ensure_remote_repo "$pid" "$repo" "$target"
  fi

  if [[ -d "$target" ]]; then
    echo "[provision] ready: $pid -> $target"
  fi

  if [[ "$do_build_images" == "1" ]]; then
    build_repo_image "$pid"
  fi
done

echo "[provision] done"
echo "[provision] repos: $BAKEOFF_REPO_ROOT"
echo "[provision] images: ${BAKEOFF_DOCKER_IMAGE_PREFIX}/*:${BAKEOFF_DOCKER_IMAGE_SUFFIX}"
