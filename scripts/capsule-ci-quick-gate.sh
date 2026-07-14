#!/usr/bin/env bash
# Fast, no-spend gate for branches landing in staging/local.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

echo "capsule-ci-quick: preparing embedded story assets"
make embed-stories >/dev/null
echo "capsule-ci-quick: checking diff hygiene"
git diff --check
echo "capsule-ci-quick: validating Capsule CI story"
go run ./cmd/kitsoki validate stories/capsule-ci/app.yaml
echo "capsule-ci-quick: replaying Capsule CI flow fixtures"
KITSOKI_CASSETTE_STRICT=1 go run ./cmd/kitsoki test flows stories/capsule-ci/app.yaml
echo "capsule-ci-quick: running focused short tests"
go test -short -count=1 ./internal/capsule/... ./internal/host ./cmd/kitsoki
echo "capsule-ci-quick: passed"
