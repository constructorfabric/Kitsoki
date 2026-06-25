#!/usr/bin/env bash
#
# Build and deploy the hosted @kitsoki GitHub agent binary to the test VM.
#
# This script intentionally requires --yes before it mutates the remote host.
# Without --yes it prints the exact commands it would run.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REMOTE="${KITSOKI_GH_AGENT_REMOTE:-root@206.189.84.218}"
REMOTE_BIN="${KITSOKI_GH_AGENT_REMOTE_BIN:-/usr/local/bin/kitsoki}"
SERVICE="${KITSOKI_GH_AGENT_SERVICE:-kitsoki-gh-agent}"
PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-https://kitsoki-test.slothattax.me}"
OUT="${KITSOKI_GH_AGENT_BUILD_OUT:-/private/tmp/kitsoki-ghagent}"
GOCACHE="${GOCACHE:-/private/tmp/kitsoki-gocache}"

YES=0
if [ "${1:-}" = "--yes" ]; then
	YES=1
	shift
fi
if [ "$#" -ne 0 ]; then
	echo "usage: scripts/deploy-gh-agent.sh [--yes]" >&2
	exit 2
fi

build_cmd=(go build -o "$OUT" ./cmd/kitsoki)
scp_cmd=(scp "$OUT" "$REMOTE:$REMOTE_BIN")
ssh_cmd=(ssh "$REMOTE" "chmod 755 '$REMOTE_BIN' && systemctl restart '$SERVICE'")
health_cmd=(curl -fsS "$PUBLIC_BASE_URL/healthz")

print_cmd() {
	printf '%q ' "$@"
	printf '\n'
}

cat <<EOF
deploy-gh-agent:
  build:  GOOS=linux GOARCH=amd64 GOCACHE=$GOCACHE $(print_cmd "${build_cmd[@]}")
  copy:   $(print_cmd "${scp_cmd[@]}")
  start:  $(print_cmd "${ssh_cmd[@]}")
  health: $(print_cmd "${health_cmd[@]}")
EOF

if [ "$YES" -ne 1 ]; then
	cat <<'EOF'

dry run only. Re-run with --yes to copy the binary to the VM and restart the service.
EOF
	exit 0
fi

GOOS=linux GOARCH=amd64 GOCACHE="$GOCACHE" "${build_cmd[@]}"
"${scp_cmd[@]}"
"${ssh_cmd[@]}"
"${health_cmd[@]}"
