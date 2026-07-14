#!/usr/bin/env bash
# smoke-target-foreign-repo.sh — change 2.8: onboarding must be able to target
# a repo OTHER than the current checkout, not just cwd. This exercises the
# real (unstubbed, no-LLM) onboarding surfaces end-to-end against a scratch
# repo living outside this checkout:
#   * `kitsoki init --target <path>` installs the agent toolkit + registers
#     the studio MCP into the foreign repo (cmd/kitsoki/doctor.go:initCmd).
#   * stories/dev-story/scripts/init_discover.py resolves a natural-language
#     "onboard <path>" request against the foreign path (not workdir/repo_root)
#     and profiles ITS files, proving discovery targets the named repo.
#   * stories/dev-story/scripts/init_apply.py writes the onboarding scaffold
#     (.kitsoki.yaml, project-profile.yaml, the generated dev-story instance)
#     into the foreign repo, not into this checkout.
# No LLM, no cassette — every step below is a real subprocess against a real
# scratch directory.
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# The scratch "foreign repo" — deliberately NOT under $repo_root and NOT cwd.
foreign="$tmp/acme-widgets"
mkdir -p "$foreign"
cat > "$foreign/go.mod" <<'EOF'
module acme-widgets

go 1.22
EOF
echo "package main" > "$foreign/main.go"

echo "== staging embeds (skills/agents toolkit) =="
make embed-skills >/dev/null

echo "== building kitsoki =="
bin="$tmp/kitsoki"
go build -o "$bin" ./cmd/kitsoki

before_mcp_sha="$(git -C "$repo_root" hash-object .mcp.json)"

echo "== kitsoki init --target <foreign repo> installs the toolkit there =="
out="$("$bin" init --target "$foreign")"
echo "$out"
echo "$out" | grep -qF "onboarded kitsoki toolkit into $foreign" || { echo "FAIL: init did not report onboarding the foreign target"; exit 1; }
[ -f "$foreign/.mcp.json" ] || { echo "FAIL: no .mcp.json written into the foreign repo"; exit 1; }
after_mcp_sha="$(git -C "$repo_root" hash-object .mcp.json)"
[ "$before_mcp_sha" = "$after_mcp_sha" ] || { echo "FAIL: init --target mutated this checkout's own .mcp.json instead of only the foreign repo's"; exit 1; }

echo "== init_discover.py profiles the NAMED foreign path, not cwd/workdir =="
discovery="$(cd "$repo_root" && python3 "stories/dev-story/scripts/init_discover.py" "onboard $foreign" "$repo_root" "$repo_root")"
echo "$discovery"
target_path="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["target_path"])' "$discovery")"
project_id="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["project_id"])' "$discovery")"
stack="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["stack"])' "$discovery")"
resolved_foreign="$(cd "$foreign" && pwd -P)"
[ "$target_path" = "$resolved_foreign" ] || { echo "FAIL: discovery target_path '$target_path' != foreign repo '$resolved_foreign'"; exit 1; }
[ "$project_id" = "acme-widgets" ] || { echo "FAIL: discovery did not read the foreign repo's own project id (got '$project_id')"; exit 1; }
case "$stack" in
  *"go project"*) ;;
  *) echo "FAIL: discovery did not detect the foreign repo's go.mod (stack='$stack')"; exit 1 ;;
esac

echo "== init_apply.py writes the onboarding scaffold INTO the foreign repo =="
apply_out="$(python3 "stories/dev-story/scripts/init_apply.py" "$target_path" "$project_id" "Acme Widgets" "$stack" "" "go test ./..." "go build ./..." "local defaults" "none")"
echo "$apply_out"
[ -f "$foreign/.kitsoki.yaml" ] || { echo "FAIL: .kitsoki.yaml not written into the foreign repo"; exit 1; }
[ -f "$foreign/.kitsoki/project-profile.yaml" ] || { echo "FAIL: project-profile.yaml not written into the foreign repo"; exit 1; }
[ -f "$foreign/.kitsoki/stories/acme-widgets-dev/app.yaml" ] || { echo "FAIL: generated dev-story instance not written into the foreign repo"; exit 1; }
[ ! -e "$repo_root/.kitsoki/stories/acme-widgets-dev" ] || { echo "FAIL: onboarding leaked into this checkout instead of the foreign repo"; exit 1; }

echo "SMOKE OK: kitsoki onboards and profiles a repo other than the current checkout (toolkit install, discovery, and apply all targeted $foreign)."
