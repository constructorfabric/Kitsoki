#!/usr/bin/env bash
#
# record-demos.sh — record every recordable feature demo at watch-speed,
# incrementally.
#
# The demo list comes from the feature catalog contract
# (.artifacts/features/features-index.json — `make features-index`). Each demo
# carries a content stamp: sha256 over its spec file, its features/<id>.yaml,
# its flow/cassette/app.yaml inputs, and the bin/kitsoki binary. A fresh stamp
# (and an existing MP4) skips the recording — so a docs-only change re-records
# nothing, while touching a feature's YAML, spec, story input, or the binary
# re-records exactly the affected demos. `--force` re-records everything.
#
# Recording posture: WEB_CHAT_PACE=1 (watch-speed; the camera default), one
# retry per spec, previous good artifacts are never deleted on failure.
# Demos marked `external: true` in the catalog are skipped (their stories live
# outside this repo). Exit nonzero if any recording ultimately failed.
#
# Used by `make demos` / `make demos-force`; CI runs it behind an actions/cache
# over .artifacts so unchanged demos cost nothing.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

INDEX=".artifacts/features/features-index.json"
RUNSTATUS_DIR="tools/runstatus"
BIN="bin/kitsoki"
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

command -v jq >/dev/null 2>&1 || { echo "record-demos: jq is required" >&2; exit 2; }
[ -f "$INDEX" ] || { echo "record-demos: $INDEX missing — run: make features-index" >&2; exit 2; }
[ -x "$BIN" ] || { echo "record-demos: $BIN missing — run: make build-bin" >&2; exit 2; }

# A restored .artifacts cache may carry a stale generated feature index. Refresh
# it after cache restore and before any skip/record decisions.
(cd "$RUNSTATUS_DIR" && pnpm exec tsx scripts/features/generate.ts --index >/dev/null)

TOUR_CHROME_PATH="${KITSOKI_TOUR_CHROME_PATH:-}"
if [ -z "$TOUR_CHROME_PATH" ] && command -v node >/dev/null 2>&1; then
	TOUR_CHROME_PATH="$(cd "$RUNSTATUS_DIR" && node -e 'try { const { chromium } = require("@playwright/test"); process.stdout.write(chromium.executablePath()); } catch {}' 2>/dev/null || true)"
	[ -x "$TOUR_CHROME_PATH" ] || TOUR_CHROME_PATH=""
fi

if command -v sha256sum >/dev/null 2>&1; then SHA="sha256sum"; else SHA="shasum -a 256"; fi

if jq -e '.features[] | select(.id == "mockup-video" or .id == "review") | select(.demo != null and .demo.external == false)' "$INDEX" >/dev/null; then
	"$ROOT/scripts/prepare-review-render.sh"
fi

# stamp <files...> — one hash over the given files' contents (missing skipped).
stamp() {
	for f in "$@"; do
		[ -n "$f" ] && [ -f "$f" ] && $SHA "$f"
	done | $SHA | awk '{print $1}'
}

recorded=0
skipped=0
declare -a FAILED

while IFS=$'\t' read -r id profile renderer specName artifactDir video yaml spec flow cassette story; do
	# Stamp inputs: catalog entry, spec, story inputs, binary. The profile picks
	# the camera env (KITSOKI_DEMO_PROFILE) + a per-profile stamp file so each
	# variant re-records independently; desktop keeps the original .stamp name and
	# empty video suffix, so it is byte-for-byte a no-op vs. the pre-matrix path.
	story_app=""
	[ -n "$story" ] && story_app="$story/app.yaml"
	review_render_script=""
	if [ "$id" = "mockup-video" ] || [ "$id" = "review" ]; then
		review_render_script="scripts/prepare-review-render.sh"
	fi
	s=$(stamp "$yaml" "$spec" "$flow" "$cassette" "$story_app" "$review_render_script" "$BIN")
	stamp_suffix=""
	[ "$profile" != "desktop" ] && stamp_suffix="--$profile"
	stamp_file="$artifactDir/.stamp$stamp_suffix"
	label="$id${stamp_suffix:+ [$profile]}"

	if [ "$FORCE" -eq 0 ] && [ -f "$video" ] && [ -f "$stamp_file" ] && [ "$(cat "$stamp_file")" = "$s" ]; then
		skipped=$((skipped + 1))
		continue
	fi

	echo "record-demos: recording $label ($specName)…"
	ok=0
	for attempt in 1 2; do
		if [ "$renderer" = "binary" ]; then
			tour_args=(tour --feature "$id" --out "$artifactDir")
			[ -n "$TOUR_CHROME_PATH" ] && tour_args+=(--chrome-path "$TOUR_CHROME_PATH")
			if KITSOKI_DEMO_PROFILE="$profile" WEB_CHAT_PACE=1 "$BIN" "${tour_args[@]}"; then
				ok=1
				break
			fi
		elif (cd "$RUNSTATUS_DIR" && KITSOKI_DEMO_PROFILE="$profile" WEB_CHAT_PACE=1 pnpm exec playwright test "$spec" --project=chromium); then
			ok=1
			break
		fi
		echo "record-demos: $label attempt $attempt failed$([ "$attempt" = 1 ] && echo ' — retrying')" >&2
	done

	if [ "$ok" -eq 1 ] && [ -f "$video" ]; then
		mkdir -p "$artifactDir"
		printf '%s' "$s" >"$stamp_file"
		recorded=$((recorded + 1))
	else
		FAILED+=("$label")
	fi
done < <(jq -r '
	.features[]
	| select(.demo != null and .demo.external == false and (.demo.spec != null or .demo.renderer == "binary"))
	| . as $f
	| ($f.demo.profiles // ["desktop"])[] as $p
	| [ $f.id, $p, ($f.demo.renderer // "playwright"), ($f.demo.specName // $f.id), $f.demo.artifactDir,
	    $f.demo.variants[$p].video,
	    "features/\($f.id).yaml", $f.demo.spec,
	    ($f.demo.flow // ""), ($f.demo.hostCassette // ""), ($f.demo.story // "") ]
	| @tsv' "$INDEX")

fail_count=0
for _failed in "${FAILED[@]+"${FAILED[@]}"}"; do
	fail_count=$((fail_count + 1))
done

echo "record-demos: $recorded recorded, $skipped fresh (skipped), $fail_count failed"
if [ "$fail_count" -gt 0 ]; then
	printf 'record-demos: FAILED: %s\n' "${FAILED[*]}" >&2
	exit 1
fi
