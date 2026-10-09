#!/usr/bin/env bash
# Focused regression tests for scripts/runstatus-node-modules-cache.sh.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cache_script="$script_dir/runstatus-node-modules-cache.sh"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-runstatus-cache-test.XXXXXX")"
cleanup() {
  chmod -R u+w "$tmp" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

repo="$tmp/repo"
mkdir -p "$repo/tools/runstatus/node_modules/.bin" "$repo/.capsules/workspaces"
git -C "$tmp" init --quiet repo
cat >"$repo/tools/runstatus/package.json" <<'JSON'
{"name":"runstatus-test","dependencies":{"tsx":"1.0.0"}}
JSON
cat >"$repo/tools/runstatus/pnpm-lock.yaml" <<'YAML'
lockfileVersion: '9.0'
YAML
cat >"$repo/tools/runstatus/node_modules/.modules.yaml" <<'YAML'
layoutVersion: 5
YAML
printf '#!/usr/bin/env sh\nexit 0\n' >"$repo/tools/runstatus/node_modules/.bin/tsx"
chmod +x "$repo/tools/runstatus/node_modules/.bin/tsx"
cat >"$repo/.kitsoki-dev-workspace.json" <<JSON
{"root":"$repo/.capsules/workspaces"}
JSON

key="$(cd "$repo" && "$cache_script" key)"
[ -n "$key" ] || fail "cache key was empty"

(cd "$repo" && "$cache_script" save) >/tmp/kitsoki-runstatus-cache-save.log
[ -L "$repo/tools/runstatus/node_modules" ] || fail "save did not replace node_modules with a symlink"
[ -d "$repo/.capsules/cache/runstatus-node-modules/$key/node_modules" ] || fail "save did not create the shared cache"
[ "$(cat "$repo/tools/runstatus/node_modules/.kitsoki-cache-key")" = "$key" ] || fail "saved cache marker did not match key"

rm "$repo/tools/runstatus/node_modules"
(cd "$repo" && "$cache_script" restore) >/tmp/kitsoki-runstatus-cache-restore.log
[ -L "$repo/tools/runstatus/node_modules" ] || fail "restore did not create a node_modules symlink"
[ -e "$repo/tools/runstatus/node_modules/.bin/tsx" ] || fail "restored cache is missing tsx"
(cd "$repo" && "$cache_script" valid) || fail "valid rejected restored node_modules"
(cd "$repo/tools/runstatus" && ./node_modules/.bin/tsx) || fail "restored immutable cache binary was not directly executable"

cat >"$repo/tools/runstatus/pnpm-lock.yaml" <<'YAML'
lockfileVersion: '9.0'
packages:
  changed: true
YAML
if (cd "$repo" && "$cache_script" restore) >/tmp/kitsoki-runstatus-cache-miss.log 2>&1; then
  fail "restore succeeded for an uncached changed lockfile"
fi
(cd "$repo" && "$cache_script" prepare-install)
[ ! -e "$repo/tools/runstatus/node_modules" ] || fail "prepare-install did not remove stale cache symlink"

protected_repo="$tmp/protected-repo"
mkdir -p "$protected_repo/tools/runstatus/node_modules/.bin"
git -C "$tmp" init --quiet protected-repo
cp "$repo/tools/runstatus/package.json" "$protected_repo/tools/runstatus/package.json"
cat >"$protected_repo/tools/runstatus/pnpm-lock.yaml" <<'YAML'
lockfileVersion: '9.0'
YAML
cat >"$protected_repo/tools/runstatus/node_modules/.modules.yaml" <<'YAML'
layoutVersion: 5
YAML
printf '#!/usr/bin/env sh\nexit 0\n' >"$protected_repo/tools/runstatus/node_modules/.bin/tsx"
chmod +x "$protected_repo/tools/runstatus/node_modules/.bin/tsx"
cat >"$protected_repo/.kitsoki-dev-workspace.json" <<JSON
{"root":"$protected_repo/.capsules/workspaces"}
JSON
chmod u-w "$protected_repo"
(cd "$protected_repo" && "$cache_script" save) >/tmp/kitsoki-runstatus-cache-protected-save.log
[ -d "$protected_repo/.capsules/cache/runstatus-node-modules" ] || fail "protected save did not create .capsules cache"
protected_mode="$(python3 - "$protected_repo" <<'PY'
import os
import stat
import sys
print("w" if os.stat(sys.argv[1]).st_mode & stat.S_IWUSR else "-")
PY
)"
[ "$protected_mode" = "-" ] || fail "protected save did not relock repo root"

echo "runstatus node_modules cache tests passed"
