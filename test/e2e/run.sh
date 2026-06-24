#!/usr/bin/env bash
#
# run.sh — host-side wrapper: build the kitsoki e2e image and run the smoke
# test inside it. This is the reusable entry point; CI and humans call this.
#
#   ./test/e2e/run.sh                 # build + run the smoke suite
#   KITSOKI_E2E_APPS="cloak" ./test/e2e/run.sh   # narrow the flow set
#   KEEP_IMAGE=1 ./test/e2e/run.sh    # don't remove the image afterwards
#
# Exit code mirrors the in-container smoke verdict (0 = pass).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE="${KITSOKI_E2E_IMAGE:-kitsoki-e2e:latest}"

cd "$REPO_ROOT"

echo ">> building $IMAGE (faithful make-install build: Go + Node + pnpm)"
DOCKER_BUILDKIT=1 docker build \
  -f test/e2e/Dockerfile \
  -t "$IMAGE" \
  .

echo ">> running in-container smoke test"
docker run --rm \
  ${KITSOKI_E2E_APPS:+-e KITSOKI_E2E_APPS="$KITSOKI_E2E_APPS"} \
  "$IMAGE"
status=$?

if [ "${KEEP_IMAGE:-0}" != "1" ]; then
  docker image rm "$IMAGE" >/dev/null 2>&1 || true
fi

exit "$status"
