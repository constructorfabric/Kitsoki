#!/usr/bin/env bash
# Hermetic install + K1 red-team proof. The temporary repo is intentional: this
# test verifies Git hook installation and exact Git command behavior.
set -euo pipefail
pack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
repo="$tmp/repo"
git init -q -b main "$repo"
git -C "$repo" config user.name "Launch Policy Gate"
git -C "$repo" config user.email "launch-policy@example.invalid"
git -C "$repo" commit -q --allow-empty -m init

"$pack_dir/install.sh" "$repo" --no-siblings
test -x "$repo/.claude/hooks/block-bare-checkout.sh"
test -x "$repo/.kitsoki/bin/claude"
test -x "$repo/.kitsoki/bin/codex"
test -f "$repo/.kitsoki/launch-policy.sh"
grep -q 'KITSOKI_AGENT_CLAUDE_BIN' "$repo/.kitsoki/launch-policy.sh"
node -e 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))' "$repo/.claude/settings.json"
grep -q 'require_capsule: false' "$repo/.kitsoki.local.yaml"
grep -q '\.capsules/workspaces' "$repo/.kitsoki.local.yaml"

if git -C "$repo" checkout -q -b should-be-blocked 2>/dev/null; then
  echo "FAIL: reference-transaction did not pin primary checkout" >&2
  exit 1
fi
(cd "$repo" && scripts/launch-policy-gate.sh)

out="$("$pack_dir/install.sh" "$repo" --no-siblings)"
grep -q 'no changes' <<<"$out" || { echo "FAIL: installer was not idempotent" >&2; exit 1; }

# The operating-principles managed block: created in AGENTS.md/README.md,
# refreshed in place on upgrade, and everything outside the markers survives.
grep -q 'BEGIN kitsoki:launch-policy' "$repo/AGENTS.md" || { echo "FAIL: AGENTS.md missing managed block" >&2; exit 1; }
grep -q 'BEGIN kitsoki:launch-policy' "$repo/README.md" || { echo "FAIL: README.md missing managed block" >&2; exit 1; }
printf '# My Repo\n\nlocal preamble\n\n' > "$repo/AGENTS.md"
"$pack_dir/install.sh" "$repo" --no-siblings >/dev/null
grep -q 'local preamble' "$repo/AGENTS.md" || { echo "FAIL: consumer AGENTS.md content lost" >&2; exit 1; }
grep -q 'BEGIN kitsoki:launch-policy' "$repo/AGENTS.md" || { echo "FAIL: block not re-added to rewritten AGENTS.md" >&2; exit 1; }
sed_i() { sed "$1" "$2" > "$2.tmp" && mv "$2.tmp" "$2"; }
sed_i 's/Launch through the shims/HAND EDIT INSIDE BLOCK/' "$repo/AGENTS.md"
"$pack_dir/install.sh" "$repo" --no-siblings >/dev/null
grep -q 'HAND EDIT INSIDE BLOCK' "$repo/AGENTS.md" && { echo "FAIL: upgrade did not refresh block content" >&2; exit 1; }
grep -q 'local preamble' "$repo/AGENTS.md" || { echo "FAIL: refresh clobbered content outside markers" >&2; exit 1; }
count="$(grep -c 'BEGIN kitsoki:launch-policy' "$repo/AGENTS.md")"
[ "$count" -eq 1 ] || { echo "FAIL: $count managed blocks after refresh" >&2; exit 1; }
echo "PASS: operating-principles managed block upsert"

# A divergent managed file must be skipped without aborting the rest of the
# install, and the exit code must flag the partial convergence.
repo2="$tmp/repo2"
git init -q -b main "$repo2"
git -C "$repo2" config user.name "Launch Policy Gate"
git -C "$repo2" config user.email "launch-policy@example.invalid"
git -C "$repo2" commit -q --allow-empty -m init
echo "# locally customized" > "$repo2/.kitsoki.local.yaml"
rc=0
"$pack_dir/install.sh" "$repo2" --no-siblings 2>/dev/null || rc=$?
[ "$rc" -eq 3 ] || { echo "FAIL: skip did not exit 3 (got $rc)" >&2; exit 1; }
grep -q '# locally customized' "$repo2/.kitsoki.local.yaml" || { echo "FAIL: divergent file was overwritten" >&2; exit 1; }
test -x "$repo2/.kitsoki/bin/claude" || { echo "FAIL: skip aborted the remaining install" >&2; exit 1; }
test -x "$repo2/scripts/launch-policy-gate.sh" || { echo "FAIL: gate script missing after skip" >&2; exit 1; }
echo "PASS: divergent-file skip continues installing"

# The activation file must self-locate under zsh (BASH_SOURCE is unset there)
# and keep PATH idempotent across repeated sourcing. It resolves physical
# paths (pwd -P), so compare against the symlink-resolved repo root.
repo_phys="$(cd "$repo" && pwd -P)"
for shell in bash zsh; do
  command -v "$shell" >/dev/null 2>&1 || continue
  got="$("$shell" -c "source '$repo/.kitsoki/launch-policy.sh'; source '$repo/.kitsoki/launch-policy.sh'; printf '%s\n%s' \"\$KITSOKI_AGENT_CLAUDE_BIN\" \"\$PATH\"")"
  bin_line="${got%%$'\n'*}"
  path_line="${got#*$'\n'}"
  [ "$bin_line" = "$repo_phys/.kitsoki/bin/claude" ] || { echo "FAIL: $shell resolved KITSOKI_AGENT_CLAUDE_BIN to $bin_line" >&2; exit 1; }
  count="$(printf '%s' "$path_line" | tr ':' '\n' | grep -cx "$repo_phys/.kitsoki/bin")" || true
  [ "$count" -eq 1 ] || { echo "FAIL: $shell PATH has $count shim entries after double-source" >&2; exit 1; }
done
echo "PASS: activation file resolves under bash and zsh with idempotent PATH"
"$pack_dir/test-delegation.sh"
echo "PASS: launch-policy pack install and red-team gate"
