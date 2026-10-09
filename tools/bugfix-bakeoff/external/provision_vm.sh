#!/usr/bin/env bash
# provision_vm.sh — make a fresh bake-off VM able to run a live cell with ZERO
# per-command tweaking. Idempotent; safe to re-run. Run AS the bake-off user
# (root on the pilot VM) directly on the box:
#
#   ssh root@<vm> 'bash -s' < tools/bugfix-bakeoff/external/provision_vm.sh
#
# It provisions the HARNESS layer the bake-off assumed but never installed (the
# gap that made live cells silently fail as "driver transient failure after
# retries"):
#
#   - codex CLI      — the ORCHESTRATOR (drives the kitsoki studio MCP) on
#                      ChatGPT subscription auth. MCP_DRIVE_MODEL=gpt-5.5.
#   - claude CLI     — the WORKER harness. The `synthetic-claude` profile points
#                      it at synthetic.new via ANTHROPIC_BASE_URL +
#                      ANTHROPIC_AUTH_TOKEN=${SYNTHETIC_API_KEY}, so GLM-5.2 is
#                      the worker. (No Anthropic account is used.)
#   - kitsoki        — cloned/refreshed from bsacrobatix/kitsoki main, then
#                      installed onto PATH (/usr/local/bin) so drive.sh and the
#                      studio MCP resolve it.
#   - env scaffold   — /etc/profile.d/bakeoff-env.sh (cache/PATH knobs).
#
# SECRETS are NOT baked here (never commit them). Inject them separately, once,
# from the operator machine — see `provision_vm_secrets()` notes at the bottom
# and docs: .context/2026-06-28-bakeoff-vm-codex-orchestrator-provisioning.md.
set -euo pipefail

KITSOKI_DIR="${KITSOKI_DIR:-/opt/bakeoff/repos/kitsoki}"
KITSOKI_REMOTE_NAME="${KITSOKI_REMOTE_NAME:-origin}"
KITSOKI_REMOTE_URL="${KITSOKI_REMOTE_URL:-https://github.com/bsacrobatix/kitsoki.git}"
KITSOKI_BRANCH="${KITSOKI_BRANCH:-main}"
log() { printf '[provision-vm] %s\n' "$*"; }

# --- 1. system deps -----------------------------------------------------------
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y >/dev/null
  apt-get install -y git jq curl ca-certificates build-essential python3 python3-venv python3-pip nodejs npm >/dev/null
fi

# Go (worker oracle for kitsoki-self is `go test`); skip if already present.
if ! command -v go >/dev/null 2>&1 && [[ ! -x /usr/local/go/bin/go ]]; then
  log "installing Go toolchain"
  curl -fsSL https://go.dev/dl/go1.23.0.linux-amd64.tar.gz -o /tmp/go.tar.gz
  tar -C /usr/local -xzf /tmp/go.tar.gz && rm -f /tmp/go.tar.gz
fi
export PATH="/usr/local/go/bin:/usr/local/bin:${PATH}"

# --- 1b. git identity ---------------------------------------------------------
# The worker's bf__implementer commits its fix via the host (`git.commit`). A
# fresh root box has no global identity, so the commit fails with "Author
# identity unknown" — the implementer errors, the pipeline bounces to bf.idle,
# and the cell scores `failed` on an UNCOMMITTED (but otherwise complete) fix.
git config --global user.email >/dev/null 2>&1 || git config --global user.email "bakeoff@kitsoki.local"
git config --global user.name  >/dev/null 2>&1 || git config --global user.name  "Kitsoki Bakeoff"
log "git identity: $(git config --global user.name) <$(git config --global user.email)>"

# --- 2. harness CLIs (orchestrator + worker) ----------------------------------
# npm global prefix is /usr/local by default → both land on PATH.
log "installing codex + claude-code CLIs (npm -g)"
npm i -g @openai/codex @anthropic-ai/claude-code >/dev/null 2>&1
log "codex:  $(command -v codex)  $(codex --version 2>&1 | head -1)"
log "claude: $(command -v claude)  $(claude --version 2>&1 | head -1)"

# --- 3. kitsoki checkout + PATH install --------------------------------------
sync_kitsoki_checkout() {
  mkdir -p "$(dirname "$KITSOKI_DIR")"
  if [[ ! -d "$KITSOKI_DIR/.git" ]]; then
    if [[ -e "$KITSOKI_DIR" ]]; then
      log "ERROR: $KITSOKI_DIR exists but is not a git checkout"
      return 1
    fi
    log "cloning kitsoki from ${KITSOKI_REMOTE_URL} (${KITSOKI_BRANCH}) into $KITSOKI_DIR"
    git clone --origin "$KITSOKI_REMOTE_NAME" --branch "$KITSOKI_BRANCH" --single-branch \
      "$KITSOKI_REMOTE_URL" "$KITSOKI_DIR"
    return 0
  fi

  log "refreshing kitsoki checkout from ${KITSOKI_REMOTE_URL} ${KITSOKI_BRANCH}"
  git -C "$KITSOKI_DIR" remote get-url "$KITSOKI_REMOTE_NAME" >/dev/null 2>&1 \
    || git -C "$KITSOKI_DIR" remote add "$KITSOKI_REMOTE_NAME" "$KITSOKI_REMOTE_URL"
  git -C "$KITSOKI_DIR" remote set-url "$KITSOKI_REMOTE_NAME" "$KITSOKI_REMOTE_URL"
  git -C "$KITSOKI_DIR" fetch "$KITSOKI_REMOTE_NAME" "$KITSOKI_BRANCH"
  if git -C "$KITSOKI_DIR" show-ref --verify --quiet "refs/heads/$KITSOKI_BRANCH"; then
    git -C "$KITSOKI_DIR" checkout "$KITSOKI_BRANCH"
  else
    git -C "$KITSOKI_DIR" checkout -b "$KITSOKI_BRANCH" "$KITSOKI_REMOTE_NAME/$KITSOKI_BRANCH"
  fi
  git -C "$KITSOKI_DIR" pull --ff-only "$KITSOKI_REMOTE_NAME" "$KITSOKI_BRANCH"
}

sync_kitsoki_checkout
log "installing kitsoki from $KITSOKI_DIR"
make -C "$KITSOKI_DIR" install >/dev/null 2>&1 || {
  # Fallback: go install straight onto PATH if the full make target is heavy.
  GOBIN=/usr/local/bin go -C "$KITSOKI_DIR" install ./cmd/kitsoki
}
command -v kitsoki >/dev/null && log "kitsoki: $(command -v kitsoki)" || log "WARN: kitsoki not on PATH"

# --- 4. env scaffold (non-secret) ---------------------------------------------
cat >/etc/profile.d/bakeoff-env.sh <<ENV
export KITSOKI_ROOT=${KITSOKI_DIR}
export PATH="/usr/local/go/bin:/usr/local/bin:/root/go/bin:\$PATH"
export EXTERNAL_BAKEOFF_CACHE="${KITSOKI_DIR}/.artifacts/external-bakeoff"
export MCP_DRIVE_MODEL=gpt-5.5
export MCP_DRIVE_MAX_ATTEMPTS=12
export MCP_DRIVE_BACKOFF_BASE=10
export MCP_DRIVE_BACKOFF_MAX=600
# The VM is a disposable ROOT sandbox. claude-code refuses
# --dangerously-skip-permissions as root unless IS_SANDBOX=1 — without it the
# worker fast-fails before any LLM call (acceptance failed, empty output) and the
# bake-off silently scores an unchanged tree.
export IS_SANDBOX=1
# codex (the orchestrator) does NOT forward this process env to the MCP server it
# spawns, so the worker harness is configured via ~/.codex/config.toml instead
# (see provision_vm_secrets notes). This tells drive.sh to emit no -c mcp
# overrides and let codex use that config (keeps SYNTHETIC_API_KEY off argv).
export MCP_DRIVE_CODEX_CONFIG_MCP=1
export GOCACHE=/opt/bakeoff/.cache/go
export CARGO_HOME=/opt/bakeoff/.cache/cargo
export CARGO_TARGET_DIR=/opt/bakeoff/.cache/cargo-target
ENV
mkdir -p /opt/bakeoff/.cache/{go,cargo,cargo-target}
log "wrote /etc/profile.d/bakeoff-env.sh"

# --- 5. verify (fail loudly on a missing piece) -------------------------------
miss=0
for bin in codex claude kitsoki go python3 jq; do
  command -v "$bin" >/dev/null || { log "MISSING: $bin"; miss=1; }
done
[[ -f /root/.codex/auth.json ]] || { log "MISSING SECRET: /root/.codex/auth.json (codex subscription auth — scp from operator)"; miss=1; }
[[ -f /etc/profile.d/bakeoff-secrets.sh ]] || { log "MISSING SECRET: /etc/profile.d/bakeoff-secrets.sh (SYNTHETIC_API_KEY — inject from operator)"; miss=1; }
if ! grep -q "mcp_servers.kitsoki.env" /root/.codex/config.toml 2>/dev/null; then
  log "MISSING: ~/.codex/config.toml [mcp_servers.kitsoki.env] (IS_SANDBOX + SYNTHETIC_API_KEY — codex won't forward parent env to the worker MCP; see secret-injection step 3)"; miss=1
fi
if command -v codex >/dev/null && [[ -f /root/.codex/auth.json ]]; then
  codex doctor 2>&1 | grep -qi 'auth is configured' && log "codex auth: OK" || { log "codex auth: NOT configured"; miss=1; }
fi
if [[ $miss -eq 0 ]]; then
  log "PROVISIONED — ready for: tools/bugfix-bakeoff/external/drive_cell.sh --project <p> --bug <b> --candidate glm-5.2 --repo-dir $KITSOKI_DIR --score"
else
  log "INCOMPLETE — inject the secrets above, then re-run to verify."
  exit 1
fi

# ── Secret injection (run from the OPERATOR machine, not here) ────────────────
# 1. Synthetic key (GLM worker auth):
#      printenv SYNTHETIC_API_KEY | ssh root@<vm> \
#        'umask 077; KEY=$(cat); printf "export SYNTHETIC_API_KEY=%q\n" "$KEY" \
#         > /etc/profile.d/bakeoff-secrets.sh; chmod 600 /etc/profile.d/bakeoff-secrets.sh'
# 2. Codex subscription auth (orchestrator):
#      ssh root@<vm> 'mkdir -p /root/.codex && chmod 700 /root/.codex'
#      scp ~/.codex/auth.json root@<vm>:/root/.codex/auth.json
#      ssh root@<vm> 'chmod 600 /root/.codex/auth.json'
# 3. Codex MCP worker env (~/.codex/config.toml). codex does NOT forward the
#    parent env to MCP subprocesses, so the kitsoki studio MCP — which forks the
#    GLM worker (claude-code → synthetic.new) — needs IS_SANDBOX + the synthetic
#    key declared on the server itself. Written ONCE on the VM (secret off argv):
#      ssh root@<vm> 'source /etc/profile.d/bakeoff-secrets.sh; umask 077
#        cat > /root/.codex/config.toml <<TOML
#      model = "gpt-5.5"
#      model_reasoning_effort = "medium"
#      approvals_reviewer = "user"
#
#      [projects."/opt/bakeoff/repos/kitsoki"]
#      trust_level = "trusted"
#
#      [mcp_servers.kitsoki]
#      command = "kitsoki"
#      args = ["mcp", "--stories-dir", "stories"]
#      startup_timeout_sec = 60
#      # A live GLM-5.2 worker turn runs many minutes; codex's default per-tool
#      # timeout (~60s) would abort session.drive/submit mid-turn → the work is
#      # never persisted and the pipeline stays stuck at bf.idle.
#      tool_timeout_sec = 3600
#      [mcp_servers.kitsoki.env]
#      IS_SANDBOX = "1"
#      SYNTHETIC_API_KEY = "\${SYNTHETIC_API_KEY}"
#      TOML
#      chmod 600 /root/.codex/config.toml'
