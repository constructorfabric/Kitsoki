#!/usr/bin/env bash
# smoke-browser-image.sh — build + prove tools/arena/Dockerfile.repo-runtime-browser.
#
# Docker-gated manual acceptance for the arena browser-capable cell image. NOT
# part of the standing no-docker CI check (that's the grep-based lint in
# docs/goals/ui-qa-scale/decomposition.yaml's arena-browser-image gate). Run
# this by hand (or from a docker-capable CI job) whenever the Dockerfile or the
# pinned Playwright version changes.
#
# What it proves:
#   1. the base repo-runtime image builds (untouched by this change)
#   2. the browser layer builds on top of it
#   3. `npx playwright --version` works inside a container run of the image
#   4. a real headless Chromium launch succeeds inside that container
#
# Usage: tools/arena/scripts/smoke-browser-image.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
BASE_TAG="kitsoki-arena-repo-runtime:latest"
BROWSER_TAG="kitsoki-arena-repo-runtime-browser:latest"

echo "==> [1/4] building base repo-runtime image ($BASE_TAG)"
docker build \
  -f "$REPO_ROOT/tools/bugfix-bakeoff/external/docker/Dockerfile.repo-runtime" \
  -t "$BASE_TAG" \
  "$REPO_ROOT/tools/bugfix-bakeoff/external/docker"

echo "==> [2/4] building browser image ($BROWSER_TAG) from $BASE_TAG"
docker build \
  -f "$REPO_ROOT/tools/arena/Dockerfile.repo-runtime-browser" \
  --build-arg "BASE_IMAGE=$BASE_TAG" \
  -t "$BROWSER_TAG" \
  "$REPO_ROOT/tools/arena"

echo "==> [3/4] npx playwright --version"
docker run --rm "$BROWSER_TAG" npx playwright --version

echo "==> [4/4] headless chromium launch smoke"
# playwright is installed globally (npm install -g); NODE_PATH is resolved at
# container-run time (via `npm root -g`) rather than baked into the image, so
# this stays correct regardless of where the base image's npm prefix lands.
docker run --rm "$BROWSER_TAG" sh -c '
NODE_PATH="$(npm root -g)" node -e "
const { chromium } = require(\"playwright\");
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.setContent(\"<h1>arena browser image smoke</h1>\");
  const title = await page.textContent(\"h1\");
  await browser.close();
  if (title !== \"arena browser image smoke\") {
    throw new Error(\"unexpected page content: \" + title);
  }
  console.log(\"chromium launch OK\");
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
"
'

echo "==> smoke-browser-image: PASS"
