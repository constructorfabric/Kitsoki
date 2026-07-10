#!/usr/bin/env bash
# smoke-release.sh — change 0.1: prove a released binary works for a stranger's
# fresh clone. A release binary must carry the agent toolkit (skills/agents) AND
# the story library embedded, so that in a project with NO kitsoki checkout,
# `kitsoki project-tools install` onboards the toolkit instead of failing with
# ErrNotStaged ("agent toolkit not staged into this binary").
#
# This smokes exactly that path: stage the embeds, build the binary, then in a
# throwaway git repo run `project-tools install` and assert it succeeds (no
# ErrNotStaged) and the toolkit + MCP registration landed.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

echo "== staging embeds (make embed-stories embed-skills) =="
make embed-stories embed-skills >/dev/null

# The embeds must be real, not the committed .gitkeep placeholders.
[ -n "$(ls -A internal/basestories/stories 2>/dev/null | grep -v '^.gitkeep$' || true)" ] \
  || { echo "FAIL: internal/basestories/stories is empty after embed-stories"; exit 1; }
[ -n "$(ls -A internal/baseskills/assets 2>/dev/null | grep -v '^.gitkeep$' || true)" ] \
  || { echo "FAIL: internal/baseskills/assets is empty after embed-skills"; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "== building the release-shaped binary =="
CGO_ENABLED=0 go build -trimpath -o "$tmp/kitsoki" ./cmd/kitsoki
bin="$tmp/kitsoki"

echo "== onboarding a scratch git repo (no kitsoki checkout present) =="
scratch="$tmp/scratch"
mkdir -p "$scratch"
git -C "$scratch" init -q

# project-tools install must succeed — a binary built without the embeds would
# fail here with ErrNotStaged.
if ! ( cd "$scratch" && "$bin" project-tools install ) ; then
  echo "FAIL: 'kitsoki project-tools install' failed in a fresh repo (ErrNotStaged?)"
  exit 1
fi

echo "== asserting the toolkit + MCP landed =="
for p in .agents/skills .agents/agents .claude/skills .mcp.json; do
  [ -e "$scratch/$p" ] || { echo "FAIL: 'project-tools install' did not create $p"; exit 1; }
done
# a stranger's .mcp.json must register the kitsoki studio MCP server
grep -q '"kitsoki"' "$scratch/.mcp.json" || { echo "FAIL: .mcp.json does not register the kitsoki MCP server"; exit 1; }

echo "SMOKE OK: release binary carries the embedded toolkit + stories; fresh-repo onboarding succeeded."
