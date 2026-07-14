#!/usr/bin/env bash
# Installed as .kitsoki/bin/{claude,codex}. It is intentionally tiny: policy
# resolution stays in `kitsoki agent launch`, while this wrapper preserves the
# exact native CLI argv that the caller supplied.
set -euo pipefail

backend="$(basename "$0")"
case "$backend" in claude|codex) ;; *) echo "unsupported Kitsoki launcher shim: $backend" >&2; exit 2 ;; esac

upper="$(printf '%s' "$backend" | tr '[:lower:]' '[:upper:]')"
override="KITSOKI_AGENT_${upper}_BIN"
real_override="KITSOKI_AGENT_${upper}_REAL_BIN"

shim_depth="${KITSOKI_AGENT_LAUNCH_SHIM_ACTIVE:-0}"
if [ "$shim_depth" -ge 1 ]; then
  # Hard backstop: a correct policy-approved launch only ever re-enters this
  # branch once (the launch plan's resolved binary lands back on this same
  # shim via PATH/override). If that ever fails to converge — e.g. a PATH
  # entry reaches this shim's directory through a symlink that doesn't
  # string-match its canonicalized form below — fail loudly instead of
  # forking itself forever.
  if [ "$shim_depth" -ge 3 ]; then
    echo "Kitsoki launcher shim: recursion guard tripped after $shim_depth levels; refusing to loop (set $real_override to break the cycle)" >&2
    exit 111
  fi
  real="${!real_override:-}"
  if [ -z "$real" ]; then
    shim_dir="$(cd "$(dirname "$0")" && pwd -P)"
    old_ifs="$IFS"
    new_path=""
    IFS=:
    for entry in $PATH; do
      [ -n "$entry" ] || continue
      # Compare canonicalized forms on both sides — $entry may reach this
      # same directory through a symlink (e.g. macOS's /tmp -> /private/tmp)
      # that would not string-match $shim_dir otherwise.
      entry_resolved="$(cd "$entry" 2>/dev/null && pwd -P || printf '%s' "$entry")"
      if [ "$entry_resolved" != "$shim_dir" ]; then
        new_path="${new_path:+$new_path:}$entry"
      fi
    done
    IFS="$old_ifs"
    PATH="$new_path"
    real="$(command -v "$backend" || true)"
  fi
  [ -n "$real" ] || { echo "Kitsoki launcher shim: real $backend binary not found; set $real_override" >&2; exit 127; }
  export KITSOKI_AGENT_LAUNCH_SHIM_ACTIVE=$((shim_depth + 1))
  exec "$real" "$@"
fi

kitsoki_bin="${KITSOKI_BIN:-kitsoki}"
# kitsoki agent launch reads --config literally relative to its own process
# CWD, with no upward search. The caller may have cd'd anywhere (that is the
# whole point of --working-dir "$PWD" below), so the installed pack's own
# config must be passed explicitly here or the policy gate silently loads an
# empty config and no-ops the moment the caller isn't sitting in the repo
# root that owns .kitsoki.yaml.
install_root="$(cd "$(dirname "$0")/../.." && pwd -P)"
args=(agent launch --raw --interactive --backend "$backend" --working-dir "$PWD" --config "$install_root/.kitsoki.yaml")
for arg in "$@"; do args+=(--raw-arg "$arg"); done
KITSOKI_AGENT_LAUNCH_SHIM_ACTIVE=1 "$kitsoki_bin" "${args[@]}"
