#!/usr/bin/env bash
#
# Build and deploy the hosted @kitsoki GitHub agent binary to the test VM.
#
# This script intentionally requires --yes before it mutates the remote host.
# Without --yes it prints the exact commands it would run.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REMOTE="${KITSOKI_GH_AGENT_REMOTE:-}"
REMOTE_BIN="${KITSOKI_GH_AGENT_REMOTE_BIN:-/usr/local/bin/kitsoki}"
REMOTE_TMP="${KITSOKI_GH_AGENT_REMOTE_TMP:-/tmp/kitsoki-ghagent.$$}"
REMOTE_REPO="${KITSOKI_GH_AGENT_REMOTE_REPO:-/opt/kitsoki}"
SERVICE="${KITSOKI_GH_AGENT_SERVICE:-kitsoki-gh-agent}"
PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-}"
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
if [ -z "$REMOTE" ]; then
	echo "KITSOKI_GH_AGENT_REMOTE is required, for example deploy@gh-agent.example.com" >&2
	exit 2
fi
if [ -z "$PUBLIC_BASE_URL" ]; then
	echo "KITSOKI_GH_AGENT_PUBLIC_BASE_URL is required, for example https://gh-agent.example.com" >&2
	exit 2
fi

build_cmd=(go build -o "$OUT" ./cmd/kitsoki)
scp_cmd=(scp "$OUT" "$REMOTE:$REMOTE_TMP")
install_cmd=(ssh "$REMOTE" "install -m 755 '$REMOTE_TMP' '$REMOTE_BIN' && rm -f '$REMOTE_TMP'")
sync_repo_cmd=(ssh "$REMOTE" "mkdir -p '$REMOTE_REPO' && tar -x -C '$REMOTE_REPO'")
ssh_cmd=(ssh "$REMOTE" "chmod 755 '$REMOTE_BIN' && systemctl restart '$SERVICE'")
health_cmd=(curl -fsS "$PUBLIC_BASE_URL/healthz")
HEALTH_ATTEMPTS="${KITSOKI_GH_AGENT_HEALTH_ATTEMPTS:-12}"
HEALTH_SLEEP_SECONDS="${KITSOKI_GH_AGENT_HEALTH_SLEEP_SECONDS:-2}"

print_cmd() {
	printf '%q ' "$@"
	printf '\n'
}

checksum_file() {
	local file="$1"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file" | awk '{print $1}'
	else
		shasum -a 256 "$file" | awk '{print $1}'
	fi
}

sync_repo() {
	git archive --format=tar HEAD | "${sync_repo_cmd[@]}"
}

wait_for_health() {
	local attempt
	for attempt in $(seq 1 "$HEALTH_ATTEMPTS"); do
		if "${health_cmd[@]}"; then
			return 0
		fi
		if [ "$attempt" -lt "$HEALTH_ATTEMPTS" ]; then
			sleep "$HEALTH_SLEEP_SECONDS"
		fi
	done
	echo "health check failed after $HEALTH_ATTEMPTS attempt(s): ${health_cmd[*]}" >&2
	return 1
}

remote_checksum_cmd=(ssh "$REMOTE" "sha256sum '$REMOTE_BIN' | awk '{print \$1}'")

cat <<EOF
deploy-gh-agent:
  build:  GOOS=linux GOARCH=amd64 GOCACHE=$GOCACHE $(print_cmd "${build_cmd[@]}")
  copy:   $(print_cmd "${scp_cmd[@]}")
  install:$(print_cmd "${install_cmd[@]}")
  sync:   git archive --format=tar HEAD | $(print_cmd "${sync_repo_cmd[@]}")
  start:  $(print_cmd "${ssh_cmd[@]}")
  verify: local sha256 == remote sha256 via $(print_cmd "${remote_checksum_cmd[@]}")
  health: $(print_cmd "${health_cmd[@]}") (up to $HEALTH_ATTEMPTS attempts)
EOF

if [ "$YES" -ne 1 ]; then
	cat <<'EOF'

dry run only. Re-run with --yes to copy the binary to the VM and restart the service.
EOF
	exit 0
fi

GOOS=linux GOARCH=amd64 GOCACHE="$GOCACHE" "${build_cmd[@]}"
local_sha="$(checksum_file "$OUT")"
"${scp_cmd[@]}"
remote_tmp_sha="$(ssh "$REMOTE" "sha256sum '$REMOTE_TMP' | awk '{print \$1}'")"
if [ "$local_sha" != "$remote_tmp_sha" ]; then
	echo "remote upload checksum mismatch: local=$local_sha remote_tmp=$remote_tmp_sha" >&2
	ssh "$REMOTE" "rm -f '$REMOTE_TMP'" >/dev/null 2>&1 || true
	exit 1
fi
"${install_cmd[@]}"
sync_repo
remote_sha="$("${remote_checksum_cmd[@]}")"
if [ "$local_sha" != "$remote_sha" ]; then
	echo "remote binary checksum mismatch: local=$local_sha remote=$remote_sha" >&2
	exit 1
fi
echo "checksum ok: $remote_sha"
"${ssh_cmd[@]}"
wait_for_health
