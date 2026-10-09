#!/usr/bin/env bash
# smoke-doctor.sh — change 4.3: assert the preflight/onboarding UX exists.
#   * `kitsoki doctor` reports provider / toolkit / MCP readiness.
#   * a top-level `kitsoki init` alias exists (onboards a project).
#   * the root help groups the ~48 commands into tiers.
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "== building =="
go build -o "$tmp/kitsoki" ./cmd/kitsoki
bin="$tmp/kitsoki"

echo "== kitsoki doctor reports readiness =="
doc="$("$bin" doctor)"
echo "$doc"
for label in provider toolkit mcp; do
  echo "$doc" | grep -qE "^  [✓✗] $label " || { echo "FAIL: doctor missing '$label' readiness line"; exit 1; }
done

echo "== top-level init alias exists =="
"$bin" init --help >/dev/null 2>&1 || { echo "FAIL: no top-level 'init' command"; exit 1; }

echo "== help is tiered =="
help="$("$bin" --help)"
for tier in "Setup & onboarding:" "Run & drive:" "Author & test:" "Agents & bench:"; do
  echo "$help" | grep -qF "$tier" || { echo "FAIL: help missing tier '$tier'"; exit 1; }
done

echo "SMOKE OK: doctor reports readiness, init alias exists, help is tiered."
