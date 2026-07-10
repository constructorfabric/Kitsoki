#!/usr/bin/env bash
#
# run-tests.sh — concise runner for the full kitsoki test suite.
#
# Runs seven suites and NEVER bails early — every failure across all is
# collected before we exit:
#   1. go test $KITSOKI_GO_TEST_FLAGS ./...
#   2. Starlark static validation     (host.starlark.run parse + resolve)
#   3. story flow fixtures            (deterministic, no-LLM `kitsoki test flows`
#                                      for each tracked stories/*/app.yaml)
#   4. runstatus Vitest               (web UI unit/component tests; started in
#                                      parallel with the Go/story lanes)
#   5. feature catalog                (features/*.yaml schema + generated tour
#                                      manifests freshness; skipped with a
#                                      warning when pnpm/node_modules absent)
#   6. demo media contract            (no-LLM product-site/deck media layout)
#   7. session-mining no-LLM invariants
#
# Output contract:
#   - success → one terse line per suite, plus the report path.
#   - failure → the full detail of every failure printed inline, plus a summary.
#   - ALWAYS  → a complete report written to .artifacts/test-reports/, with only
#               the most recent $KEEP reports retained (older ones rotated out).
#
# Used by `make test`; direct runs still skip Node-backed lanes when their
# dependencies are absent unless KITSOKI_REQUIRE_VITEST=1 is set.
#
# Timeout knobs (seconds, all overridable): KITSOKI_TEST_GO_TIMEOUT_SECONDS=300,
# KITSOKI_TEST_BUILD_TIMEOUT_SECONDS=120, KITSOKI_TEST_STARLARK_TIMEOUT_SECONDS=120,
# KITSOKI_TEST_FLOW_TIMEOUT_SECONDS=360 per story app,
# KITSOKI_TEST_VITEST_TIMEOUT_SECONDS=240, KITSOKI_TEST_FEATURES_TIMEOUT_SECONDS=120,
# KITSOKI_TEST_MEDIA_TIMEOUT_SECONDS=120, and KITSOKI_TEST_PYTHON_TIMEOUT_SECONDS=300
# per file. Set KITSOKI_TEST_DISABLE_TIMEOUTS=1 only when intentionally
# diagnosing a hang.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

REPORT_DIR=".artifacts/test-reports"
KEEP=8
mkdir -p "$REPORT_DIR"

TS="$(date +%Y%m%d-%H%M%S)"
REPORT="$REPORT_DIR/test-$TS.log"

TMP="$(mktemp -d)"
vitest_pid=""
cleanup() {
	if [ -n "${vitest_pid:-}" ]; then
		kill "$vitest_pid" 2>/dev/null || true
		wait "$vitest_pid" 2>/dev/null || true
	fi
	rm -rf "$TMP" ./.kitsoki-flows
}
trap cleanup EXIT

if [ -t 1 ]; then
	RED=$'\e[31m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'; BOLD=$'\e[1m'; DIM=$'\e[2m'; RST=$'\e[0m'
else
	RED=; GREEN=; YELLOW=; BOLD=; DIM=; RST=
fi

# section appends a banner to the full report file.
section() { printf '\n========== %s ==========\n' "$1" >>"$REPORT"; }

RUN_WITH_TIMEOUT=()
if command -v python3 >/dev/null 2>&1 && [ "${KITSOKI_TEST_DISABLE_TIMEOUTS:-0}" != "1" ]; then
	RUN_WITH_TIMEOUT=(python3 "$ROOT/scripts/with-timeout.py")
fi

run_timed() {
	local timeout_s="$1"
	local label="$2"
	shift 2
	if [ "${#RUN_WITH_TIMEOUT[@]}" -gt 0 ]; then
		"${RUN_WITH_TIMEOUT[@]}" --timeout "$timeout_s" --label "$label" -- "$@"
	else
		"$@"
	fi
}

GO_TIMEOUT_SECONDS=${KITSOKI_TEST_GO_TIMEOUT_SECONDS:-300}
BUILD_TIMEOUT_SECONDS=${KITSOKI_TEST_BUILD_TIMEOUT_SECONDS:-120}
STARLARK_TIMEOUT_SECONDS=${KITSOKI_TEST_STARLARK_TIMEOUT_SECONDS:-120}
FLOW_TIMEOUT_SECONDS=${KITSOKI_TEST_FLOW_TIMEOUT_SECONDS:-360}
VITEST_TIMEOUT_SECONDS=${KITSOKI_TEST_VITEST_TIMEOUT_SECONDS:-240}
FEATURES_TIMEOUT_SECONDS=${KITSOKI_TEST_FEATURES_TIMEOUT_SECONDS:-120}
MEDIA_TIMEOUT_SECONDS=${KITSOKI_TEST_MEDIA_TIMEOUT_SECONDS:-120}
PYTHON_TIMEOUT_SECONDS=${KITSOKI_TEST_PYTHON_TIMEOUT_SECONDS:-300}

go_failures=0
starlark_failures=0
flow_failures=0
vitest_failures=0
vitest_skipped=0
vitest_rc=0
vitest_collected=0
vitest_required=${KITSOKI_REQUIRE_VITEST:-0}
features_failures=0
features_skipped=0
media_failures=0
media_skipped=0
mining_failures=0
mining_skipped=0
mining_total=0
declare -a MINING_FAILED
VITEST_OUT="$TMP/vitest.out"

collect_vitest() {
	if [ "$vitest_collected" -eq 1 ]; then
		return
	fi
	vitest_collected=1
	section "runstatus vitest"
	if [ -n "$vitest_pid" ]; then
		if wait "$vitest_pid"; then
			vitest_rc=0
		else
			vitest_rc=$?
		fi
		vitest_pid=""
		cat "$VITEST_OUT" >>"$REPORT"
		[ "$vitest_rc" -ne 0 ] && vitest_failures=1
	else
		cat "$VITEST_OUT" >>"$REPORT"
	fi
}

# Build the plain kitsoki binary once for all suites that need a command-line
# executable. The flow suite uses it directly, and Go tests can opt into the same
# binary via KITSOKI_TEST_KITSOKI_BINARY instead of linking their own copies.
FLOW_BINARY="$TMP/kitsoki-flows"
flow_built=1
if ! run_timed "$BUILD_TIMEOUT_SECONDS" "build flow runner" go build -o "$FLOW_BINARY" ./cmd/kitsoki >"$TMP/build.log" 2>&1; then
	flow_built=0
	flow_failures=1
fi

# Start the web UI unit suite early so its wall time overlaps with the Go and
# story lanes. The Make targets install deps first and set KITSOKI_REQUIRE_VITEST
# so this lane is mandatory for `make test` / `make test-full`; direct script
# runs keep the existing "skip optional Node-backed checks when deps are absent"
# behavior.
if command -v pnpm >/dev/null 2>&1 && [ -d tools/runstatus/node_modules ]; then
	(
		cd tools/runstatus || exit 2
		run_timed "$VITEST_TIMEOUT_SECONDS" "runstatus vitest" pnpm test
	) >"$VITEST_OUT" 2>&1 &
	vitest_pid=$!
else
	if [ "$vitest_required" = "1" ]; then
		vitest_failures=1
		echo "FAILED: pnpm or tools/runstatus/node_modules missing; run 'make setup' or 'make vitest-check' first" >"$VITEST_OUT"
	else
		vitest_skipped=1
		echo "skipped: pnpm or tools/runstatus/node_modules missing (run 'make setup' + 'make web')" >"$VITEST_OUT"
	fi
fi

# ---------------------------------------------------------------------------
# Suite 1: go test $KITSOKI_GO_TEST_FLAGS ./...   (-json so we can separate signal from per-test noise)
# ---------------------------------------------------------------------------
GO_JSON="$TMP/go.json"
GO_TEST_FLAGS=${KITSOKI_GO_TEST_FLAGS:-}
GO_TEST_LABEL="go test"
if [ -n "$GO_TEST_FLAGS" ]; then
	GO_TEST_LABEL="$GO_TEST_LABEL $GO_TEST_FLAGS"
fi
GO_TEST_LABEL="$GO_TEST_LABEL ./..."
section "$GO_TEST_LABEL"
# Intentionally split KITSOKI_GO_TEST_FLAGS on shell words so callers can pass
# ordinary go-test flags like "-short -run TestName".
# shellcheck disable=SC2086
if [ "$flow_built" -eq 1 ]; then
	export KITSOKI_TEST_KITSOKI_BINARY="$FLOW_BINARY"
	run_timed "$GO_TIMEOUT_SECONDS" "$GO_TEST_LABEL" go test -json $GO_TEST_FLAGS ./... >"$GO_JSON" 2>"$TMP/go.stderr"
else
	run_timed "$GO_TIMEOUT_SECONDS" "$GO_TEST_LABEL" go test -json $GO_TEST_FLAGS ./... >"$GO_JSON" 2>"$TMP/go.stderr"
fi
go_rc=$?

# Reconstruct the conventional `go test` text into the full report.
jq -j 'select(.Action=="output") | .Output' "$GO_JSON" >>"$REPORT" 2>/dev/null
[ -s "$TMP/go.stderr" ] && { echo "--- stderr ---" >>"$REPORT"; cat "$TMP/go.stderr" >>"$REPORT"; }

# Package-level tallies (Test==null marks a package result, not a single test).
go_pkgs_total=$(jq -r 'select((.Action=="pass" or .Action=="fail" or .Action=="skip") and (.Test|not)) | .Package' "$GO_JSON" 2>/dev/null | sort -u | wc -l | tr -d ' ')
GO_FAILED_PKGS=()
while IFS= read -r pkg; do
	[ -n "$pkg" ] && GO_FAILED_PKGS+=("$pkg")
done < <(jq -r 'select(.Action=="fail" and (.Test|not)) | .Package' "$GO_JSON" 2>/dev/null | sort -u)
go_failures=${#GO_FAILED_PKGS[@]}

# A non-zero rc with no parsed package failures means go test itself failed to
# run (e.g. a build error before any package result). Surface that as a failure.
if [ "$go_rc" -ne 0 ] && [ "$go_failures" -eq 0 ]; then
	go_failures=1
fi

# ---------------------------------------------------------------------------
# Suite 2: Starlark static validation
# ---------------------------------------------------------------------------
# Static parse/resolve against the exact host.starlark.run sandbox catches
# missing main(ctx), undefined names, and ungranted builtins without executing
# scripts or touching network/LLM/cost-bearing paths.
section "starlark validation"
run_timed "$STARLARK_TIMEOUT_SECONDS" "starlark validation" make --no-print-directory starcheck-kitsoki >"$TMP/starlark.out" 2>&1
starlark_rc=$?
cat "$TMP/starlark.out" >>"$REPORT"
[ "$starlark_rc" -ne 0 ] && starlark_failures=1

# ---------------------------------------------------------------------------
# Suite 3: story flow fixtures
# ---------------------------------------------------------------------------
section "story flows"
STORY_APPS=()
while IFS= read -r app; do
	[ -n "$app" ] && STORY_APPS+=("$app")
done < <(git ls-files | grep -E '^stories/[^/]+/app\.yaml$' | sort)

declare -a FLOW_FAILED_APPS
flow_apps_total=0
FLOW_JOBS=${KITSOKI_FLOW_JOBS:-4}
if ! [[ "$FLOW_JOBS" =~ ^[1-9][0-9]*$ ]]; then
	FLOW_JOBS=4
fi

if [ "$flow_built" -ne 1 ]; then
	{ echo "FAILED to build flow runner:"; cat "$TMP/build.log"; } >>"$REPORT"
fi

# Flow quarantine: stories whose FLOW fixtures are deliberately skipped because
# they cover in-flight / WIP work that isn't finished (and shouldn't gate CI yet).
# Keep this list SMALL and documented — each entry is a known gap, not a free pass:
#   repo-bakeoff  — gears-era flow fixtures; mid-decoupling, not yet reworked.
#   bench-bugfix  — no flow fixtures authored yet (declares an app-flows/ glob
#                   with nothing in it). It still loads (covered by TestAllStoriesLoad).
# Un-quarantine by deleting the entry once the story's flows are real.
FLOW_QUARANTINE=" stories/repo-bakeoff/app.yaml stories/bench-bugfix/app.yaml "

if [ "$flow_built" -eq 1 ]; then
	FLOW_APPS_LIST="$TMP/flow-apps.list"
	: >"$FLOW_APPS_LIST"
	for app in "${STORY_APPS[@]}"; do
		if [[ "$FLOW_QUARANTINE" == *" $app "* ]]; then
			printf -- '-- %s (QUARANTINED — flows skipped; see run-tests.sh FLOW_QUARANTINE)\n' "$app" >>"$REPORT"
			continue
		fi
		flow_apps_total=$((flow_apps_total + 1))
		printf '%s\n' "$app" >>"$FLOW_APPS_LIST"
	done

	if [ "$flow_apps_total" -gt 0 ]; then
		xargs -n 1 -P "$FLOW_JOBS" bash -c '
			set -uo pipefail
			tmp="$1"
			bin="$2"
			timeout_s="$3"
			timeout_bin="$4"
			app="$5"
			slug="$(printf "%s" "$app" | tr "/" "-")"
			fj="$tmp/flow-$slug.json"
			fout="$tmp/flow-$slug.out"
			frc="$tmp/flow-$slug.rc"
			if [ -n "$timeout_bin" ]; then
				python3 "$timeout_bin" --timeout "$timeout_s" --label "story flows $app" -- "$bin" test flows "$app" --json "$fj" >"$fout" 2>&1
			else
				"$bin" test flows "$app" --json "$fj" >"$fout" 2>&1
			fi
			printf "%d\n" "$?" >"$frc"
		' _ "$TMP" "$FLOW_BINARY" "$FLOW_TIMEOUT_SECONDS" "${RUN_WITH_TIMEOUT[1]:-}" <"$FLOW_APPS_LIST"
	fi

	while IFS= read -r app; do
		[ -n "$app" ] || continue
		slug="$(printf "%s" "$app" | tr "/" "-")"
		fout="$TMP/flow-$slug.out"
		frc="$TMP/flow-$slug.rc"
		if [ -f "$frc" ]; then
			rc="$(cat "$frc")"
		else
			rc=1
			printf 'flow runner did not write a status file\n' >"$fout"
		fi
		{
			printf -- '-- %s (exit %d)\n' "$app" "$rc"
			# Strip the orchestrator WARN noise from the report body; keep the rest.
			grep -v 'WARN orchestrator' "$fout"
		} >>"$REPORT"
		if [ "$rc" -ne 0 ]; then
			FLOW_FAILED_APPS+=("$app")
			flow_failures=$((flow_failures + 1))
		fi
	done <"$FLOW_APPS_LIST"
fi

collect_vitest

# ---------------------------------------------------------------------------
# Suite 5: feature catalog (features/*.yaml ↔ generated tour manifests)
# ---------------------------------------------------------------------------
section "feature catalog"
if command -v pnpm >/dev/null 2>&1 && [ -d tools/runstatus/node_modules ]; then
	run_timed "$FEATURES_TIMEOUT_SECONDS" "feature catalog" pnpm --dir tools/runstatus --silent features:check >"$TMP/features.out" 2>&1
	features_rc=$?
	cat "$TMP/features.out" >>"$REPORT"
	[ "$features_rc" -ne 0 ] && features_failures=1
else
	features_skipped=1
	echo "skipped: pnpm or tools/runstatus/node_modules missing (run 'make setup' + 'make web')" >>"$REPORT"
fi

# ---------------------------------------------------------------------------
# Suite 6: demo media contract (source/staged product-site media + deck embeds)
# ---------------------------------------------------------------------------
section "demo media contract"
if command -v node >/dev/null 2>&1 && command -v pnpm >/dev/null 2>&1 && [ -d tools/runstatus/node_modules ]; then
	MEDIA_INDEX="$TMP/features-media"
	mkdir -p "$MEDIA_INDEX"
	if run_timed "$MEDIA_TIMEOUT_SECONDS" "demo media index" pnpm --dir tools/runstatus --silent exec tsx scripts/features/generate.ts --index --out "$MEDIA_INDEX" >"$TMP/media-index.out" 2>&1; then
		run_timed "$MEDIA_TIMEOUT_SECONDS" "demo media contract" node tools/site/scripts/check-media.mjs --index "$MEDIA_INDEX/features-index.json" >"$TMP/media.out" 2>&1
		media_rc=$?
		cat "$TMP/media-index.out" "$TMP/media.out" >>"$REPORT"
		[ "$media_rc" -ne 0 ] && media_failures=1
	else
		cat "$TMP/media-index.out" >>"$REPORT"
		media_failures=1
	fi
else
	media_skipped=1
	echo "skipped: node/pnpm or tools/runstatus/node_modules missing (run 'make setup' + 'make web')" >>"$REPORT"
fi

# ---------------------------------------------------------------------------
# Suite 7: session-mining no-LLM invariants (stdlib python, committed fixtures)
# ---------------------------------------------------------------------------
# The intent pipeline, outcome capture, git-ops coverage, and the real-cost
# stack are all pure-python and run against frozen agent JSON — NEVER a live
# LLM (AGENTS.md). `go test ./...` doesn't touch them, so they'd rot unguarded.
# Gated on python3 like the feature catalog is gated on pnpm.
section "python tool tests (session-mining + product-journey + arena + dev-story scripts)"
if command -v python3 >/dev/null 2>&1; then
	shopt -s nullglob
	MINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py stories/dev-story/scripts/*_test.py)
	shopt -u nullglob
	for t in "${MINING_TESTS[@]}"; do
		mining_total=$((mining_total + 1))
		mout="$TMP/mining-$(basename "$t").out"
		run_timed "$PYTHON_TIMEOUT_SECONDS" "python tool test $t" python3 "$t" >"$mout" 2>&1
		rc=$?
		{ printf -- '-- %s (exit %d)\n' "$t" "$rc"; cat "$mout"; } >>"$REPORT"
		if [ "$rc" -ne 0 ]; then
			MINING_FAILED+=("$t")
			mining_failures=$((mining_failures + 1))
		fi
	done
else
	mining_skipped=1
	echo "skipped: python3 missing" >>"$REPORT"
fi

# ---------------------------------------------------------------------------
# Report rotation
# ---------------------------------------------------------------------------
# shellcheck disable=SC2012
ls -1t "$REPORT_DIR"/test-*.log 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do rm -f "$old"; done

# ---------------------------------------------------------------------------
# Console summary
# ---------------------------------------------------------------------------
total_failures=$((go_failures + starlark_failures + flow_failures + vitest_failures + features_failures + media_failures + mining_failures))

if [ "$total_failures" -eq 0 ]; then
	printf '%s✓%s %s   %s%d packages%s\n' "$GREEN" "$RST" "$GO_TEST_LABEL" "$DIM" "$go_pkgs_total" "$RST"
	printf '%s✓%s starlark check\n' "$GREEN" "$RST"
	printf '%s✓%s story flows     %s%d stories%s\n' "$GREEN" "$RST" "$DIM" "$flow_apps_total" "$RST"
	if [ "$vitest_skipped" -eq 1 ]; then
		printf '%s-%s runstatus vitest %sskipped (pnpm/node_modules missing)%s\n' "$YELLOW" "$RST" "$DIM" "$RST"
	else
		printf '%s✓%s runstatus vitest\n' "$GREEN" "$RST"
	fi
	if [ "$features_skipped" -eq 1 ]; then
		printf '%s-%s feature catalog %sskipped (pnpm/node_modules missing)%s\n' "$YELLOW" "$RST" "$DIM" "$RST"
	else
		printf '%s✓%s feature catalog\n' "$GREEN" "$RST"
	fi
	if [ "$media_skipped" -eq 1 ]; then
		printf '%s-%s demo media      %sskipped (node/pnpm/node_modules missing)%s\n' "$YELLOW" "$RST" "$DIM" "$RST"
	else
		printf '%s✓%s demo media\n' "$GREEN" "$RST"
	fi
	if [ "$mining_skipped" -eq 1 ]; then
		printf '%s-%s session-mining   %sskipped (python3 missing)%s\n' "$YELLOW" "$RST" "$DIM" "$RST"
	else
		printf '%s✓%s session-mining  %s%d suites%s\n' "$GREEN" "$RST" "$DIM" "$mining_total" "$RST"
	fi
	printf '%s✓ all tests passed%s   %s· report: %s%s\n' "$BOLD$GREEN" "$RST" "$DIM" "$REPORT" "$RST"
	exit 0
fi

# --- Go test failures -------------------------------------------------------
if [ "$go_failures" -gt 0 ]; then
	printf '\n%s✗ %s%s — %d package(s) failed\n' "$BOLD$RED" "$GO_TEST_LABEL" "$RST" "$go_failures"
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

# --- Starlark failures ------------------------------------------------------
if [ "$starlark_failures" -gt 0 ]; then
	printf '\n%s✗ starlark validation%s — host.starlark.run scripts failed static checks:\n' "$BOLD$RED" "$RST"
	sed 's/^/  /' "$TMP/starlark.out"
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

# --- Runstatus Vitest failures ---------------------------------------------
if [ "$vitest_failures" -gt 0 ]; then
	printf '\n%s✗ runstatus vitest%s — web UI unit/component tests failed:\n' "$BOLD$RED" "$RST"
	sed 's/^/  /' "$VITEST_OUT"
fi

# --- Feature catalog failures -------------------------------------------------
if [ "$features_failures" -gt 0 ]; then
	printf '\n%s✗ feature catalog%s — validation/freshness failed:\n' "$BOLD$RED" "$RST"
	sed 's/^/  /' "$TMP/features.out"
fi

# --- Demo media failures -----------------------------------------------------
if [ "$media_failures" -gt 0 ]; then
	printf '\n%s✗ demo media contract%s — validation failed:\n' "$BOLD$RED" "$RST"
	sed 's/^/  /' "$TMP/media-index.out" 2>/dev/null
	sed 's/^/  /' "$TMP/media.out" 2>/dev/null
fi

# --- Session-mining failures --------------------------------------------------
if [ "$mining_failures" -gt 0 ]; then
	printf '\n%s✗ session-mining%s — %d/%d suite(s) failed\n' "$BOLD$RED" "$RST" "$mining_failures" "$mining_total"
	for t in "${MINING_FAILED[@]}"; do
		printf '\n%s%s%s\n' "$YELLOW" "$t" "$RST"
		sed 's/^/  /' "$TMP/mining-$(basename "$t").out"
	done
fi

printf '\n%s✗ %d failure group(s)%s   %s· full report: %s%s\n' \
	"$BOLD$RED" "$total_failures" "$RST" "$DIM" "$REPORT" "$RST"
exit 1
