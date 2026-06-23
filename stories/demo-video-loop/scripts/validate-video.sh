#!/usr/bin/env bash
# validate-video.sh <video_path> <frames_dir> <expectation:new|update|auto> <since_epoch>
#
# The DETERMINISTIC video-validation gate. Exit 0 iff the produced demo video is
# the real, shippable, watch-speed deliverable — not a pace-0 flash, a fast
# overwrite, or a stale artifact from a prior turn. Exit 1 (with a one-line reason
# on stdout) otherwise. The generating room binds {ok, stdout} from this call, so
# the reason becomes the maker's feedback on a loop-back.
#
# The gate (grounded in real failure modes — SCENARIOS-BRIEF §2):
#   - video_path ends in .mp4 AND is NOT *.fast.mp4 / *.SHORT-*.mp4 / *.webm.
#     (kitsoki-ui-demo down-names under-dwelled runs; a canonical .mp4 already
#      encodes "watch-speed".)
#   - the file exists and is non-empty.
#   - ffprobe duration >= ${KITSOKI_MIN_DEMO_SECONDS:-25} (the 6s-flash trap).
#   - frames_dir exists and holds >=1 *.png.
#   - NO ERROR.txt in the video's directory (the record-success signal; artifacts
#     live at repo-root .artifacts/<name>/).
#   - file mtime >= since_epoch (proves it was (re)written THIS turn — covers both
#     new and update; rejects a stale artifact from a prior iteration).
#
# Robust to missing args; shellcheck-clean; never depends on a real recording.
set -euo pipefail

fail() { printf 'video gate FAIL: %s\n' "$1"; exit 1; }

video_path="${1:-}"
frames_dir="${2:-}"
expectation="${3:-auto}"   # accepted for the contract; new|update both require mtime-this-turn
since_epoch="${4:-0}"

min_seconds="${KITSOKI_MIN_DEMO_SECONDS:-25}"

# Tolerate a non-numeric / empty since_epoch by treating it as 0 (no mtime floor).
case "${since_epoch}" in
  ''|*[!0-9]*) since_epoch=0 ;;
esac

[ -n "${video_path}" ] || fail "no video_path given"
[ -n "${frames_dir}" ] || fail "no frames_dir given"

# --- name discipline ---
case "${video_path}" in
  *.fast.mp4)   fail "video is the fast/under-dwelled artifact (${video_path}); ship the canonical .mp4" ;;
  *.SHORT-*.mp4) fail "video is a down-named SHORT artifact (${video_path}); record at watch pace" ;;
  *.webm)       fail "video is a .webm (${video_path}); ship the canonical .mp4" ;;
  *.mp4)        : ;;
  *)            fail "video_path does not end in .mp4 (${video_path})" ;;
esac

# --- existence / non-empty ---
[ -f "${video_path}" ] || fail "video file does not exist: ${video_path}"
[ -s "${video_path}" ] || fail "video file is empty: ${video_path}"

# --- duration (watch-speed, not a flash) ---
if ! command -v ffprobe >/dev/null 2>&1; then
  fail "ffprobe not found; cannot verify watch-speed duration"
fi
duration="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "${video_path}" 2>/dev/null || true)"
case "${duration}" in
  ''|*[!0-9.]*) fail "ffprobe returned no usable duration for ${video_path}" ;;
esac
# Integer-compare the truncated seconds (awk avoids a bc dependency).
dur_int="$(awk -v d="${duration}" 'BEGIN { printf "%d", d }')"
if [ "${dur_int}" -lt "${min_seconds}" ]; then
  fail "duration ${duration}s < ${min_seconds}s (pace-0 flash or fast overwrite?): ${video_path}"
fi

# --- frames ---
[ -d "${frames_dir}" ] || fail "frames_dir does not exist: ${frames_dir}"
png_count="$(find "${frames_dir}" -maxdepth 1 -type f -name '*.png' 2>/dev/null | wc -l | tr -d '[:space:]')"
[ "${png_count:-0}" -ge 1 ] || fail "no *.png frames in ${frames_dir}"

# --- record-success signal: no ERROR.txt in the video's dir ---
video_dir="$(dirname "${video_path}")"
if [ -e "${video_dir}/ERROR.txt" ]; then
  fail "ERROR.txt present in ${video_dir} (record reported failure)"
fi

# --- written THIS turn (covers new AND update) ---
if [ "${since_epoch}" -gt 0 ]; then
  # GNU stat: -c %Y ; BSD/macOS stat: -f %m. Try both.
  mtime="$(stat -c %Y "${video_path}" 2>/dev/null || stat -f %m "${video_path}" 2>/dev/null || echo 0)"
  case "${mtime}" in ''|*[!0-9]*) mtime=0 ;; esac
  if [ "${mtime}" -lt "${since_epoch}" ]; then
    fail "video mtime ${mtime} < turn start ${since_epoch} (stale artifact, not (re)written this turn): ${video_path}"
  fi
fi

printf 'video gate PASS: %s (%ss, %s png, expectation=%s)\n' \
  "${video_path}" "${duration}" "${png_count}" "${expectation}"
exit 0
