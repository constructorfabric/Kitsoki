#!/usr/bin/env bash
#
# run-tests.sh — concise runner for the full kitsoki test suite.
#
# Runs two suites and NEVER bails early — every failure across both is
# collected before we exit:
#   1. go test ./...                  (Go unit tests)
#   2. story flow fixtures            (deterministic, no-LLM `kitsoki test flows`
#                                      for each stories/*/app.yaml)
#
# Output contract:
#   - success → one terse line per suite, plus the report path.
#   - failure → the full detail of every failure printed inline, plus a summary.
#   - ALWAYS  → a complete report written to .artifacts/test-reports/, with only
#               the most recent $KEEP reports retained (older ones rotated out).
#
# Used by `make test`. Run directly for the same behaviour.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

REPORT_DIR=".artifacts/test-reports"
KEEP=8
mkdir -p "$REPORT_DIR"

TS="$(date +%Y%m%d-%H%M%S)"
REPORT="$REPORT_DIR/test-$TS.log"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP" ./.kitsoki-flows' EXIT

if [ -t 1 ]; then
	RED=$'\e[31m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'; BOLD=$'\e[1m'; DIM=$'\e[2m'; RST=$'\e[0m'
else
	RED=; GREEN=; YELLOW=; BOLD=; DIM=; RST=
fi

# section appends a banner to the full report file.
section() { printf '\n========== %s ==========\n' "$1" >>"$REPORT"; }

go_failures=0
flow_failures=0

# ---------------------------------------------------------------------------
# Suite 1: go test ./...   (-json so we can separate signal from per-test noise)
# ---------------------------------------------------------------------------
GO_JSON="$TMP/go.json"
section "go test ./..."
go test -json ./... >"$GO_JSON" 2>"$TMP/go.stderr"
go_rc=$?

# Reconstruct the conventional `go test` text into the full report.
jq -j 'select(.Action=="output") | .Output' "$GO_JSON" >>"$REPORT" 2>/dev/null
[ -s "$TMP/go.stderr" ] && { echo "--- stderr ---" >>"$REPORT"; cat "$TMP/go.stderr" >>"$REPORT"; }

# Package-level tallies (Test==null marks a package result, not a single test).
go_pkgs_total=$(jq -r 'select((.Action=="pass" or .Action=="fail" or .Action=="skip") and (.Test|not)) | .Package' "$GO_JSON" 2>/dev/null | sort -u | wc -l | tr -d ' ')
mapfile -t GO_FAILED_PKGS < <(jq -r 'select(.Action=="fail" and (.Test|not)) | .Package' "$GO_JSON" 2>/dev/null | sort -u)
go_failures=${#GO_FAILED_PKGS[@]}

# A non-zero rc with no parsed package failures means go test itself failed to
# run (e.g. a build error before any package result). Surface that as a failure.
if [ "$go_rc" -ne 0 ] && [ "$go_failures" -eq 0 ]; then
	go_failures=1
fi

# ---------------------------------------------------------------------------
# Suite 2: story flow fixtures
# ---------------------------------------------------------------------------
section "story flows"
shopt -s nullglob
STORY_APPS=(stories/*/app.yaml)
shopt -u nullglob

declare -a FLOW_FAILED_APPS
flow_apps_total=0
flow_built=1

# Plain `go build` — flow replay needs no embedded SPA.
if ! go build -o ./.kitsoki-flows ./cmd/kitsoki >"$TMP/build.log" 2>&1; then
	flow_built=0
	flow_failures=1
	{ echo "FAILED to build flow runner:"; cat "$TMP/build.log"; } >>"$REPORT"
fi

if [ "$flow_built" -eq 1 ]; then
	for app in "${STORY_APPS[@]}"; do
		flow_apps_total=$((flow_apps_total + 1))
		slug="$(echo "$app" | tr '/' '-')"
		fj="$TMP/flow-$slug.json"
		fout="$TMP/flow-$slug.out"
		./.kitsoki-flows test flows "$app" --json "$fj" >"$fout" 2>&1
		rc=$?
		{
			printf -- '-- %s (exit %d)\n' "$app" "$rc"
			# Strip the orchestrator WARN noise from the report body; keep the rest.
			grep -v 'WARN orchestrator' "$fout"
		} >>"$REPORT"
		if [ "$rc" -ne 0 ]; then
			FLOW_FAILED_APPS+=("$app")
			flow_failures=$((flow_failures + 1))
		fi
	done
fi

# ---------------------------------------------------------------------------
# Report rotation
# ---------------------------------------------------------------------------
# shellcheck disable=SC2012
ls -1t "$REPORT_DIR"/test-*.log 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do rm -f "$old"; done

# ---------------------------------------------------------------------------
# Console summary
# ---------------------------------------------------------------------------
total_failures=$((go_failures + flow_failures))

if [ "$total_failures" -eq 0 ]; then
	printf '%s✓%s go test ./...   %s%d packages%s\n' "$GREEN" "$RST" "$DIM" "$go_pkgs_total" "$RST"
	printf '%s✓%s story flows     %s%d stories%s\n' "$GREEN" "$RST" "$DIM" "$flow_apps_total" "$RST"
	printf '%s✓ all tests passed%s   %s· report: %s%s\n' "$BOLD$GREEN" "$RST" "$DIM" "$REPORT" "$RST"
	exit 0
fi

# --- Go test failures -------------------------------------------------------
if [ "$go_failures" -gt 0 ]; then
	printf '\n%s✗ go test ./...%s — %d package(s) failed\n' "$BOLD$RED" "$RST" "$go_failures"
	for pkg in "${GO_FAILED_PKGS[@]}"; do
		printf '\n%s%s%s\n' "$YELLOW" "$pkg" "$RST"
		# `go test -json` is implicitly verbose, so the package emits RUN/PASS for
		# every test. Show ONLY the failing tests' output (plus package-level lines
		# like build errors and the final FAIL line, which have no .Test).
		ftests="$(jq -c -s --arg p "$pkg" \
			'[ .[] | select(.Package==$p and .Action=="fail" and (.Test!=null)) | .Test ] | unique' \
			"$GO_JSON" 2>/dev/null)"
		jq -j --arg p "$pkg" --argjson ft "${ftests:-[]}" '
			select(.Package==$p and .Action=="output")
			| select((.Test==null) or (.Test as $t | $ft | index($t)))
			| .Output
		' "$GO_JSON" 2>/dev/null | sed 's/^/  /'
	done
	# Build/setup errors that produced a non-zero rc but no package failure.
	if [ "${#GO_FAILED_PKGS[@]}" -eq 0 ]; then
		printf '%sgo test exited %d with no package-level failure (build error?):%s\n' "$RED" "$go_rc" "$RST"
		sed 's/^/  /' "$TMP/go.stderr"
	fi
fi

# --- Flow failures ----------------------------------------------------------
if [ "$flow_failures" -gt 0 ]; then
	if [ "$flow_built" -eq 0 ]; then
		printf '\n%s✗ story flows%s — flow runner failed to build:\n' "$BOLD$RED" "$RST"
		sed 's/^/  /' "$TMP/build.log"
	else
		printf '\n%s✗ story flows%s — %d/%d stories failed\n' "$BOLD$RED" "$RST" "${#FLOW_FAILED_APPS[@]}" "$flow_apps_total"
		for app in "${FLOW_FAILED_APPS[@]}"; do
			slug="$(echo "$app" | tr '/' '-')"
			fj="$TMP/flow-$slug.json"
			printf '\n%s%s%s\n' "$YELLOW" "$app" "$RST"
			# Per-fixture failure detail: file, failing turn, and assertion messages.
			detail="$(jq -r '
				.Results[] | select(.Passed==false)
				| "  ✗ " + .File
				+ ( [ .Turns[]? | select(.Passed==false)
				      | "\n      turn " + (.TurnIndex|tostring) + " (→ " + (.NewState // "?") + "):"
				        + ( [ (.Failures // [])[] | "\n        - " + . ] | join("") ) ]
				    | join("") )
			' "$fj" 2>/dev/null)"
			if [ -n "$detail" ]; then
				printf '%s\n' "$detail"
			else
				# No per-fixture JSON (fatal startup error, exit 2): show the
				# runner's own output instead so the cause isn't swallowed.
				grep -v 'WARN orchestrator' "$TMP/flow-$slug.out" | sed 's/^/  /'
			fi
		done
	fi
fi

printf '\n%s✗ %d failure group(s)%s   %s· full report: %s%s\n' \
	"$BOLD$RED" "$total_failures" "$RST" "$DIM" "$REPORT" "$RST"
exit 1
