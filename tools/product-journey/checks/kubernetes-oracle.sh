#!/usr/bin/env bash
set -euo pipefail

REPO=${KUBERNETES_REPO:-/Users/brad/code/kubernetes}
BASELINE_SHA=${KUBERNETES_BASELINE_SHA:-0c61430054aa4ac54e4855be2f2d9a9d7e645540}
FIX_SHA=${KUBERNETES_FIX_SHA:-18fccdf6f619514dfbcba02f8b4fdc91120cd5c3}
BASELINE_WORKTREE=${KUBERNETES_BASELINE_WORKTREE:-/private/tmp/k8s-kubelet-stats-baseline}
FIX_WORKTREE=${KUBERNETES_FIX_WORKTREE:-/private/tmp/k8s-kubelet-stats-fix}
TEST_FILE="pkg/kubelet/stats/oracle_test.go"

if [[ ! -d "$REPO/.git" ]]; then
  echo "kubernetes oracle: repo not found at $REPO" >&2
  exit 1
fi

prepare_worktree() {
  local worktree=$1
  local sha=$2
  if [[ ! -d "$worktree" ]]; then
    echo "kubernetes oracle: missing prepared worktree at $worktree" >&2
    exit 1
  fi
  local head
  head=$(git -C "$worktree" rev-parse HEAD)
  if [[ "$head" != "$sha" ]]; then
    echo "kubernetes oracle: worktree $worktree is at $head, want $sha" >&2
    exit 1
  fi
  mkdir -p "$worktree/pkg/kubelet/stats"
  cat > "$worktree/$TEST_FILE" <<'EOF'
package stats

import "testing"

func TestOracleHasMemoryAndCPUInstUsageNilMemory(t *testing.T) {
	// With lib/model pointer sub-stats, Spec.HasMemory no longer guarantees a
	// non-nil Memory sample. hasMemoryAndCPUInstUsage must return false instead
	// of panicking when the memory sample is absent for a container that the
	// spec says has memory.
	info := getTestContainerInfo(4000, "pod0", "ns0", "c0")
	if !hasMemoryAndCPUInstUsage(&info) {
		t.Fatalf("precondition failed: baseline fixture should report memory and cpuinst usage")
	}
	info.Stats[0].Memory = nil
	if hasMemoryAndCPUInstUsage(&info) {
		t.Errorf("hasMemoryAndCPUInstUsage with nil Memory and HasMemory=true = true; want false")
	}
}
EOF
}

run_case() {
  local worktree=$1
  local tmpdir
  tmpdir=$(mktemp -d "/private/tmp/k8s-oracle-XXXX")
  trap 'rm -rf "$tmpdir"' RETURN
  (cd "$worktree" && GOCACHE="${GOCACHE:-$(mktemp -d /private/tmp/k8s-gocache-XXXX)}" go test ./pkg/kubelet/stats -run TestOracleHasMemoryAndCPUInstUsageNilMemory)
}

prepare_worktree "$BASELINE_WORKTREE" "$BASELINE_SHA"
prepare_worktree "$FIX_WORKTREE" "$FIX_SHA"

if run_case "$BASELINE_WORKTREE"; then
  echo "kubernetes oracle: baseline unexpectedly passed" >&2
  exit 1
fi

if ! run_case "$FIX_WORKTREE"; then
  echo "kubernetes oracle: fix failed" >&2
  exit 1
fi

echo "kubernetes oracle: baseline red / fix green"
