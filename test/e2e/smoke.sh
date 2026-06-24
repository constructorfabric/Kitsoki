#!/usr/bin/env bash
#
# smoke.sh — in-container end-to-end smoke test for the kitsoki CLI.
#
# Runs INSIDE the runtime image produced by test/e2e/Dockerfile. It assumes:
#   - the `kitsoki` binary is on PATH,
#   - the deterministic flow fixtures live under $KITSOKI_E2E_ROOT (default /opt/kitsoki).
#
# It verifies two things, in order:
#   1. System dependencies — the external binaries kitsoki shells out to at
#      runtime are present (git, bash required; gh optional). A bad/slim base
#      image fails here, loudly, instead of deep inside a session.
#   2. The CLI actually works — version, embedded assets (docs/viz), and the
#      Mode-2 deterministic flow suites (no LLM, no API key, no network) pass.
#
# Exit code is 0 only if every required check passes. This is the signal the
# host-side runner (run.sh) and CI rely on.

set -uo pipefail

ROOT="${KITSOKI_E2E_ROOT:-/opt/kitsoki}"

# Apps whose flow suites are deterministic and known-green offline. Override
# with KITSOKI_E2E_APPS="cloak dev-story" to narrow/extend the set.
# NOTE: background_jobs and choice_smoke are intentionally excluded — they have
# pre-existing fixture failures unrelated to packaging/deps.
APPS="${KITSOKI_E2E_APPS:-cloak dev-story parallel_smoke proposal_smoke timeout}"

fail=0
pass_count=0

say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$*"; pass_count=$((pass_count+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; fail=1; }
warn() { printf '  \033[33mwarn\033[0m %s\n' "$*"; }

# --- 1. system dependencies -------------------------------------------------
say "system dependencies"
for bin in kitsoki git bash; do
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$bin on PATH ($(command -v "$bin"))"
  else
    bad "$bin missing from PATH (required runtime dependency)"
  fi
done
# gh is optional: PR host effects degrade gracefully without it.
if command -v gh >/dev/null 2>&1; then
  ok "gh on PATH ($(command -v gh)) — optional, present"
else
  warn "gh not on PATH — optional; PR effects will degrade gracefully"
fi

# --- 2. binary smoke --------------------------------------------------------
say "binary smoke"
if ver=$(kitsoki version 2>&1); then
  ok "kitsoki version -> ${ver}"
else
  bad "kitsoki version failed: ${ver}"
fi

# Embedded documentation (go:embed) is readable.
if kitsoki docs >/dev/null 2>&1; then
  ok "kitsoki docs (embedded topics list)"
else
  bad "kitsoki docs failed — embedded docs not packaged correctly"
fi

# State-machine visualisation: pure, exercises YAML load + emit, no LLM.
if kitsoki viz "${ROOT}/testdata/apps/cloak/app.yaml" >/dev/null 2>&1; then
  ok "kitsoki viz cloak/app.yaml"
else
  bad "kitsoki viz failed on cloak/app.yaml"
fi

# --- 3. deterministic flow suites (the real e2e) ----------------------------
say "deterministic flow suites (Mode 2, no LLM)"
for app in $APPS; do
  appyaml="${ROOT}/testdata/apps/${app}/app.yaml"
  if [ ! -f "$appyaml" ]; then
    bad "${app}: app.yaml not found at ${appyaml}"
    continue
  fi
  out=$(kitsoki test flows "$appyaml" 2>&1)
  ec=$?
  summary=$(printf '%s\n' "$out" | grep -iE '^Summary:' | tail -1)
  if [ "$ec" -eq 0 ]; then
    ok "${app}: ${summary:-all flows pass}"
  else
    bad "${app}: flows failed (exit ${ec})"
    printf '%s\n' "$out" | sed 's/^/        /'
  fi
done

# --- verdict ----------------------------------------------------------------
say "verdict"
if [ "$fail" -eq 0 ]; then
  printf '  \033[32mPASS\033[0m — %d checks passed\n' "$pass_count"
  exit 0
fi
printf '  \033[31mFAIL\033[0m — one or more checks failed\n'
exit 1
