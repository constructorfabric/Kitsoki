#!/usr/bin/env bash
# Installed-pack acceptance proof for the K1 policy-aware launcher shims.
# Proves, against the real `kitsoki agent launch` policy gate and the real
# installed shim script (only the claude/codex backends themselves are
# faked, so no LLM ever runs):
#
#   1. policy denial blocks the backend (nothing gets invoked);
#   2. policy approval delegates to the real backend exactly once;
#   3. adversarial/native argv survives the round trip unmangled and
#      unevaluated (spaces, `--`, shell metacharacters);
#   4. KITSOKI_AGENT_*_BIN pointing at the shim itself — the activation
#      script's own standing configuration — does not recurse.
set -euo pipefail
pack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$pack_dir/../.." && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

repo="$tmp/repo"
git init -q -b main "$repo"
git -C "$repo" config user.name "Launch Policy Acceptance"
git -C "$repo" config user.email "launch-policy@example.invalid"
git -C "$repo" commit -q --allow-empty -m init

"$pack_dir/install.sh" "$repo" --no-siblings >/dev/null

# A workspace the installed policy's allowed_roots carves out. It is its own
# git repo on a non-protected branch, mirroring a real dev-workspace.sh
# clone-backed capsule: allowed_roots is a path-containment carve-out, and a
# nested checkout still needs its own branch off `main`/`staging/*` to clear
# the protected-branch check.
workspace="$repo/.capsules/workspaces/ws1"
git init -q -b work "$workspace"
git -C "$workspace" config user.name "Launch Policy Acceptance"
git -C "$workspace" config user.email "launch-policy@example.invalid"
git -C "$workspace" commit -q --allow-empty -m init

echo "running kitsoki through go run (real policy gate; only claude/codex backends are faked)..." >&2
# Keep this proof on the source surface: the shim only needs an executable
# KITSOKI_BIN, so a tiny runner gives it one without leaving a test binary
# behind. Resolve Go before the env -i calls below intentionally narrow PATH.
go_bin="$(command -v go)"
kitsoki_bin="$tmp/kitsoki"
cat > "$kitsoki_bin" <<SCRIPT
#!/usr/bin/env bash
set -euo pipefail
cd "$repo_root"
exec "$go_bin" run ./cmd/kitsoki "\$@"
SCRIPT
chmod +x "$kitsoki_bin"

# Fake backends: never a real LLM. Each records its invocation + full argv.
realbin="$tmp/realbin"
mkdir -p "$realbin"
log="$tmp/invocations.log"
: > "$log"
for backend in claude codex; do
  cat > "$realbin/$backend" <<SCRIPT
#!/usr/bin/env bash
{
  printf '=== %s invocation ===\n' "$backend"
  for a in "\$@"; do printf 'ARG:%s\n' "\$a"; done
} >> "$log"
exit 0
SCRIPT
  chmod +x "$realbin/$backend"
done

# Minimal, explicit PATH: the installed shim first, then the fake backends,
# then just enough host PATH for git/coreutils/awk. This never lets the
# operator's own claude/codex on PATH leak into the proof.
minimal_path="$repo/.kitsoki/bin:$realbin:/usr/bin:/bin"

# Mirrors sourcing .kitsoki/launch-policy.sh: PATH.kitsoki/bin first, and both
# override vars pointed AT the shim itself. This is the standing activated
# configuration, not an edge case — item 4 below is proving the normal path.
run_launch() {
  local backend="$1" workdir="$2"; shift 2
  run_with_timeout 15 bash -c '
    cd "$1" && shift
    exec env -i \
      HOME="$HOME" \
      PATH="$MINIMAL_PATH" \
      KITSOKI_BIN="$KITSOKI_BIN_PATH" \
      KITSOKI_AGENT_CLAUDE_BIN="$KITSOKI_AGENT_CLAUDE_BIN" \
      KITSOKI_AGENT_CODEX_BIN="$KITSOKI_AGENT_CODEX_BIN" \
      "$0" "$@"
  ' "$backend" "$workdir" "$@" < /dev/null
}

# macOS ships no `timeout(1)`. A recursion regression in the shim under test
# would otherwise hang this script forever instead of failing it.
run_with_timeout() {
  local secs="$1"; shift
  MINIMAL_PATH="$minimal_path" KITSOKI_BIN_PATH="$kitsoki_bin" \
    KITSOKI_AGENT_CLAUDE_BIN="$repo/.kitsoki/bin/claude" \
    KITSOKI_AGENT_CODEX_BIN="$repo/.kitsoki/bin/codex" \
    "$@" &
  local pid=$!
  ( sleep "$secs" && kill -9 "$pid" 2>/dev/null ) &
  local watchdog=$!
  local status=0
  wait "$pid" || status=$?
  kill "$watchdog" 2>/dev/null || true
  wait "$watchdog" 2>/dev/null || true
  if [ "$status" -eq 137 ] || [ "$status" -eq 143 ]; then
    fail "command timed out after ${secs}s (possible recursion): $*"
  fi
  return "$status"
}

fail() { echo "FAIL: $1" >&2; exit 1; }

# --- 1: policy denial blocks the backend -----------------------------------
: > "$log"
if out="$(run_launch claude "$repo" --model x 2>&1)"; then
  fail "denied launch (protected primary checkout) was allowed"
fi
grep -q "agent launch policy denied" <<<"$out" || fail "denial did not surface a policy-denied error: $out"
[ ! -s "$log" ] || fail "denied launch still invoked a backend: $(cat "$log")"
echo "PASS: policy denial blocks the backend before it is ever invoked"

# --- 2+3: policy approval delegates exactly once, argv survives unmangled --
: > "$log"
run_launch claude "$workspace" --model weird 'arg with spaces' -- --native-flag=1 '$(echo injected)' \
  || fail "approved claude launch failed"
[ "$(grep -c '^=== claude invocation ===$' "$log")" -eq 1 ] \
  || fail "expected exactly one claude invocation, got: $(cat "$log")"
grep -qx 'ARG:arg with spaces' "$log" || fail "argv with embedded spaces was mangled"
grep -qx 'ARG:$(echo injected)' "$log" || fail "shell metacharacters in argv were evaluated instead of passed through literally"
grep -qx 'ARG:--' "$log" || fail "the bare -- separator was lost"
echo "PASS: policy approval delegates to the real backend exactly once, argv unmangled"

# --- 2+3 again on codex, with shell-injection-shaped argv ------------------
: > "$log"
run_launch codex "$workspace" -m fast '; rm -rf /' '`whoami`' \
  || fail "approved codex launch failed"
[ "$(grep -c '^=== codex invocation ===$' "$log")" -eq 1 ] \
  || fail "expected exactly one codex invocation, got: $(cat "$log")"
grep -qx 'ARG:; rm -rf /' "$log" || fail "codex argv injection payload was altered"
grep -qx 'ARG:`whoami`' "$log" || fail "codex argv backtick payload was evaluated instead of passed through literally"
echo "PASS: adversarial native argv (shell metacharacters, injection-shaped strings) survives unevaluated"

# --- 4: KITSOKI_AGENT_*_BIN pointing at the shim does not recurse ----------
# Exercised implicitly by every run_launch call above (KITSOKI_AGENT_*_BIN is
# always set to the shim path itself, matching the activation script). Prove
# it explicitly and prove the hard depth-cap backstop fails fast instead of
# hanging when a real backend genuinely cannot be found.
backstop_out="$tmp/backstop.out"
: > "$backstop_out"
env -i HOME="$HOME" PATH="$repo/.kitsoki/bin:/usr/bin:/bin" \
  KITSOKI_AGENT_LAUNCH_SHIM_ACTIVE=1 \
  "$repo/.kitsoki/bin/claude" --model x > "$backstop_out" 2>&1 &
backstop_pid=$!
( sleep 15 && kill -9 "$backstop_pid" 2>/dev/null ) &
watchdog_pid=$!
backstop_status=0
wait "$backstop_pid" || backstop_status=$?
kill "$watchdog_pid" 2>/dev/null || true
wait "$watchdog_pid" 2>/dev/null || true
[ "$backstop_status" -ne 137 ] && [ "$backstop_status" -ne 143 ] \
  || fail "shim with no real backend on PATH hung instead of failing fast (recursion)"
[ "$backstop_status" -ne 0 ] || fail "shim with no real backend on PATH should have failed, not succeeded"
grep -q "real claude binary not found" "$backstop_out" || fail "expected a real-binary-not-found error, got: $(cat "$backstop_out")"
echo "PASS: KITSOKI_AGENT_*_BIN pointing at the shim does not recurse (backstop fails fast, no hang)"

echo "PASS: launch-policy shim delegation acceptance proof"
