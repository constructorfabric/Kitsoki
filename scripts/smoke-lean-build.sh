#!/usr/bin/env bash
# smoke-lean-build.sh — change 4.6: a headless build must not require pnpm/node.
# Shadow node/pnpm/npm with failing stubs on PATH, run `make build-lean`, and
# assert it still produces a working binary (proving the lean path never shells
# out to the JS toolchain).
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

shim="$tmp/shim"
mkdir -p "$shim"
for t in node pnpm npm npx corepack; do
  printf '#!/bin/sh\necho "smoke-lean-build: %s must NOT be needed for a headless build" >&2\nexit 1\n' "$t" > "$shim/$t"
  chmod +x "$shim/$t"
done

echo "== make build-lean with node/pnpm shadowed =="
PATH="$shim:$PATH" make build-lean BINARY="$tmp/kitsoki"

[ -x "$tmp/kitsoki" ] || { echo "FAIL: build-lean produced no binary"; exit 1; }
"$tmp/kitsoki" version >/dev/null 2>&1 || { echo "FAIL: lean binary does not run"; exit 1; }

echo "SMOKE OK: make build-lean produced a working binary with node/pnpm unavailable."
