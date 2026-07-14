#!/usr/bin/env bash
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && git rev-parse --show-toplevel)"
CACHE="${EXTERNAL_BAKEOFF_CACHE:-$ROOT/.artifacts/external-bakeoff}"
PREFIX="${BAKEOFF_DOCKER_IMAGE_PREFIX:-kitsoki-bakeoff-repo}"
DEFAULT_IMAGE_SUFFIX="workspace-latest"

project=""
repo_dir=""
image_override=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --image) image_override="$2"; shift 2;;
    --help|-h)
      echo "usage: run_repo_docker.sh --project <name> --repo-dir <path> [--image <image>] -- <command...>"
      exit 0
      ;;
    --) shift; break;;
    *)
      break
      ;;
  esac
done

if [[ -z "${project}" || -z "${repo_dir}" ]]; then
  echo "usage: run_repo_docker.sh --project <name> --repo-dir <path> [--image <image>] -- <command...>" >&2
  exit 2
fi
if [[ ! -d "${repo_dir}/.git" ]]; then
  echo "[docker] repo-dir must be a git checkout: ${repo_dir}" >&2
  exit 2
fi

if [[ "$#" -eq 0 ]]; then
  echo "[docker] command required: pass '-- <command...>'" >&2
  exit 2
fi

image="${image_override:-${PREFIX}/${project}:${DEFAULT_IMAGE_SUFFIX}}"

docker run --rm \
  -v "${repo_dir}:/workspace/repo" \
  -v "${ROOT}:/workspace/kitsoki:ro" \
  -v "${CACHE}:/workspace/.artifacts" \
  -e EXTERNAL_BAKEOFF_CACHE="/workspace/.artifacts/external-bakeoff" \
  -e KITSOKI_REPO="/workspace/repo" \
  -e BUGFIX_BAKEOFF_REPO="/workspace/repo" \
  -e MCP_DRIVE_MAX_ATTEMPTS="${MCP_DRIVE_MAX_ATTEMPTS:-12}" \
  -e MCP_DRIVE_BACKOFF_BASE="${MCP_DRIVE_BACKOFF_BASE:-10}" \
  -e MCP_DRIVE_BACKOFF_MAX="${MCP_DRIVE_BACKOFF_MAX:-600}" \
  -e MCP_DRIVE_RETRY_VERBOSE="${MCP_DRIVE_RETRY_VERBOSE:-1}" \
  -e SYNTHETIC_API_KEY="${SYNTHETIC_API_KEY:-}" \
  -e OPENAI_API_KEY="${OPENAI_API_KEY:-}" \
  -e ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-}" \
  -e GITHUB_TOKEN="${GITHUB_TOKEN:-}" \
  -w /workspace/repo \
  "$image" \
  "$@"
