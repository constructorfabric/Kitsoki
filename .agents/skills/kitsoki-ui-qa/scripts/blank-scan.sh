#!/usr/bin/env bash
# blank-scan.sh — DETERMINISTIC (no-LLM) blank / solid-region detector.
#
# Flags demo frames containing a large CONTIGUOUS block of a single flat colour
# — ANY colour, not just white/black — the signature of a broken/blank render
# where UI content was expected (an html2canvas pane that rasterized to a white
# box, a missing image's placeholder grey, an unstyled solid panel, etc.). The
# cheap, reproducible safety net under the LLM visual-integrity check in
# qa-review.sh: same frames in → same findings out, no API cost.
#
# How it works (pure ffmpeg + python3 stdlib — no PIL/ImageMagick):
#   • ffmpeg area-downscales each frame to a coarse GRID, so a tile reads a flat
#     colour only when it sits INSIDE a solid block;
#   • tile colours are quantized into buckets; the most common bucket is the
#     page BACKGROUND (a themed bg is legitimately monochromatic — a sparse dark
#     UI is 90%+ background, so it must never self-flag);
#   • a flood fill finds the largest contiguous blob of a single colour whose
#     CONTRAST from the background exceeds --contrast (RGB distance) — flagged
#     when it covers >= --min-coverage. Contrast is the key: a broken render
#     (white box, grey placeholder, colour fill) stands OUT from the bg, whereas
#     a dark panel on a dark theme is low-contrast and ignored;
#   • EDGE-GUTTER check (contrast-independent, colour-AGNOSTIC): from each edge
#     inward, count the contiguous band of rows/columns that are each ~entirely
#     ONE flat bucket AND share that bucket across the band. A wide such band is
#     flagged whatever its colour. Two failures both look like this and both are
#     missed by the blob/contrast logic: (a) a dead margin the content never
#     reaches whose colour MATCHES the theme bg (e.g. left-packed 80-col content
#     in a wide panel → dead right third); (b) a FOREIGN flat bar composited OVER
#     the frame — most importantly a VIDEO RECORDER letterbox/pad bar that appears
#     when the captured window is smaller than the recordVideo size (a solid grey
#     strip down one edge of the .mp4 — invisible in window screenshots). It only
#     fires when the frame has real content elsewhere, so a sparse screen is quiet;
#   • separately, a frame whose single most-common colour covers >=
#     --empty-coverage is flagged as near-empty (essentially nothing rendered).
#
# Scans a frames dir, a single image, OR a VIDEO (.mp4/.webm/.mov) — a video is
# sampled to frames first, because recorder-pad bars live in the video, not in
# the window screenshots.
#
# Real content breaks into many small differing tiles, so only a genuine solid
# rectangle clusters into one big blob — white text or a busy UI won't trip it,
# and the contrast gate keeps a legitimately sparse dark screen quiet.
#
# Usage:
#   blank-scan.sh <frames-dir|image> [--out scan.json] [--grid WxH]
#                 [--quant N] [--contrast D] [--min-coverage F]
#                 [--empty-coverage F] [--gutter-min F] [--gutter-uniform F]
#                 [--fail-on-find]
# Defaults: --grid 48x27 --quant 24 --contrast 64 --min-coverage 0.10
#           --empty-coverage 0.985 --gutter-min 0.10 --gutter-uniform 0.94
# Exit: 0 = scanned OK (no flags, or flags but advisory);
#       3 = flags found AND --fail-on-find; 2 = usage/tool error.
#
# This is an ADVISORY nudge — a large high-contrast flat block is suspicious but
# not always a bug. It flags frames for a human glance; the context-aware LLM
# check in qa-review.sh is the hard gate.
set -euo pipefail

command -v ffmpeg  >/dev/null 2>&1 || { echo "ffmpeg not on PATH"  >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not on PATH" >&2; exit 2; }

src="${1:-}"; shift || true
[ -n "$src" ] || { echo "usage: blank-scan.sh <frames-dir|image> [opts]" >&2; exit 2; }

out="" grid="48x27" quant=24 contrast=64 min_cov="0.10" empty_cov="0.985" fail_on_find=0
gutter_min="0.10" gutter_uniform="0.94"
while [ $# -gt 0 ]; do
  case "$1" in
    --out)            out="$2"; shift 2 ;;
    --grid)           grid="$2"; shift 2 ;;
    --quant)          quant="$2"; shift 2 ;;
    --contrast)       contrast="$2"; shift 2 ;;
    --min-coverage)   min_cov="$2"; shift 2 ;;
    --empty-coverage) empty_cov="$2"; shift 2 ;;
    --gutter-min)     gutter_min="$2"; shift 2 ;;
    --gutter-uniform) gutter_uniform="$2"; shift 2 ;;
    --fail-on-find)   fail_on_find=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Collect frames. A dir → its PNGs sorted; a VIDEO (.mp4/.webm/.mov) → frames
# sampled out of it (the recorded video is where compositing artifacts like a
# recorder letterbox bar live — screenshots capture the window directly and miss
# them); any other single file → just it.
frames=()
tmp_extracted=""
case "$src" in
  *.mp4|*.webm|*.mov|*.MP4|*.WEBM|*.MOV)
    tmp_extracted="$(mktemp -d)"
    # ~1 frame every 2s, capped, so a long tour stays cheap but every beat is seen.
    ffmpeg -loglevel error -i "$src" -vf "fps=1/2" "$tmp_extracted/f%04d.png" \
      || { echo "ffmpeg failed to extract frames from $src" >&2; exit 2; }
    while IFS= read -r f; do frames+=("$f"); done < <(find "$tmp_extracted" -type f -name '*.png' | sort)
    ;;
  *)
    if [ -d "$src" ]; then
      while IFS= read -r f; do frames+=("$f"); done < <(find "$src" -maxdepth 1 -type f -name '*.png' | sort)
    else
      frames+=("$src")
    fi
    ;;
esac
[ "${#frames[@]}" -gt 0 ] || { echo "no frames to scan under $src" >&2; exit 2; }
cleanup() { [ -n "$tmp_extracted" ] && rm -rf "$tmp_extracted"; }
trap cleanup EXIT

GW="${grid%x*}"; GH="${grid#*x}"

python3 - "$GW" "$GH" "$quant" "$contrast" "$min_cov" "$empty_cov" "$gutter_min" "$gutter_uniform" "$fail_on_find" "$out" "${frames[@]}" <<'PY'
import sys, json, subprocess
from collections import Counter
gw, gh = int(sys.argv[1]), int(sys.argv[2])
quant = max(1, int(sys.argv[3]))
contrast = float(sys.argv[4])
min_cov = float(sys.argv[5]); empty_cov = float(sys.argv[6])
gutter_min = float(sys.argv[7]); gutter_uniform = float(sys.argv[8])
fail_on_find = sys.argv[9] == "1"
out = sys.argv[10]; frames = sys.argv[11:]
total = gw * gh

def dist(a, b):
    return ((a[0]-b[0])**2 + (a[1]-b[1])**2 + (a[2]-b[2])**2) ** 0.5

def grid_rgb(path):
    # Area-average each frame down to gw x gh, raw rgb24 bytes.
    p = subprocess.run(
        ["ffmpeg", "-loglevel", "error", "-i", path,
         "-vf", f"scale={gw}:{gh}:flags=area", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
        capture_output=True)
    if p.returncode != 0 or len(p.stdout) < total * 3:
        return None
    return p.stdout

def buckets(buf):
    # Quantize each tile colour into a coarse bucket so anti-aliasing / minor
    # gradients collapse to one value. Returns a list of bucket tuples.
    bs = []
    for i in range(total):
        r, g, b = buf[3*i], buf[3*i+1], buf[3*i+2]
        bs.append(((r//quant)*quant, (g//quant)*quant, (b//quant)*quant))
    return bs

def hexof(bucket):
    return "#%02x%02x%02x" % bucket

def largest_blob(bs, bg):
    # Largest 4-connected component of tiles sharing one bucket whose CONTRAST
    # from the background bucket exceeds the threshold (low-contrast blobs — a
    # dark panel on a dark theme — are ignored as normal UI).
    seen = [False]*total
    best = (0, None, None)  # (area, bucket, bbox)
    for start in range(total):
        if seen[start] or dist(bs[start], bg) < contrast:
            continue
        target = bs[start]
        stack = [start]; seen[start] = True; cells = []
        while stack:
            c = stack.pop(); cells.append(c)
            cy, cx = divmod(c, gw)
            for ny, nx in ((cy-1,cx),(cy+1,cx),(cy,cx-1),(cy,cx+1)):
                if 0 <= ny < gh and 0 <= nx < gw:
                    n = ny*gw + nx
                    if not seen[n] and bs[n] == target:
                        seen[n] = True; stack.append(n)
        if len(cells) > best[0]:
            xs = [c % gw for c in cells]; ys = [c // gw for c in cells]
            box = {"x": round(min(xs)/gw, 3), "y": round(min(ys)/gh, 3),
                   "w": round((max(xs)-min(xs)+1)/gw, 3),
                   "h": round((max(ys)-min(ys)+1)/gh, 3)}
            best = (len(cells), target, box)
    return best

def edge_gutters(bs, bg):
    # Contrast-INDEPENDENT, colour-AGNOSTIC edge check. From each edge inward,
    # count the contiguous band of lines (columns for left/right, rows for
    # top/bottom) that are each ~entirely ONE flat bucket AND share that bucket
    # across the whole band. Two failure modes both surface as such a band:
    #   • a dead margin the content never reaches (the band is the page bg) — the
    #     case the contrast gate is blind to;
    #   • a foreign flat bar composited OVER the UI (e.g. a video recorder padding
    #     the frame with its grey background because the window < the record size)
    #     — the band is a DISTINCT colour, so a bg-only check misses it.
    # Returns per side {width: frac-of-axis, color: hex} for the widest band.
    def line_top(idxs_for_line):
        # Most-common bucket in a line + its coverage fraction.
        c = Counter(bs[i] for i in idxs_for_line)
        bucket, n = c.most_common(1)[0]
        return bucket, n / len(idxs_for_line)
    def scan(lines):
        # lines: ordered list (edge → inward) of index-lists. Walk while each line
        # is uniform (>= gutter_uniform) and matches the band's bucket.
        band_bucket = None
        n = 0
        for idxs in lines:
            bucket, frac = line_top(idxs)
            if frac < gutter_uniform:
                break
            if band_bucket is None:
                band_bucket = bucket
            elif dist(bucket, band_bucket) > quant:
                break
            n += 1
        return n, band_bucket
    cols = [[y*gw + x for y in range(gh)] for x in range(gw)]
    rows = [[y*gw + x for x in range(gw)] for y in range(gh)]
    out = {}
    for side, lines, axis in (
        ("right",  list(reversed(cols)), gw),
        ("left",   cols,                 gw),
        ("bottom", list(reversed(rows)), gh),
        ("top",    rows,                 gh),
    ):
        n, bucket = scan(lines)
        out[side] = {"width": round(n / axis, 3),
                     "color": hexof(bucket) if bucket is not None else None}
    return out

results, flagged = [], []
for path in frames:
    name = path.rsplit("/", 1)[-1]
    buf = grid_rgb(path)
    if buf is None:
        results.append({"frame": name, "error": "decode-failed"}); continue
    bs = buckets(buf)
    counts = Counter(bs)
    bg_bucket, bg_n = counts.most_common(1)[0]
    bg_cov = round(bg_n / total, 4)
    area, blob_bucket, box = largest_blob(bs, bg_bucket)
    blob_cov = round(area / total, 4)
    gutters = edge_gutters(bs, bg_bucket)
    rec = {"frame": name,
           "background": {"color": hexof(bg_bucket), "coverage": bg_cov},
           "block": {"color": hexof(blob_bucket) if blob_bucket else None,
                     "coverage": blob_cov, "bbox": box},
           "gutters": gutters}
    reasons = []
    if blob_cov >= min_cov and blob_bucket is not None:
        reasons.append(f"a solid {hexof(blob_bucket)} block (high-contrast vs "
                       f"the {hexof(bg_bucket)} background) covers "
                       f"{blob_cov*100:.0f}% of the frame")
    # Edge gutter: a wide flat band hugging one edge — either a dead margin the
    # content never reaches, or a foreign bar composited over the UI (e.g. a video
    # recorder padding the frame). Only on a frame that DOES have content (a
    # near-empty page is reported separately below).
    if bg_cov < empty_cov:
        worst_side = max(gutters, key=lambda s: gutters[s]["width"])
        worst = gutters[worst_side]["width"]
        gcol = gutters[worst_side]["color"]
        if worst >= gutter_min:
            foreign = gcol is not None and dist(
                tuple(int(gcol[i:i+2], 16) for i in (1, 3, 5)), bg_bucket) > contrast
            kind = (f"a foreign flat {gcol} bar (distinct from the {hexof(bg_bucket)} "
                    f"UI — likely a recorder/letterbox bar composited OVER the frame)"
                    if foreign else
                    f"a flat {gcol} {worst_side} gutter the content never reaches")
            reasons.append(f"{kind} spans {worst*100:.0f}% of the frame at the "
                           f"{worst_side} edge")
    if bg_cov >= empty_cov:
        reasons.append(f"the frame is {bg_cov*100:.0f}% a single flat colour "
                       f"{hexof(bg_bucket)} — almost nothing rendered")
    results.append(rec)
    if reasons:
        rec_f = dict(rec)
        rec_f["issue"] = ("; ".join(reasons) +
                          " — likely a blank/broken render where content was expected")
        flagged.append(rec_f)

report = {"grid": f"{gw}x{gh}", "quant": quant, "contrast": contrast,
          "min_coverage": min_cov, "empty_coverage": empty_cov,
          "gutter_min": gutter_min, "gutter_uniform": gutter_uniform,
          "frames_scanned": len(frames), "flagged": flagged, "frames": results}
text = json.dumps(report, indent=2)
if out:
    with open(out, "w") as f: f.write(text + "\n")
else:
    print(text)

if flagged:
    print(f"blank-scan: {len(flagged)} frame(s) with a large monochromatic "
          f"region — review:", file=sys.stderr)
    for r in flagged:
        print(f"  {r['frame']}: {r['issue']}", file=sys.stderr)
else:
    print(f"blank-scan: no large monochromatic regions in {len(frames)} "
          f"frame(s)", file=sys.stderr)

sys.exit(3 if (flagged and fail_on_find) else 0)
PY
