#!/usr/bin/env bash
# Install K1 launch-policy layers into a Git repository.
set -euo pipefail

pack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
force=0
siblings=1
target=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --force) force=1; shift ;;
    --no-siblings) siblings=0; shift ;;
    -h|--help) echo "usage: install.sh <target-repo> [--force] [--no-siblings]"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *) [ -z "$target" ] || { echo "one target repository is required" >&2; exit 2; }; target="$1"; shift ;;
  esac
done
[ -n "$target" ] || { echo "usage: install.sh <target-repo> [--force] [--no-siblings]" >&2; exit 2; }
git -C "$target" rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "$target is not a Git repository" >&2; exit 2; }
target="$(git -C "$target" rev-parse --show-toplevel)"

changed=0
skipped=0
install_file() {
  local src="$1" dst="$2" mode="$3"
  if [ -e "$dst" ] && cmp -s "$src" "$dst"; then return; fi
  if [ -e "$dst" ] && [ "$force" -ne 1 ]; then
    echo "SKIP $dst differs locally (use --force after review)" >&2
    skipped=$((skipped + 1))
    return 0
  fi
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
  chmod "$mode" "$dst"
  changed=$((changed + 1))
  echo "installed ${dst#$target/}"
}

roots="    - ."
if [ "$siblings" -eq 1 ]; then
  parent="$(cd "$target/.." && pwd -P)"
  for sibling in "$parent"/*; do
    [ -d "$sibling/.git" ] || continue
    sibling="$(cd "$sibling" && pwd -P)"
    [ "$sibling" = "$target" ] && continue
    roots="$roots\n    - ../$(basename "$sibling")"
  done
fi
policy_tmp="$(mktemp)"
trap 'rm -f "$policy_tmp"' EXIT
sed "s|{{PROTECTED_ROOTS}}|$roots|" "$pack_dir/templates/agent-launch-policy.yaml" > "$policy_tmp"
install_file "$policy_tmp" "$target/.kitsoki.local.yaml" 0644
install_file "$pack_dir/.claude/hooks/block-bare-checkout.sh" "$target/.claude/hooks/block-bare-checkout.sh" 0755
install_file "$pack_dir/templates/settings.json" "$target/.claude/settings.json" 0644
install_file "$pack_dir/scripts/launch-policy-gate.sh" "$target/scripts/launch-policy-gate.sh" 0755
install_file "$pack_dir/templates/agent-launcher-shim.sh" "$target/.kitsoki/bin/claude" 0755
install_file "$pack_dir/templates/agent-launcher-shim.sh" "$target/.kitsoki/bin/codex" 0755
install_file "$pack_dir/templates/launcher-env.sh" "$target/.kitsoki/launch-policy.sh" 0644

# The main-pinning git hook belongs only in a primary checkout. A managed
# capsule workspace (or any clone carrying the capsule sentinel/manifest) is
# the sanctioned place for branch work — installing the hook there locks the
# workspace onto main and breaks it.
if [ -e "$target/capsule-manifest.json" ] || [ -e "$target/.kitsoki-capsule" ] || [ -e "$target/.kitsoki-clone" ]; then
  echo "skipping reference-transaction hook: $target is a capsule workspace"
else
  hooks_dir="$(git -C "$target" rev-parse --git-path hooks)"
  case "$hooks_dir" in /*) ;; *) hooks_dir="$target/$hooks_dir" ;; esac
  install_file "$pack_dir/git-hooks/reference-transaction" "$hooks_dir/reference-transaction" 0755
fi

# Upsert the operating-principles section as a marker-delimited managed block
# in AGENTS.md and README.md, so pack/kit upgrades refresh it in place while
# everything outside the markers stays the consumer's own. Never SKIPs: the
# block is pack-owned by contract, unlike whole managed files.
DOC_BEGIN="<!-- BEGIN kitsoki:launch-policy (managed by pack/launch-policy/install.sh; edits inside are overwritten on upgrade) -->"
DOC_END="<!-- END kitsoki:launch-policy -->"
upsert_doc_block() {
  local dst="$1" body="$pack_dir/templates/operating-principles.md" tmp
  tmp="$(mktemp)"
  if [ -e "$dst" ] && grep -qF "$DOC_BEGIN" "$dst"; then
    awk -v begin="$DOC_BEGIN" -v end="$DOC_END" -v body="$body" '
      index($0, begin) == 1 {
        print
        while ((getline line < body) > 0) print line
        close(body)
        skipping = 1
        next
      }
      index($0, end) == 1 { skipping = 0 }
      !skipping { print }
    ' "$dst" > "$tmp"
  else
    { if [ -e "$dst" ]; then cat "$dst"; printf '\n'; fi
      printf '%s\n' "$DOC_BEGIN"
      cat "$body"
      printf '%s\n' "$DOC_END"; } > "$tmp"
  fi
  if [ -e "$dst" ] && cmp -s "$tmp" "$dst"; then rm -f "$tmp"; return; fi
  if [ -e "$dst" ] && [ ! -w "$dst" ]; then
    rm -f "$tmp"
    echo "SKIP $dst is not writable (protected checkout?); rerun from a workspace" >&2
    skipped=$((skipped + 1))
    return 0
  fi
  mv "$tmp" "$dst"
  changed=$((changed + 1))
  echo "updated ${dst#$target/} (kitsoki:launch-policy block)"
}
upsert_doc_block "$target/AGENTS.md"
upsert_doc_block "$target/README.md"

mkdir -p "$target/.capsules/workspaces"
if [ "$changed" -eq 0 ] && [ "$skipped" -eq 0 ]; then
  echo "launch-policy install: no changes"
else
  echo "launch-policy install: $changed file(s) installed, $skipped skipped"
fi
# Divergent managed files were left in place; signal the caller so automation
# doesn't treat a partial install as converged.
[ "$skipped" -eq 0 ] || exit 3
